package cliproxyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestTargetValidatesConfigAndProviderMatrix(t *testing.T) {
	t.Parallel()

	invalid := []TargetConfig{
		{BaseURL: "https://cpa.example.com", ManagementKey: "key"},
		{TargetID: "target", BaseURL: "/relative", ManagementKey: "key"},
		{TargetID: "target", BaseURL: "https://cpa.example.com"},
	}
	for _, cfg := range invalid {
		if target, err := NewTarget(cfg, nil); err == nil {
			_ = target
			t.Fatalf("NewTarget(%#v) unexpectedly succeeded", cfg)
		}
	}
	if _, err := NewTarget(TargetConfig{TargetID: "target", BaseURL: "https://cpa.example.com", ManagementKey: "key"}, nil); err != nil {
		t.Fatalf("NewTarget(valid) error = %v", err)
	}

	static := map[string]string{
		platform.ProviderGemini:    "gemini-api-key",
		platform.ProviderAIStudio:  "gemini-api-key",
		platform.ProviderAnthropic: "claude-api-key",
		platform.ProviderCodex:     "codex-api-key",
		platform.ProviderXAI:       "xai-api-key",
		platform.ProviderVertex:    "vertex-api-key",
		platform.ProviderVertexAI:  "vertex-api-key",
		platform.ProviderOpenAI:    "openai-compatibility",
	}
	for provider, endpoint := range static {
		route, err := targetRouteForInput(platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: provider})
		if err != nil || route.endpoint != endpoint {
			t.Fatalf("static route %s = %#v, %v", provider, route, err)
		}
	}

	native := []string{
		platform.ProviderAnthropic,
		platform.ProviderGemini,
		platform.ProviderCodex,
		platform.ProviderXAI,
		platform.ProviderVertex,
		platform.ProviderVertexAI,
		platform.ProviderAIStudio,
		platform.ProviderAntigravity,
		platform.ProviderKimi,
		platform.ProviderKiro,
	}
	for _, provider := range native {
		route, err := targetRouteForInput(platform.CreateChannelInput{Mode: platform.SyncModeNativeAuthFile, Provider: provider})
		if err != nil || len(route.rawProviders) == 0 {
			t.Fatalf("native route %s = %#v, %v", provider, route, err)
		}
	}

	rawProviders := []string{"gemini", "aistudio", "claude", "anthropic", "codex", "xai", "vertex", "openai", "openai-compatibility"}
	for _, provider := range rawProviders {
		if _, ok := targetRouteForRawProvider(provider); !ok {
			t.Fatalf("raw provider %q unsupported", provider)
		}
	}
	if _, ok := targetRouteForRawProvider("future-plugin"); ok {
		t.Fatal("future plugin unexpectedly has a static target route")
	}
	if _, err := targetRouteForInput(platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderKimi}); err == nil {
		t.Fatal("Kimi static key unexpectedly supported")
	}
	if _, err := targetRouteForInput(platform.CreateChannelInput{Mode: platform.SyncModeProxyEndpoint, Provider: platform.ProviderAnthropic}); err == nil {
		t.Fatal("proxy mode without base URL unexpectedly supported")
	}

	name := stableConfigName(platform.CreateChannelInput{Name: " Claude / Team A ", AssetID: "asset"})
	if name != "Claude---Team-A" {
		t.Fatalf("stable config name = %q", name)
	}
	generated := stableConfigName(platform.CreateChannelInput{AssetID: "asset"})
	if !strings.HasPrefix(generated, "synchub-") {
		t.Fatalf("generated config name = %q", generated)
	}
	nested := map[string]any{"api-key-entries": []any{map[string]any{"auth-index": "nested-index"}}}
	if !containsAuthIndex(nested, "nested-index") || containsAuthIndex(nested, "missing") {
		t.Fatalf("nested auth-index lookup failed")
	}
}

func TestTargetCreatesProxyEndpointAsOpenAICompatibility(t *testing.T) {
	t.Parallel()

	var created atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodGet:
			if created.Load() {
				_, _ = fmt.Fprint(w, `{"files":[{"id":"proxy-real-id","auth_index":"proxy-index","name":"proxy","provider":"openai-compatible-claude-proxy","status":"ready","runtime_only":true}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"files":[]}`)
			}
		case r.URL.Path == "/v0/management/openai-compatibility" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `{"openai-compatibility":[]}`)
		case r.URL.Path == "/v0/management/openai-compatibility" && r.Method == http.MethodPut:
			var entries []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
				t.Errorf("decode proxy PUT: %v", err)
			}
			if len(entries) != 1 || entries[0]["base-url"] != "https://proxy.example.com/v1" {
				t.Errorf("proxy entries = %#v", entries)
			}
			keys, _ := entries[0]["api-key-entries"].([]any)
			if len(keys) != 1 || keys[0].(map[string]any)["api-key"] != "proxy-client-secret" {
				t.Errorf("proxy key entries = %#v", keys)
			}
			created.Store(true)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target, err := NewTarget(TargetConfig{TargetID: "target", BaseURL: server.URL, ManagementKey: "key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		AssetID:  "source:cpa:oauth-pool",
		Mode:     platform.SyncModeProxyEndpoint,
		Name:     "Claude Proxy",
		Provider: platform.ProviderAnthropic,
		BaseURL:  "https://proxy.example.com/v1/",
		Secret:   []byte("proxy-client-secret"),
		Models:   []string{"claude-sonnet-4"},
		Group:    "default",
		Weight:   100,
	})
	if err != nil {
		t.Fatalf("CreateChannel(proxy) error = %v", err)
	}
	if channel.ID != "proxy-real-id" || channel.Provider != platform.ProviderAnthropic {
		t.Fatalf("created proxy channel = %#v", channel)
	}
}

func TestTargetUpdatesAndDeletesNativeAuthFile(t *testing.T) {
	t.Parallel()

	var fieldPatches atomic.Int32
	var statusPatches atomic.Int32
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `{"files":[{"id":"native-real-id","auth_index":"native-index","name":"native.json","provider":"codex","status":"ready","runtime_only":false}]}`)
		case r.URL.Path == "/v0/management/auth-files/fields" && r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "native-real-id" || body["priority"] != float64(7) {
				t.Errorf("field patch = %#v", body)
			}
			fieldPatches.Add(1)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		case r.URL.Path == "/v0/management/auth-files/status" && r.Method == http.MethodPatch:
			statusPatches.Add(1)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodDelete:
			if r.URL.Query().Get("name") != "native.json" {
				t.Errorf("delete name = %q", r.URL.Query().Get("name"))
			}
			deletes.Add(1)
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target, err := NewTarget(TargetConfig{TargetID: "target", BaseURL: server.URL, ManagementKey: "key", UseManagementKeyHeader: true}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := target.UpdateChannel(context.Background(), "native-real-id", platform.UpdateChannelInput{
		Group: "default", Priority: 7, Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateChannel(native) error = %v", err)
	}
	if updated.Provider != platform.ProviderCodex || !updated.Enabled || updated.Priority != 7 {
		t.Fatalf("updated native channel = %#v", updated)
	}
	if err := target.DeleteChannel(context.Background(), "native-real-id"); err != nil {
		t.Fatalf("DeleteChannel(native) error = %v", err)
	}
	if fieldPatches.Load() != 1 || statusPatches.Load() != 1 || deletes.Load() != 1 {
		t.Fatalf("fields=%d status=%d deletes=%d", fieldPatches.Load(), statusPatches.Load(), deletes.Load())
	}
}
