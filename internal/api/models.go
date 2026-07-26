package api

import (
	"bytes"
	"encoding/json"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

type successEnvelope struct {
	Success   bool   `json:"success"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}

type errorEnvelope struct {
	Success   bool          `json:"success"`
	Error     errorResponse `json:"error"`
	RequestID string        `json:"request_id"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type publicAppConfig struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	ReconcileInterval string `json:"reconcile_interval"`
	RequestTimeout    string `json:"request_timeout"`
	SyncConcurrency   int    `json:"sync_concurrency"`
}

type publicTarget struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	UserID  int    `json:"user_id,omitempty"`
}

type publicUpstream struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	BaseURL      string                 `json:"base_url"`
	UserID       int                    `json:"user_id,omitempty"`
	SyncMappings []platform.SyncMapping `json:"sync_mappings"`
}

type publicConfig struct {
	App       publicAppConfig  `json:"app"`
	Targets   []publicTarget   `json:"targets"`
	Upstreams []publicUpstream `json:"upstreams"`
}

type appUpdateRequest struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	ReconcileInterval string `json:"reconcile_interval"`
	RequestTimeout    string `json:"request_timeout"`
	SyncConcurrency   int    `json:"sync_concurrency"`
}

type targetCreateRequest struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Type          string      `json:"type"`
	BaseURL       string      `json:"base_url"`
	AccessToken   string      `json:"access_token,omitempty"`
	ManagementKey string      `json:"management_key,omitempty"`
	APIKey        string      `json:"api_key,omitempty"`
	UserID        optionalInt `json:"user_id"`
}

type optionalString struct {
	set   bool
	null  bool
	value string
}

type optionalInt struct {
	set   bool
	value int
}

func (o *optionalInt) UnmarshalJSON(data []byte) error {
	o.set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errInvalidInput
	}
	return json.Unmarshal(data, &o.value)
}

func (o *optionalString) UnmarshalJSON(data []byte) error {
	o.set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.null = true
		o.value = ""
		return nil
	}
	o.null = false
	return json.Unmarshal(data, &o.value)
}

type targetUpdateRequest struct {
	Name          string         `json:"name"`
	BaseURL       string         `json:"base_url"`
	AccessToken   optionalString `json:"access_token"`
	ManagementKey optionalString `json:"management_key"`
	APIKey        optionalString `json:"api_key"`
	UserID        optionalInt    `json:"user_id"`
}

type upstreamCreateRequest struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Type          string         `json:"type"`
	BaseURL       string         `json:"base_url"`
	AccessToken   string         `json:"access_token,omitempty"`
	ManagementKey string         `json:"management_key,omitempty"`
	APIKey        string         `json:"api_key,omitempty"`
	ProxyAPIKey   optionalString `json:"proxy_api_key"`
	UserID        optionalInt    `json:"user_id"`
}

type upstreamUpdateRequest struct {
	Name          string         `json:"name"`
	BaseURL       string         `json:"base_url"`
	AccessToken   optionalString `json:"access_token"`
	ManagementKey optionalString `json:"management_key"`
	APIKey        optionalString `json:"api_key"`
	ProxyAPIKey   optionalString `json:"proxy_api_key"`
	UserID        optionalInt    `json:"user_id"`
}

type channelUpdateRequest struct {
	Name     string   `json:"name"`
	BaseURL  string   `json:"base_url"`
	Models   []string `json:"models"`
	Group    string   `json:"group"`
	Priority int      `json:"priority"`
	Weight   int      `json:"weight"`
	Enabled  *bool    `json:"enabled"`
}

type managedChannel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Provider        string   `json:"provider,omitempty"`
	RawType         string   `json:"raw_type,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	Models          []string `json:"models"`
	Group           string   `json:"group"`
	Priority        int      `json:"priority"`
	Weight          int      `json:"weight"`
	Enabled         bool     `json:"enabled"`
	Managed         bool     `json:"managed"`
	UpstreamAssetID string   `json:"upstream_asset_id,omitempty"`
}

type syncRequest struct {
	UpstreamID string                   `json:"upstream_id"`
	AssetID    string                   `json:"asset_id"`
	TargetIDs  []string                 `json:"target_ids"`
	Settings   platform.ChannelSettings `json:"settings"`
	Grant      syncGrantRequest         `json:"grant"`
}

type syncGrantRequest struct {
	SecurityProof string `json:"security_proof"`
	AllowAuthFile bool   `json:"allow_auth_file"`
}

type acceptDriftRequest struct {
	UpstreamAssetID string `json:"upstream_asset_id"`
	ChannelID       string `json:"channel_id"`
}

type matrixTarget struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
}

type matrixCell struct {
	TargetID    string             `json:"target_id"`
	Status      string             `json:"status"`
	ChannelID   string             `json:"channel_id,omitempty"`
	Differences []matrixDifference `json:"differences,omitempty"`
}

type matrixDifference struct {
	Field    string `json:"field"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

type matrixRow struct {
	Asset platform.UpstreamAsset `json:"asset"`
	Cells []matrixCell           `json:"cells"`
}

type matrixResponse struct {
	UpstreamID string         `json:"upstream_id"`
	Refreshed  bool           `json:"refreshed"`
	Targets    []matrixTarget `json:"targets"`
	Rows       []matrixRow    `json:"rows"`
}

func redactConfig(cfg config.Config) publicConfig {
	result := publicConfig{
		App: publicAppConfig{
			Host:              cfg.App.Host,
			Port:              cfg.App.Port,
			ReconcileInterval: cfg.App.ReconcileInterval.String(),
			RequestTimeout:    cfg.App.RequestTimeout.String(),
			SyncConcurrency:   cfg.App.SyncConcurrency,
		},
		Targets:   make([]publicTarget, len(cfg.Targets)),
		Upstreams: make([]publicUpstream, len(cfg.Upstreams)),
	}
	for i, target := range cfg.Targets {
		result.Targets[i] = redactTarget(target)
	}
	for i, upstream := range cfg.Upstreams {
		result.Upstreams[i] = redactUpstream(upstream)
	}
	return result
}

func redactTarget(target config.TargetConfig) publicTarget {
	return publicTarget{ID: target.ID, Name: target.Name, Type: target.Type, BaseURL: target.BaseURL, UserID: target.UserID}
}

func redactUpstream(upstream config.UpstreamConfig) publicUpstream {
	mappings := cloneMappingsForResponse(upstream.SyncMappings)
	if mappings == nil {
		mappings = []platform.SyncMapping{}
	}
	return publicUpstream{
		ID: upstream.ID, Name: upstream.Name, Type: upstream.Type, BaseURL: upstream.BaseURL, UserID: upstream.UserID, SyncMappings: mappings,
	}
}

func toManagedChannel(channel platform.Channel, mapping *platform.SyncMapping) managedChannel {
	result := managedChannel{
		ID: channel.ID, Name: channel.Name, Provider: channel.Provider, RawType: channel.RawType,
		BaseURL: channel.BaseURL, Models: append([]string(nil), channel.Models...), Group: channel.Group,
		Priority: channel.Priority, Weight: channel.Weight, Enabled: channel.Enabled,
	}
	if result.Models == nil {
		result.Models = []string{}
	}
	if mapping != nil {
		result.Managed = true
		result.UpstreamAssetID = mapping.UpstreamAssetID
	}
	return result
}

func cloneMappingsForResponse(mappings []platform.SyncMapping) []platform.SyncMapping {
	result := append([]platform.SyncMapping(nil), mappings...)
	for i := range result {
		result[i].Snapshot.Models = append([]string(nil), mappings[i].Snapshot.Models...)
		if result[i].Snapshot.Models == nil {
			result[i].Snapshot.Models = []string{}
		}
	}
	return result
}
