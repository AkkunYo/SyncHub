package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestTargetValidationCapabilitySummaryIsDeepCopied(t *testing.T) {
	cfg := validTestConfig()
	checkedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	cfg.Targets[0].ValidationStatus = TargetValidationVerified
	cfg.Targets[0].ValidatedAt = &checkedAt
	cfg.Targets[0].ValidationCapabilities = platform.TargetCapabilities{
		Platform: "newapi",
		Providers: map[string]platform.ProviderCapability{
			platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
		},
	}

	cloned := deepCopy(cfg)
	cloned.Targets[0].ValidationCapabilities.Providers[platform.ProviderOpenAI].Modes[0] = platform.SyncModeProxyEndpoint
	cloned.Targets[0].ValidationCapabilities.Providers[platform.ProviderAnthropic] = platform.ProviderCapability{}
	cloned.Targets[0].ValidatedAt = nil

	got := cfg.Targets[0]
	if got.ValidatedAt == nil || !got.ValidatedAt.Equal(checkedAt) {
		t.Fatalf("validated_at changed through clone: %v", got.ValidatedAt)
	}
	if modes := got.ValidationCapabilities.Providers[platform.ProviderOpenAI].Modes; len(modes) != 1 || modes[0] != platform.SyncModeStaticKey {
		t.Fatalf("provider modes changed through clone: %#v", modes)
	}
	if _, exists := got.ValidationCapabilities.Providers[platform.ProviderAnthropic]; exists {
		t.Fatal("provider map changed through clone")
	}
}

func TestTargetValidationRoundTripsAndLegacyDefaultsToUnverified(t *testing.T) {
	cfg := validTestConfig()
	checkedAt := time.Date(2026, 8, 6, 12, 30, 0, 0, time.FixedZone("test", 8*60*60))
	cfg.Targets[0].ValidationStatus = TargetValidationVerified
	cfg.Targets[0].ValidatedAt = &checkedAt
	cfg.Targets[0].ValidationCapabilities = platform.TargetCapabilities{
		Platform: "newapi",
		Providers: map[string]platform.ProviderCapability{
			platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey, platform.SyncModeProxyEndpoint}},
		},
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeAtomic(path, cfg); err != nil {
		t.Fatalf("writeAtomic() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	target := loaded.Targets[0]
	if target.ValidationStatus != TargetValidationVerified || target.ValidatedAt == nil || !target.ValidatedAt.Equal(checkedAt) {
		t.Fatalf("round-tripped validation=%#v", target)
	}
	if modes := target.ValidationCapabilities.Providers[platform.ProviderOpenAI].Modes; len(modes) != 2 {
		t.Fatalf("round-tripped capabilities=%#v", target.ValidationCapabilities)
	}

	legacy := validTestConfig()
	if err := Validate(&legacy); err != nil {
		t.Fatalf("Validate(legacy) error = %v", err)
	}
	if legacy.Targets[0].ValidationStatus != TargetValidationUnverified || legacy.Targets[0].ValidatedAt != nil {
		t.Fatalf("legacy validation=%#v", legacy.Targets[0])
	}
}
