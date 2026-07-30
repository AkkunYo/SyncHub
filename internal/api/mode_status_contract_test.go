package api

import (
	"net/http"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestConfigExposesSafeNewAPIDiscoveryModeStatus(t *testing.T) {
	env := newTestEnvironment()
	env.store.cfg.Upstreams[0].DiscoveryMode = config.DiscoveryModeAuto
	env.store.cfg.Upstreams[0].ManageTokens = true
	env.resolver.modeStatuses["source-a"] = platform.DiscoveryModeStatus{
		EffectiveMode: "token", Status: "ready",
	}
	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/config", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	upstream := dataObject(t, envelope)["upstreams"].([]any)[0].(map[string]any)
	if upstream["discovery_mode"] != "auto" || upstream["effective_discovery_mode"] != "token" || upstream["mode_status"] != "ready" || upstream["manage_tokens"] != true {
		t.Fatalf("upstream=%#v", upstream)
	}
	if _, exists := upstream["mode_error_code"]; exists {
		t.Fatalf("ready status contains error: %#v", upstream)
	}
}

func TestConfigExposesOnlyStableModeErrorCode(t *testing.T) {
	env := newTestEnvironment()
	env.store.cfg.Upstreams[0].DiscoveryMode = config.DiscoveryModeAuto
	env.resolver.modeStatuses["source-a"] = platform.DiscoveryModeStatus{
		EffectiveMode: "unresolved", Status: "error", ErrorCode: "rate_limited",
	}
	_, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/config", "", "")
	upstream := dataObject(t, envelope)["upstreams"].([]any)[0].(map[string]any)
	if upstream["mode_error_code"] != "rate_limited" || upstream["mode_status"] != "error" {
		t.Fatalf("upstream=%#v", upstream)
	}
}

func TestCreateAndUpdateNewAPIUpstreamDiscoverySettings(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)
	create := `{"id":"source-b","name":"Source B","type":"newapi","base_url":"https://source-b.example.com","access_token":"test-token","manage_tokens":true}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/upstreams", create, "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	created := dataObject(t, envelope)
	if created["discovery_mode"] != "token" || created["manage_tokens"] != true {
		t.Fatalf("created=%#v", created)
	}

	update := `{"name":"Source B","base_url":"https://source-b.example.com","discovery_mode":"token","manage_tokens":false}`
	recorder, envelope = request(t, router, http.MethodPut, "/api/v1/upstreams/source-b", update, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated := dataObject(t, envelope)
	if updated["discovery_mode"] != "token" || updated["manage_tokens"] != false {
		t.Fatalf("updated=%#v", updated)
	}
}
