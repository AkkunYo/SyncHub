package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

var _ platform.TargetAdapter = (*Target)(nil)

func TestTargetListsEveryChannelPageAndNormalizesFields(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assertTargetAuthorization(t, r, "target-management-token")
		if got := r.Header.Get("New-Api-User"); got != "" {
			t.Errorf("New-Api-User = %q, want omitted", got)
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/channel/" {
			t.Errorf("request = %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("page_size"); got != "2" {
			t.Errorf("page_size = %q", got)
		}
		switch r.URL.Query().Get("p") {
		case "1":
			writeTargetList(t, w, 1, 2, 3, []map[string]any{
				{"id": 11, "type": 1, "name": "OpenAI primary", "status": 1, "base_url": "https://openai.example.com/", "models": "gpt-4o, gpt-4.1", "group": "default", "priority": 3, "weight": 80},
				{"id": 12, "type": 999, "name": "Future provider", "status": 2, "base_url": "", "models": "future-model", "group": "experimental", "priority": 0, "weight": 20},
			})
		case "2":
			writeTargetList(t, w, 2, 2, 3, []map[string]any{
				{"id": 13, "type": 24, "name": "Gemini", "status": 1, "base_url": "https://gemini.example.com", "models": "gemini-2.5-pro", "group": "premium", "priority": 5, "weight": 100},
			})
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("p"))
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{
		TargetID:    "target-newapi",
		BaseURL:     server.URL + "/",
		AccessToken: "target-management-token",
		PageSize:    2,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	channels, err := target.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if requests.Load() != 2 || len(channels) != 3 {
		t.Fatalf("requests=%d channels=%#v", requests.Load(), channels)
	}
	if got := channels[0]; got.ID != "11" || got.Provider != platform.ProviderOpenAI || got.RawType != "1" || got.BaseURL != "https://openai.example.com" || strings.Join(got.Models, ",") != "gpt-4o,gpt-4.1" || !got.Enabled {
		t.Fatalf("first channel = %#v", got)
	}
	if got := channels[1]; got.Provider != platform.ProviderUnknown || got.RawType != "999" || got.Enabled {
		t.Fatalf("unknown channel = %#v", got)
	}
	if got := channels[2]; got.Provider != platform.ProviderGemini || got.Group != "premium" || got.Priority != 5 || got.Weight != 100 {
		t.Fatalf("last channel = %#v", got)
	}

	capabilities := target.Capabilities()
	if capabilities.Platform != "newapi" {
		t.Fatalf("Capabilities() = %#v", capabilities)
	}
	if modes := capabilities.Providers[platform.ProviderOpenAI].Modes; !containsSyncMode(modes, platform.SyncModeStaticKey) {
		t.Fatalf("OpenAI modes = %#v", modes)
	}
}

func TestTargetSendsConfiguredUserIdentityOnCRUDRequests(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("New-Api-User"); got != "59" {
			t.Errorf("New-Api-User = %q, want 59", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer REPLACE_WITH_TARGET_TOKEN" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
			writeTargetList(t, w, 1, 100, 0, nil)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":71}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":71,"type":1,"name":"updated","status":1}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/71/status":
			_, _ = fmt.Fprint(w, `{"success":true,"data":true}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/channel/71":
			_, _ = fmt.Fprint(w, `{"success":true,"data":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	cfg := TargetConfig{TargetID: "target-a", BaseURL: server.URL, AccessToken: "REPLACE_WITH_TARGET_TOKEN"}
	setNewAPIUserIDForTest(t, &cfg, 59)
	target, err := NewTarget(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	created, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI, Secret: []byte("REPLACE_WITH_ASSET_KEY"), Name: "created",
	})
	if err != nil || created.ID != "71" {
		t.Fatalf("CreateChannel() = %#v, %v", created, err)
	}
	if _, err := target.UpdateChannel(context.Background(), "71", platform.UpdateChannelInput{Name: "updated", Enabled: true}); err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if err := target.DeleteChannel(context.Background(), "71"); err != nil {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
}

func TestTargetListChannelsDiscardsPartialResultsWhenLaterPageFails(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("p") == "1" {
			writeTargetList(t, w, 1, 1, 2, []map[string]any{{"id": 1, "type": 1, "name": "first", "status": 1}})
			return
		}
		http.Error(w, "response-body-sensitive", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	target := newTestTarget(t, server, TargetConfig{PageSize: 1, AccessToken: "management-sensitive"})
	channels, err := target.ListChannels(context.Background())
	if err == nil {
		t.Fatal("ListChannels() unexpectedly succeeded")
	}
	if channels != nil {
		t.Fatalf("partial channels returned: %#v", channels)
	}
	assertErrorOmits(t, err, "management-sensitive", "response-body-sensitive")
}

func TestTargetCreatesMultiKeyChannelAndReturnsRealID(t *testing.T) {
	t.Parallel()

	var created atomic.Bool
	var createBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertTargetAuthorization(t, r, "target-management-token")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
			items := []map[string]any{{"id": 7, "type": 1, "name": "existing", "status": 1}}
			if created.Load() {
				items = append(items, map[string]any{
					"id": 72, "type": 24, "name": "Gemini pool", "status": 1,
					"base_url": "https://gemini.example.com", "models": "gemini-2.5-pro,gemini-2.5-flash",
					"group": "premium", "priority": 4, "weight": 75,
				})
			}
			writeTargetList(t, w, 1, 100, len(items), items)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/":
			var err error
			createBody, err = readAllForTest(r)
			if err != nil {
				t.Errorf("read create body: %v", err)
			}
			var request struct {
				Mode         string `json:"mode"`
				MultiKeyMode string `json:"multi_key_mode"`
				Channel      struct {
					Type     int    `json:"type"`
					Key      string `json:"key"`
					Status   int    `json:"status"`
					Name     string `json:"name"`
					Weight   int    `json:"weight"`
					BaseURL  string `json:"base_url"`
					Models   string `json:"models"`
					Group    string `json:"group"`
					Priority int    `json:"priority"`
				} `json:"channel"`
			}
			if err := json.Unmarshal(createBody, &request); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if request.Mode != "multi_to_single" || request.MultiKeyMode != "polling" {
				t.Errorf("multi-key request = %#v", request)
			}
			channel := request.Channel
			if channel.Type != 24 || channel.Key != "channel-key-alpha\nchannel-key-beta" || channel.Status != 1 || channel.Name != "Gemini pool" || channel.Weight != 75 || channel.BaseURL != "https://gemini.example.com" || channel.Models != "gemini-2.5-pro,gemini-2.5-flash" || channel.Group != "premium" || channel.Priority != 4 {
				t.Errorf("create channel = %#v", channel)
			}
			created.Store(true)
			_, _ = fmt.Fprint(w, `{"success":true,"message":""}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target := newTestTarget(t, server, TargetConfig{AccessToken: "target-management-token"})
	channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		AssetID:  "source:channel:9",
		Mode:     platform.SyncModeStaticKey,
		Name:     " Gemini pool ",
		Provider: platform.ProviderGemini,
		BaseURL:  "https://gemini.example.com/",
		Secret:   []byte("channel-key-alpha\nchannel-key-beta"),
		Models:   []string{"gemini-2.5-pro", " gemini-2.5-flash "},
		Group:    "premium",
		Priority: 4,
		Weight:   75,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.ID != "72" || channel.Provider != platform.ProviderGemini || channel.RawType != "24" || channel.BaseURL != "https://gemini.example.com" || channel.Priority != 4 || channel.Weight != 75 || !channel.Enabled {
		t.Fatalf("created channel = %#v", channel)
	}
	if strings.Contains(string(createBody), "target-management-token") {
		t.Fatal("management token leaked into create body")
	}
}

func TestTargetUpdatesAndDeletesChannelUsingPublicAPI(t *testing.T) {
	t.Parallel()

	var updates atomic.Int32
	var statusUpdates atomic.Int32
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertTargetAuthorization(t, r, "target-management-token")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			if _, exists := body["key"]; exists {
				t.Errorf("update body unexpectedly contains key: %#v", body)
			}
			if _, exists := body["status"]; exists {
				t.Errorf("update body unexpectedly contains status: %#v", body)
			}
			if body["id"] != float64(42) || body["name"] != "Claude updated" || body["base_url"] != "https://claude.example.com" || body["models"] != "claude-sonnet-4,claude-opus-4" || body["group"] != "premium" || body["priority"] != float64(8) || body["weight"] != float64(60) {
				t.Errorf("update body = %#v", body)
			}
			updates.Add(1)
			_, _ = fmt.Fprint(w, `{"success":true,"message":"","data":{"id":42,"type":14,"name":"Claude updated","status":1,"base_url":"https://claude.example.com/","models":"claude-sonnet-4,claude-opus-4","group":"premium","priority":8,"weight":60}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/42/status":
			var body struct {
				Status int `json:"status"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Status != 2 {
				t.Errorf("status body = %#v, err=%v", body, err)
			}
			statusUpdates.Add(1)
			_, _ = fmt.Fprint(w, `{"success":true,"message":"","data":true}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/channel/42":
			deletes.Add(1)
			_, _ = fmt.Fprint(w, `{"success":true,"message":""}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target := newTestTarget(t, server, TargetConfig{AccessToken: "target-management-token"})
	updated, err := target.UpdateChannel(context.Background(), "42", platform.UpdateChannelInput{
		Name:     " Claude updated ",
		BaseURL:  "https://claude.example.com/",
		Models:   []string{"claude-sonnet-4", "claude-opus-4"},
		Group:    "premium",
		Priority: 8,
		Weight:   60,
		Enabled:  false,
	})
	if err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if updated.ID != "42" || updated.Provider != platform.ProviderAnthropic || updated.RawType != "14" || updated.Enabled || updated.BaseURL != "https://claude.example.com" {
		t.Fatalf("updated channel = %#v", updated)
	}
	if err := target.DeleteChannel(context.Background(), "42"); err != nil {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
	if updates.Load() != 1 || statusUpdates.Load() != 1 || deletes.Load() != 1 {
		t.Fatalf("updates=%d status=%d deletes=%d", updates.Load(), statusUpdates.Load(), deletes.Load())
	}
}

func TestTargetRejectsUnknownProviderAndMissingSecretBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{})

	_, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode: platform.SyncModeStaticKey, Provider: platform.ProviderUnknown, Secret: []byte("channel-sensitive"),
	})
	if !errors.Is(err, platform.ErrIncompatibleTarget) {
		t.Fatalf("unknown provider error = %v", err)
	}
	_, err = target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI,
	})
	if !errors.Is(err, platform.ErrSecretUnavailable) {
		t.Fatalf("missing secret error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("validation made %d requests", requests.Load())
	}
}

func newTestTarget(t *testing.T, server *httptest.Server, overrides TargetConfig) *Target {
	t.Helper()
	cfg := TargetConfig{
		TargetID:    "target-newapi",
		BaseURL:     server.URL,
		AccessToken: "target-management-token",
		PageSize:    100,
	}
	if overrides.TargetID != "" {
		cfg.TargetID = overrides.TargetID
	}
	if overrides.BaseURL != "" {
		cfg.BaseURL = overrides.BaseURL
	}
	if overrides.AccessToken != "" {
		cfg.AccessToken = overrides.AccessToken
	}
	if overrides.PageSize != 0 {
		cfg.PageSize = overrides.PageSize
	}
	if overrides.RequestTimeout != 0 {
		cfg.RequestTimeout = overrides.RequestTimeout
	}
	target, err := NewTarget(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func writeTargetList(t *testing.T, w http.ResponseWriter, page, pageSize, total int, items []map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"data":    map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize},
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func assertTargetAuthorization(t *testing.T, r *http.Request, token string) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("Authorization = %q", got)
	}
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
}

func containsSyncMode(modes []platform.SyncMode, wanted platform.SyncMode) bool {
	for _, mode := range modes {
		if mode == wanted {
			return true
		}
	}
	return false
}

func assertErrorOmits(t *testing.T, err error, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}
