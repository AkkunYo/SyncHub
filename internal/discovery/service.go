package discovery

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

var (
	ErrCursorLoop              = errors.New("discovery cursor loop")
	ErrAssetConflict           = errors.New("discovery asset metadata conflict")
	ErrGroupCatalogUnsupported = errors.New("discovery group catalog unsupported")
	ErrGroupCatalogInvalid     = errors.New("discovery group catalog invalid")
)

// Snapshot is a complete metadata-only view of the assets exposed by one
// upstream source.
type Snapshot struct {
	SourceID     string                   `json:"source_id"`
	Assets       []platform.UpstreamAsset `json:"assets"`
	GroupCatalog *platform.GroupCatalog   `json:"group_catalog,omitempty"`
}

// Service keeps the latest successfully refreshed snapshot for each source.
type Service struct {
	mu        sync.RWMutex
	snapshots map[string]Snapshot
}

func NewService() *Service {
	return &Service{snapshots: make(map[string]Snapshot)}
}

// Refresh reads every asset page and publishes the result only after the
// entire traversal succeeds. It deliberately uses only ListAssets: resolving
// secrets is outside metadata discovery's responsibility.
func (s *Service) Refresh(ctx context.Context, sourceID string, adapter platform.UpstreamAdapter) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	capabilities, err := adapter.Capabilities(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("refresh capabilities for source %q: %w", sourceID, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	cursor := platform.PageCursor{}
	seenCursors := map[platform.PageCursor]struct{}{cursor: {}}
	assets := make([]platform.UpstreamAsset, 0)
	assetsByID := make(map[string]platform.UpstreamAsset)

	for {
		page, err := adapter.ListAssets(ctx, cursor)
		if err != nil {
			return Snapshot{}, fmt.Errorf("refresh assets for source %q: %w", sourceID, err)
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}

		if page.HasMore {
			if _, seen := seenCursors[page.Next]; seen {
				return Snapshot{}, ErrCursorLoop
			}
		}

		for _, asset := range page.Assets {
			if existing, exists := assetsByID[asset.ID]; exists {
				if !assetsEqual(existing, asset) {
					return Snapshot{}, fmt.Errorf("%w: asset %q", ErrAssetConflict, asset.ID)
				}
				continue
			}

			copied := cloneAsset(asset)
			assetsByID[copied.ID] = copied
			assets = append(assets, copied)
		}

		if !page.HasMore {
			break
		}
		seenCursors[page.Next] = struct{}{}
		cursor = page.Next
	}

	refreshed := Snapshot{SourceID: sourceID, Assets: assets}
	if capabilities.GroupCatalog {
		provider, ok := adapter.(platform.GroupCatalogProvider)
		if !ok {
			return Snapshot{}, fmt.Errorf("%w for source %q", ErrGroupCatalogUnsupported, sourceID)
		}
		catalog, err := provider.GroupCatalog(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("refresh group catalog for source %q: %w", sourceID, err)
		}
		if strings.TrimSpace(catalog.SourceID) != sourceID {
			return Snapshot{}, fmt.Errorf("%w for source %q", ErrGroupCatalogInvalid, sourceID)
		}
		refreshed.GroupCatalog = cloneGroupCatalog(&catalog)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	published := cloneSnapshot(refreshed)
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return Snapshot{}, err
	}
	if s.snapshots == nil {
		s.snapshots = make(map[string]Snapshot)
	}
	s.snapshots[sourceID] = published
	s.mu.Unlock()

	return cloneSnapshot(published), nil
}

// Snapshot returns an isolated copy of the latest complete snapshot.
func (s *Service) Snapshot(sourceID string) (Snapshot, bool) {
	s.mu.RLock()
	snapshot, ok := s.snapshots[sourceID]
	if ok {
		snapshot = cloneSnapshot(snapshot)
	}
	s.mu.RUnlock()
	return snapshot, ok
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := Snapshot{
		SourceID:     snapshot.SourceID,
		Assets:       make([]platform.UpstreamAsset, len(snapshot.Assets)),
		GroupCatalog: cloneGroupCatalog(snapshot.GroupCatalog),
	}
	for i, asset := range snapshot.Assets {
		cloned.Assets[i] = cloneAsset(asset)
	}
	return cloned
}

func cloneGroupCatalog(source *platform.GroupCatalog) *platform.GroupCatalog {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Groups = make([]platform.UpstreamGroup, len(source.Groups))
	for i, group := range source.Groups {
		cloned.Groups[i] = group
		cloned.Groups[i].Models = slices.Clone(group.Models)
	}
	return &cloned
}

func cloneAsset(asset platform.UpstreamAsset) platform.UpstreamAsset {
	asset.Models = slices.Clone(asset.Models)
	asset.Metadata = maps.Clone(asset.Metadata)
	return asset
}

func assetsEqual(left, right platform.UpstreamAsset) bool {
	return left.ID == right.ID &&
		left.SourceID == right.SourceID &&
		left.SourceType == right.SourceType &&
		left.Provider == right.Provider &&
		left.RawType == right.RawType &&
		left.Kind == right.Kind &&
		left.Name == right.Name &&
		left.BaseURL == right.BaseURL &&
		slices.Equal(left.Models, right.Models) &&
		left.Enabled == right.Enabled &&
		left.SecretReadable == right.SecretReadable &&
		maps.Equal(left.Metadata, right.Metadata)
}
