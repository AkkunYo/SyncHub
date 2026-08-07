package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	stdsync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/discovery"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

const (
	testRequestID = "req_generated_01"
	testSecret    = "credential-fixture-value"
)

type fakeConfigStore struct {
	mu        stdsync.Mutex
	cfg       config.Config
	updateErr error
	updates   int
}

func (s *fakeConfigStore) Snapshot() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneConfig(s.cfg)
}

func (s *fakeConfigStore) Update(ctx context.Context, mutate func(*config.Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := cloneConfig(s.cfg)
	if err := mutate(&next); err != nil {
		return err
	}
	if s.updateErr != nil {
		return s.updateErr
	}
	if err := config.Validate(&next); err != nil {
		return err
	}
	s.cfg = cloneConfig(next)
	s.updates++
	return nil
}

type fakeTarget struct {
	channels    []platform.Channel
	listErr     error
	updateErr   error
	deleteErr   error
	updateOut   platform.Channel
	listCalls   int
	updateCalls int
	deleteCalls int
	updatedID   string
	updated     platform.UpdateChannelInput
	deletedID   string
	listFn      func(context.Context) ([]platform.Channel, error)
	updateFn    func(context.Context, string, platform.UpdateChannelInput) (platform.Channel, error)
}

func (t *fakeTarget) ListChannels(ctx context.Context) ([]platform.Channel, error) {
	t.listCalls++
	if t.listFn != nil {
		return t.listFn(ctx)
	}
	if t.listErr != nil {
		return nil, t.listErr
	}
	return append([]platform.Channel(nil), t.channels...), nil
}

func (t *fakeTarget) CreateChannel(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("unexpected direct create")
}

func (t *fakeTarget) UpdateChannel(ctx context.Context, id string, input platform.UpdateChannelInput) (platform.Channel, error) {
	t.updateCalls++
	t.updatedID = id
	t.updated = input
	if t.updateFn != nil {
		return t.updateFn(ctx, id, input)
	}
	if t.updateErr != nil {
		return platform.Channel{}, t.updateErr
	}
	return t.updateOut, nil
}

func (t *fakeTarget) DeleteChannel(_ context.Context, id string) error {
	t.deleteCalls++
	t.deletedID = id
	return t.deleteErr
}

type fakeUpstream struct {
	pages          []platform.AssetPage
	listErr        error
	listCalls      int
	resolveCalls   int
	resolvedGrant  platform.SecretGrant
	resolvedAsset  string
	resolvedSecret platform.ResolvedSecret
}

func (u *fakeUpstream) Capabilities(context.Context) (platform.SourceCapabilities, error) {
	return platform.SourceCapabilities{}, nil
}

func (u *fakeUpstream) ListAssets(_ context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	u.listCalls++
	if u.listErr != nil {
		return platform.AssetPage{}, u.listErr
	}
	index := cursor.Page
	if index < 0 || index >= len(u.pages) {
		return platform.AssetPage{Assets: []platform.UpstreamAsset{}}, nil
	}
	return u.pages[index], nil
}

func (u *fakeUpstream) ResolveSecret(_ context.Context, assetID string, grant platform.SecretGrant) (platform.ResolvedSecret, error) {
	u.resolveCalls++
	u.resolvedAsset = assetID
	u.resolvedGrant = grant
	return u.resolvedSecret, nil
}

type targetResolution struct {
	adapter      platform.TargetAdapter
	capabilities platform.TargetCapabilities
	err          error
}

type fakeResolver struct {
	mu            stdsync.Mutex
	targets       map[string]targetResolution
	upstreams     map[string]platform.UpstreamAdapter
	upstreamErr   map[string]error
	targetCalls   []string
	upstreamCalls []string
	modeStatuses  map[string]platform.DiscoveryModeStatus
}

func (r *fakeResolver) DiscoveryModeStatus(cfg config.UpstreamConfig) platform.DiscoveryModeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.modeStatuses[cfg.ID]
}

func (r *fakeResolver) ResolveTarget(_ context.Context, cfg config.TargetConfig) (platform.TargetAdapter, platform.TargetCapabilities, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targetCalls = append(r.targetCalls, cfg.ID)
	resolved, ok := r.targets[cfg.ID]
	if !ok {
		return nil, platform.TargetCapabilities{}, errors.New("target adapter unavailable")
	}
	return resolved.adapter, resolved.capabilities, resolved.err
}

func (r *fakeResolver) ResolveUpstream(_ context.Context, cfg config.UpstreamConfig) (platform.UpstreamAdapter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upstreamCalls = append(r.upstreamCalls, cfg.ID)
	if err := r.upstreamErr[cfg.ID]; err != nil {
		return nil, err
	}
	adapter, ok := r.upstreams[cfg.ID]
	if !ok {
		return nil, errors.New("upstream adapter unavailable")
	}
	return adapter, nil
}

type fakeDiscovery struct {
	mu           stdsync.Mutex
	snapshots    map[string]discovery.Snapshot
	refreshErr   error
	refreshCalls int
}

func (d *fakeDiscovery) Refresh(_ context.Context, sourceID string, _ platform.UpstreamAdapter) (discovery.Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refreshCalls++
	if d.refreshErr != nil {
		return discovery.Snapshot{}, d.refreshErr
	}
	snapshot := cloneDiscoverySnapshot(d.snapshots[sourceID])
	return snapshot, nil
}

func (d *fakeDiscovery) Snapshot(sourceID string) (discovery.Snapshot, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	snapshot, ok := d.snapshots[sourceID]
	return cloneDiscoverySnapshot(snapshot), ok
}

type fakeSyncService struct {
	mu           stdsync.Mutex
	result       syncservice.BatchResult
	multiResult  syncservice.MultiResult
	err          error
	calls        int
	sourceID     string
	concurrency  int
	request      syncservice.BatchRequest
	multiRequest syncservice.MultiRequest
	fn           func(context.Context, string, int, syncservice.BatchRequest) (syncservice.BatchResult, error)
	multiFn      func(context.Context, string, int, syncservice.MultiRequest) (syncservice.MultiResult, error)
}

func (s *fakeSyncService) SyncUnits(ctx context.Context, sourceID string, concurrency int, request syncservice.MultiRequest) (syncservice.MultiResult, error) {
	if s.multiFn != nil {
		return s.multiFn(ctx, sourceID, concurrency, request)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.sourceID = sourceID
	s.concurrency = concurrency
	s.multiRequest = request
	return s.multiResult, s.err
}

func (s *fakeSyncService) Sync(ctx context.Context, sourceID string, concurrency int, request syncservice.BatchRequest) (syncservice.BatchResult, error) {
	if s.fn != nil {
		return s.fn(ctx, sourceID, concurrency, request)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.sourceID = sourceID
	s.concurrency = concurrency
	s.request = request
	return s.result, s.err
}

type fakeMappings struct {
	mu          stdsync.Mutex
	byTarget    map[string][]platform.SyncMapping
	listErr     error
	deleteErr   error
	updateErr   error
	deleted     []platform.SyncMapping
	updated     []platform.SyncMapping
	listCalls   int
	deleteCalls int
	updateCalls int
}

func (m *fakeMappings) ListMappings(_ context.Context, targetID string) ([]platform.SyncMapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	if m.listErr != nil {
		return nil, m.listErr
	}
	return cloneMappings(m.byTarget[targetID]), nil
}

func (m *fakeMappings) DeleteMappings(_ context.Context, mappings []platform.SyncMapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	m.deleted = cloneMappings(mappings)
	return m.deleteErr
}

func (m *fakeMappings) UpdateMapping(_ context.Context, mapping platform.SyncMapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls++
	m.updated = append(m.updated, mapping)
	return m.updateErr
}

type fakeReconcile struct {
	report      reconcile.Report
	checkErr    error
	acceptErr   error
	checkFn     func(context.Context, string, platform.TargetAdapter) (reconcile.Report, error)
	checkCalls  int
	acceptCalls int
	checkedID   string
	accepted    platform.SyncMapping
	current     platform.Channel
}

func (r *fakeReconcile) Check(ctx context.Context, targetID string, target platform.TargetAdapter) (reconcile.Report, error) {
	r.checkCalls++
	r.checkedID = targetID
	if r.checkFn != nil {
		return r.checkFn(ctx, targetID, target)
	}
	return r.report, r.checkErr
}

func (r *fakeReconcile) AcceptDrift(_ context.Context, mapping platform.SyncMapping, current platform.Channel) error {
	r.acceptCalls++
	r.accepted = mapping
	r.current = current
	return r.acceptErr
}

type testEnvironment struct {
	store      *fakeConfigStore
	resolver   *fakeResolver
	discovery  DiscoveryService
	fakeDisc   *fakeDiscovery
	syncer     *fakeSyncService
	mappings   *fakeMappings
	reconciler *fakeReconcile
}

func newTestEnvironment() *testEnvironment {
	asset := platform.UpstreamAsset{
		ID:             "source-a:channel:7:key:0",
		SourceID:       "source-a",
		SourceType:     "newapi",
		Provider:       platform.ProviderOpenAI,
		RawType:        "1",
		Kind:           platform.AssetStaticAPIKey,
		Name:           "OpenAI source",
		Models:         []string{"gpt-4.1"},
		Enabled:        true,
		SecretReadable: true,
	}
	target := &fakeTarget{channels: []platform.Channel{}, updateOut: platform.Channel{
		ID: "42", Name: "updated", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true,
	}}
	upstream := &fakeUpstream{}
	disc := &fakeDiscovery{snapshots: map[string]discovery.Snapshot{
		"source-a": {SourceID: "source-a", Assets: []platform.UpstreamAsset{asset}},
	}}
	return &testEnvironment{
		store: &fakeConfigStore{cfg: config.Config{
			App: config.AppConfig{
				Host: "127.0.0.1", Port: 8888,
				ReconcileInterval: config.Duration(5 * time.Minute),
				RequestTimeout:    config.Duration(15 * time.Second), SyncConcurrency: 4,
			},
			Targets: []config.TargetConfig{{
				ID: "target-a", Name: "Target A", Type: "newapi", BaseURL: "https://target.example.com", AccessToken: testSecret,
				ValidationStatus: config.TargetValidationVerified,
				ValidatedAt:      validationTime(2026, time.August, 6),
				ValidationCapabilities: platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
					platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
				}},
			}},
			Upstreams: []config.UpstreamConfig{{
				ID: "source-a", Name: "Source A", Type: "newapi", BaseURL: "https://source.example.com", AccessToken: testSecret,
			}},
		}},
		resolver: &fakeResolver{
			targets: map[string]targetResolution{
				"target-a": {
					adapter: target,
					capabilities: platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
						platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
					}},
				},
			},
			upstreams:    map[string]platform.UpstreamAdapter{"source-a": upstream},
			upstreamErr:  map[string]error{},
			modeStatuses: map[string]platform.DiscoveryModeStatus{},
		},
		discovery: disc,
		fakeDisc:  disc,
		syncer: &fakeSyncService{multiResult: syncservice.MultiResult{Units: []syncservice.UnitResult{{
			UnitID: "u-1", AssetID: asset.ID, TargetID: "target-a", Status: syncservice.TargetSynced, ChannelID: "42",
			EffectiveModels: []string{"gpt-4.1"}, ExcludedModels: []string{}, Warnings: []string{},
		}}}},
		mappings:   &fakeMappings{byTarget: map[string][]platform.SyncMapping{}},
		reconciler: &fakeReconcile{},
	}
}

func (e *testEnvironment) dependencies() Dependencies {
	return Dependencies{
		Config:             e.store,
		Adapters:           e.resolver,
		Discovery:          e.discovery,
		Sync:               e.syncer,
		Mappings:           e.mappings,
		Reconcile:          e.reconciler,
		Version:            "v-test",
		BuildDate:          "2026-07-29T16:00:00+08:00",
		RequestIDGenerator: func() string { return testRequestID },
	}
}

func (e *testEnvironment) router(t *testing.T) http.Handler {
	t.Helper()
	router, err := NewRouter(e.dependencies())
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func request(t *testing.T, router http.Handler, method, path, body, contentType string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not JSON: status=%d body=%q error=%v", recorder.Code, recorder.Body.String(), err)
	}
	return recorder, envelope
}

func dataObject(t *testing.T, envelope map[string]any) map[string]any {
	t.Helper()
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want object", envelope["data"])
	}
	return data
}

func errorCode(t *testing.T, envelope map[string]any) string {
	t.Helper()
	errorObject, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %#v, want object", envelope["error"])
	}
	code, _ := errorObject["code"].(string)
	return code
}

func staticSyncBody(unitID, upstreamID, assetID, targetID string, weight int) string {
	return fmt.Sprintf(`{"upstream_id":%q,"units":[{"unit_id":%q,"asset_id":%q,"target_id":%q,"settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":%d}}]}`,
		upstreamID, unitID, assetID, targetID, weight)
}

func TestHealthEnvelopeAndRequestID(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("X-Request-ID", "client-request:01")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("X-Request-ID"); got != "client-request:01" {
		t.Fatalf("X-Request-ID = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["success"] != true || envelope["request_id"] != "client-request:01" {
		t.Fatalf("envelope = %#v", envelope)
	}
	data := dataObject(t, envelope)
	if data["status"] != "ok" || data["version"] != "v-test" || data["build_date"] != "2026-07-29T16:00:00+08:00" {
		t.Fatalf("health data = %#v", data)
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	invalid.Header.Set("X-Request-ID", "bad request id")
	invalidRecorder := httptest.NewRecorder()
	router.ServeHTTP(invalidRecorder, invalid)
	if invalidRecorder.Header().Get("X-Request-ID") != testRequestID {
		t.Fatalf("invalid request id was reused: %q", invalidRecorder.Header().Get("X-Request-ID"))
	}
}

func TestStrictJSONAndBodyLimit(t *testing.T) {
	router := newTestEnvironment().router(t)
	valid := `{"host":"127.0.0.1","port":8888,"reconcile_interval":"5m","request_timeout":"15s","sync_concurrency":4}`
	tests := []struct {
		name        string
		body        string
		contentType string
	}{
		{name: "missing content type", body: valid},
		{name: "wrong content type", body: valid, contentType: "text/plain"},
		{name: "unknown field", body: strings.TrimSuffix(valid, "}") + `,"access_token":"not-accepted"}`, contentType: "application/json"},
		{name: "multiple values", body: valid + `{}`, contentType: "application/json"},
		{name: "trailing content", body: valid + `x`, contentType: "application/json"},
		{name: "null", body: `null`, contentType: "application/json"},
		{name: "oversized", body: `{"host":"` + strings.Repeat("a", (1<<20)+1) + `"}`, contentType: "application/json"},
		{name: "encoded body", body: valid, contentType: "application/json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/v1/config/app", strings.NewReader(test.body))
			if test.contentType != "" {
				req.Header.Set("Content-Type", test.contentType)
			}
			if test.name == "encoded body" {
				req.Header.Set("Content-Encoding", "gzip")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var envelope map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if got := errorCode(t, envelope); got != "invalid_request" {
				t.Fatalf("code = %q", got)
			}
			if envelope["request_id"] == "" {
				t.Fatal("request_id is empty")
			}
		})
	}

	recorder, envelope := request(t, router, http.MethodPut, "/api/v1/config/app", valid+" \n\t", "application/json; charset=utf-8")
	if recorder.Code != http.StatusOK || envelope["success"] != true {
		t.Fatalf("valid JSON with whitespace status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestConfigResponseIsRedactedAndDurationsAreStrings(t *testing.T) {
	env := newTestEnvironment()
	env.store.cfg.Targets[0].ManagementKey = "target-management-secret"
	env.store.cfg.Targets[0].APIKey = "target-api-secret"
	env.store.cfg.Upstreams[0].ManagementKey = "source-management-secret"
	env.store.cfg.Upstreams[0].APIKey = "source-api-secret"
	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/config", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{
		"access_token", "management_key", "api_key", testSecret,
		"target-management-secret", "target-api-secret", "source-management-secret", "source-api-secret",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
	app := dataObject(t, envelope)["app"].(map[string]any)
	if app["reconcile_interval"] != "5m0s" || app["request_timeout"] != "15s" {
		t.Fatalf("app durations = %#v", app)
	}
}

func TestAppAndTargetConfigCRUD(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)

	appBody := `{"host":"localhost","port":9000,"reconcile_interval":"10m","request_timeout":"20s","sync_concurrency":8}`
	recorder, _ := request(t, router, http.MethodPut, "/api/v1/config/app", appBody, "application/json")
	if recorder.Code != http.StatusOK || env.store.cfg.App.Port != 9000 || env.store.cfg.App.SyncConcurrency != 8 {
		t.Fatalf("app update failed: status=%d cfg=%#v", recorder.Code, env.store.cfg.App)
	}

	create := `{"id":"target-b","name":"Target B","type":"newapi","base_url":"https://target-b.example.com/","access_token":"` + testSecret + `"}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets", create, "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), testSecret) || strings.Contains(recorder.Body.String(), "access_token") {
		t.Fatalf("create response leaked credential: %s", recorder.Body.String())
	}
	created := dataObject(t, envelope)
	if created["id"] != "target-b" || created["base_url"] != "https://target-b.example.com" {
		t.Fatalf("created target = %#v", created)
	}
	if recorder.Header().Get("Location") != "/api/v1/targets/target-b" {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}

	update := `{"name":"Target B renamed","base_url":"https://new-target.example.com"}`
	recorder, _ = request(t, router, http.MethodPut, "/api/v1/targets/target-b", update, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated config.TargetConfig
	for _, target := range env.store.cfg.Targets {
		if target.ID == "target-b" {
			updated = target
		}
	}
	if updated.AccessToken != testSecret || updated.Name != "Target B renamed" {
		t.Fatalf("credential was not retained: %#v", updated)
	}

	invalidUpdate := `{"name":"Target B","base_url":"https://new-target.example.com","access_token":""}`
	recorder, envelope = request(t, router, http.MethodPut, "/api/v1/targets/target-b", invalidUpdate, "application/json")
	if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
		t.Fatalf("empty credential status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = request(t, router, http.MethodDelete, "/api/v1/targets/target-b", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if env.store.updates != 4 {
		t.Fatalf("atomic update count = %d, want 4", env.store.updates)
	}
}

func TestConfigResourceConflictsAndUpstreamCRUD(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{UpstreamAssetID: "asset", TargetID: "target-a", TargetChannelID: "42"}
	env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{mapping}
	router := env.router(t)

	recorder, envelope := request(t, router, http.MethodDelete, "/api/v1/targets/target-a", "", "")
	if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "resource_in_use" {
		t.Fatalf("target conflict status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder, envelope = request(t, router, http.MethodDelete, "/api/v1/upstreams/source-a", "", "")
	if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "resource_in_use" {
		t.Fatalf("upstream conflict status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	create := `{"id":"source-b","name":"Source B","type":"generic","base_url":"https://source-b.example.com/","api_key":"` + testSecret + `"}`
	recorder, envelope = request(t, router, http.MethodPost, "/api/v1/upstreams", create, "application/json")
	if recorder.Code != http.StatusCreated || strings.Contains(recorder.Body.String(), testSecret) || strings.Contains(recorder.Body.String(), "api_key") {
		t.Fatalf("create upstream status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if dataObject(t, envelope)["id"] != "source-b" {
		t.Fatalf("created upstream = %#v", envelope)
	}

	update := `{"name":"Source B renamed","base_url":"https://source-b2.example.com"}`
	recorder, _ = request(t, router, http.MethodPut, "/api/v1/upstreams/source-b", update, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("update upstream status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated config.UpstreamConfig
	for _, upstream := range env.store.cfg.Upstreams {
		if upstream.ID == "source-b" {
			updated = upstream
		}
	}
	if len(updated.Keys) != 1 || updated.Keys[0].APIKey != testSecret || updated.Name != "Source B renamed" {
		t.Fatalf("credential was not retained: %#v", updated)
	}

	recorder, _ = request(t, router, http.MethodDelete, "/api/v1/upstreams/source-b", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete upstream status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder, envelope = request(t, router, http.MethodPost, "/api/v1/targets", `{"id":"target-a","name":"duplicate","type":"newapi","base_url":"https://duplicate.example.com","access_token":"x"}`, "application/json")
	if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
		t.Fatalf("duplicate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGenericUpstreamCRUDUsesOnlySharedAPIKeyAndRedactsIt(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)

	create := `{"id":"source-generic","name":"Shared Endpoint","type":"generic","base_url":"https://provider.example.com/v1/","api_key":"` + testSecret + `"}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/upstreams", create, "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), testSecret) || strings.Contains(recorder.Body.String(), "api_key") {
		t.Fatalf("create response leaked shared key: %s", recorder.Body.String())
	}
	created := dataObject(t, envelope)
	if created["type"] != "generic" || created["base_url"] != "https://provider.example.com/v1" {
		t.Fatalf("created generic upstream = %#v", created)
	}
	for _, forbidden := range []string{"user_id", "discovery_mode", "effective_discovery_mode", "mode_status", "manage_tokens"} {
		if _, exists := created[forbidden]; exists {
			t.Fatalf("generic response exposed %q: %#v", forbidden, created)
		}
	}

	update := `{"name":"Shared Endpoint 2","base_url":"https://provider-2.example.com/v1"}`
	recorder, _ = request(t, router, http.MethodPut, "/api/v1/upstreams/source-generic", update, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated config.UpstreamConfig
	for _, upstream := range env.store.cfg.Upstreams {
		if upstream.ID == "source-generic" {
			updated = upstream
		}
	}
	if len(updated.Keys) != 1 || updated.Keys[0].APIKey != testSecret || updated.Name != "Shared Endpoint 2" {
		t.Fatalf("credential was not retained: %#v", updated)
	}

	const replacement = "replacement-shared-key"
	update = `{"name":"Shared Endpoint 2","base_url":"https://provider-2.example.com/v1","api_key":"` + replacement + `"}`
	recorder, _ = request(t, router, http.MethodPut, "/api/v1/upstreams/source-generic", update, "application/json")
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), replacement) || strings.Contains(recorder.Body.String(), "api_key") {
		t.Fatalf("credential update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, upstream := range env.store.cfg.Upstreams {
		if upstream.ID == "source-generic" && (len(upstream.Keys) != 1 || upstream.Keys[0].APIKey != replacement) {
			t.Fatalf("replacement credential was not stored: %#v", upstream)
		}
	}
}

func TestGenericUpstreamCRUDRejectsLoginAndManagementFields(t *testing.T) {
	t.Parallel()

	createFields := []string{
		`"access_token":"user-token"`,
		`"management_key":"management-key"`,
		`"proxy_api_key":"proxy-key"`,
		`"user_id":1`,
		`"discovery_mode":"token"`,
		`"manage_tokens":true`,
		`"managed_token_namespace":"synchub"`,
	}
	for _, field := range createFields {
		field := field
		t.Run("create "+field, func(t *testing.T) {
			t.Parallel()
			env := newTestEnvironment()
			body := `{"id":"source-generic","name":"Shared Endpoint","type":"generic","base_url":"https://provider.example.com/v1","api_key":"shared-key",` + field + `}`
			recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/upstreams", body, "application/json")
			if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	updateFields := []string{
		`"access_token":"user-token"`,
		`"management_key":"management-key"`,
		`"proxy_api_key":"proxy-key"`,
		`"user_id":1`,
		`"discovery_mode":"token"`,
		`"manage_tokens":true`,
		`"api_key":""`,
	}
	for _, field := range updateFields {
		field := field
		t.Run("update "+field, func(t *testing.T) {
			t.Parallel()
			env := newTestEnvironment()
			env.store.cfg.Upstreams = append(env.store.cfg.Upstreams, config.UpstreamConfig{
				ID: "source-generic", Name: "Shared Endpoint", Type: "generic",
				BaseURL: "https://provider.example.com/v1", APIKey: "shared-key",
			})
			body := `{"name":"Shared Endpoint","base_url":"https://provider.example.com/v1",` + field + `}`
			recorder, envelope := request(t, env.router(t), http.MethodPut, "/api/v1/upstreams/source-generic", body, "application/json")
			if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestTargetChannelsAreLiveAndAnnotated(t *testing.T) {
	env := newTestEnvironment()
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.channels = []platform.Channel{
		{ID: "42", Name: "managed", Provider: "openai", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true},
		{ID: "99", Name: "native", Provider: "gemini", Models: []string{"gemini-2.5"}, Group: "default", Weight: 100, Enabled: true},
	}
	mapping := platform.SyncMapping{UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42"}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}

	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/targets/target-a/channels", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	channels := dataObject(t, envelope)["channels"].([]any)
	managed := channels[0].(map[string]any)
	native := channels[1].(map[string]any)
	if managed["managed"] != true || managed["upstream_asset_id"] != mapping.UpstreamAssetID {
		t.Fatalf("managed channel = %#v", managed)
	}
	if native["managed"] != false {
		t.Fatalf("native channel = %#v", native)
	}
	if _, exists := native["upstream_asset_id"]; exists {
		t.Fatalf("native channel has fabricated source: %#v", native)
	}
	if target.listCalls != 1 || env.mappings.listCalls != 1 {
		t.Fatalf("calls list=%d mappings=%d", target.listCalls, env.mappings.listCalls)
	}
}

func TestUpdateAndDeleteTargetChannelMaintainMappings(t *testing.T) {
	env := newTestEnvironment()
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42",
		Snapshot: platform.ChannelSnapshot{Models: []string{"old"}, Group: "old", Weight: 50},
	}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
	target.updateOut = platform.Channel{ID: "42", Name: "renamed", BaseURL: "https://api.example.com", Models: []string{"gpt-4.1"}, Group: "default", Priority: 2, Weight: 100, Enabled: true}
	router := env.router(t)

	body := `{"name":"renamed","base_url":"https://api.example.com","models":["gpt-4.1"],"group":"default","priority":2,"weight":100,"enabled":true}`
	recorder, _ := request(t, router, http.MethodPut, "/api/v1/targets/target-a/channels/42", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if target.updatedID != "42" || target.updated.Weight != 100 || env.mappings.updateCalls != 1 {
		t.Fatalf("update was not forwarded: target=%#v mappings=%#v", target.updated, env.mappings.updated)
	}
	if got := env.mappings.updated[0].Snapshot; got.Group != "default" || got.Priority != 2 || got.Weight != 100 {
		t.Fatalf("snapshot = %#v", got)
	}

	recorder, _ = request(t, router, http.MethodDelete, "/api/v1/targets/target-a/channels/42", "", "")
	if recorder.Code != http.StatusOK || target.deletedID != "42" || env.mappings.deleteCalls != 1 {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(env.mappings.deleted) != 1 || env.mappings.deleted[0].TargetChannelID != "42" {
		t.Fatalf("deleted mappings = %#v", env.mappings.deleted)
	}
}

func TestChannelErrorsAreMappedAndSanitized(t *testing.T) {
	t.Run("channel not found", func(t *testing.T) {
		env := newTestEnvironment()
		target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
		target.updateErr = ErrChannelNotFound
		body := `{"name":"renamed","base_url":"","models":["gpt-4.1"],"group":"default","priority":0,"weight":100,"enabled":true}`
		recorder, envelope := request(t, env.router(t), http.MethodPut, "/api/v1/targets/target-a/channels/42", body, "application/json")
		if recorder.Code != http.StatusNotFound || errorCode(t, envelope) != "channel_not_found" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("upstream failure", func(t *testing.T) {
		env := newTestEnvironment()
		target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
		target.listErr = errors.New("Authorization: Bearer " + testSecret + " upstream-body")
		recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/targets/target-a/channels", "", "")
		if recorder.Code != http.StatusBadGateway || errorCode(t, envelope) != "upstream_failure" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), testSecret) || strings.Contains(recorder.Body.String(), "upstream-body") {
			t.Fatalf("error leaked details: %s", recorder.Body.String())
		}
	})

	t.Run("timeout", func(t *testing.T) {
		env := newTestEnvironment()
		target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
		target.listErr = context.DeadlineExceeded
		recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/targets/target-a/channels", "", "")
		if recorder.Code != http.StatusGatewayTimeout || errorCode(t, envelope) != "upstream_timeout" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("mapping persistence after remote delete", func(t *testing.T) {
		env := newTestEnvironment()
		env.mappings.byTarget["target-a"] = []platform.SyncMapping{{UpstreamAssetID: "asset", TargetID: "target-a", TargetChannelID: "42"}}
		env.mappings.deleteErr = errors.New("X-Security-Proof: " + testSecret)
		recorder, envelope := request(t, env.router(t), http.MethodDelete, "/api/v1/targets/target-a/channels/42", "", "")
		if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), testSecret) {
			t.Fatalf("error leaked proof: %s", recorder.Body.String())
		}
	})
}

func TestUpstreamRefreshAndAssetsNeverResolveSecrets(t *testing.T) {
	env := newTestEnvironment()
	upstream := env.resolver.upstreams["source-a"].(*fakeUpstream)
	asset := platform.UpstreamAsset{
		ID: "source-a:channel:8", SourceID: "source-a", SourceType: "newapi", Provider: "openai", RawType: "1",
		Kind: platform.AssetStaticAPIKey, Name: "metadata only", Models: []string{"gpt-4.1"}, Enabled: true, SecretReadable: true,
	}
	upstream.pages = []platform.AssetPage{{Assets: []platform.UpstreamAsset{asset}}}
	actualDiscovery := discovery.NewService()
	env.discovery = actualDiscovery
	router := env.router(t)

	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/upstreams/source-a/refresh", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if dataObject(t, envelope)["refreshed"] != true || upstream.listCalls != 1 || upstream.resolveCalls != 0 {
		t.Fatalf("refresh data=%#v list=%d resolve=%d", envelope, upstream.listCalls, upstream.resolveCalls)
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/upstreams/source-a/assets", "", "")
	if recorder.Code != http.StatusOK || dataObject(t, envelope)["refreshed"] != true {
		t.Fatalf("assets status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if upstream.resolveCalls != 0 || strings.Contains(recorder.Body.String(), "secret") && strings.Contains(recorder.Body.String(), testSecret) {
		t.Fatalf("asset list resolved or leaked a secret: %s", recorder.Body.String())
	}
}

func TestAssetsWithoutSnapshotAndQueryValidation(t *testing.T) {
	env := newTestEnvironment()
	env.fakeDisc.snapshots = map[string]discovery.Snapshot{}
	router := env.router(t)
	recorder, envelope := request(t, router, http.MethodGet, "/api/v1/upstreams/source-a/assets", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	if data["refreshed"] != false || len(data["assets"].([]any)) != 0 {
		t.Fatalf("data=%#v", data)
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/upstreams/source-a/assets?unexpected=1", "", "")
	if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
		t.Fatalf("query status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBatchSyncKeepsUnitsAndGrantRequestScoped(t *testing.T) {
	env := newTestEnvironment()
	targetB := &fakeTarget{}
	targetBConfig := env.store.cfg.Targets[0]
	targetBConfig.ID, targetBConfig.Name = "target-b", "Target B"
	targetBConfig.BaseURL = "https://target-b.example.com"
	env.store.cfg.Targets = append(env.store.cfg.Targets, targetBConfig)
	env.resolver.targets["target-b"] = targetResolution{
		adapter: targetB,
		capabilities: platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
			platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
		}},
	}
	env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{
		{UnitID: "u-a", AssetID: "source-a:channel:7:key:0", TargetID: "target-a", Status: syncservice.TargetSynced, ChannelID: "42", EffectiveModels: []string{"gpt-4.1"}, ExcludedModels: []string{}, Warnings: []string{}},
		{UnitID: "u-b", AssetID: "source-a:channel:7:key:0", TargetID: "target-b", Status: syncservice.TargetIncompatible, Code: "incompatible_target", EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{}},
	}}
	router := env.router(t)
	proof := "proof-request-only"
	body := `{"upstream_id":"source-a","units":[{"unit_id":"u-a","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}},{"unit_id":"u-b","asset_id":"source-a:channel:7:key:0","target_id":"target-b","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}],"grant":{"security_proof":"` + proof + `","allow_auth_file":false}}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/sync", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), proof) || strings.Contains(recorder.Body.String(), "security_proof") || strings.Contains(recorder.Body.String(), "allow_auth_file") {
		t.Fatalf("response leaked request-only grant: %s", recorder.Body.String())
	}
	if env.syncer.calls != 1 || env.syncer.sourceID != "source-a" || env.syncer.concurrency != 4 {
		t.Fatalf("sync call = %#v", env.syncer)
	}
	if len(env.syncer.multiRequest.Units) != 2 || env.syncer.multiRequest.Units[0].Target.ID != "target-a" || env.syncer.multiRequest.Units[1].Target.ID != "target-b" {
		t.Fatalf("units = %#v", env.syncer.multiRequest.Units)
	}
	if env.syncer.multiRequest.Grant.SecurityProof != proof || env.syncer.multiRequest.Grant.AllowAuthFile {
		t.Fatalf("grant = %#v", env.syncer.multiRequest.Grant)
	}
	results := dataObject(t, envelope)["units"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["status"] != "synced" || results[1].(map[string]any)["status"] != "incompatible" {
		t.Fatalf("results = %#v", results)
	}
}

func TestBatchSyncValidRequestReturnsPartialResultsDespiteServiceError(t *testing.T) {
	env := newTestEnvironment()
	env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{{
		UnitID: "u-1", AssetID: "source-a:channel:7:key:0", TargetID: "target-a", Status: syncservice.TargetNeedsReconcile,
		Code: "mapping_persist_failed", ChannelID: "42", Retryable: true, EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{},
	}}}
	env.syncer.err = errors.New("upstream response " + testSecret)
	body := `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}],"grant":{"security_proof":"proof"}}`
	recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), testSecret) {
		t.Fatalf("response leaked service error: %s", recorder.Body.String())
	}
	result := dataObject(t, envelope)["units"].([]any)[0].(map[string]any)
	if result["status"] != "needs_reconcile" || result["code"] != "mapping_persist_failed" || result["retryable"] != true {
		t.Fatalf("result = %#v", result)
	}
}

func TestBatchSyncRequestErrors(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "asset missing", body: staticSyncBody("u-1", "source-a", "missing", "target-a", 100), status: 404, code: "asset_not_found"},
		{name: "target missing", body: staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "missing", 100), status: 404, code: "target_not_found"},
		{name: "upstream missing", body: staticSyncBody("u-1", "missing", "source-a:channel:7:key:0", "target-a", 100), status: 404, code: "upstream_not_found"},
		{name: "empty units", body: `{"upstream_id":"source-a","units":[]}`, status: 400, code: "invalid_request"},
		{name: "duplicate models", body: `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1","gpt-4.1"],"target_group":"default","priority":0,"weight":100}}]}`, status: 400, code: "invalid_request"},
		{name: "negative weight", body: staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", -1), status: 400, code: "invalid_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, envelope := request(t, newTestEnvironment().router(t), http.MethodPost, "/api/v1/sync", test.body, "application/json")
			if recorder.Code != test.status || errorCode(t, envelope) != test.code {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestConcurrentSyncForSameTupleIsSerialized(t *testing.T) {
	env := newTestEnvironment()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	var active atomic.Int32
	var maximum atomic.Int32
	env.syncer.multiFn = func(_ context.Context, _ string, _ int, request syncservice.MultiRequest) (syncservice.MultiResult, error) {
		calls.Add(1)
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		unit := request.Units[0]
		return syncservice.MultiResult{Units: []syncservice.UnitResult{{UnitID: unit.UnitID, AssetID: unit.Asset.ID, TargetID: unit.Target.ID, Status: syncservice.TargetSynced, ChannelID: "42", EffectiveModels: []string{"gpt-4.1"}, ExcludedModels: []string{}, Warnings: []string{}}}}, nil
	}
	router := env.router(t)
	body := staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", 100)

	done := make(chan struct{}, 2)
	call := func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(httptest.NewRecorder(), req)
		done <- struct{}{}
	}
	go call()
	<-entered
	go call()
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("same tuple entered %d sync calls concurrently", calls.Load())
	}
	close(release)
	<-done
	<-done
	if maximum.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("maximum=%d calls=%d", maximum.Load(), calls.Load())
	}
}

func TestMatrixCombinesAssetsTargetsMappingsAndCompatibility(t *testing.T) {
	env := newTestEnvironment()
	unknown := platform.UpstreamAsset{
		ID: "source-a:channel:unknown", SourceID: "source-a", SourceType: "newapi", Provider: platform.ProviderUnknown,
		Kind: platform.AssetStaticAPIKey, Name: "unknown", Models: []string{}, Enabled: true,
	}
	snapshot := env.fakeDisc.snapshots["source-a"]
	snapshot.Assets = append(snapshot.Assets, unknown)
	env.fakeDisc.snapshots["source-a"] = snapshot
	env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{{
		UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42",
	}}

	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	if data["upstream_id"] != "source-a" || data["refreshed"] != true {
		t.Fatalf("matrix data=%#v", data)
	}
	rows := data["rows"].([]any)
	first := rows[0].(map[string]any)["cells"].([]any)[0].(map[string]any)
	second := rows[1].(map[string]any)["cells"].([]any)[0].(map[string]any)
	if first["status"] != "synced" || first["channel_id"] != "42" || second["status"] != "incompatible" {
		t.Fatalf("matrix cells first=%#v second=%#v", first, second)
	}
}

func TestReconcileAndAcceptDriftUseLiveTargetState(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42",
		Snapshot: platform.ChannelSnapshot{Models: []string{"old"}, Group: "default", Weight: 100},
	}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
	env.reconciler.report = reconcile.Report{TargetID: "target-a", Mappings: []reconcile.MappingState{{
		Mapping: mapping, Status: reconcile.StatusDrifted,
		Drift: map[string]reconcile.FieldDrift{"models": {Expected: []string{"old"}, Actual: []string{"gpt-4.1"}}},
	}}}
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.channels = []platform.Channel{{ID: "42", Name: "live", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true}}
	router := env.router(t)

	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")
	if recorder.Code != http.StatusOK || env.reconciler.checkCalls != 1 || env.reconciler.checkedID != "target-a" {
		t.Fatalf("reconcile status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if dataObject(t, envelope)["target_id"] != "target-a" {
		t.Fatalf("report=%#v", envelope)
	}

	acceptedMapping := mapping
	acceptedMapping.Snapshot = platform.SnapshotFromChannel(target.channels[0])
	env.reconciler.report = reconcile.Report{TargetID: "target-a", Mappings: []reconcile.MappingState{{
		Mapping: acceptedMapping, Status: reconcile.StatusSynced,
	}}}
	body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`
	recorder, envelope = request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/accept", body, "application/json")
	if recorder.Code != http.StatusOK || env.reconciler.acceptCalls != 1 || env.reconciler.checkCalls != 2 {
		t.Fatalf("accept status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if env.reconciler.accepted.UpstreamAssetID != mapping.UpstreamAssetID || env.reconciler.current.Models[0] != "gpt-4.1" {
		t.Fatalf("accepted mapping=%#v current=%#v", env.reconciler.accepted, env.reconciler.current)
	}
	accepted := dataObject(t, envelope)["mapping"].(map[string]any)
	snapshot := accepted["snapshot"].(map[string]any)
	if snapshot["models"].([]any)[0] != "gpt-4.1" {
		t.Fatalf("accepted response=%#v", accepted)
	}
}

func TestAcceptDriftRejectsMissingLiveChannelAndClientSnapshot(t *testing.T) {
	env := newTestEnvironment()
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{{UpstreamAssetID: "asset-a", TargetID: "target-a", TargetChannelID: "42"}}
	router := env.router(t)

	body := `{"upstream_asset_id":"asset-a","channel_id":"42"}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/accept", body, "application/json")
	if recorder.Code != http.StatusNotFound || errorCode(t, envelope) != "channel_not_found" {
		t.Fatalf("missing channel status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	withSnapshot := `{"upstream_asset_id":"asset-a","channel_id":"42","models":["client-value"]}`
	recorder, envelope = request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/accept", withSnapshot, "application/json")
	if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
		t.Fatalf("client snapshot status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRestoreDriftRestoresDesiredStateAndVerifiesTarget(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0",
		TargetID:        "target-a",
		TargetChannelID: "42",
		Snapshot: platform.ChannelSnapshot{
			Models: []string{"gpt-4.1", "gpt-4.1-mini"},
			Group:  "desired", Priority: 3, Weight: 120,
		},
	}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	current := platform.Channel{
		ID: "42", Name: "operator name", BaseURL: "https://runtime.example.com/v1",
		Models: []string{"wrong"}, Group: "manual", Priority: 9, Weight: 20, Enabled: false,
	}
	updated := platform.Channel{
		ID: "42", Name: current.Name, BaseURL: current.BaseURL,
		Models: []string{"gpt-4.1", "gpt-4.1-mini"}, Group: "desired", Priority: 3, Weight: 120, Enabled: false,
	}
	target.channels = []platform.Channel{current}
	target.updateFn = func(_ context.Context, id string, input platform.UpdateChannelInput) (platform.Channel, error) {
		if id != "42" {
			t.Fatalf("UpdateChannel id=%q", id)
		}
		target.channels = []platform.Channel{updated}
		return updated, nil
	}
	env.reconciler.checkFn = func(ctx context.Context, targetID string, checked platform.TargetAdapter) (reconcile.Report, error) {
		channels, err := checked.ListChannels(ctx)
		if err != nil {
			t.Fatalf("verification ListChannels() error = %v", err)
		}
		if targetID != "target-a" || len(channels) != 1 || channels[0].Weight != 120 || channels[0].Group != "desired" {
			t.Fatalf("verification target=%q channels=%#v", targetID, channels)
		}
		return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{{
			Mapping: mapping,
			Status:  reconcile.StatusSynced,
		}}}, nil
	}
	runtimeState := NewRuntime()
	router, err := NewRouterWithRuntime(env.dependencies(), runtimeState)
	if err != nil {
		t.Fatalf("NewRouterWithRuntime() error = %v", err)
	}

	body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/restore", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if target.listCalls != 2 || target.updateCalls != 1 || env.reconciler.checkCalls != 1 || env.reconciler.checkedID != "target-a" {
		t.Fatalf("list=%d update=%d check=%d checked=%q", target.listCalls, target.updateCalls, env.reconciler.checkCalls, env.reconciler.checkedID)
	}
	if target.updated.Name != current.Name || target.updated.BaseURL != current.BaseURL || target.updated.Enabled != current.Enabled ||
		strings.Join(target.updated.Models, ",") != "gpt-4.1,gpt-4.1-mini" || target.updated.Group != "desired" ||
		target.updated.Priority != 3 || target.updated.Weight != 120 {
		t.Fatalf("UpdateChannel input=%#v", target.updated)
	}
	stored := env.mappings.byTarget["target-a"][0]
	if env.mappings.updateCalls != 0 || strings.Join(stored.Snapshot.Models, ",") != "gpt-4.1,gpt-4.1-mini" ||
		stored.Snapshot.Group != "desired" || stored.Snapshot.Priority != 3 || stored.Snapshot.Weight != 120 {
		t.Fatalf("mapping was mutated: updates=%d mapping=%#v", env.mappings.updateCalls, stored)
	}
	data := dataObject(t, envelope)
	responseMapping := data["mapping"].(map[string]any)
	responseSnapshot := responseMapping["snapshot"].(map[string]any)
	responseChannel := data["channel"].(map[string]any)
	responseReport := data["report"].(map[string]any)
	if responseSnapshot["weight"] != float64(120) || responseChannel["name"] != current.Name ||
		responseChannel["base_url"] != current.BaseURL || responseChannel["enabled"] != false || responseReport["target_id"] != "target-a" {
		t.Fatalf("restore response=%#v", data)
	}
	if strings.Contains(recorder.Body.String(), testSecret) {
		t.Fatalf("restore response leaked a credential: %s", recorder.Body.String())
	}
	if pending, needsReconcile, differences := runtimeState.matrixState(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: mapping.TargetID}); needsReconcile || pending.channelID != "" || len(differences) != 0 {
		t.Fatalf("successful restore runtime pending=%#v needs=%v differences=%#v", pending, needsReconcile, differences)
	}
}

func TestRestoreDriftUsesStrictValidationAndStableNotFoundErrors(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		body  string
		setup func(*testEnvironment)
		code  string
		want  int
	}{
		{
			name: "unknown field", path: "/api/v1/targets/target-a/drift/restore",
			body: `{"upstream_asset_id":"asset-a","channel_id":"42","models":["client-value"]}`,
			code: "invalid_request", want: http.StatusBadRequest,
		},
		{
			name: "target", path: "/api/v1/targets/missing/drift/restore",
			body: `{"upstream_asset_id":"asset-a","channel_id":"42"}`,
			code: "target_not_found", want: http.StatusNotFound,
		},
		{
			name: "exact mapping", path: "/api/v1/targets/target-a/drift/restore",
			body: `{"upstream_asset_id":"asset-a","channel_id":"42"}`,
			setup: func(env *testEnvironment) {
				env.mappings.byTarget["target-a"] = []platform.SyncMapping{{
					UpstreamAssetID: "asset-a", TargetID: "target-b", TargetChannelID: "42",
				}}
			},
			code: "channel_not_found", want: http.StatusNotFound,
		},
		{
			name: "live channel", path: "/api/v1/targets/target-a/drift/restore",
			body: `{"upstream_asset_id":"asset-a","channel_id":"42"}`,
			setup: func(env *testEnvironment) {
				env.mappings.byTarget["target-a"] = []platform.SyncMapping{{
					UpstreamAssetID: "asset-a", TargetID: "target-a", TargetChannelID: "42",
				}}
			},
			code: "channel_not_found", want: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnvironment()
			if test.setup != nil {
				test.setup(env)
			}
			recorder, envelope := request(t, env.router(t), http.MethodPost, test.path, test.body, "application/json")
			if recorder.Code != test.want || errorCode(t, envelope) != test.code {
				t.Fatalf("status=%d code=%q body=%s", recorder.Code, errorCode(t, envelope), recorder.Body.String())
			}
		})
	}
}

func TestRestoreDriftVerificationConflictsPreserveRuntimeEvidence(t *testing.T) {
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42",
		Snapshot: platform.ChannelSnapshot{Models: []string{"gpt-4.1"}, Group: "default", Priority: 0, Weight: 100},
	}
	current := platform.Channel{
		ID: "42", Name: "live", BaseURL: "https://runtime.example.com", Models: []string{"gpt-4.1"},
		Group: "default", Priority: 0, Weight: 80, Enabled: true,
	}
	updated := current
	updated.Weight = 100
	key := runtimeKey{assetID: mapping.UpstreamAssetID, targetID: mapping.TargetID}
	body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`

	t.Run("check error", func(t *testing.T) {
		env := newTestEnvironment()
		env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
		target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
		target.channels = []platform.Channel{current}
		target.updateOut = updated
		env.reconciler.checkErr = errors.New("verification failed " + testSecret)
		runtimeState := NewRuntime()
		runtimeState.recordReconcileReport(reconcile.Report{TargetID: "target-a", Mappings: []reconcile.MappingState{{
			Mapping: mapping, Status: reconcile.StatusDrifted,
			Drift: map[string]reconcile.FieldDrift{"weight": {Expected: 100, Actual: 80}},
		}}}, []platform.Channel{current}, true)
		router, err := NewRouterWithRuntime(env.dependencies(), runtimeState)
		if err != nil {
			t.Fatalf("NewRouterWithRuntime() error = %v", err)
		}

		recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/restore", body, "application/json")
		if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" || strings.Contains(recorder.Body.String(), testSecret) {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		pending, needsReconcile, differences := runtimeState.matrixState(key)
		if !needsReconcile || pending.channelID != "42" || len(differences) != 1 || differences[0].Field != "weight" {
			t.Fatalf("runtime pending=%#v needs=%v differences=%#v", pending, needsReconcile, differences)
		}
		if target.updateCalls != 1 || env.reconciler.checkCalls != 1 || env.mappings.updateCalls != 0 {
			t.Fatalf("update=%d check=%d mapping updates=%d", target.updateCalls, env.reconciler.checkCalls, env.mappings.updateCalls)
		}
	})

	t.Run("selected mapping remains drifted", func(t *testing.T) {
		env := newTestEnvironment()
		env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
		target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
		target.channels = []platform.Channel{current}
		target.updateOut = updated
		env.reconciler.report = reconcile.Report{TargetID: "target-a", Mappings: []reconcile.MappingState{{
			Mapping: mapping, Status: reconcile.StatusDrifted,
			Drift: map[string]reconcile.FieldDrift{"weight": {Expected: 100, Actual: 90}},
		}}}
		runtimeState := NewRuntime()
		router, err := NewRouterWithRuntime(env.dependencies(), runtimeState)
		if err != nil {
			t.Fatalf("NewRouterWithRuntime() error = %v", err)
		}

		recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/restore", body, "application/json")
		if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		pending, needsReconcile, differences := runtimeState.matrixState(key)
		if needsReconcile || pending.channelID != "" || len(differences) != 1 || differences[0].Field != "weight" ||
			differences[0].Expected != 100 || differences[0].Actual != 90 {
			t.Fatalf("runtime pending=%#v needs=%v differences=%#v", pending, needsReconcile, differences)
		}
		if target.updateCalls != 1 || env.reconciler.checkCalls != 1 || env.mappings.updateCalls != 0 {
			t.Fatalf("update=%d check=%d mapping updates=%d", target.updateCalls, env.reconciler.checkCalls, env.mappings.updateCalls)
		}
	})
}

func TestNotFoundBodylessAndFallbackRoutesUseEnvelope(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)

	tests := []struct {
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{method: http.MethodGet, path: "/api/v1/targets/missing/channels", status: 404, code: "target_not_found"},
		{method: http.MethodPost, path: "/api/v1/upstreams/missing/refresh", status: 404, code: "upstream_not_found"},
		{method: http.MethodGet, path: "/api/v1/does-not-exist", status: 400, code: "invalid_request"},
		{method: http.MethodPatch, path: "/api/v1/config", status: 400, code: "invalid_request"},
		{method: http.MethodDelete, path: "/api/v1/targets/target-a", body: `{}`, status: 400, code: "invalid_request"},
	}
	for _, test := range tests {
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if recorder.Code != test.status {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
		var envelope map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if errorCode(t, envelope) != test.code || envelope["request_id"] == "" {
			t.Fatalf("%s %s envelope=%#v", test.method, test.path, envelope)
		}
	}
}

func TestPanicRecoveryDoesNotExposeStackOrHeaders(t *testing.T) {
	env := newTestEnvironment()
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.listFn = func(context.Context) ([]platform.Channel, error) {
		panic("Authorization: Bearer " + testSecret)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/targets/target-a/channels", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	recorder := httptest.NewRecorder()
	env.router(t).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), testSecret) || strings.Contains(recorder.Body.String(), "panic") || strings.Contains(recorder.Body.String(), "goroutine") {
		t.Fatalf("panic response leaked details: %s", recorder.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if errorCode(t, envelope) != "internal_error" || envelope["request_id"] == "" {
		t.Fatalf("envelope=%#v", envelope)
	}
}

func TestNewRouterRejectsMissingDependencies(t *testing.T) {
	_, err := NewRouter(Dependencies{})
	if err == nil {
		t.Fatal("NewRouter() accepted missing dependencies")
	}
}

func cloneConfig(cfg config.Config) config.Config {
	cloned := cfg
	cloned.Targets = append([]config.TargetConfig(nil), cfg.Targets...)
	for i := range cloned.Targets {
		if cfg.Targets[i].ValidatedAt != nil {
			validatedAt := *cfg.Targets[i].ValidatedAt
			cloned.Targets[i].ValidatedAt = &validatedAt
		}
		cloned.Targets[i].ValidationCapabilities = cloneTargetCapabilities(cfg.Targets[i].ValidationCapabilities)
	}
	cloned.Upstreams = append([]config.UpstreamConfig(nil), cfg.Upstreams...)
	for i := range cloned.Upstreams {
		cloned.Upstreams[i].Keys = append([]config.GenericKeyConfig(nil), cfg.Upstreams[i].Keys...)
		for j := range cloned.Upstreams[i].Keys {
			cloned.Upstreams[i].Keys[j].Models = append([]string(nil), cfg.Upstreams[i].Keys[j].Models...)
		}
		cloned.Upstreams[i].SyncMappings = cloneMappings(cfg.Upstreams[i].SyncMappings)
	}
	return cloned
}

func validationTime(year int, month time.Month, day int) *time.Time {
	value := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &value
}

func cloneTargetCapabilities(value platform.TargetCapabilities) platform.TargetCapabilities {
	cloned := value
	if value.Providers != nil {
		cloned.Providers = make(map[string]platform.ProviderCapability, len(value.Providers))
		for provider, capability := range value.Providers {
			capability.Modes = append([]platform.SyncMode(nil), capability.Modes...)
			cloned.Providers[provider] = capability
		}
	}
	return cloned
}

func cloneMappings(mappings []platform.SyncMapping) []platform.SyncMapping {
	cloned := append([]platform.SyncMapping(nil), mappings...)
	for i := range cloned {
		cloned[i].Snapshot.Models = append([]string(nil), mappings[i].Snapshot.Models...)
	}
	return cloned
}

func cloneDiscoverySnapshot(snapshot discovery.Snapshot) discovery.Snapshot {
	cloned := snapshot
	cloned.Assets = append([]platform.UpstreamAsset(nil), snapshot.Assets...)
	for i := range cloned.Assets {
		cloned.Assets[i].Models = append([]string(nil), snapshot.Assets[i].Models...)
		if snapshot.Assets[i].Metadata != nil {
			cloned.Assets[i].Metadata = make(map[string]string, len(snapshot.Assets[i].Metadata))
			for key, value := range snapshot.Assets[i].Metadata {
				cloned.Assets[i].Metadata[key] = value
			}
		}
	}
	if snapshot.GroupCatalog != nil {
		catalog := *snapshot.GroupCatalog
		catalog.Groups = append([]platform.UpstreamGroup(nil), snapshot.GroupCatalog.Groups...)
		for i := range catalog.Groups {
			catalog.Groups[i].Models = append([]string(nil), snapshot.GroupCatalog.Groups[i].Models...)
		}
		cloned.GroupCatalog = &catalog
	}
	return cloned
}
