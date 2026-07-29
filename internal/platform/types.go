package platform

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrIncompatibleTarget  = errors.New("incompatible target")
	ErrSecretGrantRequired = errors.New("secret grant required")
	ErrSecretUnavailable   = errors.New("secret unavailable")
	ErrAssetDisabled       = errors.New("asset disabled")
	ErrRateLimited         = errors.New("upstream rate limited")
	ErrGroupRequired       = errors.New("upstream group is required")
)

// RateLimitError preserves the upstream's retry guidance while remaining
// compatible with errors.Is(err, ErrRateLimited).
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return ErrRateLimited.Error()
}

func (e *RateLimitError) Unwrap() error {
	return ErrRateLimited
}

type AssetKind string

const (
	AssetStaticAPIKey AssetKind = "static_api_key"
	AssetOAuthFile    AssetKind = "oauth_auth_file"
	AssetProxyKey     AssetKind = "proxy_endpoint_key"
)

type SyncMode string

const (
	SyncModeStaticKey      SyncMode = "static_key"
	SyncModeNativeAuthFile SyncMode = "native_auth_file"
	SyncModeProxyEndpoint  SyncMode = "proxy_endpoint"
)

type PageCursor struct {
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
	Token    string `json:"token,omitempty"`
}

type AssetPage struct {
	Assets  []UpstreamAsset `json:"assets"`
	Next    PageCursor      `json:"next,omitempty"`
	HasMore bool            `json:"has_more"`
}

type UpstreamAsset struct {
	ID             string            `json:"id"`
	SourceID       string            `json:"source_id"`
	SourceType     string            `json:"source_type"`
	Provider       string            `json:"provider"`
	RawType        string            `json:"raw_type"`
	Kind           AssetKind         `json:"kind"`
	Name           string            `json:"name"`
	BaseURL        string            `json:"base_url,omitempty"`
	Models         []string          `json:"models"`
	Enabled        bool              `json:"enabled"`
	SecretReadable bool              `json:"secret_readable"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type SourceCapabilities struct {
	AssetKinds       []AssetKind `json:"asset_kinds"`
	SecretResolution bool        `json:"secret_resolution"`
	GroupCatalog     bool        `json:"group_catalog"`
}

// UpstreamGroup describes one scheduling group an upstream exposes. Ratio and
// Models drive the operator's cost decision, so both carry an explicit
// "is this value trustworthy" flag instead of a silent zero value.
type UpstreamGroup struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Ratio          float64  `json:"ratio"`
	RatioKnown     bool     `json:"ratio_known"`
	Models         []string `json:"models"`
	ModelsVerified bool     `json:"models_verified"`
	Auto           bool     `json:"auto"`
}

// GroupCatalog is the full set of groups the configured credential may use.
type GroupCatalog struct {
	SourceID     string          `json:"source_id"`
	DefaultGroup string          `json:"default_group,omitempty"`
	Groups       []UpstreamGroup `json:"groups"`
}

// GroupCatalogProvider is implemented by upstream adapters whose assets are
// scoped to a billing group the operator must choose explicitly.
type GroupCatalogProvider interface {
	GroupCatalog(ctx context.Context) (GroupCatalog, error)
}

// DiscoveryModeStatus is a sanitized view of runtime mode probing. ErrorCode
// is stable API metadata and never contains an upstream response body.
type DiscoveryModeStatus struct {
	EffectiveMode string `json:"effective_discovery_mode"`
	Status        string `json:"mode_status"`
	ErrorCode     string `json:"mode_error_code,omitempty"`
}

type DiscoveryModeStatusProvider interface {
	DiscoveryModeStatus() DiscoveryModeStatus
}

type SecretGrant struct {
	SecurityProof string `json:"-"`
	AllowAuthFile bool   `json:"-"`
}

type ResolvedSecret struct {
	Kind        AssetKind         `json:"-"`
	Bytes       []byte            `json:"-"`
	ContentType string            `json:"-"`
	Metadata    map[string]string `json:"-"`
}

func (s *ResolvedSecret) Wipe() {
	if s == nil {
		return
	}
	for i := range s.Bytes {
		s.Bytes[i] = 0
	}
}

type ProviderCapability struct {
	Modes []SyncMode `json:"modes"`
}

type TargetCapabilities struct {
	Platform         string                        `json:"platform"`
	NativeAuthSchema string                        `json:"native_auth_schema,omitempty"`
	Providers        map[string]ProviderCapability `json:"providers"`
}

type ChannelSettings struct {
	Models   []string `json:"models" yaml:"models"`
	Group    string   `json:"group" yaml:"group"`
	Priority int      `json:"priority" yaml:"priority"`
	Weight   int      `json:"weight" yaml:"weight"`
}

type Channel struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Provider string   `json:"provider,omitempty"`
	RawType  string   `json:"raw_type,omitempty"`
	BaseURL  string   `json:"base_url,omitempty"`
	Models   []string `json:"models"`
	Group    string   `json:"group"`
	Priority int      `json:"priority"`
	Weight   int      `json:"weight"`
	Enabled  bool     `json:"enabled"`
}

type CreateChannelInput struct {
	AssetID  string
	Mode     SyncMode
	Name     string
	Provider string
	RawType  string
	BaseURL  string
	Secret   []byte
	Models   []string
	Group    string
	Priority int
	Weight   int
}

type UpdateChannelInput struct {
	Name     string
	BaseURL  string
	Models   []string
	Group    string
	Priority int
	Weight   int
	Enabled  bool
}

type ChannelSnapshot struct {
	Models   []string `json:"models" yaml:"models"`
	Group    string   `json:"group" yaml:"group"`
	Priority int      `json:"priority" yaml:"priority"`
	Weight   int      `json:"weight" yaml:"weight"`
}

// UpstreamGroupSnapshot records the upstream billing group a mapping was
// created against, so a later ratio or model change on the upstream side is
// reported as drift instead of silently changing cost.
type UpstreamGroupSnapshot struct {
	Group          string   `json:"group" yaml:"group"`
	Ratio          float64  `json:"ratio" yaml:"ratio"`
	RatioKnown     bool     `json:"ratio_known" yaml:"ratio_known"`
	Models         []string `json:"models" yaml:"models,omitempty"`
	ModelsVerified bool     `json:"models_verified" yaml:"models_verified"`
}

type SyncMapping struct {
	UpstreamAssetID     string          `json:"upstream_asset_id" yaml:"upstream_asset_id,omitempty"`
	LegacyUpstreamKeyID string          `json:"-" yaml:"upstream_key_id,omitempty"`
	TargetID            string          `json:"target_id" yaml:"target_id"`
	TargetChannelID     string          `json:"target_channel_id" yaml:"target_channel_id"`
	SourceProvider      string          `json:"source_provider" yaml:"source_provider"`
	AssetKind           AssetKind       `json:"asset_kind" yaml:"asset_kind"`
	Snapshot            ChannelSnapshot `json:"snapshot" yaml:"snapshot"`

	// UpstreamGroup is only populated for assets whose upstream cost depends on
	// a selected group, currently New API token assets.
	UpstreamGroup *UpstreamGroupSnapshot `json:"upstream_group,omitempty" yaml:"upstream_group,omitempty"`
}

type UpstreamAdapter interface {
	Capabilities(ctx context.Context) (SourceCapabilities, error)
	ListAssets(ctx context.Context, cursor PageCursor) (AssetPage, error)
	ResolveSecret(ctx context.Context, assetID string, grant SecretGrant) (ResolvedSecret, error)
}

// BatchSecretResolver is implemented by sources whose secret endpoint is rate
// limited per request rather than per key, making one batched call materially
// cheaper than N single calls. Adapters that do not implement it keep the
// per-asset ResolveSecret behaviour unchanged.
type BatchSecretResolver interface {
	ResolveSecrets(ctx context.Context, assetIDs []string, grant SecretGrant) (map[string]ResolvedSecret, error)
	MaxSecretBatchSize() int
}

type TargetAdapter interface {
	ListChannels(ctx context.Context) ([]Channel, error)
	CreateChannel(ctx context.Context, input CreateChannelInput) (Channel, error)
	UpdateChannel(ctx context.Context, id string, input UpdateChannelInput) (Channel, error)
	DeleteChannel(ctx context.Context, id string) error
}

func SelectSyncMode(asset UpstreamAsset, capabilities TargetCapabilities) (SyncMode, error) {
	if asset.Provider == "" || asset.Provider == ProviderUnknown {
		return "", ErrIncompatibleTarget
	}
	provider, ok := capabilities.Providers[asset.Provider]
	if !ok {
		return "", ErrIncompatibleTarget
	}
	supports := func(want SyncMode) bool {
		for _, mode := range provider.Modes {
			if mode == want {
				return true
			}
		}
		return false
	}

	switch asset.Kind {
	case AssetStaticAPIKey:
		if supports(SyncModeStaticKey) {
			return SyncModeStaticKey, nil
		}
	case AssetOAuthFile:
		schema := asset.Metadata["schema_version"]
		if supports(SyncModeNativeAuthFile) && strings.TrimSpace(asset.SourceType) != "" && asset.SourceType == capabilities.Platform && strings.TrimSpace(schema) != "" && schema == capabilities.NativeAuthSchema {
			return SyncModeNativeAuthFile, nil
		}
	case AssetProxyKey:
		if supports(SyncModeProxyEndpoint) && asset.BaseURL != "" {
			return SyncModeProxyEndpoint, nil
		}
	}
	return "", ErrIncompatibleTarget
}

func SnapshotFromChannel(channel Channel) ChannelSnapshot {
	return ChannelSnapshot{
		Models:   append([]string(nil), channel.Models...),
		Group:    channel.Group,
		Priority: channel.Priority,
		Weight:   channel.Weight,
	}
}
