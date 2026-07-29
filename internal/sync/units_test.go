package sync

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestSyncUnitsBatchesUniqueAssetsNarrowsModelsAndPreservesOrder(t *testing.T) {
	t.Parallel()

	source := &fakeBatchSource{max: 2}
	secretBackings := make(map[string][]byte)
	source.resolve = func(ids []string) (map[string]platform.ResolvedSecret, error) {
		result := make(map[string]platform.ResolvedSecret, len(ids))
		for _, id := range ids {
			bytes := []byte("secret-for-" + id)
			secretBackings[id] = bytes
			result[id] = platform.ResolvedSecret{Kind: platform.AssetProxyKey, Bytes: bytes}
		}
		return result, nil
	}
	store := &fakeMappingStore{}
	var createCalls atomic.Int32
	target := &fakeTarget{id: "target", create: func(_ context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
		createCalls.Add(1)
		return platform.Channel{
			ID: input.AssetID + "-channel", Models: append([]string(nil), input.Models...), Group: input.Group,
			Priority: input.Priority, Weight: input.Weight,
		}, nil
	}}
	group := &platform.UpstreamGroup{
		Name: "vip", Ratio: 1.5, RatioKnown: true,
		Models: []string{"gpt-4o", "gpt-4o-mini"}, ModelsVerified: true,
	}

	units := []UnitRequest{
		tokenUnit("u-1", "asset-1", "target-a", target, group, []string{"gpt-4o", "forbidden"}),
		tokenUnit("u-2", "asset-1", "target-b", target, group, nil),
		tokenUnit("u-3", "asset-2", "target-a", target, group, nil),
		tokenUnit("u-4", "asset-3", "target-a", target, group, nil),
		tokenUnit("u-5", "asset-4", "target-a", target, group, nil),
		tokenUnit("u-6", "asset-5", "target-a", target, group, nil),
	}
	result, err := NewService(store, Options{Concurrency: 3}).SyncUnits(context.Background(), MultiRequest{
		Source: source,
		Units:  units,
	})
	if err != nil {
		t.Fatalf("SyncUnits() error = %v", err)
	}
	if got := unitIDs(result.Units); !reflect.DeepEqual(got, []string{"u-1", "u-2", "u-3", "u-4", "u-5", "u-6"}) {
		t.Fatalf("result order = %#v", got)
	}
	for _, unit := range result.Units {
		if unit.Status != TargetSynced {
			t.Fatalf("unit result = %#v", unit)
		}
	}
	if !reflect.DeepEqual(result.Units[0].EffectiveModels, []string{"gpt-4o"}) || !reflect.DeepEqual(result.Units[0].ExcludedModels, []string{"forbidden"}) || !reflect.DeepEqual(result.Units[0].Warnings, []string{"models_out_of_group"}) {
		t.Fatalf("narrowed result = %#v", result.Units[0])
	}
	if !reflect.DeepEqual(result.Units[1].EffectiveModels, []string{"gpt-4o", "gpt-4o-mini"}) {
		t.Fatalf("default group models = %#v", result.Units[1].EffectiveModels)
	}
	if got := source.batchCalls(); !reflect.DeepEqual(got, [][]string{{"asset-1", "asset-2"}, {"asset-3", "asset-4"}, {"asset-5"}}) {
		t.Fatalf("secret batches = %#v", got)
	}
	if source.resolveCalls.Load() != 0 {
		t.Fatalf("single ResolveSecret calls = %d", source.resolveCalls.Load())
	}
	if createCalls.Load() != int32(len(units)) {
		t.Fatalf("CreateChannel calls = %d", createCalls.Load())
	}
	store.mu.Lock()
	if store.calls != 1 || len(store.mappings) != len(units) {
		t.Fatalf("stored mappings calls=%d mappings=%#v", store.calls, store.mappings)
	}
	for _, mapping := range store.mappings {
		if mapping.UpstreamGroup == nil || mapping.UpstreamGroup.Group != "vip" || !mapping.UpstreamGroup.ModelsVerified {
			t.Fatalf("mapping upstream group = %#v", mapping.UpstreamGroup)
		}
	}
	store.mu.Unlock()
	for id, bytes := range secretBackings {
		assertWiped(t, bytes)
		_ = id
	}
}

func TestSyncUnitsStopsAfterRateLimitedBatchAndKeepsEarlierSuccesses(t *testing.T) {
	t.Parallel()

	var call atomic.Int32
	source := &fakeBatchSource{max: 2}
	source.resolve = func(ids []string) (map[string]platform.ResolvedSecret, error) {
		if call.Add(1) == 2 {
			return nil, &platform.RateLimitError{RetryAfter: 17 * time.Second}
		}
		result := make(map[string]platform.ResolvedSecret, len(ids))
		for _, id := range ids {
			result[id] = platform.ResolvedSecret{Kind: platform.AssetProxyKey, Bytes: []byte("secret-" + id)}
		}
		return result, nil
	}
	store := &fakeMappingStore{}
	target := &fakeTarget{id: "target", create: func(_ context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
		return platform.Channel{ID: input.AssetID + "-channel", Models: input.Models}, nil
	}}
	group := &platform.UpstreamGroup{Name: "vip", Models: []string{"gpt-4o"}, ModelsVerified: true}
	units := make([]UnitRequest, 0, 5)
	for i := 1; i <= 5; i++ {
		units = append(units, tokenUnit("u-"+string(rune('0'+i)), "asset-"+string(rune('0'+i)), "target", target, group, nil))
	}

	result, err := NewService(store, Options{Concurrency: 2}).SyncUnits(context.Background(), MultiRequest{Source: source, Units: units})
	if err != nil {
		t.Fatalf("SyncUnits() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if result.Units[i].Status != TargetSynced {
			t.Fatalf("earlier unit %d = %#v", i, result.Units[i])
		}
	}
	for i := 2; i < 5; i++ {
		unit := result.Units[i]
		if unit.Status != TargetFailed || unit.Code != "rate_limited" || !unit.Retryable || unit.RetryAfterSeconds != 17 {
			t.Fatalf("rate-limited unit %d = %#v", i, unit)
		}
	}
	if got := source.batchCalls(); len(got) != 2 {
		t.Fatalf("secret batches after rate limit = %#v", got)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.mappings) != 2 {
		t.Fatalf("stored mappings = %#v", store.mappings)
	}
}

func TestSyncUnitsIsolatesGroupAndMissingSecretFailures(t *testing.T) {
	t.Parallel()

	source := &fakeBatchSource{max: 100, resolve: func(_ []string) (map[string]platform.ResolvedSecret, error) {
		return map[string]platform.ResolvedSecret{}, nil
	}}
	store := &fakeMappingStore{}
	var creates atomic.Int32
	target := &fakeTarget{id: "target", create: func(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
		creates.Add(1)
		return platform.Channel{ID: "unexpected"}, nil
	}}
	verified := &platform.UpstreamGroup{Name: "vip", Models: []string{"gpt-4o"}, ModelsVerified: true}
	mismatch := tokenUnit("mismatch", "asset-mismatch", "target", target, &platform.UpstreamGroup{Name: "default", ModelsVerified: true}, nil)
	required := tokenUnit("required", "asset-required", "target", target, nil, nil)
	missing := tokenUnit("missing", "asset-missing", "target", target, verified, nil)

	result, err := NewService(store, Options{Concurrency: 1}).SyncUnits(context.Background(), MultiRequest{
		Source: source, Units: []UnitRequest{mismatch, required, missing},
	})
	if err != nil {
		t.Fatalf("SyncUnits() error = %v", err)
	}
	wantCodes := []string{"group_mismatch", "group_required", "secret_unavailable"}
	for i, want := range wantCodes {
		if result.Units[i].Status != TargetFailed || result.Units[i].Code != want {
			t.Fatalf("unit %d = %#v, want %s", i, result.Units[i], want)
		}
	}
	if creates.Load() != 0 {
		t.Fatalf("CreateChannel calls = %d", creates.Load())
	}
	if got := source.batchCalls(); !reflect.DeepEqual(got, [][]string{{"asset-missing"}}) {
		t.Fatalf("secret batches = %#v", got)
	}
}

func TestSyncUnitsRejectsDuplicateUnitIdentityBeforeExternalCalls(t *testing.T) {
	t.Parallel()

	source := &fakeBatchSource{max: 100}
	store := &fakeMappingStore{}
	target := &fakeTarget{id: "target"}
	group := &platform.UpstreamGroup{Name: "vip", ModelsVerified: true}
	unit := tokenUnit("u-1", "asset-1", "target", target, group, nil)
	duplicate := unit
	duplicate.UnitID = "u-2"
	result, err := NewService(store, Options{}).SyncUnits(context.Background(), MultiRequest{Source: source, Units: []UnitRequest{unit, duplicate}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SyncUnits() error = %v, want ErrInvalidRequest", err)
	}
	for _, item := range result.Units {
		if item.Status != TargetFailed || item.Code != "invalid_request" {
			t.Fatalf("invalid result = %#v", item)
		}
	}
	if len(source.batchCalls()) != 0 || source.resolveCalls.Load() != 0 {
		t.Fatal("invalid request resolved secrets")
	}
}

type fakeBatchSource struct {
	fakeSource
	max     int
	mu      sync.Mutex
	batches [][]string
	resolve func([]string) (map[string]platform.ResolvedSecret, error)
}

func (s *fakeBatchSource) MaxSecretBatchSize() int { return s.max }

func (s *fakeBatchSource) ResolveSecrets(_ context.Context, ids []string, _ platform.SecretGrant) (map[string]platform.ResolvedSecret, error) {
	s.mu.Lock()
	s.batches = append(s.batches, append([]string(nil), ids...))
	s.mu.Unlock()
	if s.resolve == nil {
		return nil, errors.New("unexpected batch resolution")
	}
	return s.resolve(ids)
}

func (s *fakeBatchSource) batchCalls() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([][]string, len(s.batches))
	for i := range s.batches {
		result[i] = append([]string(nil), s.batches[i]...)
	}
	return result
}

func tokenUnit(unitID, assetID, targetID string, target platform.TargetAdapter, group *platform.UpstreamGroup, models []string) UnitRequest {
	return UnitRequest{
		UnitID: unitID,
		Asset: platform.UpstreamAsset{
			ID: assetID, SourceID: "source-a", SourceType: "newapi", Provider: platform.ProviderOpenAI,
			RawType: "newapi-token", Kind: platform.AssetProxyKey, Name: assetID, BaseURL: "https://upstream.example", Enabled: true, SecretReadable: true,
			Metadata: map[string]string{"upstream_group": "vip"},
		},
		Target:        TargetRequest{ID: targetID, Adapter: target, Capabilities: proxyCapabilities()},
		UpstreamGroup: group,
		Settings:      platform.ChannelSettings{Models: models, Group: "default", Priority: 0, Weight: 100},
	}
}

func proxyCapabilities() platform.TargetCapabilities {
	return platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
		platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeProxyEndpoint}},
	}}
}

func unitIDs(results []UnitResult) []string {
	ids := make([]string, len(results))
	for i := range results {
		ids[i] = results[i].UnitID
	}
	return ids
}
