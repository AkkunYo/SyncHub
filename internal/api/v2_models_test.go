package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/modelcatalog"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/platform/generic"
	"github.com/AkkunYo/SyncHub/internal/probe"
)

func TestV2NewAPIKeyListUsesStableRedactedOrdinaryUserMetadata(t *testing.T) {
	env := newTestEnvironment()
	upstream := env.resolver.upstreams["source-a"].(*fakeUpstream)
	upstream.pages = []platform.AssetPage{{Assets: []platform.UpstreamAsset{{
		ID: "source-a:token:41", SourceID: "source-a", SourceType: "newapi",
		Provider: platform.ProviderOpenAI, RawType: "newapi-token", Kind: platform.AssetProxyKey,
		Name: "Personal key", BaseURL: "https://source.example.com", Models: []string{},
		Enabled: true, SecretReadable: true, Metadata: map[string]string{
			"token_id": "41", "upstream_group": "default", "masked_key": "masked-sensitive-fragment",
		},
	}}}}

	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/upstreams/source-a/keys", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	keys := dataObject(t, envelope)["keys"].([]any)
	key := keys[0].(map[string]any)
	if key["id"] != "41" || key["name"] != "Personal key" || key["source_group"] != "default" || key["credential_present"] != true {
		t.Fatalf("key = %#v", key)
	}
	if upstream.resolveCalls != 0 {
		t.Fatalf("key listing resolved %d secrets", upstream.resolveCalls)
	}
	for _, forbidden := range []string{testSecret, "masked-sensitive-fragment", "access_token", "api_key"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestV2GenericDiscoveryModelsAndSingleProbeFlow(t *testing.T) {
	const apiKey = "flow-secret-key"
	var modelRequests atomic.Int32
	var probeRequests atomic.Int32
	var capturedPrompt string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			modelRequests.Add(1)
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			probeRequests.Add(1)
			var payload struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Messages) != 1 {
				t.Fatalf("probe payload decode error=%v payload=%#v", err, payload)
			}
			capturedPrompt = payload.Messages[0].Content
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]string{"content": capturedPrompt},
			}}})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer provider.Close()

	env := newTestEnvironment()
	upstreamConfig := config.UpstreamConfig{
		ID: "source-generic", Name: "Generic", Type: "generic", BaseURL: provider.URL,
		Keys: []config.GenericKeyConfig{{ID: "primary", Name: "Primary", APIKey: apiKey, Enabled: true}},
	}
	env.store.cfg.Upstreams = append(env.store.cfg.Upstreams, upstreamConfig)
	adapter, err := generic.NewMultiKeySource(generic.MultiKeyConfig{
		SourceID: upstreamConfig.ID, Name: upstreamConfig.Name, BaseURL: upstreamConfig.BaseURL,
		Keys:           []generic.KeyConfig{{ID: "primary", Name: "Primary", APIKey: apiKey, Enabled: true}},
		RequestTimeout: 5e9,
	}, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	env.resolver.upstreams[upstreamConfig.ID] = adapter
	dependencies := env.dependencies()
	dependencies.Models = modelcatalog.NewService(env.store, provider.Client(), probe.NewService(provider.Client()))
	router, err := NewRouter(dependencies)
	if err != nil {
		t.Fatal(err)
	}

	recorder, envelope := request(
		t, router, http.MethodPost, "/api/v1/upstreams/source-generic/model-discoveries",
		`{"key_ids":["primary"]}`, "application/json",
	)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("discovery status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	task := dataObject(t, envelope)
	if task["task_id"] == "" || task["completed"] != true || task["status"] != string(modelcatalog.TaskSucceeded) {
		t.Fatalf("task = %#v", task)
	}
	items := task["items"].([]any)
	if items[0].(map[string]any)["status"] != string(modelcatalog.DiscoverySucceeded) {
		t.Fatalf("items = %#v", items)
	}
	if modelRequests.Load() != 1 {
		t.Fatalf("model requests = %d, want 1", modelRequests.Load())
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/upstreams/source-generic/keys/primary/models", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("models status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	models := dataObject(t, envelope)["models"].([]any)
	if len(models) != 2 || models[0].(map[string]any)["id"] != "model-a" {
		t.Fatalf("models = %#v", models)
	}

	recorder, envelope = request(
		t, router, http.MethodPost, "/api/v1/upstreams/source-generic/keys/primary/model-probes",
		`{"model":"model-a","protocol":"chat_completions"}`, "application/json",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	result := dataObject(t, envelope)
	if result["key_id"] != "primary" || result["model"] != "model-a" || result["status"] != string(probe.StatusHealthy) || result["template_version"] != probe.TemplateVersion {
		t.Fatalf("probe result = %#v", result)
	}
	if probeRequests.Load() != 1 || capturedPrompt == "" {
		t.Fatalf("probe requests=%d prompt=%q", probeRequests.Load(), capturedPrompt)
	}
	for _, forbidden := range []string{apiKey, capturedPrompt, "prompt", "response_body", "api_key"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("probe response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}

	recorder, envelope = request(
		t, router, http.MethodPost, "/api/v1/upstreams/source-generic/keys/primary/model-probes",
		`{"model":"not-in-snapshot","protocol":"auto"}`, "application/json",
	)
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, envelope) != "model_unavailable" {
		t.Fatalf("unknown model status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if probeRequests.Load() != 1 {
		t.Fatalf("unknown model triggered probe: %d", probeRequests.Load())
	}
	if got := env.store.Snapshot().Upstreams[1].Keys[0].Models; !slices.Equal(got, []string{"model-a", "model-b"}) {
		t.Fatalf("discovered models were not persisted: %#v", got)
	}
}
