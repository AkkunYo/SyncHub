package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

func TestSyncRejectsUnverifiedTargetBeforeExternalCalls(t *testing.T) {
	env := newTestEnvironment()
	env.store.cfg.Targets[0].ValidationStatus = config.TargetValidationUnverified
	env.store.cfg.Targets[0].ValidatedAt = nil
	env.store.cfg.Targets[0].ValidationCapabilities = platform.TargetCapabilities{}
	body := `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}],"grant":{}}`

	recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	units := dataObject(t, envelope)["units"].([]any)
	unit := units[0].(map[string]any)
	if unit["status"] != string(syncservice.TargetIncompatible) || unit["code"] != "target_unverified" {
		t.Fatalf("unit=%#v, want target_unverified", unit)
	}
	if env.syncer.calls != 0 || len(env.resolver.upstreamCalls) != 0 {
		t.Fatalf("sync side effects: sync=%d upstream=%d", env.syncer.calls, len(env.resolver.upstreamCalls))
	}
}

func TestTargetConnectionValidationPersistsSanitizedState(t *testing.T) {
	env := newTestEnvironment()
	recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/targets/target-a/connection-tests", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if dataObject(t, envelope)["authenticated"] != true {
		t.Fatalf("connection result=%#v", dataObject(t, envelope))
	}
	target := env.store.cfg.Targets[0]
	if target.ValidationStatus != config.TargetValidationVerified || target.ValidatedAt == nil || target.ValidatedAt.IsZero() {
		t.Fatalf("persisted validation status=%q at=%v", target.ValidationStatus, target.ValidatedAt)
	}
	if target.ValidationCapabilities.Platform != "newapi" || len(target.ValidationCapabilities.Providers) == 0 {
		t.Fatalf("persisted capabilities=%#v", target.ValidationCapabilities)
	}
	getRecorder, getEnvelope := request(t, env.router(t), http.MethodGet, "/api/v1/config", "", "")
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET config status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	publicTarget := dataObject(t, getEnvelope)["targets"].([]any)[0].(map[string]any)
	if publicTarget["validation_status"] != string(config.TargetValidationVerified) || publicTarget["validated_at"] == "" || publicTarget["validation_capabilities"] == nil {
		t.Fatalf("public validation fields=%#v", publicTarget)
	}
	if containsSecret(getRecorder.Body.String(), testSecret) {
		t.Fatalf("validation response leaked credential: %s", getRecorder.Body.String())
	}
}

func TestTargetCredentialUpdateInvalidatesValidation(t *testing.T) {
	env := newTestEnvironment()
	seedTargetValidation(t, env)
	recorder, _ := request(t, env.router(t), http.MethodPut, "/api/v1/targets/target-a", `{"name":"Target A","base_url":"https://target.example.com","access_token":"replacement-secret"}`, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := env.store.cfg.Targets[0].ValidationStatus; got != config.TargetValidationUnverified {
		t.Fatalf("validation survived credential update: %#v", got)
	}
	if env.store.cfg.Targets[0].ValidatedAt != nil || len(env.store.cfg.Targets[0].ValidationCapabilities.Providers) != 0 {
		t.Fatalf("validation summary survived credential update: %#v", env.store.cfg.Targets[0])
	}
}

func TestFailedTargetConnectionTestDoesNotPersistValidation(t *testing.T) {
	env := newTestEnvironment()
	before := cloneConfig(env.store.cfg).Targets[0]
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.listErr = platform.ErrRateLimited
	recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/targets/target-a/connection-tests", "", "")
	if recorder.Code == http.StatusOK || errorCode(t, envelope) == "internal_error" {
		t.Fatalf("failed validation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	got := env.store.cfg.Targets[0]
	if got.ValidationStatus != before.ValidationStatus || !got.ValidatedAt.Equal(*before.ValidatedAt) || got.ValidationCapabilities.Platform != before.ValidationCapabilities.Platform {
		t.Fatalf("failed validation changed persisted state: before=%#v after=%#v", before, got)
	}
}

func seedTargetValidation(t *testing.T, env *testEnvironment) {
	t.Helper()
	recorder, _ := request(t, env.router(t), http.MethodPost, "/api/v1/targets/target-a/connection-tests", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("seed validation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func containsSecret(value, secret string) bool {
	return secret != "" && strings.Contains(value, secret)
}
