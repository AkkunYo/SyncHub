package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	stdsync "sync"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

type snapshotSignalStore struct {
	*fakeConfigStore
	armed    chan struct{}
	observed chan struct{}
	once     stdsync.Once
}

func (s *snapshotSignalStore) Snapshot() config.Config {
	cfg := s.fakeConfigStore.Snapshot()
	select {
	case <-s.armed:
		s.once.Do(func() { close(s.observed) })
	default:
	}
	return cfg
}

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

func TestSyncRechecksTargetValidationAfterWaitingForTupleLock(t *testing.T) {
	env := newTestEnvironment()
	store := &snapshotSignalStore{
		fakeConfigStore: env.store,
		armed:           make(chan struct{}),
		observed:        make(chan struct{}),
	}
	dependencies := env.dependencies()
	dependencies.Config = store
	router, err := NewRouter(dependencies)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var syncCalls atomic.Int32
	env.syncer.multiFn = func(_ context.Context, _ string, _ int, request syncservice.MultiRequest) (syncservice.MultiResult, error) {
		call := syncCalls.Add(1)
		if call == 1 {
			close(firstEntered)
			<-releaseFirst
		}
		unit := request.Units[0]
		return syncservice.MultiResult{Units: []syncservice.UnitResult{{
			UnitID: unit.UnitID, AssetID: unit.Asset.ID, TargetID: unit.Target.ID,
			Status: syncservice.TargetSynced, ChannelID: "42", EffectiveModels: []string{"gpt-4.1"},
			ExcludedModels: []string{}, Warnings: []string{},
		}}}, nil
	}

	performSync := func() *httptest.ResponseRecorder {
		body := staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", 100)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- performSync() }()
	<-firstEntered

	close(store.armed)
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- performSync() }()
	<-store.observed

	updateRecorder, _ := request(t, router, http.MethodPut, "/api/v1/targets/target-a", `{"name":"Target A","base_url":"https://target.example.com","access_token":"replacement-secret"}`, "application/json")
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("target update status=%d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	if got := store.Snapshot().Targets[0].ValidationStatus; got != config.TargetValidationUnverified {
		t.Fatalf("target validation after update = %q, want unverified", got)
	}

	close(releaseFirst)
	firstRecorder := <-firstDone
	secondRecorder := <-secondDone
	if firstRecorder.Code != http.StatusOK || secondRecorder.Code != http.StatusOK {
		t.Fatalf("sync statuses: first=%d second=%d", firstRecorder.Code, secondRecorder.Code)
	}
	var envelope map[string]any
	if err := json.Unmarshal(secondRecorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode second sync response: %v", err)
	}
	unit := dataObject(t, envelope)["units"].([]any)[0].(map[string]any)
	if unit["code"] != "target_unverified" || unit["status"] != string(syncservice.TargetIncompatible) {
		t.Fatalf("second sync unit=%#v, want target_unverified", unit)
	}
	if got := syncCalls.Load(); got != 1 {
		t.Fatalf("remote sync calls=%d, want only the request already in progress", got)
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

func TestTargetCredentialUpdateWithSameValuePreservesValidation(t *testing.T) {
	env := newTestEnvironment()
	original := cloneConfig(env.store.cfg).Targets[0]

	recorder, _ := request(t, env.router(t), http.MethodPut, "/api/v1/targets/target-a", `{"name":"Target A","base_url":"https://target.example.com","access_token":"credential-fixture-value"}`, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated := env.store.cfg.Targets[0]
	if updated.ValidationStatus != config.TargetValidationVerified || updated.ValidatedAt == nil || !updated.ValidatedAt.Equal(*original.ValidatedAt) {
		t.Fatalf("same credential invalidated validation: before=%#v after=%#v", original, updated)
	}
	if updated.ValidationCapabilities.Platform != original.ValidationCapabilities.Platform || len(updated.ValidationCapabilities.Providers) != len(original.ValidationCapabilities.Providers) {
		t.Fatalf("same credential changed validation capabilities: before=%#v after=%#v", original.ValidationCapabilities, updated.ValidationCapabilities)
	}
}

func TestTargetBaseURLUpdateInvalidatesValidationWhileNameUpdatePreservesIt(t *testing.T) {
	env := newTestEnvironment()
	originalValidatedAt := *env.store.cfg.Targets[0].ValidatedAt

	recorder, _ := request(t, env.router(t), http.MethodPut, "/api/v1/targets/target-a", `{"name":"Renamed","base_url":"https://target.example.com"}`, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("name update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	preserved := env.store.cfg.Targets[0]
	if preserved.ValidationStatus != config.TargetValidationVerified || preserved.ValidatedAt == nil || !preserved.ValidatedAt.Equal(originalValidatedAt) {
		t.Fatalf("name update invalidated validation: %#v", preserved)
	}

	recorder, _ = request(t, env.router(t), http.MethodPut, "/api/v1/targets/target-a", `{"name":"Renamed","base_url":"https://replacement.example.com"}`, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("base URL update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	invalidated := env.store.cfg.Targets[0]
	if invalidated.ValidationStatus != config.TargetValidationUnverified || invalidated.ValidatedAt != nil || len(invalidated.ValidationCapabilities.Providers) != 0 {
		t.Fatalf("base URL update retained validation: %#v", invalidated)
	}
}

func TestCreatedTargetStartsUnverified(t *testing.T) {
	env := newTestEnvironment()
	recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/targets", `{"id":"target-new","name":"New","type":"newapi","base_url":"https://new.example.com","access_token":"write-only-secret"}`, "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if dataObject(t, envelope)["validation_status"] != string(config.TargetValidationUnverified) || strings.Contains(recorder.Body.String(), "write-only-secret") {
		t.Fatalf("created target=%s", recorder.Body.String())
	}
	target, ok := targetByID(env.store.cfg, "target-new")
	if !ok || target.ValidationStatus != config.TargetValidationUnverified || target.ValidatedAt != nil {
		t.Fatalf("stored target=%#v found=%v", target, ok)
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

func TestMatrixTargetIncludesValidationSummary(t *testing.T) {
	env := newTestEnvironment()
	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	target := dataObject(t, envelope)["targets"].([]any)[0].(map[string]any)
	if target["validation_status"] != string(config.TargetValidationVerified) || target["validated_at"] == "" || target["validation_capabilities"] == nil {
		t.Fatalf("matrix target validation=%#v", target)
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
