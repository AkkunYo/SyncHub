package mapping

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

var (
	ErrInvalidArgument  = errors.New("invalid mapping repository argument")
	ErrStoreUnavailable = errors.New("mapping repository store is unavailable")
	ErrSourceNotFound   = errors.New("mapping source not found")
	ErrMappingNotFound  = errors.New("mapping not found")
	ErrAmbiguousMapping = errors.New("mapping identity is ambiguous")
	ErrDuplicateMapping = errors.New("mapping is duplicated in batch")
)

// Repository exposes all persisted mappings for reconciliation operations.
type Repository struct {
	store *config.Store
}

// SourceStore binds synchronization writes to one upstream. The source is
// explicit because SyncMapping is nested beneath its upstream in YAML.
type SourceStore struct {
	repository *Repository
	sourceID   string
}

func NewRepository(store *config.Store) *Repository {
	return &Repository{store: store}
}

func (r *Repository) ForSource(sourceID string) *SourceStore {
	return &SourceStore{repository: r, sourceID: strings.TrimSpace(sourceID)}
}

func (s *SourceStore) SaveMappings(ctx context.Context, mappings []platform.SyncMapping) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if s == nil || s.repository == nil || s.repository.store == nil {
		return ErrStoreUnavailable
	}
	if s.sourceID == "" {
		return ErrInvalidArgument
	}
	incoming, err := normalizeMappings(mappings, upsertIdentity)
	if err != nil {
		return err
	}
	if len(incoming) == 0 {
		return nil
	}

	err = s.repository.store.Update(ctx, func(cfg *config.Config) error {
		upstreamIndex := -1
		for i := range cfg.Upstreams {
			if cfg.Upstreams[i].ID == s.sourceID {
				upstreamIndex = i
				break
			}
		}
		if upstreamIndex < 0 {
			return ErrSourceNotFound
		}

		stored := cfg.Upstreams[upstreamIndex].SyncMappings
		positions := make(map[string]int, len(stored))
		for i := range stored {
			key := upsertIdentity(stored[i])
			if _, exists := positions[key]; exists {
				return fmt.Errorf("%w: %s", ErrAmbiguousMapping, key)
			}
			positions[key] = i
		}
		for _, mapping := range incoming {
			key := upsertIdentity(mapping)
			if index, exists := positions[key]; exists {
				stored[index] = config.SyncMapping(cloneMapping(mapping))
				continue
			}
			positions[key] = len(stored)
			stored = append(stored, config.SyncMapping(cloneMapping(mapping)))
		}
		cfg.Upstreams[upstreamIndex].SyncMappings = stored
		return nil
	})
	if err != nil {
		return fmt.Errorf("save mappings for source %q: %w", s.sourceID, err)
	}
	return nil
}

func (r *Repository) ListMappings(ctx context.Context, targetID string) ([]platform.SyncMapping, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if r == nil || r.store == nil {
		return nil, ErrStoreUnavailable
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil, ErrInvalidArgument
	}

	snapshot := r.store.Snapshot()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mappings := make([]platform.SyncMapping, 0)
	for _, upstream := range snapshot.Upstreams {
		for _, mapping := range upstream.SyncMappings {
			if mapping.TargetID == targetID {
				mappings = append(mappings, cloneMapping(mapping))
			}
		}
	}
	return mappings, nil
}

func (r *Repository) DeleteMappings(ctx context.Context, mappings []platform.SyncMapping) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if r == nil || r.store == nil {
		return ErrStoreUnavailable
	}
	requested, err := normalizeMappings(mappings, exactIdentity)
	if err != nil {
		return err
	}
	if len(requested) == 0 {
		return nil
	}

	err = r.store.Update(ctx, func(cfg *config.Config) error {
		counts := mappingIdentityCounts(cfg)
		deletions := make(map[string]struct{}, len(requested))
		for _, mapping := range requested {
			key := exactIdentity(mapping)
			if counts[key] > 1 {
				return fmt.Errorf("%w: %s", ErrAmbiguousMapping, key)
			}
			if counts[key] == 1 {
				deletions[key] = struct{}{}
			}
		}

		for upstreamIndex := range cfg.Upstreams {
			stored := cfg.Upstreams[upstreamIndex].SyncMappings
			kept := stored[:0]
			for _, mapping := range stored {
				if _, remove := deletions[exactIdentity(mapping)]; !remove {
					kept = append(kept, mapping)
				}
			}
			cfg.Upstreams[upstreamIndex].SyncMappings = kept
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete mappings: %w", err)
	}
	return nil
}

func (r *Repository) UpdateMapping(ctx context.Context, mapping platform.SyncMapping) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if r == nil || r.store == nil {
		return ErrStoreUnavailable
	}
	normalized, err := normalizeMapping(mapping)
	if err != nil {
		return err
	}
	key := exactIdentity(normalized)

	err = r.store.Update(ctx, func(cfg *config.Config) error {
		count := 0
		upstreamIndex := -1
		mappingIndex := -1
		for i := range cfg.Upstreams {
			for j := range cfg.Upstreams[i].SyncMappings {
				if exactIdentity(cfg.Upstreams[i].SyncMappings[j]) == key {
					count++
					upstreamIndex = i
					mappingIndex = j
				}
			}
		}
		switch {
		case count == 0:
			return ErrMappingNotFound
		case count > 1:
			return fmt.Errorf("%w: %s", ErrAmbiguousMapping, key)
		default:
			cfg.Upstreams[upstreamIndex].SyncMappings[mappingIndex] = config.SyncMapping(cloneMapping(normalized))
			return nil
		}
	})
	if err != nil {
		return fmt.Errorf("update mapping: %w", err)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidArgument
	}
	return ctx.Err()
}

func normalizeMappings(mappings []platform.SyncMapping, identity func(platform.SyncMapping) string) ([]platform.SyncMapping, error) {
	normalized := make([]platform.SyncMapping, len(mappings))
	seen := make(map[string]struct{}, len(mappings))
	for i, mapping := range mappings {
		item, err := normalizeMapping(mapping)
		if err != nil {
			return nil, err
		}
		key := identity(item)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateMapping, key)
		}
		seen[key] = struct{}{}
		normalized[i] = item
	}
	return normalized, nil
}

func normalizeMapping(mapping platform.SyncMapping) (platform.SyncMapping, error) {
	mapping.UpstreamAssetID = strings.TrimSpace(mapping.UpstreamAssetID)
	mapping.TargetID = strings.TrimSpace(mapping.TargetID)
	mapping.TargetChannelID = strings.TrimSpace(mapping.TargetChannelID)
	if mapping.UpstreamAssetID == "" || mapping.TargetID == "" || mapping.TargetChannelID == "" {
		return platform.SyncMapping{}, ErrInvalidArgument
	}
	return cloneMapping(mapping), nil
}

func mappingIdentityCounts(cfg *config.Config) map[string]int {
	counts := make(map[string]int)
	for _, upstream := range cfg.Upstreams {
		for _, mapping := range upstream.SyncMappings {
			counts[exactIdentity(mapping)]++
		}
	}
	return counts
}

func upsertIdentity(mapping platform.SyncMapping) string {
	return mapping.UpstreamAssetID + "\x00" + mapping.TargetID
}

func exactIdentity(mapping platform.SyncMapping) string {
	return upsertIdentity(mapping) + "\x00" + mapping.TargetChannelID
}

func cloneMapping(mapping platform.SyncMapping) platform.SyncMapping {
	mapping.Snapshot.Models = append([]string(nil), mapping.Snapshot.Models...)
	return mapping
}
