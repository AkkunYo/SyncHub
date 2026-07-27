package cliproxyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestTargetListsChannelsFromAuthFilesAndStaticConfigs(t *testing.T) {
	t.Parallel()

	var staticReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer management-key" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[`+
				`{"id":"claude-static-real-id","auth_index":"claude-index","name":"claude-key","provider":"claude","status":"ready","priority":4,"disabled":false,"runtime_only":true},`+
				`{"id":"codex-oauth-real-id","auth_index":"codex-index","name":"codex.json","provider":"codex","status":"disabled","priority":1,"disabled":true,"runtime_only":false}`+
				`]}`)
		case "/v0/management/auth-files/models":
			if r.URL.Query().Get("name") == "claude-key" {
				_, _ = fmt.Fprint(w, `{"models":[{"id":"claude-sonnet-4"}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"models":[{"id":"gpt-5-codex"}]}`)
			}
		default:
			staticReads.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{TargetID: "target-cpa", BaseURL: server.URL, ManagementKey: "management-key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	channels, err := target.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("channels = %#v", channels)
	}
	if got := channels[0]; got.ID != "claude-static-real-id" || got.Provider != platform.ProviderAnthropic || got.Priority != 4 || !got.Enabled || strings.Join(got.Models, ",") != "claude-sonnet-4" {
		t.Fatalf("static channel = %#v", got)
	}
	if got := channels[1]; got.ID != "codex-oauth-real-id" || got.Provider != platform.ProviderCodex || got.Enabled || got.Group != "default" || got.Weight != 100 {
		t.Fatalf("OAuth channel = %#v", got)
	}
	if staticReads.Load() != int32(len(knownStaticRoutes)) {
		t.Fatalf("expected %d static config reads, got %d", len(knownStaticRoutes), staticReads.Load())
	}
	capabilities := target.Capabilities()
	if capabilities.Platform != "cliproxyapi" || capabilities.NativeAuthSchema != "cpa-auth-v1" {
		t.Fatalf("Capabilities() = %#v", capabilities)
	}
}

func TestTargetListsManuallyConfiguredStaticChannels(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[]}`)
		case "/v0/management/auth-files/models":
			_, _ = fmt.Fprint(w, `{"models":[{"id":"gpt-4.1"}]}`)
		case "/v0/management/openai-compatibility":
			_, _ = fmt.Fprint(w, `{"openai-compatibility":[{"name":"my-openai","base-url":"https://api.openai.com","api-key":"sk-secret","priority":5}]}`)
		case "/v0/management/claude-api-key":
			_, _ = fmt.Fprint(w, `{"claude-api-key":[{"api-key":"sk-ant-secret","disabled":true}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{TargetID: "t", BaseURL: server.URL, ManagementKey: "key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	channels, err := target.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("got %d channels, want 2: %#v", len(channels), channels)
	}

	oai := channels[0]
	if oai.Name != "my-openai" || oai.Provider != platform.ProviderOpenAI || oai.Priority != 5 || !oai.Enabled {
		t.Fatalf("openai channel = %#v", oai)
	}

	claude := channels[1]
	if claude.Provider != platform.ProviderAnthropic || claude.Enabled {
		t.Fatalf("claude channel = %#v", claude)
	}
}

func TestTargetCreatesStaticChannelAndReturnsRealAuthID(t *testing.T) {
	t.Parallel()

	var created atomic.Bool
	var putBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodGet:
			if created.Load() {
				_, _ = fmt.Fprint(w, `{"files":[`+
					`{"id":"existing-real-id","auth_index":"old-index","name":"existing","provider":"gemini","status":"ready","runtime_only":true},`+
					`{"id":"new-real-id","auth_index":"new-index","name":"new","provider":"gemini","status":"ready","runtime_only":true}`+
					`]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"files":[{"id":"existing-real-id","auth_index":"old-index","name":"existing","provider":"gemini","status":"ready","runtime_only":true}]}`)
			}
		case r.URL.Path == "/v0/management/gemini-api-key" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `{"gemini-api-key":[{"api-key":"existing-secret","base-url":"https://existing.example.com","auth-index":"old-index"}]}`)
		case r.URL.Path == "/v0/management/gemini-api-key" && r.Method == http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			var entries []map[string]any
			if err := json.Unmarshal(putBody, &entries); err != nil {
				t.Errorf("PUT body invalid: %v", err)
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if len(entries) != 2 || entries[0]["api-key"] != "existing-secret" || entries[1]["api-key"] != "new-static-secret" {
				t.Errorf("PUT entries = %#v", entries)
			}
			models, _ := entries[1]["models"].([]any)
			if len(models) != 2 {
				t.Errorf("new models = %#v", entries[1]["models"])
			}
			created.Store(true)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{TargetID: "target-cpa", BaseURL: server.URL, ManagementKey: "management-key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		AssetID:  "source:channel:7",
		Mode:     platform.SyncModeStaticKey,
		Name:     "Gemini source",
		Provider: platform.ProviderGemini,
		BaseURL:  "https://generativelanguage.googleapis.com/",
		Secret:   []byte("new-static-secret"),
		Models:   []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		Group:    "default",
		Priority: 3,
		Weight:   100,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.ID != "new-real-id" || channel.Provider != platform.ProviderGemini || channel.Priority != 3 {
		t.Fatalf("created channel = %#v", channel)
	}
	if strings.Contains(string(putBody), "management-key") {
		t.Fatal("management key leaked into request body")
	}
}

func TestTargetCreatesNativeAuthFileWithoutTransformingSecret(t *testing.T) {
	t.Parallel()

	var uploaded atomic.Bool
	secret := `{"type":"claude","access_token":"oauth-sensitive"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodGet:
			if uploaded.Load() {
				_, _ = fmt.Fprint(w, `{"files":[{"id":"uploaded-real-id","auth_index":"uploaded-index","name":"`+r.URL.Query().Get("ignored")+`synchub-auth.json","provider":"claude","status":"ready","runtime_only":false}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"files":[]}`)
			}
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodPost:
			if name := r.URL.Query().Get("name"); !strings.HasPrefix(name, "synchub-") || !strings.HasSuffix(name, ".json") {
				t.Errorf("upload name = %q", name)
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != secret {
				t.Errorf("uploaded auth file transformed: %q", body)
			}
			uploaded.Store(true)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{TargetID: "target-cpa", BaseURL: server.URL, ManagementKey: "management-key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		AssetID:  "source:cpa:claude-auth",
		Mode:     platform.SyncModeNativeAuthFile,
		Name:     "Claude OAuth",
		Provider: platform.ProviderAnthropic,
		Secret:   []byte(secret),
		Group:    "default",
		Weight:   100,
	})
	if err != nil {
		t.Fatalf("CreateChannel(native) error = %v", err)
	}
	if channel.ID != "uploaded-real-id" || channel.Provider != platform.ProviderAnthropic {
		t.Fatalf("created native channel = %#v", channel)
	}
}

func TestTargetRejectsIncompatibleNativeAuthFilesBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{TargetID: "target", BaseURL: server.URL, ManagementKey: "key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	marker := "credential-marker-must-not-leak"
	tests := []struct {
		name     string
		provider string
		secret   string
	}{
		{name: "provider mismatch", provider: platform.ProviderAnthropic, secret: `{"type":"codex","access_token":"` + marker + `"}`},
		{name: "missing type", provider: platform.ProviderAnthropic, secret: `{"access_token":"` + marker + `"}`},
		{name: "non-object", provider: platform.ProviderAnthropic, secret: `[{"type":"claude","access_token":"` + marker + `"}]`},
		{name: "missing credentials", provider: platform.ProviderAnthropic, secret: `{"type":"claude","email":"` + marker + `"}`},
		{name: "escaped whitespace credential", provider: platform.ProviderAnthropic, secret: `{"type":"claude","access_token":"\u0020\u0009"}`},
		{name: "nested escaped whitespace credential", provider: platform.ProviderGemini, secret: `{"type":"gemini-cli","token":{"access_token":"\u0020\u000a"}}`},
		{name: "vertex missing private key", provider: platform.ProviderVertex, secret: `{"type":"vertex","project_id":"project","service_account":{"client_email":"service@example.com"}}`},
		{name: "vertex escaped whitespace private key", provider: platform.ProviderVertexAI, secret: `{"type":"vertex","project_id":"project","service_account":{"client_email":"service@example.com","private_key":"\u0020\u000d"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, createErr := target.CreateChannel(context.Background(), platform.CreateChannelInput{
				AssetID: "source:cpa:native", Mode: platform.SyncModeNativeAuthFile,
				Provider: test.provider, Secret: []byte(test.secret), Group: "default", Weight: 100,
			})
			if !errors.Is(createErr, platform.ErrIncompatibleTarget) {
				t.Fatalf("CreateChannel() error = %v", createErr)
			}
			if createErr != nil && strings.Contains(createErr.Error(), marker) {
				t.Fatalf("error leaked auth file content: %v", createErr)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid auth files triggered %d HTTP requests", got)
	}
}

func TestTargetAcceptsExplicitCompatibleNativeAuthSchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		provider    string
		rawProvider string
		secret      string
	}{
		{name: "claude", provider: platform.ProviderAnthropic, rawProvider: "claude", secret: `{"type":"claude","access_token":"access"}`},
		{name: "codex", provider: platform.ProviderCodex, rawProvider: "codex", secret: `{"type":"codex","refresh_token":"refresh"}`},
		{name: "gemini cli", provider: platform.ProviderGemini, rawProvider: "gemini-cli", secret: `{"type":"gemini-cli","project_id":"project","token":{"access_token":"access"}}`},
		{name: "vertex", provider: platform.ProviderVertex, rawProvider: "vertex", secret: `{"type":"vertex","project_id":"project","service_account":{"client_email":"service@example.com","private_key":"test-private-key-material"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var uploaded atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
					if uploaded.Load() {
						_, _ = fmt.Fprintf(w, `{"files":[{"id":"real-id","name":"native.json","provider":%q,"status":"ready","runtime_only":false}]}`, test.rawProvider)
					} else {
						_, _ = fmt.Fprint(w, `{"files":[]}`)
					}
				case r.Method == http.MethodPost && r.URL.Path == "/v0/management/auth-files":
					body, readErr := io.ReadAll(r.Body)
					if readErr != nil || string(body) != test.secret {
						t.Errorf("uploaded body = %q, error = %v", body, readErr)
					}
					uploaded.Store(true)
					_, _ = fmt.Fprint(w, `{"status":"ok"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			target, err := NewTarget(TargetConfig{TargetID: "target", BaseURL: server.URL, ManagementKey: "key", RequestTimeout: time.Second}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
				AssetID: "source:cpa:" + test.name, Mode: platform.SyncModeNativeAuthFile,
				Provider: test.provider, Secret: []byte(test.secret), Group: "default", Weight: 100,
			})
			if err != nil || channel.ID != "real-id" || !uploaded.Load() {
				t.Fatalf("CreateChannel() = %#v, %v", channel, err)
			}
		})
	}
}

func TestTargetUpdatesAndDeletesStaticChannelByAuthIndex(t *testing.T) {
	t.Parallel()

	var puts atomic.Int32
	var statusPatches atomic.Int32
	entries := []map[string]any{{
		"api-key":    "static-sensitive",
		"base-url":   "https://old.example.com",
		"auth-index": "stable-index",
		"models":     []any{map[string]any{"name": "old-model", "alias": "old-model"}},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `{"files":[{"id":"target-real-id","auth_index":"stable-index","name":"claude-static","provider":"claude","status":"ready","runtime_only":true}]}`)
		case r.URL.Path == "/v0/management/claude-api-key" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"claude-api-key": entries})
		case r.URL.Path == "/v0/management/claude-api-key" && r.Method == http.MethodPut:
			var next []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
				t.Errorf("decode PUT: %v", err)
			}
			entries = next
			puts.Add(1)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		case r.URL.Path == "/v0/management/auth-files/status" && r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "target-real-id" || body["disabled"] != true {
				t.Errorf("status patch = %#v", body)
			}
			statusPatches.Add(1)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{TargetID: "target-cpa", BaseURL: server.URL, ManagementKey: "management-key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := target.UpdateChannel(context.Background(), "target-real-id", platform.UpdateChannelInput{
		Name:     "Claude updated",
		BaseURL:  "https://new.example.com/",
		Models:   []string{"new-model"},
		Group:    "default",
		Priority: 6,
		Weight:   100,
		Enabled:  false,
	})
	if err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if updated.ID != "target-real-id" || updated.Enabled || updated.Priority != 6 {
		t.Fatalf("updated channel = %#v", updated)
	}
	if len(entries) != 1 || entries[0]["api-key"] != "static-sensitive" || entries[0]["base-url"] != "https://new.example.com" {
		t.Fatalf("updated entries = %#v", entries)
	}
	if puts.Load() != 1 || statusPatches.Load() != 1 {
		t.Fatalf("puts=%d status patches=%d", puts.Load(), statusPatches.Load())
	}

	if err := target.DeleteChannel(context.Background(), "target-real-id"); err != nil {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
	if len(entries) != 0 || puts.Load() != 2 {
		t.Fatalf("delete left entries=%#v puts=%d", entries, puts.Load())
	}
}

func TestTargetRejectsUnsupportedSettingsAndRedactsErrors(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "management-sensitive and static-sensitive", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	target, err := NewTarget(TargetConfig{TargetID: "target-cpa", BaseURL: server.URL, ManagementKey: "management-sensitive"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode:     platform.SyncModeStaticKey,
		Provider: platform.ProviderUnknown,
		Secret:   []byte("static-sensitive"),
		Group:    "default",
		Weight:   100,
	})
	if !errors.Is(err, platform.ErrIncompatibleTarget) || requests.Load() != 0 {
		t.Fatalf("unknown provider error=%v requests=%d", err, requests.Load())
	}
	_, err = target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode:     platform.SyncModeStaticKey,
		Provider: platform.ProviderGemini,
		Secret:   []byte("static-sensitive"),
		Group:    "premium",
		Weight:   50,
	})
	if !errors.Is(err, platform.ErrIncompatibleTarget) || requests.Load() != 0 {
		t.Fatalf("unsupported settings error=%v requests=%d", err, requests.Load())
	}
	_, err = target.ListChannels(context.Background())
	if err == nil || strings.Contains(err.Error(), "management-sensitive") || strings.Contains(err.Error(), "static-sensitive") {
		t.Fatalf("ListChannels() error leaked secret: %v", err)
	}
}
