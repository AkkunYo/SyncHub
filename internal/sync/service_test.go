package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestServiceSynchronizesCompatibleTargetsWithBoundedConcurrency(t *testing.T) {
	t.Parallel()

	secretBytes := []byte("source-secret")
	source := &fakeSource{secret: platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: secretBytes}}
	store := &fakeMappingStore{}
	var active atomic.Int32
	var maximum atomic.Int32
	newTarget := func(id string, fail bool) *fakeTarget {
		return &fakeTarget{id: id, create: func(ctx context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
			current := active.Add(1)
			for {
				old := maximum.Load()
				if current <= old || maximum.CompareAndSwap(old, current) {
					break
				}
			}
			defer active.Add(-1)
			time.Sleep(15 * time.Millisecond)
			if got := string(input.Secret); got != "source-secret" {
				return platform.Channel{}, fmt.Errorf("received secret %q", got)
			}
			if fail {
				return platform.Channel{}, errors.New("upstream rejected channel")
			}
			return platform.Channel{ID: id + "-channel", Models: input.Models, Group: input.Group, Priority: input.Priority, Weight: input.Weight}, nil
		}}
	}
	compatible := platform.TargetCapabilities{
		Platform: "newapi",
		Providers: map[string]platform.ProviderCapability{
			platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
		},
	}
	incompatible := platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{}}

	service := NewService(store, Options{Concurrency: 2})
	result, err := service.Sync(context.Background(), BatchRequest{
		Asset: platform.UpstreamAsset{
			ID:         "source-a:channel:7",
			SourceID:   "source-a",
			SourceType: "newapi",
			Provider:   platform.ProviderOpenAI,
			Kind:       platform.AssetStaticAPIKey,
			Name:       "source channel",
			Enabled:    true,
		},
		Source: source,
		Grant:  platform.SecretGrant{SecurityProof: "proof"},
		Settings: platform.ChannelSettings{
			Models:   []string{"gpt-4.1"},
			Group:    "default",
			Priority: 3,
			Weight:   80,
		},
		Targets: []TargetRequest{
			{ID: "target-1", Adapter: newTarget("target-1", false), Capabilities: compatible},
			{ID: "target-2", Adapter: newTarget("target-2", true), Capabilities: compatible},
			{ID: "target-3", Adapter: newTarget("target-3", false), Capabilities: compatible},
			{ID: "target-4", Adapter: newTarget("target-4", false), Capabilities: incompatible},
		},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := maximum.Load(); got < 2 || got > 2 {
		t.Fatalf("maximum concurrency = %d, want 2", got)
	}
	if source.resolveCalls.Load() != 1 {
		t.Fatalf("ResolveSecret calls = %d", source.resolveCalls.Load())
	}
	assertTargetResult(t, result, "target-1", TargetSynced, "")
	assertTargetResult(t, result, "target-2", TargetFailed, "target_create_failed")
	assertTargetResult(t, result, "target-3", TargetSynced, "")
	assertTargetResult(t, result, "target-4", TargetIncompatible, "incompatible_target")

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.calls != 1 || len(store.mappings) != 2 {
		t.Fatalf("saved mappings calls=%d mappings=%#v", store.calls, store.mappings)
	}
	for _, mapping := range store.mappings {
		if mapping.TargetID == "target-2" || mapping.TargetID == "target-4" {
			t.Fatalf("failed target persisted: %#v", mapping)
		}
		if mapping.UpstreamAssetID != "source-a:channel:7" || mapping.Snapshot.Weight != 80 {
			t.Fatalf("mapping = %#v", mapping)
		}
	}
	for i, b := range secretBytes {
		if b != 0 {
			t.Fatalf("resolved secret byte %d not wiped", i)
		}
	}
}

func TestServiceDoesNotResolveSecretForDisabledOrFullyIncompatibleAsset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		asset platform.UpstreamAsset
		code  string
	}{
		{
			name:  "disabled",
			asset: platform.UpstreamAsset{ID: "disabled", Provider: platform.ProviderOpenAI, Kind: platform.AssetStaticAPIKey, Enabled: false},
			code:  "asset_disabled",
		},
		{
			name:  "unknown provider",
			asset: platform.UpstreamAsset{ID: "unknown", Provider: platform.ProviderUnknown, Kind: platform.AssetStaticAPIKey, Enabled: true},
			code:  "incompatible_target",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &fakeSource{secret: platform.ResolvedSecret{Bytes: []byte("must-not-resolve")}}
			store := &fakeMappingStore{}
			service := NewService(store, Options{Concurrency: 1})
			result, err := service.Sync(context.Background(), BatchRequest{
				Asset:  tt.asset,
				Source: source,
				Targets: []TargetRequest{{
					ID:      "target",
					Adapter: &fakeTarget{id: "target"},
					Capabilities: platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
						platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
					}},
				}},
			})
			if err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			if source.resolveCalls.Load() != 0 {
				t.Fatalf("ResolveSecret called %d times", source.resolveCalls.Load())
			}
			if len(result.Targets) != 1 || result.Targets[0].Code != tt.code {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestServiceMarksCreatedChannelsForReconcileWhenMappingPersistenceFails(t *testing.T) {
	t.Parallel()

	store := &fakeMappingStore{err: errors.New("disk full while writing source-secret")}
	source := &fakeSource{secret: platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: []byte("source-secret")}}
	target := &fakeTarget{id: "target", create: func(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
		return platform.Channel{ID: "created-42"}, nil
	}}
	service := NewService(store, Options{Concurrency: 1})
	result, err := service.Sync(context.Background(), BatchRequest{
		Asset:  platform.UpstreamAsset{ID: "asset", SourceID: "source", Provider: platform.ProviderOpenAI, Kind: platform.AssetStaticAPIKey, Enabled: true},
		Source: source,
		Targets: []TargetRequest{{
			ID:      "target",
			Adapter: target,
			Capabilities: platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
				platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
			}},
		}},
	})
	if !errors.Is(err, ErrMappingPersist) {
		t.Fatalf("Sync() error = %v", err)
	}
	if strings.Contains(err.Error(), "source-secret") {
		t.Fatalf("persistence error leaked secret: %v", err)
	}
	assertTargetResult(t, result, "target", TargetNeedsReconcile, "mapping_persist_failed")
	if result.Targets[0].ChannelID != "created-42" {
		t.Fatalf("created channel ID lost: %#v", result.Targets[0])
	}
}

func assertTargetResult(t *testing.T, result BatchResult, targetID string, status TargetStatus, code string) {
	t.Helper()
	for _, target := range result.Targets {
		if target.TargetID == targetID {
			if target.Status != status || target.Code != code {
				t.Fatalf("target %s result = %#v, want status=%s code=%s", targetID, target, status, code)
			}
			return
		}
	}
	t.Fatalf("target %s missing from %#v", targetID, result.Targets)
}

type fakeSource struct {
	secret       platform.ResolvedSecret
	err          error
	resolveCalls atomic.Int32
}

func (s *fakeSource) Capabilities(context.Context) (platform.SourceCapabilities, error) {
	return platform.SourceCapabilities{}, nil
}

func (s *fakeSource) ListAssets(context.Context, platform.PageCursor) (platform.AssetPage, error) {
	return platform.AssetPage{}, nil
}

func (s *fakeSource) ResolveSecret(context.Context, string, platform.SecretGrant) (platform.ResolvedSecret, error) {
	s.resolveCalls.Add(1)
	return s.secret, s.err
}

type fakeTarget struct {
	id     string
	create func(context.Context, platform.CreateChannelInput) (platform.Channel, error)
}

func (t *fakeTarget) ListChannels(context.Context) ([]platform.Channel, error) {
	return nil, nil
}

func (t *fakeTarget) CreateChannel(ctx context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
	if t.create != nil {
		return t.create(ctx, input)
	}
	return platform.Channel{ID: t.id + "-channel"}, nil
}

func (t *fakeTarget) UpdateChannel(context.Context, string, platform.UpdateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, nil
}

func (t *fakeTarget) DeleteChannel(context.Context, string) error { return nil }

type fakeMappingStore struct {
	mu       sync.Mutex
	mappings []platform.SyncMapping
	err      error
	calls    int
}

func (s *fakeMappingStore) SaveMappings(_ context.Context, mappings []platform.SyncMapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.mappings = append([]platform.SyncMapping(nil), mappings...)
	return s.err
}
