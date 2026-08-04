package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadNormalizesLegacyGenericAPIKeyToDefaultKey(t *testing.T) {
	t.Parallel()

	path := writeLegacyGenericConfig(t)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	upstream := cfg.Upstreams[0]
	wantKey := GenericKeyConfig{
		ID:      DefaultGenericKeyID,
		Name:    "Default",
		APIKey:  "legacy-shared-key",
		Enabled: true,
	}
	if !reflect.DeepEqual(upstream.Keys, []GenericKeyConfig{wantKey}) {
		t.Fatalf("normalized keys = %#v, want %#v", upstream.Keys, []GenericKeyConfig{wantKey})
	}
	if upstream.APIKey != "" {
		t.Fatalf("legacy api_key remained after normalization")
	}
	if got := upstream.SyncMappings[0].UpstreamAssetID; got != "source-legacy:endpoint" {
		t.Fatalf("legacy mapping changed to %q", got)
	}

	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalGenericKeyYAML(t, encoded)
}

func TestLoadDefaultsOmittedGenericKeyEnabledToTrue(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := baseConfigYAML(`  - id: source-generic
    name: Generic Source
    type: generic
    base_url: https://source.example.com
    keys:
      - id: primary
        name: Primary
        api_key: primary-secret
`)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Upstreams[0].Keys[0].Enabled {
		t.Fatal("omitted enabled did not default to true")
	}
}

func TestValidateNormalizesGenericKeys(t *testing.T) {
	t.Parallel()

	cfg := genericMultiKeyConfig()
	cfg.Upstreams[0].Keys[0].ID = " primary "
	cfg.Upstreams[0].Keys[0].Name = " Primary Key "
	cfg.Upstreams[0].Keys[0].Models = []string{" gpt-4.1 ", "claude-sonnet-4"}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	got := cfg.Upstreams[0].Keys[0]
	if got.ID != "primary" || got.Name != "Primary Key" {
		t.Fatalf("normalized identity = %#v", got)
	}
	if !reflect.DeepEqual(got.Models, []string{"gpt-4.1", "claude-sonnet-4"}) {
		t.Fatalf("normalized models = %#v", got.Models)
	}
	if cfg.Upstreams[0].Keys[1].Enabled {
		t.Fatal("explicitly disabled key was enabled")
	}
}

func TestValidateRejectsInvalidGenericKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*UpstreamConfig)
		want   string
	}{
		{name: "missing keys", mutate: func(upstream *UpstreamConfig) { upstream.Keys = nil }, want: "keys"},
		{name: "missing id", mutate: func(upstream *UpstreamConfig) { upstream.Keys[0].ID = " " }, want: "keys[0].id"},
		{name: "invalid id", mutate: func(upstream *UpstreamConfig) { upstream.Keys[0].ID = "bad/id" }, want: "keys[0].id"},
		{name: "missing name", mutate: func(upstream *UpstreamConfig) { upstream.Keys[0].Name = " " }, want: "keys[0].name"},
		{name: "missing api key", mutate: func(upstream *UpstreamConfig) { upstream.Keys[0].APIKey = " " }, want: "keys[0].api_key"},
		{name: "duplicate id", mutate: func(upstream *UpstreamConfig) { upstream.Keys[1].ID = "primary" }, want: "keys[1].id"},
		{name: "duplicate name", mutate: func(upstream *UpstreamConfig) { upstream.Keys[1].Name = "PRIMARY" }, want: "keys[1].name"},
		{name: "blank model", mutate: func(upstream *UpstreamConfig) { upstream.Keys[0].Models = []string{"gpt-4.1", " "} }, want: "keys[0].models"},
		{name: "duplicate model", mutate: func(upstream *UpstreamConfig) { upstream.Keys[0].Models = []string{"gpt-4.1", "gpt-4.1"} }, want: "keys[0].models"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := genericMultiKeyConfig()
			test.mutate(&cfg.Upstreams[0])
			err := Validate(&cfg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsManualKeysForNewAPI(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Upstreams[0] = newAPIUpstreamConfig()
	cfg.Upstreams[0].Keys = []GenericKeyConfig{{
		ID: "manual", Name: "Manual", APIKey: "must-not-be-accepted", Enabled: true,
	}}
	if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "keys") {
		t.Fatalf("Validate() error = %v, want keys rejection", err)
	}
}

func TestGenericKeysAreWriteOnlyAndDeepCopied(t *testing.T) {
	t.Parallel()

	cfg := genericMultiKeyConfig()
	if err := Validate(&cfg); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "primary-secret") || strings.Contains(string(encoded), "backup-secret") || strings.Contains(string(encoded), "api_key") {
		t.Fatalf("JSON leaked generic key credential: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"keys"`) || !strings.Contains(string(encoded), `"primary"`) {
		t.Fatalf("JSON omitted public key metadata: %s", encoded)
	}

	cloned := deepCopy(cfg)
	cloned.Upstreams[0].Keys[0].APIKey = "mutated-secret"
	cloned.Upstreams[0].Keys[0].Models[0] = "mutated-model"
	cloned.Upstreams[0].Keys = append(cloned.Upstreams[0].Keys, GenericKeyConfig{ID: "third"})
	if got := cfg.Upstreams[0].Keys; len(got) != 2 || got[0].APIKey != "primary-secret" || !reflect.DeepEqual(got[0].Models, []string{"gpt-4.1"}) {
		t.Fatalf("deepCopy shared generic key storage: %#v", got)
	}
}

func TestStoreCopiesAndAtomicallyPersistsGenericKeys(t *testing.T) {
	t.Parallel()

	path := writeLegacyGenericConfig(t)
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	snapshot := store.Snapshot()
	snapshot.Upstreams[0].Keys[0].APIKey = "snapshot-mutation"
	if got := store.Snapshot().Upstreams[0].Keys[0].APIKey; got != "legacy-shared-key" {
		t.Fatalf("snapshot mutation leaked into store: %q", got)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(context.Background(), func(cfg *Config) error {
		cfg.Upstreams[0].Keys = append(cfg.Upstreams[0].Keys, cfg.Upstreams[0].Keys[0])
		return nil
	})
	if err == nil {
		t.Fatal("duplicate key update unexpectedly succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || len(store.Snapshot().Upstreams[0].Keys) != 1 {
		t.Fatal("failed key update changed persisted or in-memory config")
	}

	err = store.Update(context.Background(), func(cfg *Config) error {
		cfg.Upstreams[0].Keys = append(cfg.Upstreams[0].Keys, GenericKeyConfig{
			ID: "backup", Name: "Backup", APIKey: "backup-secret", Enabled: false,
			Models: []string{"gpt-4.1-mini"},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Upstreams[0].Keys, store.Snapshot().Upstreams[0].Keys) {
		t.Fatalf("reloaded keys = %#v, snapshot = %#v", reloaded.Upstreams[0].Keys, store.Snapshot().Upstreams[0].Keys)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalGenericKeyYAML(t, encoded)
}

func genericMultiKeyConfig() Config {
	cfg := validTestConfig()
	cfg.Upstreams[0].APIKey = ""
	cfg.Upstreams[0].Keys = []GenericKeyConfig{
		{ID: "primary", Name: "Primary", APIKey: "primary-secret", Enabled: true, Models: []string{"gpt-4.1"}},
		{ID: "backup", Name: "Backup", APIKey: "backup-secret", Enabled: false},
	}
	return cfg
}

func writeLegacyGenericConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := baseConfigYAML(`  - id: source-legacy
    name: Legacy Source
    type: generic
    base_url: https://source.example.com/v1/
    api_key: legacy-shared-key
    sync_mappings:
      - upstream_asset_id: source-legacy:endpoint
        target_id: target-1
        target_channel_id: "42"
`)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func baseConfigYAML(upstreams string) string {
	return `app:
  host: 127.0.0.1
  port: 8888
  reconcile_interval: 5m
  request_timeout: 15s
  sync_concurrency: 4
targets:
  - id: target-1
    name: Target
    type: newapi
    base_url: https://target.example.com
    access_token: target-token
upstreams:
` + upstreams
}

func assertCanonicalGenericKeyYAML(t *testing.T, encoded []byte) {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	upstreams, ok := document["upstreams"].([]any)
	if !ok || len(upstreams) != 1 {
		t.Fatalf("encoded upstreams = %#v", document["upstreams"])
	}
	upstream, ok := upstreams[0].(map[string]any)
	if !ok {
		t.Fatalf("encoded upstream = %#v", upstreams[0])
	}
	if _, exists := upstream["api_key"]; exists {
		t.Fatalf("canonical YAML retained top-level api_key:\n%s", encoded)
	}
	if _, exists := upstream["keys"]; !exists {
		t.Fatalf("canonical YAML omitted keys:\n%s", encoded)
	}
}
