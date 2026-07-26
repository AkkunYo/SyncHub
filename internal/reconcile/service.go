package reconcile

import (
	"context"
	"slices"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

type Status string

const (
	StatusSynced  Status = "synced"
	StatusDrifted Status = "drifted"
	StatusRemoved Status = "removed"
)

type FieldDrift struct {
	Expected any `json:"expected"`
	Actual   any `json:"actual"`
}

type MappingState struct {
	Mapping platform.SyncMapping  `json:"mapping"`
	Status  Status                `json:"status"`
	Drift   map[string]FieldDrift `json:"drift,omitempty"`
}

type Report struct {
	TargetID string         `json:"target_id"`
	Mappings []MappingState `json:"mappings"`
}

type Repository interface {
	ListMappings(ctx context.Context, targetID string) ([]platform.SyncMapping, error)
	DeleteMappings(ctx context.Context, mappings []platform.SyncMapping) error
	UpdateMapping(ctx context.Context, mapping platform.SyncMapping) error
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Check(ctx context.Context, targetID string, target platform.TargetAdapter) (Report, error) {
	report := Report{TargetID: targetID, Mappings: []MappingState{}}
	mappings, err := s.repository.ListMappings(ctx, targetID)
	if err != nil {
		return report, err
	}

	channels, err := target.ListChannels(ctx)
	if err != nil {
		return report, err
	}
	channelsByID := make(map[string]platform.Channel, len(channels))
	for _, channel := range channels {
		channelsByID[channel.ID] = channel
	}

	report.Mappings = make([]MappingState, 0, len(mappings))
	missing := make([]platform.SyncMapping, 0)
	for _, mapping := range mappings {
		state := MappingState{Mapping: cloneMapping(mapping)}
		channel, exists := channelsByID[mapping.TargetChannelID]
		if !exists {
			state.Status = StatusRemoved
			missing = append(missing, mapping)
		} else if drift := compareSnapshot(mapping.Snapshot, channel); len(drift) != 0 {
			state.Status = StatusDrifted
			state.Drift = drift
		} else {
			state.Status = StatusSynced
		}
		report.Mappings = append(report.Mappings, state)
	}

	if len(missing) != 0 {
		if err := s.repository.DeleteMappings(ctx, missing); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (s *Service) AcceptDrift(ctx context.Context, mapping platform.SyncMapping, current platform.Channel) error {
	mapping.Snapshot = platform.SnapshotFromChannel(current)
	return s.repository.UpdateMapping(ctx, mapping)
}

func compareSnapshot(snapshot platform.ChannelSnapshot, current platform.Channel) map[string]FieldDrift {
	drift := make(map[string]FieldDrift)
	if !slices.Equal(snapshot.Models, current.Models) {
		drift["models"] = FieldDrift{
			Expected: slices.Clone(snapshot.Models),
			Actual:   slices.Clone(current.Models),
		}
	}
	if snapshot.Group != current.Group {
		drift["group"] = FieldDrift{Expected: snapshot.Group, Actual: current.Group}
	}
	if snapshot.Priority != current.Priority {
		drift["priority"] = FieldDrift{Expected: snapshot.Priority, Actual: current.Priority}
	}
	if snapshot.Weight != current.Weight {
		drift["weight"] = FieldDrift{Expected: snapshot.Weight, Actual: current.Weight}
	}
	return drift
}

func cloneMapping(mapping platform.SyncMapping) platform.SyncMapping {
	mapping.Snapshot.Models = slices.Clone(mapping.Snapshot.Models)
	return mapping
}
