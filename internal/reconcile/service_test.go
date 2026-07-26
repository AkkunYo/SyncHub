package reconcile

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestServiceRemovesMissingMappingsAndReportsDriftAfterCompleteTargetRead(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{mappings: []platform.SyncMapping{
		mapping("asset-1", "target", "channel-1", platform.ChannelSnapshot{Models: []string{"gpt-4.1"}, Group: "default", Priority: 0, Weight: 100}),
		mapping("asset-2", "target", "channel-2", platform.ChannelSnapshot{Models: []string{"claude-sonnet-4"}, Group: "default", Priority: 1, Weight: 100}),
		mapping("asset-3", "target", "channel-missing", platform.ChannelSnapshot{Models: []string{"gemini-2.5-pro"}, Group: "default", Priority: 0, Weight: 100}),
	}}
	target := &fakeTarget{channels: []platform.Channel{
		{ID: "channel-1", Models: []string{"gpt-4.1"}, Group: "default", Priority: 0, Weight: 100},
		{ID: "channel-2", Models: []string{"claude-sonnet-4", "claude-opus-4"}, Group: "default", Priority: 1, Weight: 50},
		{ID: "native-channel", Models: []string{"native"}, Group: "default", Priority: 0, Weight: 100},
	}}

	service := NewService(repository)
	report, err := service.Check(context.Background(), "target", target)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	assertState(t, report, "asset-1", StatusSynced)
	drifted := assertState(t, report, "asset-2", StatusDrifted)
	if _, ok := drifted.Drift["models"]; !ok {
		t.Fatalf("models drift missing: %#v", drifted.Drift)
	}
	if weight, ok := drifted.Drift["weight"]; !ok || weight.Expected != 100 || weight.Actual != 50 {
		t.Fatalf("weight drift = %#v", weight)
	}
	assertState(t, report, "asset-3", StatusRemoved)

	if len(repository.deleted) != 1 || repository.deleted[0].UpstreamAssetID != "asset-3" {
		t.Fatalf("deleted mappings = %#v", repository.deleted)
	}
	if len(repository.updated) != 0 {
		t.Fatalf("drift check unexpectedly accepted snapshots: %#v", repository.updated)
	}
}

func TestServicePreservesMappingsWhenTargetListFails(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{mappings: []platform.SyncMapping{
		mapping("asset-1", "target", "channel-1", platform.ChannelSnapshot{}),
	}}
	targetErr := errors.New("second page failed")
	service := NewService(repository)
	_, err := service.Check(context.Background(), "target", &fakeTarget{err: targetErr})
	if !errors.Is(err, targetErr) {
		t.Fatalf("Check() error = %v", err)
	}
	if len(repository.deleted) != 0 || len(repository.updated) != 0 {
		t.Fatalf("failed read mutated mappings: deleted=%#v updated=%#v", repository.deleted, repository.updated)
	}
}

func TestServiceAcceptDriftUpdatesOnlyTheSelectedMappingSnapshot(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{}
	service := NewService(repository)
	m := mapping("asset-2", "target", "channel-2", platform.ChannelSnapshot{Models: []string{"old"}, Weight: 100})
	current := platform.Channel{ID: "channel-2", Models: []string{"new-a", "new-b"}, Group: "premium", Priority: 4, Weight: 60}
	if err := service.AcceptDrift(context.Background(), m, current); err != nil {
		t.Fatalf("AcceptDrift() error = %v", err)
	}
	if len(repository.updated) != 1 {
		t.Fatalf("updated mappings = %#v", repository.updated)
	}
	want := platform.ChannelSnapshot{Models: []string{"new-a", "new-b"}, Group: "premium", Priority: 4, Weight: 60}
	if !reflect.DeepEqual(repository.updated[0].Snapshot, want) {
		t.Fatalf("accepted snapshot = %#v, want %#v", repository.updated[0].Snapshot, want)
	}
	current.Models[0] = "mutated-after-save"
	if repository.updated[0].Snapshot.Models[0] != "new-a" {
		t.Fatal("accepted snapshot retained caller-owned model slice")
	}
}

func mapping(assetID, targetID, channelID string, snapshot platform.ChannelSnapshot) platform.SyncMapping {
	return platform.SyncMapping{
		UpstreamAssetID: assetID,
		TargetID:        targetID,
		TargetChannelID: channelID,
		SourceProvider:  platform.ProviderOpenAI,
		AssetKind:       platform.AssetStaticAPIKey,
		Snapshot:        snapshot,
	}
}

func assertState(t *testing.T, report Report, assetID string, status Status) MappingState {
	t.Helper()
	for _, state := range report.Mappings {
		if state.Mapping.UpstreamAssetID == assetID {
			if state.Status != status {
				t.Fatalf("asset %s status = %s, want %s", assetID, state.Status, status)
			}
			return state
		}
	}
	t.Fatalf("asset %s missing from report %#v", assetID, report)
	return MappingState{}
}

type fakeRepository struct {
	mappings  []platform.SyncMapping
	deleted   []platform.SyncMapping
	updated   []platform.SyncMapping
	listErr   error
	deleteErr error
	updateErr error
}

func (r *fakeRepository) ListMappings(context.Context, string) ([]platform.SyncMapping, error) {
	return append([]platform.SyncMapping(nil), r.mappings...), r.listErr
}

func (r *fakeRepository) DeleteMappings(_ context.Context, mappings []platform.SyncMapping) error {
	r.deleted = append(r.deleted, mappings...)
	return r.deleteErr
}

func (r *fakeRepository) UpdateMapping(_ context.Context, mapping platform.SyncMapping) error {
	mapping.Snapshot.Models = append([]string(nil), mapping.Snapshot.Models...)
	r.updated = append(r.updated, mapping)
	return r.updateErr
}

type fakeTarget struct {
	channels []platform.Channel
	err      error
}

func (t *fakeTarget) ListChannels(context.Context) ([]platform.Channel, error) {
	return append([]platform.Channel(nil), t.channels...), t.err
}

func (t *fakeTarget) CreateChannel(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("not implemented in reconcile fake")
}

func (t *fakeTarget) UpdateChannel(context.Context, string, platform.UpdateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("not implemented in reconcile fake")
}

func (t *fakeTarget) DeleteChannel(context.Context, string) error {
	return errors.New("not implemented in reconcile fake")
}
