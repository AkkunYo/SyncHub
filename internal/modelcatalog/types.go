package modelcatalog

import (
	"context"
	"errors"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/probe"
)

var (
	ErrInvalidRequest      = errors.New("invalid model catalog request")
	ErrKeyNotFound         = errors.New("upstream key not found")
	ErrModelUnavailable    = errors.New("model is unavailable for upstream key")
	ErrOperationInProgress = errors.New("model operation is already in progress")
	ErrUnsupported         = errors.New("model operation is unsupported")
)

type SnapshotScope string

const (
	SnapshotScopePersisted SnapshotScope = "persisted"
	SnapshotScopeRuntime   SnapshotScope = "runtime"
)

type SnapshotStatus string

const (
	SnapshotReady      SnapshotStatus = "ready"
	SnapshotEmpty      SnapshotStatus = "empty"
	SnapshotStale      SnapshotStatus = "stale"
	SnapshotUnverified SnapshotStatus = "unverified"
)

type DiscoveryStatus string

const (
	DiscoverySucceeded            DiscoveryStatus = "succeeded"
	DiscoveryEmpty                DiscoveryStatus = "empty"
	DiscoveryAuthenticationFailed DiscoveryStatus = "authentication_failed"
	DiscoveryRateLimited          DiscoveryStatus = "rate_limited"
	DiscoveryUnsupported          DiscoveryStatus = "unsupported"
	DiscoveryFailed               DiscoveryStatus = "failed"
)

type TaskStatus string

const (
	TaskSucceeded       TaskStatus = "succeeded"
	TaskPartiallyFailed TaskStatus = "partially_failed"
	TaskFailed          TaskStatus = "failed"
)

type ConfigStore interface {
	Snapshot() config.Config
	Update(context.Context, func(*config.Config) error) error
}

type Prober interface {
	Probe(context.Context, probe.Input) probe.Result
}

type Key struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Enabled           bool           `json:"enabled"`
	Source            string         `json:"source"`
	SourceGroup       string         `json:"source_group,omitempty"`
	CredentialPresent bool           `json:"credential_present"`
	ModelCount        int            `json:"model_count"`
	DiscoveryStatus   string         `json:"discovery_status"`
	SnapshotStatus    SnapshotStatus `json:"snapshot_status"`
	DiscoveredAt      *time.Time     `json:"discovered_at,omitempty"`

	assetID string
}

type DiscoveryItem struct {
	KeyID             string          `json:"key_id"`
	Status            DiscoveryStatus `json:"status"`
	ModelCount        int             `json:"model_count"`
	DiscoveredAt      *time.Time      `json:"discovered_at,omitempty"`
	ErrorCode         string          `json:"error_code,omitempty"`
	Retryable         bool            `json:"retryable"`
	RetryAfterSeconds int64           `json:"retry_after_seconds,omitempty"`
}

type DiscoveryTask struct {
	TaskID    string          `json:"task_id"`
	KeyIDs    []string        `json:"key_ids"`
	Completed bool            `json:"completed"`
	Status    TaskStatus      `json:"status"`
	Items     []DiscoveryItem `json:"items"`
}

type ModelProbe struct {
	KeyID             string         `json:"key_id"`
	Model             string         `json:"model"`
	Protocol          probe.Protocol `json:"protocol"`
	Status            probe.Status   `json:"status"`
	LatencyMS         int64          `json:"latency_ms"`
	CheckedAt         time.Time      `json:"checked_at"`
	ErrorCode         string         `json:"error_code"`
	Retryable         bool           `json:"retryable"`
	RetryAfterSeconds int64          `json:"retry_after_seconds,omitempty"`
	TemplateVersion   string         `json:"template_version"`
}

type Model struct {
	ID              string      `json:"id"`
	DiscoveryStatus string      `json:"discovery_status"`
	Probe           *ModelProbe `json:"probe,omitempty"`
}

type KeyModels struct {
	UpstreamID     string         `json:"upstream_id"`
	KeyID          string         `json:"key_id"`
	Models         []Model        `json:"models"`
	SnapshotStatus SnapshotStatus `json:"snapshot_status"`
	DiscoveredAt   *time.Time     `json:"discovered_at,omitempty"`
	SnapshotScope  SnapshotScope  `json:"snapshot_scope"`
	Verified       bool           `json:"verified"`
	Stale          bool           `json:"stale"`
}

func (models KeyModels) ModelIDs() []string {
	result := make([]string, len(models.Models))
	for index := range models.Models {
		result[index] = models.Models[index].ID
	}
	return result
}

type Catalog interface {
	ListKeys(context.Context, config.UpstreamConfig, platform.UpstreamAdapter) ([]Key, error)
	Discover(context.Context, config.UpstreamConfig, platform.UpstreamAdapter, []string) (DiscoveryTask, error)
	Models(upstreamID, keyID string) (KeyModels, bool)
	Probe(context.Context, config.UpstreamConfig, platform.UpstreamAdapter, string, string, probe.Protocol) (ModelProbe, error)
	MutateKey(context.Context, string, string, func() error) error
	MutateUpstream(context.Context, string, func() error) error
}
