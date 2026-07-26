package config

import (
	"errors"
	"fmt"
	"net/url"
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
	ID            string        `json:"id" yaml:"id"`
	Name          string        `json:"name" yaml:"name"`
	Type          string        `json:"type" yaml:"type"`
	BaseURL       string        `json:"base_url" yaml:"base_url"`
	UserID        int           `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	AccessToken   string        `json:"-" yaml:"access_token,omitempty"`
	APIKey        string        `json:"-" yaml:"api_key,omitempty"`
	ManagementKey string        `json:"-" yaml:"management_key,omitempty"`
	ProxyAPIKey   string        `json:"-" yaml:"proxy_api_key,omitempty"`
	DiscoveryMode string        `json:"discovery_mode,omitempty" yaml:"discovery_mode,omitempty"`
	ManageTokens  bool          `json:"manage_tokens,omitempty" yaml:"manage_tokens,omitempty"`
	SyncMappings  []SyncMapping `json:"sync_mappings" yaml:"sync_mappings,omitempty"`
}

// New API discovery modes. Only newapi upstreams accept these values; the mode
// is a hint for credential-privilege probing, not an override of it.
const (
	DiscoveryModeAuto    = "auto"
	DiscoveryModeChannel = "channel"
	DiscoveryModeToken   = "token"
)

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
				upstream.DiscoveryMode = DiscoveryModeAuto
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
		} else {
			if upstream.DiscoveryMode != "" {
				return fmt.Errorf("upstream[%d].discovery_mode is only supported for newapi", i)
			}
			if upstream.ManageTokens {
				return fmt.Errorf("upstream[%d].manage_tokens is only supported for newapi", i)
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
		copyConfig.Upstreams[i].SyncMappings = make([]SyncMapping, len(cfg.Upstreams[i].SyncMappings))
		for j := range cfg.Upstreams[i].SyncMappings {
			copyConfig.Upstreams[i].SyncMappings[j] = cfg.Upstreams[i].SyncMappings[j]
			copyConfig.Upstreams[i].SyncMappings[j].Snapshot.Models = append([]string(nil), cfg.Upstreams[i].SyncMappings[j].Snapshot.Models...)
		}
	}
	return copyConfig
}
