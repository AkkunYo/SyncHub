package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func newAPIUpstreamConfig() UpstreamConfig {
	return UpstreamConfig{
		ID:          "source-newapi",
		Name:        "Source",
		Type:        "newapi",
		BaseURL:     "https://source.example.com",
		AccessToken: "REPLACE_WITH_SOURCE_TOKEN",
	}
}

func TestValidateDefaultsDiscoveryModeToTokenForNewAPI(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := cfg.Upstreams[0].DiscoveryMode; got != DiscoveryModeToken {
		t.Fatalf("discovery_mode = %q, want %q", got, DiscoveryModeToken)
	}
}

func TestValidateAcceptsExplicitTokenDiscoveryMode(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].DiscoveryMode = DiscoveryModeToken
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate(token) error = %v", err)
	}
}

func TestValidateNormalizesDiscoveryModeCase(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].DiscoveryMode = "  Token  "
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := cfg.Upstreams[0].DiscoveryMode; got != DiscoveryModeToken {
		t.Fatalf("normalized discovery_mode = %q, want %q", got, DiscoveryModeToken)
	}
}

func TestValidateRejectsUnknownDiscoveryMode(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].DiscoveryMode = "hybrid"
	if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "discovery_mode") {
		t.Fatalf("Validate() error = %v, want discovery_mode error", err)
	}
}

func TestValidateRejectsDiscoveryModeOnNonNewAPIUpstream(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	// Upstream 0 is generic by default.
	cfg.Upstreams[0].DiscoveryMode = DiscoveryModeToken
	if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "discovery_mode") {
		t.Fatalf("Validate() error = %v, want discovery_mode error", err)
	}
}

func TestValidateRejectsManageTokensOnNonNewAPIUpstream(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0].ManageTokens = true
	if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "manage_tokens") {
		t.Fatalf("Validate() error = %v, want manage_tokens error", err)
	}
}

func TestValidateRejectsRetiredChannelModeBeforeManageTokens(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].DiscoveryMode = DiscoveryModeChannel
	cfg.Upstreams[0].ManageTokens = true
	if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "discovery_mode") {
		t.Fatalf("Validate() error = %v, want discovery_mode error", err)
	}
}

func TestValidateAllowsManageTokensInTokenMode(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].DiscoveryMode = DiscoveryModeToken
	cfg.Upstreams[0].ManageTokens = true
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDiscoveryModeAndManageTokensYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].DiscoveryMode = DiscoveryModeToken
	cfg.Upstreams[0].ManageTokens = true
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), "discovery_mode: token") {
		t.Fatalf("round-trip YAML omitted discovery_mode: %s", encoded)
	}
	if !strings.Contains(string(encoded), "manage_tokens: true") {
		t.Fatalf("round-trip YAML omitted manage_tokens: %s", encoded)
	}

	var decoded Config
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if got := decoded.Upstreams[0].DiscoveryMode; got != DiscoveryModeToken {
		t.Fatalf("decoded discovery_mode = %q", got)
	}
	if !decoded.Upstreams[0].ManageTokens {
		t.Fatalf("decoded manage_tokens = false, want true")
	}
}
