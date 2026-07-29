package app

import (
	"context"
	"errors"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
)

func TestAdapterResolverAllowsOnlyNewAPIUserTokenAndGenericUpstreams(t *testing.T) {
	t.Parallel()

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, nil)
	tests := []struct {
		name     string
		upstream config.UpstreamConfig
	}{
		{
			name:     "New API auto mode",
			upstream: config.UpstreamConfig{ID: "source-newapi", Name: "New API", Type: "newapi", BaseURL: "https://newapi.example.com", AccessToken: "user-token", DiscoveryMode: "auto"},
		},
		{
			name:     "New API channel mode",
			upstream: config.UpstreamConfig{ID: "source-newapi", Name: "New API", Type: "newapi", BaseURL: "https://newapi.example.com", AccessToken: "user-token", DiscoveryMode: "channel"},
		},
		{
			name:     "New API API key alias",
			upstream: config.UpstreamConfig{ID: "source-newapi", Name: "New API", Type: "newapi", BaseURL: "https://newapi.example.com", APIKey: "ambiguous-key", DiscoveryMode: "token"},
		},
		{
			name:     "CPA management source",
			upstream: config.UpstreamConfig{ID: "source-cpa", Name: "CPA", Type: "cliproxyapi", BaseURL: "https://cpa.example.com", ManagementKey: "management-key"},
		},
		{
			name:     "Sub2Api management source",
			upstream: config.UpstreamConfig{ID: "source-sub2api", Name: "Sub2Api", Type: "sub2api", BaseURL: "https://sub2api.example.com", APIKey: "admin-key"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := resolver.ResolveUpstream(context.Background(), test.upstream); !errors.Is(err, ErrUnsupportedPlatform) {
				t.Fatalf("ResolveUpstream() error = %v, want ErrUnsupportedPlatform", err)
			}
		})
	}
}
