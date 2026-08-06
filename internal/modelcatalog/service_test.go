package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/probe"
)

const catalogTestSecret = "catalog-test-secret"

func TestListKeysExposesLegacyGenericAPIKeyAsDefaultKey(t *testing.T) {
	store := newCatalogStore(config.Config{
		App: config.Default().App,
		Upstreams: []config.UpstreamConfig{{
			ID: "source-legacy", Name: "Legacy", Type: "generic",
			BaseURL: "https://provider.example.com", APIKey: "legacy-secret",
		}},
	})
	service := NewService(store, nil, &catalogProber{})

	keys, err := service.ListKeys(context.Background(), store.cfg.Upstreams[0], nil)
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0].ID != config.DefaultGenericKeyID || keys[0].Name != "Legacy" ||
		!keys[0].Enabled || !keys[0].CredentialPresent || keys[0].assetID != "source-legacy:endpoint" {
		t.Fatalf("legacy keys = %#v", keys)
	}
}

func TestGenericDiscoveryPersistsCompleteSnapshotsAndRetainsThemOnFailure(t *testing.T) {
	var responseMode atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+catalogTestSecret {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch responseMode.Load() {
		case 0:
			_, _ = w.Write([]byte(`{"data":[{"id":"model-b"},{"id":"model-a"},{"id":"model-a"}]}`))
		case 1:
			http.Error(w, "sensitive remote failure", http.StatusInternalServerError)
		case 2:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer server.Close()

	store := newCatalogStore(genericCatalogConfig(server.URL))
	adapter := &catalogUpstream{secrets: map[string]string{
		"source-generic:key:primary": catalogTestSecret,
	}}
	service := NewService(store, server.Client(), &catalogProber{})

	keys, err := service.ListKeys(context.Background(), store.cfg.Upstreams[0], adapter)
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0].ID != "primary" || keys[0].ModelCount != 1 || keys[0].CredentialPresent != true {
		t.Fatalf("keys = %#v", keys)
	}
	initial, ok := service.Models("source-generic", "primary")
	if !ok || !slices.Equal(initial.ModelIDs(), []string{"configured-model"}) || initial.SnapshotScope != SnapshotScopePersisted {
		t.Fatalf("initial snapshot = %#v, ok=%v", initial, ok)
	}

	task, err := service.Discover(context.Background(), store.cfg.Upstreams[0], adapter, []string{"primary"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if task.Status != TaskSucceeded || len(task.Items) != 1 || task.Items[0].Status != DiscoverySucceeded || task.Items[0].ModelCount != 2 {
		t.Fatalf("task = %#v", task)
	}
	if requests.Load() != 1 {
		t.Fatalf("request count = %d, want 1", requests.Load())
	}
	persisted := store.Snapshot().Upstreams[0].Keys[0].Models
	if !slices.Equal(persisted, []string{"model-a", "model-b"}) {
		t.Fatalf("persisted models = %#v", persisted)
	}
	snapshot, ok := service.Models("source-generic", "primary")
	if !ok || snapshot.Stale || !snapshot.Verified || !slices.Equal(snapshot.ModelIDs(), persisted) {
		t.Fatalf("successful snapshot = %#v, ok=%v", snapshot, ok)
	}

	responseMode.Store(1)
	failed, err := service.Discover(context.Background(), store.Snapshot().Upstreams[0], adapter, []string{"primary"})
	if err != nil {
		t.Fatalf("failed Discover() request error = %v", err)
	}
	if failed.Items[0].Status != DiscoveryFailed || failed.Items[0].Retryable {
		t.Fatalf("failed task = %#v", failed)
	}
	retained, _ := service.Models("source-generic", "primary")
	if !retained.Stale || !slices.Equal(retained.ModelIDs(), []string{"model-a", "model-b"}) {
		t.Fatalf("snapshot after failure = %#v", retained)
	}

	responseMode.Store(2)
	empty, err := service.Discover(context.Background(), store.Snapshot().Upstreams[0], adapter, []string{"primary"})
	if err != nil {
		t.Fatalf("empty Discover() request error = %v", err)
	}
	if empty.Items[0].Status != DiscoveryEmpty {
		t.Fatalf("empty task = %#v", empty)
	}
	retained, _ = service.Models("source-generic", "primary")
	if !retained.Stale || !slices.Equal(retained.ModelIDs(), []string{"model-a", "model-b"}) {
		t.Fatalf("snapshot after empty result = %#v", retained)
	}
	if got := store.Snapshot().Upstreams[0].Keys[0].Models; !slices.Equal(got, []string{"model-a", "model-b"}) {
		t.Fatalf("empty discovery overwrote config: %#v", got)
	}
}

func TestNewAPIKeysUseOrdinaryMetadataAndBatchSecretResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-23" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-23"}]}`))
	}))
	defer server.Close()

	upstream := config.UpstreamConfig{
		ID: "source-newapi", Name: "New API", Type: "newapi", BaseURL: server.URL,
		AccessToken: "ordinary-user-management-token", DiscoveryMode: config.DiscoveryModeToken,
	}
	store := newCatalogStore(config.Config{App: catalogAppConfig(), Upstreams: []config.UpstreamConfig{upstream}})
	adapter := &catalogUpstream{
		pages: []platform.AssetPage{{Assets: []platform.UpstreamAsset{
			newAPIKeyAsset("source-newapi", "17", "First", "vip"),
			newAPIKeyAsset("source-newapi", "23", "Second", "default"),
		}}},
		secrets: map[string]string{
			"source-newapi:token:17": "secret-17",
			"source-newapi:token:23": "secret-23",
		},
	}
	service := NewService(store, server.Client(), &catalogProber{})

	keys, err := service.ListKeys(context.Background(), upstream, adapter)
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	if len(keys) != 2 || keys[0].ID != "17" || keys[1].ID != "23" || keys[1].SourceGroup != "default" {
		t.Fatalf("keys = %#v", keys)
	}
	if adapter.resolveCalls != 0 || adapter.batchCalls != 0 {
		t.Fatalf("metadata listing resolved secrets: single=%d batch=%d", adapter.resolveCalls, adapter.batchCalls)
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ordinary-user-management-token", "secret-17", "secret-23", "masked-key"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("key response leaked %q: %s", forbidden, encoded)
		}
	}

	task, err := service.Discover(context.Background(), upstream, adapter, []string{"23"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(task.Items) != 1 || task.Items[0].KeyID != "23" || task.Items[0].Status != DiscoverySucceeded {
		t.Fatalf("task = %#v", task)
	}
	if adapter.batchCalls != 1 || adapter.resolveCalls != 0 || !slices.Equal(adapter.batchAssets, []string{"source-newapi:token:23"}) {
		t.Fatalf("secret calls: batch=%d single=%d assets=%#v", adapter.batchCalls, adapter.resolveCalls, adapter.batchAssets)
	}
	snapshot, ok := service.Models("source-newapi", "23")
	if !ok || snapshot.SnapshotScope != SnapshotScopeRuntime || !slices.Equal(snapshot.ModelIDs(), []string{"model-23"}) {
		t.Fatalf("snapshot = %#v, ok=%v", snapshot, ok)
	}
}

func TestProbeChecksCurrentKeySnapshotAndRejectsConcurrentUse(t *testing.T) {
	store := newCatalogStore(genericCatalogConfig("https://provider.example.com"))
	store.cfg.Upstreams[0].Keys = []config.GenericKeyConfig{
		{ID: "primary", Name: "Primary", APIKey: "secret-primary", Enabled: true, Models: []string{"model-a"}},
		{ID: "backup", Name: "Backup", APIKey: "secret-backup", Enabled: true, Models: []string{"model-a"}},
	}
	adapter := &catalogUpstream{secrets: map[string]string{
		"source-generic:key:primary": "secret-primary",
		"source-generic:key:backup":  "secret-backup",
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	prober := &catalogProber{
		started: started,
		release: release,
		result: probe.Result{
			Status: probe.StatusHealthy, Protocol: probe.ProtocolChatCompletions,
			Latency: 125 * time.Millisecond, CheckedAt: time.Date(2026, 8, 5, 6, 0, 0, 0, time.UTC),
			TemplateVersion: probe.TemplateVersion,
		},
	}
	service := NewService(store, http.DefaultClient, prober)
	if _, err := service.ListKeys(context.Background(), store.cfg.Upstreams[0], adapter); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Probe(context.Background(), store.cfg.Upstreams[0], adapter, "primary", "missing-model", probe.ProtocolAuto); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("missing model error = %v", err)
	}
	if adapter.resolveCalls != 0 || prober.calls() != 0 {
		t.Fatalf("missing model touched a secret or probe: resolve=%d probe=%d", adapter.resolveCalls, prober.calls())
	}

	resultChannel := make(chan ModelProbe, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := service.Probe(context.Background(), store.cfg.Upstreams[0], adapter, "primary", "model-a", probe.ProtocolAuto)
		resultChannel <- result
		errorChannel <- err
	}()
	<-started
	conflictStarted := time.Now()
	if _, err := service.Probe(context.Background(), store.cfg.Upstreams[0], adapter, "primary", "model-a", probe.ProtocolAuto); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("concurrent probe error = %v", err)
	}
	if elapsed := time.Since(conflictStarted); elapsed > 250*time.Millisecond {
		t.Fatalf("concurrent probe blocked for %s", elapsed)
	}
	close(release)
	result := <-resultChannel
	if err := <-errorChannel; err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if result.Status != probe.StatusHealthy || result.KeyID != "primary" || result.Model != "model-a" || result.TemplateVersion != probe.TemplateVersion {
		t.Fatalf("result = %#v", result)
	}
	if adapter.resolveCalls != 1 || adapter.resolvedAssets[0] != "source-generic:key:primary" {
		t.Fatalf("resolved assets = %#v", adapter.resolvedAssets)
	}
	if prober.lastInput.APIKey != "secret-primary" || prober.lastInput.Model != "model-a" {
		t.Fatalf("probe input = %#v", prober.lastInput)
	}
	snapshot, _ := service.Models("source-generic", "primary")
	if len(snapshot.Models) != 1 || snapshot.Models[0].Probe == nil || snapshot.Models[0].Probe.Status != probe.StatusHealthy {
		t.Fatalf("snapshot probe summary = %#v", snapshot)
	}
}

func TestGenericKeyMutationInvalidatesVerifiedSnapshotAndProbeHistory(t *testing.T) {
	store := newCatalogStore(genericCatalogConfig("https://provider.example.com"))
	service := NewService(store, http.DefaultClient, &catalogProber{})
	upstream := store.Snapshot().Upstreams[0]
	if _, err := service.ListKeys(context.Background(), upstream, &catalogUpstream{}); err != nil {
		t.Fatal(err)
	}
	resource := resourceKey{upstreamID: upstream.ID, keyID: "primary"}
	service.publish(resource, discoveredModels{
		models: []string{"verified-model"}, at: time.Date(2026, 8, 5, 7, 0, 0, 0, time.UTC),
	}, SnapshotScopePersisted)
	service.mu.Lock()
	verified := service.snapshots[resource]
	verified.probes["verified-model"] = ModelProbe{KeyID: "primary", Model: "verified-model", Status: probe.StatusHealthy}
	service.snapshots[resource] = verified
	service.mu.Unlock()

	err := service.MutateKey(context.Background(), upstream.ID, "primary", func() error {
		return store.Update(context.Background(), func(cfg *config.Config) error {
			cfg.Upstreams[0].Keys[0].APIKey = "rotated-secret"
			cfg.Upstreams[0].Keys[0].Models = []string{"manual-model"}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("MutateKey() error = %v", err)
	}
	if inherited, exists := service.Models(upstream.ID, "primary"); exists {
		t.Fatalf("mutation retained snapshot: %#v", inherited)
	}

	updated := store.Snapshot().Upstreams[0]
	if _, err := service.ListKeys(context.Background(), updated, &catalogUpstream{}); err != nil {
		t.Fatal(err)
	}
	current, exists := service.Models(upstream.ID, "primary")
	if !exists || current.SnapshotStatus != SnapshotUnverified || current.Verified ||
		!slices.Equal(current.ModelIDs(), []string{"manual-model"}) || current.Models[0].Probe != nil {
		t.Fatalf("updated snapshot = %#v, exists=%v", current, exists)
	}

	service.publish(resource, discoveredModels{models: []string{"deleted-model"}, at: time.Now()}, SnapshotScopePersisted)
	if err := service.MutateKey(context.Background(), upstream.ID, "primary", func() error { return nil }); err != nil {
		t.Fatalf("delete invalidation error = %v", err)
	}
	recreated := updated
	recreated.Keys[0].APIKey = "recreated-secret"
	recreated.Keys[0].Models = []string{"recreated-model"}
	if _, err := service.ListKeys(context.Background(), recreated, &catalogUpstream{}); err != nil {
		t.Fatal(err)
	}
	current, exists = service.Models(upstream.ID, "primary")
	if !exists || current.Verified || !slices.Equal(current.ModelIDs(), []string{"recreated-model"}) {
		t.Fatalf("recreated snapshot inherited old state: %#v, exists=%v", current, exists)
	}
}

func TestUpstreamMutationInvalidatesEveryKeyAndSerializesWithKeyOperations(t *testing.T) {
	store := newCatalogStore(genericCatalogConfig("https://provider.example.com"))
	store.cfg.Upstreams[0].Keys = append(store.cfg.Upstreams[0].Keys, config.GenericKeyConfig{
		ID: "backup", Name: "Backup", APIKey: "backup-secret", Enabled: true, Models: []string{"backup-model"},
	})
	service := NewService(store, http.DefaultClient, &catalogProber{})
	upstream := store.Snapshot().Upstreams[0]
	if _, err := service.ListKeys(context.Background(), upstream, &catalogUpstream{}); err != nil {
		t.Fatal(err)
	}
	service.publish(resourceKey{upstream.ID, "primary"}, discoveredModels{models: []string{"old-primary"}, at: time.Now()}, SnapshotScopePersisted)
	service.publish(resourceKey{upstream.ID, "backup"}, discoveredModels{models: []string{"old-backup"}, at: time.Now()}, SnapshotScopePersisted)

	active := resourceKey{upstream.ID, "primary"}
	if !service.limiter.tryKey(active) {
		t.Fatal("failed to establish active key fixture")
	}
	if err := service.MutateUpstream(context.Background(), upstream.ID, func() error { return nil }); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("upstream mutation during key operation error = %v", err)
	}
	service.limiter.releaseKey(active)

	err := service.MutateUpstream(context.Background(), upstream.ID, func() error {
		return store.Update(context.Background(), func(cfg *config.Config) error {
			cfg.Upstreams[0].BaseURL = "https://rotated.example.com"
			cfg.Upstreams[0].Keys[0].APIKey = "rotated-primary"
			return nil
		})
	})
	if err != nil {
		t.Fatalf("MutateUpstream() error = %v", err)
	}
	for _, keyID := range []string{"primary", "backup"} {
		if inherited, exists := service.Models(upstream.ID, keyID); exists {
			t.Fatalf("upstream mutation retained %s snapshot: %#v", keyID, inherited)
		}
	}

	recreated := store.Snapshot().Upstreams[0]
	recreated.Keys[0].Models = []string{"recreated-primary"}
	recreated.Keys[1].Models = []string{"recreated-backup"}
	if _, err := service.ListKeys(context.Background(), recreated, &catalogUpstream{}); err != nil {
		t.Fatal(err)
	}
	for _, keyID := range []string{"primary", "backup"} {
		current, exists := service.Models(upstream.ID, keyID)
		if !exists || current.Verified || current.SnapshotStatus != SnapshotUnverified {
			t.Fatalf("recreated %s inherited runtime state: %#v, exists=%v", keyID, current, exists)
		}
	}
}

func genericCatalogConfig(baseURL string) config.Config {
	return config.Config{
		App: catalogAppConfig(),
		Upstreams: []config.UpstreamConfig{{
			ID: "source-generic", Name: "Generic", Type: "generic", BaseURL: baseURL,
			Keys: []config.GenericKeyConfig{{
				ID: "primary", Name: "Primary", APIKey: catalogTestSecret, Enabled: true,
				Models: []string{"configured-model"},
			}},
		}},
	}
}

func catalogAppConfig() config.AppConfig {
	return config.AppConfig{
		Host: "127.0.0.1", Port: 8888, ReconcileInterval: config.Duration(time.Minute),
		RequestTimeout: config.Duration(15 * time.Second), SyncConcurrency: 4,
	}
}

func newAPIKeyAsset(upstreamID, tokenID, name, group string) platform.UpstreamAsset {
	return platform.UpstreamAsset{
		ID: upstreamID + ":token:" + tokenID, SourceID: upstreamID, SourceType: "newapi",
		Provider: platform.ProviderOpenAI, RawType: "newapi-token", Kind: platform.AssetProxyKey,
		Name: name, BaseURL: "https://newapi.example.com", Enabled: true, SecretReadable: true,
		Models: []string{}, Metadata: map[string]string{
			"token_id": tokenID, "upstream_group": group, "masked_key": "masked-key",
		},
	}
}

type catalogStore struct {
	mu  sync.Mutex
	cfg config.Config
}

func newCatalogStore(cfg config.Config) *catalogStore {
	return &catalogStore{cfg: cloneCatalogConfig(cfg)}
}

func (s *catalogStore) Snapshot() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCatalogConfig(s.cfg)
}

func (s *catalogStore) Update(ctx context.Context, mutate func(*config.Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := cloneCatalogConfig(s.cfg)
	if err := mutate(&next); err != nil {
		return err
	}
	if err := config.Validate(&next); err != nil {
		return err
	}
	s.cfg = cloneCatalogConfig(next)
	return nil
}

func cloneCatalogConfig(cfg config.Config) config.Config {
	cloned := cfg
	cloned.Targets = append([]config.TargetConfig(nil), cfg.Targets...)
	cloned.Upstreams = append([]config.UpstreamConfig(nil), cfg.Upstreams...)
	for index := range cloned.Upstreams {
		cloned.Upstreams[index].Keys = append([]config.GenericKeyConfig(nil), cfg.Upstreams[index].Keys...)
		for keyIndex := range cloned.Upstreams[index].Keys {
			cloned.Upstreams[index].Keys[keyIndex].Models = append([]string(nil), cfg.Upstreams[index].Keys[keyIndex].Models...)
		}
	}
	return cloned
}

type catalogUpstream struct {
	mu             sync.Mutex
	pages          []platform.AssetPage
	secrets        map[string]string
	resolveCalls   int
	batchCalls     int
	resolvedAssets []string
	batchAssets    []string
}

func (u *catalogUpstream) Capabilities(context.Context) (platform.SourceCapabilities, error) {
	return platform.SourceCapabilities{SecretResolution: true}, nil
}

func (u *catalogUpstream) ListAssets(_ context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	index := cursor.Page
	if index < 0 || index >= len(u.pages) {
		return platform.AssetPage{Assets: []platform.UpstreamAsset{}}, nil
	}
	return u.pages[index], nil
}

func (u *catalogUpstream) ResolveSecret(_ context.Context, assetID string, _ platform.SecretGrant) (platform.ResolvedSecret, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.resolveCalls++
	u.resolvedAssets = append(u.resolvedAssets, assetID)
	secret, ok := u.secrets[assetID]
	if !ok {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	return platform.ResolvedSecret{Kind: platform.AssetProxyKey, Bytes: []byte(secret)}, nil
}

func (u *catalogUpstream) ResolveSecrets(_ context.Context, assetIDs []string, _ platform.SecretGrant) (map[string]platform.ResolvedSecret, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.batchCalls++
	u.batchAssets = append([]string(nil), assetIDs...)
	result := make(map[string]platform.ResolvedSecret, len(assetIDs))
	for _, assetID := range assetIDs {
		secret, ok := u.secrets[assetID]
		if ok {
			result[assetID] = platform.ResolvedSecret{Kind: platform.AssetProxyKey, Bytes: []byte(secret)}
		}
	}
	return result, nil
}

func (u *catalogUpstream) MaxSecretBatchSize() int { return 100 }

type catalogProber struct {
	mu        sync.Mutex
	callCount int
	lastInput probe.Input
	started   chan struct{}
	release   chan struct{}
	result    probe.Result
}

func (p *catalogProber) Probe(ctx context.Context, input probe.Input) probe.Result {
	p.mu.Lock()
	p.callCount++
	p.lastInput = input
	started, release, result := p.started, p.release, p.result
	p.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return probe.Result{Status: probe.StatusCancelled, Protocol: input.Protocol}
		}
	}
	return result
}

func (p *catalogProber) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}
