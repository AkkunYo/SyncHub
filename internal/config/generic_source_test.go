package config

import (
	"strings"
	"testing"
)

func TestValidateAcceptsGenericUpstreamWithOnlySharedAPIKey(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = UpstreamConfig{
		ID: " source-generic ", Name: " Shared Endpoint ", Type: " GENERIC ",
		BaseURL: "https://provider.example.com/v1/", APIKey: "shared-api-key",
	}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	got := cfg.Upstreams[0]
	if got.ID != "source-generic" || got.Name != "Shared Endpoint" || got.Type != "generic" || got.BaseURL != "https://provider.example.com/v1" {
		t.Fatalf("normalized generic upstream = %#v", got)
	}
}

func TestValidateRejectsGenericUpstreamManagementAndLoginFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*UpstreamConfig)
		field  string
	}{
		{name: "missing api key", mutate: func(upstream *UpstreamConfig) { upstream.APIKey = "" }, field: "api_key"},
		{name: "access token", mutate: func(upstream *UpstreamConfig) { upstream.AccessToken = "user-token" }, field: "access_token"},
		{name: "management key", mutate: func(upstream *UpstreamConfig) { upstream.ManagementKey = "management-key" }, field: "management_key"},
		{name: "proxy api key", mutate: func(upstream *UpstreamConfig) { upstream.ProxyAPIKey = "proxy-key" }, field: "proxy_api_key"},
		{name: "user id", mutate: func(upstream *UpstreamConfig) { upstream.UserID = 1 }, field: "user_id"},
		{name: "discovery mode", mutate: func(upstream *UpstreamConfig) { upstream.DiscoveryMode = DiscoveryModeToken }, field: "discovery_mode"},
		{name: "manage tokens", mutate: func(upstream *UpstreamConfig) { upstream.ManageTokens = true }, field: "manage_tokens"},
		{name: "managed token namespace", mutate: func(upstream *UpstreamConfig) { upstream.ManagedTokenNamespace = "synchub" }, field: "managed_token_namespace"},
		{name: "managed token records", mutate: func(upstream *UpstreamConfig) {
			upstream.ManagedTokens = []ManagedTokenRecord{{IdempotencyKey: "managed-1"}}
		}, field: "managed_tokens"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := validTestConfig()
			cfg.Upstreams[0] = UpstreamConfig{
				ID: "source-generic", Name: "Shared Endpoint", Type: "generic",
				BaseURL: "https://provider.example.com/v1", APIKey: "shared-api-key",
			}
			test.mutate(&cfg.Upstreams[0])
			if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("Validate() error = %v, want %s validation error", err, test.field)
			}
		})
	}
}
