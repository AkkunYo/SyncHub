package reconcile

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestServiceReadsTargetOnceAndReportsEveryDriftFieldWithoutMutatingChannels(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{mappings: []platform.SyncMapping{
		mapping("asset-1", "target", "channel-1", platform.ChannelSnapshot{
			Models: []string{"old-model"}, Group: "default", Priority: 1, Weight: 100,
		}),
	}}
	target := &recordingTarget{channels: []platform.Channel{
		{ID: "channel-1", Models: []string{"new-model"}, Group: "premium", Priority: 4, Weight: 60},
		{ID: "native-channel", Models: []string{"native"}, Group: "default", Weight: 100},
	}}

	report, err := NewService(repository).Check(context.Background(), "target", target)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if target.listCalls != 1 {
		t.Fatalf("ListChannels() calls = %d, want 1", target.listCalls)
	}
	if target.createCalls != 0 || target.updateCalls != 0 || target.deleteCalls != 0 {
		t.Fatalf("Check() mutated target channels: create=%d update=%d delete=%d", target.createCalls, target.updateCalls, target.deleteCalls)
	}

	state := assertState(t, report, "asset-1", StatusDrifted)
	want := map[string]FieldDrift{
		"models":   {Expected: []string{"old-model"}, Actual: []string{"new-model"}},
		"group":    {Expected: "default", Actual: "premium"},
		"priority": {Expected: 1, Actual: 4},
		"weight":   {Expected: 100, Actual: 60},
	}
	if !reflect.DeepEqual(state.Drift, want) {
		t.Fatalf("drift = %#v, want %#v", state.Drift, want)
	}
}

func TestServicePropagatesMappingRepositoryErrors(t *testing.T) {
	t.Parallel()

	t.Run("list mappings", func(t *testing.T) {
		listErr := errors.New("list mappings failed")
		target := &recordingTarget{}
		_, err := NewService(&fakeRepository{listErr: listErr}).Check(context.Background(), "target", target)
		if !errors.Is(err, listErr) {
			t.Fatalf("Check() error = %v, want %v", err, listErr)
		}
		if target.listCalls != 0 {
			t.Fatalf("ListChannels() calls = %d after mapping list failure, want 0", target.listCalls)
		}
	})

	t.Run("delete mappings", func(t *testing.T) {
		deleteErr := errors.New("delete mappings failed")
		repository := &fakeRepository{
			mappings:  []platform.SyncMapping{mapping("asset-1", "target", "missing", platform.ChannelSnapshot{})},
			deleteErr: deleteErr,
		}
		_, err := NewService(repository).Check(context.Background(), "target", &recordingTarget{})
		if !errors.Is(err, deleteErr) {
			t.Fatalf("Check() error = %v, want %v", err, deleteErr)
		}
		if len(repository.deleted) != 1 {
			t.Fatalf("DeleteMappings() received %#v", repository.deleted)
		}
	})
}

func TestServiceAcceptDriftPropagatesUpdateErrorAndOwnsSnapshotModels(t *testing.T) {
	t.Parallel()

	updateErr := errors.New("update mapping failed")
	repository := &retainingRepository{updateErr: updateErr}
	current := platform.Channel{ID: "channel-1", Models: []string{"new-model"}}
	err := NewService(repository).AcceptDrift(
		context.Background(),
		mapping("asset-1", "target", "channel-1", platform.ChannelSnapshot{}),
		current,
	)
	if !errors.Is(err, updateErr) {
		t.Fatalf("AcceptDrift() error = %v, want %v", err, updateErr)
	}
	current.Models[0] = "mutated"
	if got := repository.updated.Snapshot.Models[0]; got != "new-model" {
		t.Fatalf("stored model = %q after caller mutation, want new-model", got)
	}
}

type recordingTarget struct {
	channels    []platform.Channel
	listCalls   int
	createCalls int
	updateCalls int
	deleteCalls int
}

func (t *recordingTarget) ListChannels(context.Context) ([]platform.Channel, error) {
	t.listCalls++
	return append([]platform.Channel(nil), t.channels...), nil
}

func (t *recordingTarget) CreateChannel(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
	t.createCalls++
	return platform.Channel{}, errors.New("unexpected CreateChannel call")
}

func (t *recordingTarget) UpdateChannel(context.Context, string, platform.UpdateChannelInput) (platform.Channel, error) {
	t.updateCalls++
	return platform.Channel{}, errors.New("unexpected UpdateChannel call")
}

func (t *recordingTarget) DeleteChannel(context.Context, string) error {
	t.deleteCalls++
	return errors.New("unexpected DeleteChannel call")
}

type retainingRepository struct {
	updated   platform.SyncMapping
	updateErr error
}

func (r *retainingRepository) ListMappings(context.Context, string) ([]platform.SyncMapping, error) {
	return nil, nil
}

func (r *retainingRepository) DeleteMappings(context.Context, []platform.SyncMapping) error {
	return nil
}

func (r *retainingRepository) UpdateMapping(_ context.Context, mapping platform.SyncMapping) error {
	r.updated = mapping
	return r.updateErr
}
