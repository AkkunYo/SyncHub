package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestV2GenericKeyLifecycleIsRedacted(t *testing.T) {
	env := newTestEnvironment()
	env.store.cfg.Upstreams = append(env.store.cfg.Upstreams, config.UpstreamConfig{
		ID: "source-generic", Name: "Generic", Type: "generic", BaseURL: "https://generic.example.com",
		Keys: []config.GenericKeyConfig{{
			ID: "primary", Name: "Primary", APIKey: "primary-secret", Enabled: true, Models: []string{"gpt-4.1"},
		}},
	})
	router := env.router(t)

	recorder, envelope := request(t, router, http.MethodGet, "/api/v1/upstreams/source-generic/keys", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoKeySecret(t, recorder.Body.String())
	keys := dataObject(t, envelope)["keys"].([]any)
	primary := keys[0].(map[string]any)
	if primary["id"] != "primary" || primary["credential_present"] != true || primary["enabled"] != true {
		t.Fatalf("primary = %#v", primary)
	}

	create := `{"id":"backup","name":"Backup","api_key":"backup-secret","models":["gpt-4.1-mini"]}`
	recorder, envelope = request(t, router, http.MethodPost, "/api/v1/upstreams/source-generic/keys", create, "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoKeySecret(t, recorder.Body.String())
	if recorder.Header().Get("Location") != "/api/v1/upstreams/source-generic/keys/backup" {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}

	patch := `{"name":"Backup renamed","enabled":false}`
	recorder, envelope = request(t, router, http.MethodPatch, "/api/v1/upstreams/source-generic/keys/backup", patch, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated := dataObject(t, envelope)
	if updated["name"] != "Backup renamed" || updated["enabled"] != false {
		t.Fatalf("updated = %#v", updated)
	}
	if got := env.store.cfg.Upstreams[1].Keys[1].APIKey; got != "backup-secret" {
		t.Fatalf("omitted credential was not retained: %q", got)
	}

	recorder, _ = request(t, router, http.MethodDelete, "/api/v1/upstreams/source-generic/keys/backup", "", "")
	if recorder.Code != http.StatusOK || len(env.store.cfg.Upstreams[1].Keys) != 1 {
		t.Fatalf("delete status=%d keys=%#v", recorder.Code, env.store.cfg.Upstreams[1].Keys)
	}

	recorder, _ = request(t, router, http.MethodGet, "/api/v1/config", "", "")
	assertNoKeySecret(t, recorder.Body.String())
	if !strings.Contains(recorder.Body.String(), `"credential_present":true`) {
		t.Fatalf("config omitted key presence metadata: %s", recorder.Body.String())
	}
}

func TestV2GenericKeyDeleteRejectsNewAndLegacyMappings(t *testing.T) {
	for _, assetID := range []string{"source-generic:key:primary", "source-generic:endpoint"} {
		t.Run(assetID, func(t *testing.T) {
			env := newTestEnvironment()
			env.store.cfg.Upstreams = append(env.store.cfg.Upstreams, config.UpstreamConfig{
				ID: "source-generic", Name: "Generic", Type: "generic", BaseURL: "https://generic.example.com",
				Keys: []config.GenericKeyConfig{{ID: "primary", Name: "Primary", APIKey: "secret", Enabled: true}},
				SyncMappings: []config.SyncMapping{{
					UpstreamAssetID: assetID, TargetID: "target-a", TargetChannelID: "42",
					SourceProvider: platform.ProviderOpenAI, AssetKind: platform.AssetProxyKey,
				}},
			})

			recorder, envelope := request(
				t,
				env.router(t),
				http.MethodDelete,
				"/api/v1/upstreams/source-generic/keys/primary",
				"",
				"",
			)
			if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "resource_in_use" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func assertNoKeySecret(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"primary-secret", "backup-secret", `"api_key"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}
