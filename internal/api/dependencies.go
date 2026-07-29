package api

import (
	"context"
	"errors"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/discovery"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

var (
	// Dependency adapters may wrap these sentinels to request a stable API
	// classification without exposing their original error text.
	ErrTargetNotFound     = errors.New("target not found")
	ErrUpstreamNotFound   = errors.New("upstream not found")
	ErrAssetNotFound      = errors.New("asset not found")
	ErrChannelNotFound    = errors.New("channel not found")
	ErrResourceInUse      = errors.New("resource in use")
	ErrIncompatibleTarget = errors.New("incompatible target")
	ErrNeedsReconcile     = errors.New("needs reconcile")
	ErrSecretUnavailable  = errors.New("secret unavailable")
	ErrUpstreamFailure    = errors.New("upstream failure")
	ErrUpstreamTimeout    = errors.New("upstream timeout")
)

// ConfigStore is the atomic configuration boundary required by the API.
type ConfigStore interface {
	Snapshot() config.Config
	Update(ctx context.Context, mutate func(*config.Config) error) error
}

// AdapterResolver constructs platform interfaces from the current persisted
// configuration. Implementations keep concrete adapter details out of HTTP.
type AdapterResolver interface {
	ResolveTarget(ctx context.Context, cfg config.TargetConfig) (platform.TargetAdapter, platform.TargetCapabilities, error)
	ResolveUpstream(ctx context.Context, cfg config.UpstreamConfig) (platform.UpstreamAdapter, error)
	DiscoveryModeStatus(cfg config.UpstreamConfig) platform.DiscoveryModeStatus
}

type DiscoveryService interface {
	Refresh(ctx context.Context, sourceID string, adapter platform.UpstreamAdapter) (discovery.Snapshot, error)
	Snapshot(sourceID string) (discovery.Snapshot, bool)
}

// SyncService is a thin assembly boundary. T10 can create a sync.Service with
// mapping.Repository.ForSource(sourceID) and the current concurrency setting.
type SyncService interface {
	SyncUnits(ctx context.Context, sourceID string, concurrency int, request syncservice.MultiRequest) (syncservice.MultiResult, error)
}

type MappingRepository interface {
	ListMappings(ctx context.Context, targetID string) ([]platform.SyncMapping, error)
	DeleteMappings(ctx context.Context, mappings []platform.SyncMapping) error
	UpdateMapping(ctx context.Context, mapping platform.SyncMapping) error
}

type ReconcileService interface {
	Check(ctx context.Context, targetID string, target platform.TargetAdapter) (reconcile.Report, error)
	AcceptDrift(ctx context.Context, mapping platform.SyncMapping, current platform.Channel) error
}

type Dependencies struct {
	Config    ConfigStore
	Adapters  AdapterResolver
	Discovery DiscoveryService
	Sync      SyncService
	Mappings  MappingRepository
	Reconcile ReconcileService

	Version            string
	RequestIDGenerator func() string
}
