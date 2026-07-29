package config

import (
	"strings"
	"testing"
)

func TestValidateAllowsOnlyNewAPIUserTokenAndGenericUpstreams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		upstream UpstreamConfig
		wantErr  string
	}{
		{
			name: "New API user token with implicit token mode",
			upstream: UpstreamConfig{
				ID: "source-newapi", Name: "New API User", Type: "newapi",
				BaseURL: "https://newapi.example.com", AccessToken: "user-management-token",
			},
		},
		{
			name: "New API explicit token compatibility value",
			upstream: UpstreamConfig{
				ID: "source-newapi", Name: "New API User", Type: "newapi",
				BaseURL: "https://newapi.example.com", AccessToken: "user-management-token", DiscoveryMode: DiscoveryModeToken,
			},
		},
		{
			name: "generic shared endpoint",
			upstream: UpstreamConfig{
				ID: "source-generic", Name: "Shared Endpoint", Type: "generic",
				BaseURL: "https://provider.example.com/v1", APIKey: "shared-api-key",
			},
		},
		{
			name: "New API auto mode",
			upstream: UpstreamConfig{
				ID: "source-newapi", Name: "New API User", Type: "newapi",
				BaseURL: "https://newapi.example.com", AccessToken: "user-management-token", DiscoveryMode: DiscoveryModeAuto,
			},
			wantErr: "discovery_mode",
		},
		{
			name: "New API channel mode",
			upstream: UpstreamConfig{
				ID: "source-newapi", Name: "New API User", Type: "newapi",
				BaseURL: "https://newapi.example.com", AccessToken: "user-management-token", DiscoveryMode: DiscoveryModeChannel,
			},
			wantErr: "discovery_mode",
		},
		{
			name: "New API API key alias",
			upstream: UpstreamConfig{
				ID: "source-newapi", Name: "New API User", Type: "newapi",
				BaseURL: "https://newapi.example.com", APIKey: "ambiguous-key",
			},
			wantErr: "api_key",
		},
		{
			name: "CPA management source",
			upstream: UpstreamConfig{
				ID: "source-cpa", Name: "CPA", Type: "cliproxyapi",
				BaseURL: "https://cpa.example.com", ManagementKey: "management-key",
			},
			wantErr: "type",
		},
		{
			name: "Sub2Api management source",
			upstream: UpstreamConfig{
				ID: "source-sub2api", Name: "Sub2Api", Type: "sub2api",
				BaseURL: "https://sub2api.example.com", APIKey: "admin-key",
			},
			wantErr: "type",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Upstreams = []UpstreamConfig{test.upstream}
			err := Validate(&cfg)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				if cfg.Upstreams[0].Type == "newapi" && cfg.Upstreams[0].DiscoveryMode != DiscoveryModeToken {
					t.Fatalf("discovery_mode = %q, want token", cfg.Upstreams[0].DiscoveryMode)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want %s rejection", err, test.wantErr)
			}
		})
	}
}
