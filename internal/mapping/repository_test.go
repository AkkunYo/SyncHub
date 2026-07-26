package mapping_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/mapping"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

func TestRepositoryImplementsApplicationContracts(t *testing.T) {
	t.Parallel()

	var _ syncservice.MappingStore = (*mapping.SourceStore)(nil)
	var _ reconcile.Repository = (*mapping.Repository)(nil)
}

func TestSourceStoreSavesMappingsOnlyUnderItsBoundSourceAndUpserts(t *testing.T) {
	t.Parallel()

	store := openStore(t, nil)
	repository := mapping.NewRepository(store)
	source := repository.ForSource(" source-a ")
	first := testMapping("asset-1", "target-a", "channel-1", "model-a")
	second := testMapping("asset-1", "target-b", "channel-2", "model-b")

	if err := source.SaveMappings(context.Background(), []platform.SyncMapping{first, second}); err != nil {
		t.Fatalf("SaveMappings() error = %v", err)
	}
	first.Snapshot.Models[0] = "caller-mutated"

	assertSourceMappings(t, store, "source-a", []platform.SyncMapping{
		testMapping("asset-1", "target-a", "channel-1", "model-a"),
		second,
	})
	assertSourceMappings(t, store, "source-b", []platform.SyncMapping{})

	replacement := testMapping("asset-1", "target-a", "channel-replacement", "model-new")
	if err := source.SaveMappings(context.Background(), []platform.SyncMapping{replacement}); err != nil {
		t.Fatalf("upsert SaveMappings() error = %v", err)
	}
	assertSourceMappings(t, store, "source-a", []platform.SyncMapping{replacement, second})
}

func TestSourceStoreRejectsInvalidBatchesWithoutPartialPersistence(t *testing.T) {
	t.Parallel()

	store := openStore(t, nil)
	repository := mapping.NewRepository(store)
	valid := testMapping("asset-1", "target-a", "channel-1", "model-a")

	tests := []struct {
		name  string
		store *mapping.SourceStore
		input []platform.SyncMapping
		is    error
	}{
		{
			name:  "unknown source",
			store: repository.ForSource("missing-source"),
			input: []platform.SyncMapping{valid},
			is:    mapping.ErrSourceNotFound,
		},
		{
			name:  "blank source",
			store: repository.ForSource("  "),
			input: []platform.SyncMapping{valid},
			is:    mapping.ErrInvalidArgument,
		},
		{
			name:  "duplicate identity in one batch",
			store: repository.ForSource("source-a"),
			input: []platform.SyncMapping{valid, testMapping("asset-1", "target-a", "channel-2", "model-b")},
			is:    mapping.ErrDuplicateMapping,
		},
		{
			name:  "one invalid target makes whole batch invalid",
			store: repository.ForSource("source-a"),
			input: []platform.SyncMapping{valid, testMapping("asset-2", "missing-target", "channel-2", "model-b")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := store.Snapshot()
			err := tt.store.SaveMappings(context.Background(), tt.input)
			if err == nil {
				t.Fatal("SaveMappings() error = nil")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Fatalf("SaveMappings() error = %v, want %v", err, tt.is)
			}
			if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed batch changed config:\nafter:  %#v\nbefore: %#v", after, before)
			}
		})
	}
}

func TestRepositoryListsTargetMappingsAcrossSourcesAndReturnsDeepCopies(t *testing.T) {
	t.Parallel()

	want := []platform.SyncMapping{
		testMapping("source-a:asset-1", "target-a", "channel-1", "model-a"),
		testMapping("source-b:asset-2", "target-a", "channel-2", "model-b"),
	}
	store := openStore(t, func(cfg *config.Config) {
		cfg.Upstreams[0].SyncMappings = []config.SyncMapping{want[0]}
		cfg.Upstreams[1].SyncMappings = []config.SyncMapping{
			want[1],
			testMapping("source-b:asset-3", "target-b", "channel-3", "model-c"),
		}
	})
	repository := mapping.NewRepository(store)

	got, err := repository.ListMappings(context.Background(), " target-a ")
	if err != nil {
		t.Fatalf("ListMappings() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListMappings() = %#v, want %#v", got, want)
	}
	got[0].Snapshot.Models[0] = "caller-mutated"
	again, err := repository.ListMappings(context.Background(), "target-a")
	if err != nil {
		t.Fatalf("second ListMappings() error = %v", err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("stored mappings were mutated: %#v", again)
	}
}

func TestRepositoryDeletesExactMappingsAtomicallyAndIdempotently(t *testing.T) {
	t.Parallel()

	remove := testMapping("source-a:asset-1", "target-a", "channel-1", "model-a")
	keep := testMapping("source-a:asset-1", "target-a", "replacement-channel", "model-new")
	other := testMapping("source-b:asset-2", "target-a", "channel-2", "model-b")
	store := openStore(t, func(cfg *config.Config) {
		cfg.Upstreams[0].SyncMappings = []config.SyncMapping{remove, keep}
		cfg.Upstreams[1].SyncMappings = []config.SyncMapping{other}
	})
	repository := mapping.NewRepository(store)

	if err := repository.DeleteMappings(context.Background(), []platform.SyncMapping{remove}); err != nil {
		t.Fatalf("DeleteMappings() error = %v", err)
	}
	assertSourceMappings(t, store, "source-a", []platform.SyncMapping{keep})
	assertSourceMappings(t, store, "source-b", []platform.SyncMapping{other})
	if err := repository.DeleteMappings(context.Background(), []platform.SyncMapping{remove}); err != nil {
		t.Fatalf("idempotent DeleteMappings() error = %v", err)
	}
}

func TestRepositoryRejectsAmbiguousDeleteWithoutDeletingOtherMappings(t *testing.T) {
	t.Parallel()

	ambiguous := testMapping("shared-asset", "target-a", "channel-1", "model-a")
	unique := testMapping("source-a:unique", "target-b", "channel-2", "model-b")
	store := openStore(t, func(cfg *config.Config) {
		cfg.Upstreams[0].SyncMappings = []config.SyncMapping{ambiguous, unique}
		cfg.Upstreams[1].SyncMappings = []config.SyncMapping{ambiguous}
	})
	repository := mapping.NewRepository(store)
	before := store.Snapshot()

	err := repository.DeleteMappings(context.Background(), []platform.SyncMapping{unique, ambiguous})
	if !errors.Is(err, mapping.ErrAmbiguousMapping) {
		t.Fatalf("DeleteMappings() error = %v, want ErrAmbiguousMapping", err)
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("ambiguous delete partially changed config:\nafter:  %#v\nbefore: %#v", after, before)
	}
}

func TestRepositoryUpdatesOneExactMappingAndOwnsSnapshotModels(t *testing.T) {
	t.Parallel()

	existing := testMapping("source-a:asset-1", "target-a", "channel-1", "model-old")
	store := openStore(t, func(cfg *config.Config) {
		cfg.Upstreams[0].SyncMappings = []config.SyncMapping{existing}
	})
	repository := mapping.NewRepository(store)
	updated := existing
	updated.Snapshot = platform.ChannelSnapshot{Models: []string{"model-new"}, Group: "premium", Priority: 3, Weight: 70}

	if err := repository.UpdateMapping(context.Background(), updated); err != nil {
		t.Fatalf("UpdateMapping() error = %v", err)
	}
	updated.Snapshot.Models[0] = "caller-mutated"
	want := existing
	want.Snapshot = platform.ChannelSnapshot{Models: []string{"model-new"}, Group: "premium", Priority: 3, Weight: 70}
	assertSourceMappings(t, store, "source-a", []platform.SyncMapping{want})
}

func TestRepositoryReportsMissingAmbiguousAndCancelledOperations(t *testing.T) {
	t.Parallel()

	ambiguous := testMapping("shared-asset", "target-a", "channel-1", "model-a")
	store := openStore(t, func(cfg *config.Config) {
		cfg.Upstreams[0].SyncMappings = []config.SyncMapping{ambiguous}
		cfg.Upstreams[1].SyncMappings = []config.SyncMapping{ambiguous}
	})
	repository := mapping.NewRepository(store)

	if err := repository.UpdateMapping(context.Background(), testMapping("missing", "target-a", "missing", "model")); !errors.Is(err, mapping.ErrMappingNotFound) {
		t.Fatalf("missing UpdateMapping() error = %v, want ErrMappingNotFound", err)
	}
	if err := repository.UpdateMapping(context.Background(), ambiguous); !errors.Is(err, mapping.ErrAmbiguousMapping) {
		t.Fatalf("ambiguous UpdateMapping() error = %v, want ErrAmbiguousMapping", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.ListMappings(cancelled, "target-a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ListMappings() error = %v, want context.Canceled", err)
	}
	if err := repository.DeleteMappings(cancelled, []platform.SyncMapping{ambiguous}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled DeleteMappings() error = %v, want context.Canceled", err)
	}
	if err := repository.ForSource("source-a").SaveMappings(cancelled, []platform.SyncMapping{ambiguous}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SaveMappings() error = %v, want context.Canceled", err)
	}
}

func openStore(t *testing.T, mutate func(*config.Config)) *config.Store {
	t.Helper()

	store, err := config.Open(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatalf("config.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})
	err = store.Update(context.Background(), func(cfg *config.Config) error {
		cfg.Targets = []config.TargetConfig{
			{ID: "target-a", Name: "Target A", Type: "newapi", BaseURL: "https://target-a.example", AccessToken: "placeholder-target-a-token"},
			{ID: "target-b", Name: "Target B", Type: "newapi", BaseURL: "https://target-b.example", AccessToken: "placeholder-target-b-token"},
		}
		cfg.Upstreams = []config.UpstreamConfig{
			{ID: "source-a", Name: "Source A", Type: "newapi", BaseURL: "https://source-a.example", AccessToken: "placeholder-source-a-token"},
			{ID: "source-b", Name: "Source B", Type: "newapi", BaseURL: "https://source-b.example", AccessToken: "placeholder-source-b-token"},
		}
		if mutate != nil {
			mutate(cfg)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed Store.Update() error = %v", err)
	}
	return store
}

func testMapping(assetID, targetID, channelID, model string) platform.SyncMapping {
	return platform.SyncMapping{
		UpstreamAssetID: assetID,
		TargetID:        targetID,
		TargetChannelID: channelID,
		SourceProvider:  platform.ProviderOpenAI,
		AssetKind:       platform.AssetStaticAPIKey,
		Snapshot: platform.ChannelSnapshot{
			Models: []string{model},
			Group:  "default",
			Weight: 100,
		},
	}
}

func assertSourceMappings(t *testing.T, store *config.Store, sourceID string, want []platform.SyncMapping) {
	t.Helper()

	for _, upstream := range store.Snapshot().Upstreams {
		if upstream.ID == sourceID {
			got := []platform.SyncMapping(upstream.SyncMappings)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("source %s mappings = %#v, want %#v", sourceID, got, want)
			}
			return
		}
	}
	t.Fatalf("source %s not found", sourceID)
}
