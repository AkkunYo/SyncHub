package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.ScalarNode {
		return errors.New("duration must be a string")
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(node.Value))
	if err != nil {
		return fmt.Errorf("invalid duration: %w", err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

type AppConfig struct {
	Host              string   `json:"host" yaml:"host"`
	Port              int      `json:"port" yaml:"port"`
	ReconcileInterval Duration `json:"reconcile_interval" yaml:"reconcile_interval"`
	RequestTimeout    Duration `json:"request_timeout" yaml:"request_timeout"`
	SyncConcurrency   int      `json:"sync_concurrency" yaml:"sync_concurrency"`
}

type TargetConfig struct {
	ID            string `json:"id" yaml:"id"`
	Name          string `json:"name" yaml:"name"`
	Type          string `json:"type" yaml:"type"`
	BaseURL       string `json:"base_url" yaml:"base_url"`
	UserID        int    `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	AccessToken   string `json:"-" yaml:"access_token,omitempty"`
	ManagementKey string `json:"-" yaml:"management_key,omitempty"`
	APIKey        string `json:"-" yaml:"api_key,omitempty"`
}

type UpstreamConfig struct {
	ID                    string               `json:"id" yaml:"id"`
	Name                  string               `json:"name" yaml:"name"`
	Type                  string               `json:"type" yaml:"type"`
	BaseURL               string               `json:"base_url" yaml:"base_url"`
	UserID                int                  `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	AccessToken           string               `json:"-" yaml:"access_token,omitempty"`
	APIKey                string               `json:"-" yaml:"api_key,omitempty"`
	ManagementKey         string               `json:"-" yaml:"management_key,omitempty"`
	ProxyAPIKey           string               `json:"-" yaml:"proxy_api_key,omitempty"`
	DiscoveryMode         string               `json:"discovery_mode,omitempty" yaml:"discovery_mode,omitempty"`
	ManageTokens          bool                 `json:"manage_tokens,omitempty" yaml:"manage_tokens,omitempty"`
	ManagedTokenNamespace string               `json:"managed_token_namespace,omitempty" yaml:"managed_token_namespace,omitempty"`
	ManagedTokens         []ManagedTokenRecord `json:"-" yaml:"managed_tokens,omitempty"`
	SyncMappings          []SyncMapping        `json:"sync_mappings" yaml:"sync_mappings,omitempty"`
}

type ManagedTokenRecord struct {
	IdempotencyKey string   `json:"idempotency_key" yaml:"idempotency_key"`
	Status         string   `json:"status" yaml:"status,omitempty"`
	TokenID        int      `json:"token_id,omitempty" yaml:"token_id,omitempty"`
	AssetID        string   `json:"asset_id,omitempty" yaml:"asset_id,omitempty"`
	Name           string   `json:"name" yaml:"name"`
	TargetID       string   `json:"target_id" yaml:"target_id"`
	UpstreamGroup  string   `json:"upstream_group" yaml:"upstream_group"`
	Quota          int64    `json:"quota" yaml:"quota"`
	ExpiresAt      int64    `json:"expires_at" yaml:"expires_at"`
	Models         []string `json:"models" yaml:"models"`
}

// New API discovery modes. Only newapi upstreams accept these values; the mode
// is a hint for credential-privilege probing, not an override of it.
const (
	DiscoveryModeAuto    = "auto"
	DiscoveryModeChannel = "channel"
	DiscoveryModeToken   = "token"
	ManagedTokenPending  = "pending"
	ManagedTokenReady    = "ready"
)

var managedTokenNamespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type SyncMapping = platform.SyncMapping

type Config struct {
	App       AppConfig        `json:"app" yaml:"app"`
	Targets   []TargetConfig   `json:"targets" yaml:"targets"`
	Upstreams []UpstreamConfig `json:"upstreams" yaml:"upstreams"`
}

func Default() Config {
	return Config{
		App: AppConfig{
			Host:              "127.0.0.1",
			Port:              8888,
			ReconcileInterval: Duration(5 * time.Minute),
			RequestTimeout:    Duration(15 * time.Second),
			SyncConcurrency:   4,
		},
		Targets:   []TargetConfig{},
		Upstreams: []UpstreamConfig{},
	}
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	cfg.App.Host = strings.TrimSpace(cfg.App.Host)
	if cfg.App.Host == "" {
		return errors.New("app.host is required")
	}
	if cfg.App.Port < 1 || cfg.App.Port > 65535 {
		return errors.New("app.port must be between 1 and 65535")
	}
	if time.Duration(cfg.App.ReconcileInterval) <= 0 {
		return errors.New("app.reconcile_interval must be positive")
	}
	if time.Duration(cfg.App.RequestTimeout) <= 0 {
		return errors.New("app.request_timeout must be positive")
	}
	if cfg.App.SyncConcurrency < 1 {
		return errors.New("app.sync_concurrency must be positive")
	}

	targetIDs := make(map[string]struct{}, len(cfg.Targets))
	for i := range cfg.Targets {
		target := &cfg.Targets[i]
		target.ID = strings.TrimSpace(target.ID)
		target.Name = strings.TrimSpace(target.Name)
		target.Type = strings.ToLower(strings.TrimSpace(target.Type))
		if target.UserID < 0 {
			return fmt.Errorf("target[%d].user_id must not be negative", i)
		}
		if target.ID == "" {
			return fmt.Errorf("target[%d].id is required", i)
		}
		if _, exists := targetIDs[target.ID]; exists {
			return fmt.Errorf("target id %q is duplicated", target.ID)
		}
		targetIDs[target.ID] = struct{}{}
		if target.Name == "" {
			return fmt.Errorf("target[%d].name is required", i)
		}
		if target.Type != "newapi" && target.Type != "cliproxyapi" {
			return fmt.Errorf("target[%d].type %q is unsupported", i, target.Type)
		}
		if target.Type != "newapi" && target.UserID != 0 {
			return fmt.Errorf("target[%d].user_id is only supported for newapi", i)
		}
		baseURL, err := normalizeBaseURL(target.BaseURL)
		if err != nil {
			return fmt.Errorf("target[%d].base_url: %w", i, err)
		}
		target.BaseURL = baseURL
		switch target.Type {
		case "newapi":
			if strings.TrimSpace(target.AccessToken) == "" {
				return fmt.Errorf("target[%d].access_token is required", i)
			}
		case "cliproxyapi":
			if strings.TrimSpace(target.ManagementKey) == "" && strings.TrimSpace(target.APIKey) == "" {
				return fmt.Errorf("target[%d].management_key is required", i)
			}
		}
	}

	upstreamIDs := make(map[string]struct{}, len(cfg.Upstreams))
	for i := range cfg.Upstreams {
		upstream := &cfg.Upstreams[i]
		upstream.ID = strings.TrimSpace(upstream.ID)
		upstream.Name = strings.TrimSpace(upstream.Name)
		upstream.Type = strings.ToLower(strings.TrimSpace(upstream.Type))
		if upstream.UserID < 0 {
			return fmt.Errorf("upstream[%d].user_id must not be negative", i)
		}
		if upstream.ID == "" {
			return fmt.Errorf("upstream[%d].id is required", i)
		}
		if _, exists := upstreamIDs[upstream.ID]; exists {
			return fmt.Errorf("upstream id %q is duplicated", upstream.ID)
		}
		upstreamIDs[upstream.ID] = struct{}{}
		if upstream.Name == "" {
			return fmt.Errorf("upstream[%d].name is required", i)
		}
		switch upstream.Type {
		case "newapi", "cliproxyapi", "sub2api":
		default:
			return fmt.Errorf("upstream[%d].type %q is unsupported", i, upstream.Type)
		}
		if upstream.Type != "newapi" && upstream.UserID != 0 {
			return fmt.Errorf("upstream[%d].user_id is only supported for newapi", i)
		}
		if upstream.Type != "cliproxyapi" && strings.TrimSpace(upstream.ProxyAPIKey) != "" {
			return fmt.Errorf("upstream[%d].proxy_api_key is only supported for cliproxyapi", i)
		}
		upstream.DiscoveryMode = strings.ToLower(strings.TrimSpace(upstream.DiscoveryMode))
		if upstream.Type == "newapi" {
			if upstream.DiscoveryMode == "" {
				upstream.DiscoveryMode = DiscoveryModeToken
			}
			switch upstream.DiscoveryMode {
			case DiscoveryModeAuto, DiscoveryModeChannel, DiscoveryModeToken:
			default:
				return fmt.Errorf("upstream[%d].discovery_mode %q is unsupported", i, upstream.DiscoveryMode)
			}
			// manage_tokens is a token-mode capability. Allowing it under channel
			// mode would imply SyncHub creating tokens on a credential we are
			// treating as Admin/Root, which contradicts the mode's intent.
			if upstream.ManageTokens && upstream.DiscoveryMode == DiscoveryModeChannel {
				return fmt.Errorf("upstream[%d].manage_tokens is not allowed in channel discovery mode", i)
			}
			upstream.ManagedTokenNamespace = strings.TrimSpace(upstream.ManagedTokenNamespace)
			if upstream.ManagedTokenNamespace == "" {
				upstream.ManagedTokenNamespace = "synchub"
			}
			if !managedTokenNamespacePattern.MatchString(upstream.ManagedTokenNamespace) {
				return fmt.Errorf("upstream[%d].managed_token_namespace is invalid", i)
			}
		} else {
			if upstream.DiscoveryMode != "" {
				return fmt.Errorf("upstream[%d].discovery_mode is only supported for newapi", i)
			}
			if upstream.ManageTokens {
				return fmt.Errorf("upstream[%d].manage_tokens is only supported for newapi", i)
			}
			if strings.TrimSpace(upstream.ManagedTokenNamespace) != "" || len(upstream.ManagedTokens) != 0 {
				return fmt.Errorf("upstream[%d].managed_token_namespace and managed_tokens are only supported for newapi", i)
			}
		}
		baseURL, err := normalizeBaseURL(upstream.BaseURL)
		if err != nil {
			return fmt.Errorf("upstream[%d].base_url: %w", i, err)
		}
		upstream.BaseURL = baseURL
		switch upstream.Type {
		case "newapi":
			if strings.TrimSpace(upstream.AccessToken) == "" && strings.TrimSpace(upstream.APIKey) == "" {
				return fmt.Errorf("upstream[%d].access_token is required", i)
			}
		case "cliproxyapi":
			if strings.TrimSpace(upstream.ManagementKey) == "" && strings.TrimSpace(upstream.APIKey) == "" {
				return fmt.Errorf("upstream[%d].management_key is required", i)
			}
		case "sub2api":
			if strings.TrimSpace(upstream.APIKey) == "" {
				return fmt.Errorf("upstream[%d].api_key is required", i)
			}
		}

		if err := validateManagedTokens(i, upstream, targetIDs); err != nil {
			return err
		}

		for mappingIndex := range upstream.SyncMappings {
			mapping := &upstream.SyncMappings[mappingIndex]
			migrateMapping(upstream.ID, mapping)
			mapping.UpstreamAssetID = strings.TrimSpace(mapping.UpstreamAssetID)
			mapping.TargetID = strings.TrimSpace(mapping.TargetID)
			mapping.TargetChannelID = strings.TrimSpace(mapping.TargetChannelID)
			if mapping.UpstreamAssetID == "" {
				return fmt.Errorf("upstream[%d].sync_mappings[%d].upstream_asset_id is required", i, mappingIndex)
			}
			if _, exists := targetIDs[mapping.TargetID]; !exists {
				return fmt.Errorf("upstream[%d].sync_mappings[%d].target_id %q does not exist", i, mappingIndex, mapping.TargetID)
			}
			if mapping.TargetChannelID == "" {
				return fmt.Errorf("upstream[%d].sync_mappings[%d].target_channel_id is required", i, mappingIndex)
			}
		}
	}
	return nil
}

func validateManagedTokens(upstreamIndex int, upstream *UpstreamConfig, targetIDs map[string]struct{}) error {
	keys := make(map[string]struct{}, len(upstream.ManagedTokens))
	names := make(map[string]struct{}, len(upstream.ManagedTokens))
	for index := range upstream.ManagedTokens {
		record := &upstream.ManagedTokens[index]
		record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
		record.Status = strings.ToLower(strings.TrimSpace(record.Status))
		record.AssetID = strings.TrimSpace(record.AssetID)
		record.Name = strings.TrimSpace(record.Name)
		record.TargetID = strings.TrimSpace(record.TargetID)
		record.UpstreamGroup = strings.TrimSpace(record.UpstreamGroup)
		prefix := fmt.Sprintf("upstream[%d].managed_tokens[%d]", upstreamIndex, index)
		if record.IdempotencyKey == "" || len(record.IdempotencyKey) > 256 {
			return fmt.Errorf("%s.idempotency_key is invalid", prefix)
		}
		if _, exists := keys[record.IdempotencyKey]; exists {
			return fmt.Errorf("%s.idempotency_key is duplicated", prefix)
		}
		keys[record.IdempotencyKey] = struct{}{}
		if record.Status == "" {
			if record.TokenID > 0 {
				record.Status = ManagedTokenReady
			} else {
				record.Status = ManagedTokenPending
			}
		}
		if record.Name == "" || !strings.HasPrefix(record.Name, upstream.ManagedTokenNamespace+"-") {
			return fmt.Errorf("%s.name is invalid", prefix)
		}
		if _, exists := names[record.Name]; exists {
			return fmt.Errorf("%s.name is duplicated", prefix)
		}
		names[record.Name] = struct{}{}
		if _, exists := targetIDs[record.TargetID]; !exists {
			return fmt.Errorf("%s.target_id does not exist", prefix)
		}
		if record.UpstreamGroup == "" || record.Quota <= 0 || record.ExpiresAt <= 0 {
			return fmt.Errorf("%s has invalid group, quota, or expiry", prefix)
		}
		models := make([]string, 0, len(record.Models))
		seenModels := make(map[string]struct{}, len(record.Models))
		for _, value := range record.Models {
			model := strings.TrimSpace(value)
			if model == "" {
				return fmt.Errorf("%s.models is invalid", prefix)
			}
			if _, duplicate := seenModels[model]; duplicate {
				return fmt.Errorf("%s.models is duplicated", prefix)
			}
			seenModels[model] = struct{}{}
			models = append(models, model)
		}
		if len(models) == 0 {
			return fmt.Errorf("%s.models is required", prefix)
		}
		record.Models = models
		switch record.Status {
		case ManagedTokenPending:
			if record.TokenID != 0 || record.AssetID != "" {
				return fmt.Errorf("%s pending record has a token identity", prefix)
			}
		case ManagedTokenReady:
			expectedAssetID := fmt.Sprintf("%s:token:%d", upstream.ID, record.TokenID)
			if record.TokenID <= 0 || record.AssetID != expectedAssetID {
				return fmt.Errorf("%s ready record has an invalid token identity", prefix)
			}
		default:
			return fmt.Errorf("%s.status is invalid", prefix)
		}
	}
	return nil
}

func normalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", errors.New("must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("must use http or https")
	}
	if parsed.User != nil {
		return "", errors.New("must not contain user information")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func migrateMapping(sourceID string, mapping *SyncMapping) {
	if mapping == nil {
		return
	}
	legacy := strings.TrimSpace(mapping.LegacyUpstreamKeyID)
	if strings.TrimSpace(mapping.UpstreamAssetID) == "" && legacy != "" {
		mapping.UpstreamAssetID = strings.TrimSpace(sourceID) + ":key:" + legacy
	}
	mapping.LegacyUpstreamKeyID = ""
}

func deepCopy(cfg Config) Config {
	copyConfig := cfg
	copyConfig.Targets = append([]TargetConfig(nil), cfg.Targets...)
	copyConfig.Upstreams = make([]UpstreamConfig, len(cfg.Upstreams))
	for i := range cfg.Upstreams {
		copyConfig.Upstreams[i] = cfg.Upstreams[i]
		copyConfig.Upstreams[i].ManagedTokens = append([]ManagedTokenRecord(nil), cfg.Upstreams[i].ManagedTokens...)
		for j := range copyConfig.Upstreams[i].ManagedTokens {
			copyConfig.Upstreams[i].ManagedTokens[j].Models = append([]string(nil), cfg.Upstreams[i].ManagedTokens[j].Models...)
		}
		copyConfig.Upstreams[i].SyncMappings = make([]SyncMapping, len(cfg.Upstreams[i].SyncMappings))
		for j := range cfg.Upstreams[i].SyncMappings {
			copyConfig.Upstreams[i].SyncMappings[j] = cfg.Upstreams[i].SyncMappings[j]
			copyConfig.Upstreams[i].SyncMappings[j].Snapshot.Models = append([]string(nil), cfg.Upstreams[i].SyncMappings[j].Snapshot.Models...)
			if source := cfg.Upstreams[i].SyncMappings[j].UpstreamGroup; source != nil {
				group := *source
				group.Models = append([]string(nil), source.Models...)
				copyConfig.Upstreams[i].SyncMappings[j].UpstreamGroup = &group
			}
		}
	}
	return copyConfig
}
