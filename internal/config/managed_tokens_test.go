package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func managedTokenConfig() Config {
	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].DiscoveryMode = DiscoveryModeToken
	cfg.Upstreams[0].ManageTokens = true
	return cfg
}

func TestValidateDefaultsAndValidatesManagedTokenNamespace(t *testing.T) {
	cfg := managedTokenConfig()
	if err := Validate(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Upstreams[0].ManagedTokenNamespace != "synchub" {
		t.Fatalf("namespace=%q", cfg.Upstreams[0].ManagedTokenNamespace)
	}
	for _, invalid := range []string{"UPPER", "-leading", "contains space", strings.Repeat("a", 33)} {
		cfg := managedTokenConfig()
		cfg.Upstreams[0].ManagedTokenNamespace = invalid
		if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "managed_token_namespace") {
			t.Fatalf("namespace %q error=%v", invalid, err)
		}
	}

	disabled := validTestConfig()
	disabled.Upstreams[0] = newAPIUpstreamConfig()
	disabled.Upstreams[0].ManagedTokenNamespace = "synchub"
	if err := Validate(&disabled); err == nil || !strings.Contains(err.Error(), "managed_token_namespace") {
		t.Fatalf("disabled namespace error=%v", err)
	}
}

func TestValidateManagedTokenPendingAndReadyRecords(t *testing.T) {
	cfg := managedTokenConfig()
	cfg.Upstreams[0].ManagedTokens = []ManagedTokenRecord{
		{IdempotencyKey: "pending-1", Status: ManagedTokenPending, Name: "synchub-target-a-vip-a1", TargetID: "target-1", UpstreamGroup: "vip", Quota: 1000, ExpiresAt: 1798761600, Models: []string{"gpt-4o"}},
		{IdempotencyKey: "ready-1", Status: ManagedTokenReady, TokenID: 42, AssetID: "source-newapi:token:42", Name: "synchub-target-a-default-b2", TargetID: "target-1", UpstreamGroup: "default", Quota: 2000, ExpiresAt: 1798761600, Models: []string{"gpt-4o-mini"}},
	}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() error=%v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ManagedTokenRecord)
	}{
		{"unknown status", func(r *ManagedTokenRecord) { r.Status = "unknown" }},
		{"pending has token", func(r *ManagedTokenRecord) { r.TokenID = 1 }},
		{"ready lacks token", func(r *ManagedTokenRecord) { r.Status = ManagedTokenReady; r.TokenID = 0; r.AssetID = "" }},
		{"invalid quota", func(r *ManagedTokenRecord) { r.Quota = 0 }},
		{"missing models", func(r *ManagedTokenRecord) { r.Models = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := managedTokenConfig()
			record := ManagedTokenRecord{IdempotencyKey: "key", Status: ManagedTokenPending, Name: "synchub-target-vip", TargetID: "target-1", UpstreamGroup: "vip", Quota: 1000, ExpiresAt: 1798761600, Models: []string{"gpt-4o"}}
			test.mutate(&record)
			cfg.Upstreams[0].ManagedTokens = []ManagedTokenRecord{record}
			if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "managed_tokens") {
				t.Fatalf("Validate() error=%v", err)
			}
		})
	}
}

func TestManagedTokenRecordsAreUniqueDeepCopiedAndYAMLRoundTrip(t *testing.T) {
	cfg := managedTokenConfig()
	record := ManagedTokenRecord{IdempotencyKey: "key-1", Status: ManagedTokenPending, Name: "synchub-target-vip", TargetID: "target-1", UpstreamGroup: "vip", Quota: 1000, ExpiresAt: 1798761600, Models: []string{"gpt-4o"}}
	cfg.Upstreams[0].ManagedTokens = []ManagedTokenRecord{record}
	if err := Validate(&cfg); err != nil {
		t.Fatal(err)
	}
	cloned := deepCopy(cfg)
	cloned.Upstreams[0].ManagedTokens[0].Models[0] = "mutated"
	if !reflect.DeepEqual(cfg.Upstreams[0].ManagedTokens[0].Models, []string{"gpt-4o"}) {
		t.Fatalf("original mutated: %#v", cfg.Upstreams[0].ManagedTokens)
	}

	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Upstreams[0].ManagedTokens, cfg.Upstreams[0].ManagedTokens) {
		t.Fatalf("decoded=%#v want=%#v", decoded.Upstreams[0].ManagedTokens, cfg.Upstreams[0].ManagedTokens)
	}

	duplicate := managedTokenConfig()
	duplicate.Upstreams[0].ManagedTokens = []ManagedTokenRecord{record, record}
	if err := Validate(&duplicate); err == nil || !strings.Contains(err.Error(), "idempotency_key") {
		t.Fatalf("duplicate error=%v", err)
	}
}
