package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestNewAPIUserIDYAMLRoundTripAndValidation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `app:
  host: 127.0.0.1
  port: 8888
  reconcile_interval: 5m
  request_timeout: 15s
  sync_concurrency: 4
targets:
  - id: target-newapi
    name: Target
    type: newapi
    base_url: https://target.example.com
    access_token: REPLACE_WITH_TARGET_TOKEN
    user_id: 17
upstreams:
  - id: source-newapi
    name: Source
    type: newapi
    base_url: https://source.example.com
    access_token: REPLACE_WITH_SOURCE_TOKEN
    user_id: 23
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := testUserID(t, &cfg.Targets[0]); got != 17 {
		t.Fatalf("target user_id = %d, want 17", got)
	}
	if got := testUserID(t, &cfg.Upstreams[0]); got != 23 {
		t.Fatalf("upstream user_id = %d, want 23", got)
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), "user_id: 17") || !strings.Contains(string(encoded), "user_id: 23") {
		t.Fatalf("round-trip YAML = %s", encoded)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "negative target", mutate: func(cfg *Config) { setTestUserID(t, &cfg.Targets[0], -1) }},
		{name: "non-New API target", mutate: func(cfg *Config) {
			cfg.Targets[0].Type = "cliproxyapi"
			cfg.Targets[0].AccessToken = ""
			cfg.Targets[0].ManagementKey = "REPLACE_WITH_MANAGEMENT_KEY"
			setTestUserID(t, &cfg.Targets[0], 1)
		}},
		{name: "negative upstream", mutate: func(cfg *Config) {
			cfg.Upstreams[0].Type = "newapi"
			cfg.Upstreams[0].APIKey = ""
			cfg.Upstreams[0].AccessToken = "REPLACE_WITH_SOURCE_TOKEN"
			setTestUserID(t, &cfg.Upstreams[0], -1)
		}},
		{name: "non-New API upstream", mutate: func(cfg *Config) { setTestUserID(t, &cfg.Upstreams[0], 1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validTestConfig()
			test.mutate(&cfg)
			if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "user_id") {
				t.Fatalf("Validate() error = %v, want user_id validation error", err)
			}
		})
	}
}

func setTestUserID(t *testing.T, value any, userID int) {
	t.Helper()
	field := reflect.ValueOf(value).Elem().FieldByName("UserID")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Int {
		t.Fatal("configuration is missing an integer UserID field")
	}
	field.SetInt(int64(userID))
}

func testUserID(t *testing.T, value any) int {
	t.Helper()
	field := reflect.ValueOf(value).Elem().FieldByName("UserID")
	if !field.IsValid() || field.Kind() != reflect.Int {
		t.Fatal("configuration is missing an integer UserID field")
	}
	return int(field.Int())
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if cfg.App.Host != "127.0.0.1" {
		t.Fatalf("default host = %q, want 127.0.0.1", cfg.App.Host)
	}
	if cfg.App.Port != 8888 {
		t.Fatalf("default port = %d, want 8888", cfg.App.Port)
	}
	if time.Duration(cfg.App.ReconcileInterval) != 5*time.Minute {
		t.Fatalf("default reconcile interval = %s", cfg.App.ReconcileInterval)
	}
	if time.Duration(cfg.App.RequestTimeout) != 15*time.Second {
		t.Fatalf("default request timeout = %s", cfg.App.RequestTimeout)
	}
	if cfg.App.SyncConcurrency != 4 {
		t.Fatalf("default sync concurrency = %d, want 4", cfg.App.SyncConcurrency)
	}
}

func TestValidateNormalizesURLsAndRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	valid := validTestConfig()
	valid.Targets[0].BaseURL = " https://target.example.com/// "
	valid.Upstreams[0].BaseURL = "https://source.example.com/"
	if err := Validate(&valid); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}
	if got := valid.Targets[0].BaseURL; got != "https://target.example.com" {
		t.Fatalf("normalized target URL = %q", got)
	}
	if got := valid.Upstreams[0].BaseURL; got != "https://source.example.com" {
		t.Fatalf("normalized upstream URL = %q", got)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"invalid port", func(c *Config) { c.App.Port = 0 }, "port"},
		{"empty host", func(c *Config) { c.App.Host = "" }, "host"},
		{"invalid concurrency", func(c *Config) { c.App.SyncConcurrency = 0 }, "sync_concurrency"},
		{"duplicate target id", func(c *Config) { c.Targets = append(c.Targets, c.Targets[0]) }, "target"},
		{"duplicate upstream id", func(c *Config) { c.Upstreams = append(c.Upstreams, c.Upstreams[0]) }, "upstream"},
		{"relative target url", func(c *Config) { c.Targets[0].BaseURL = "/api" }, "base_url"},
		{"unsupported target", func(c *Config) { c.Targets[0].Type = "sub2api" }, "type"},
		{"unsupported upstream", func(c *Config) { c.Upstreams[0].Type = "mystery" }, "type"},
		{"missing newapi token", func(c *Config) { c.Targets[0].AccessToken = "" }, "access_token"},
		{"missing generic api key", func(c *Config) { c.Upstreams[0].APIKey = "" }, "api_key"},
		{"mapping references missing target", func(c *Config) {
			c.Upstreams[0].SyncMappings = []SyncMapping{{
				UpstreamAssetID: "source:key:1",
				TargetID:        "missing",
				TargetChannelID: "1",
			}}
		}, "target_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validTestConfig()
			tt.mutate(&cfg)
			err := Validate(&cfg)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadMigratesLegacyMappingAndParsesDurations(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `app:
  host: 127.0.0.1
  port: 8888
  reconcile_interval: 30s
  request_timeout: 2s
  sync_concurrency: 2
targets:
  - id: target-1
    name: Target
    type: newapi
    base_url: https://target.example.com/
    access_token: target-token
upstreams:
  - id: source-1
    name: Source
    type: generic
    base_url: https://source.example.com/
    api_key: source-key
    sync_mappings:
      - upstream_key_id: legacy-key-7
        target_id: target-1
        target_channel_id: "42"
        source_provider: openai
        asset_kind: static_api_key
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := time.Duration(cfg.App.ReconcileInterval); got != 30*time.Second {
		t.Fatalf("reconcile interval = %s", got)
	}
	mapping := cfg.Upstreams[0].SyncMappings[0]
	if mapping.UpstreamAssetID != "source-1:key:legacy-key-7" {
		t.Fatalf("migrated asset id = %q", mapping.UpstreamAssetID)
	}
	if mapping.LegacyUpstreamKeyID != "" {
		t.Fatalf("legacy key id retained: %q", mapping.LegacyUpstreamKeyID)
	}
}

func TestOpenCreatesSecureDefaultAndLocksExclusively(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %#o, want 0600", got)
	}
	if got := store.Snapshot().App.Port; got != 8888 {
		t.Fatalf("stored default port = %d", got)
	}

	second, err := Open(path)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open() error = %v, want ErrLocked", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open() after Close error = %v", err)
	}
	_ = reopened.Close()
}

func TestOpenUsesInterprocessLock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	runConfigLockHelper(t, path, "locked")

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	runConfigLockHelper(t, path, "available")
}

func TestConfigLockHelperProcess(t *testing.T) {
	path := os.Getenv("SYNCHUB_TEST_CONFIG_LOCK_PATH")
	if path == "" {
		return
	}

	store, err := Open(path)
	if store != nil {
		defer store.Close()
	}
	switch os.Getenv("SYNCHUB_TEST_CONFIG_LOCK_STATE") {
	case "locked":
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("Open() error = %v, want ErrLocked", err)
		}
	case "available":
		if err != nil {
			t.Fatalf("Open() error = %v, want success", err)
		}
	default:
		t.Fatal("missing config lock helper state")
	}
}

func runConfigLockHelper(t *testing.T, path, state string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestConfigLockHelperProcess$")
	command.Env = append(os.Environ(),
		"SYNCHUB_TEST_CONFIG_LOCK_PATH="+path,
		"SYNCHUB_TEST_CONFIG_LOCK_STATE="+state,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("config lock helper (%s) failed: %v\n%s", state, err, output)
	}
}

func TestSnapshotIsDeepCopyAndUpdateIsAtomic(t *testing.T) {
	t.Parallel()

	path := writeValidConfig(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	snapshot := store.Snapshot()
	snapshot.Targets[0].AccessToken = "changed-outside-store"
	snapshot.Upstreams[0].SyncMappings = append(snapshot.Upstreams[0].SyncMappings, SyncMapping{TargetID: "x"})
	if got := store.Snapshot().Targets[0].AccessToken; got != "target-token" {
		t.Fatalf("snapshot mutation leaked into store: %q", got)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("stop update")
	err = store.Update(context.Background(), func(cfg *Config) error {
		cfg.Targets[0].AccessToken = "must-not-persist"
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Update() error = %v, want %v", err, wantErr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed mutation changed config file")
	}
	if got := store.Snapshot().Targets[0].AccessToken; got != "target-token" {
		t.Fatalf("failed mutation changed snapshot: %q", got)
	}

	err = store.Update(context.Background(), func(cfg *Config) error {
		cfg.App.Port = 0
		return nil
	})
	if err == nil {
		t.Fatal("invalid update unexpectedly succeeded")
	}
	if got := store.Snapshot().App.Port; got != 8888 {
		t.Fatalf("invalid update changed port to %d", got)
	}

	if err := store.Update(context.Background(), func(cfg *Config) error {
		cfg.Targets[0].Name = "Updated Target"
		return nil
	}); err != nil {
		t.Fatalf("valid Update() error = %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Targets[0].Name != "Updated Target" {
		t.Fatalf("persisted name = %q", reloaded.Targets[0].Name)
	}
}

func TestUpdateHonorsCancellationAndSerializesConcurrentMutations(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := store.Update(ctx, func(*Config) error { called = true; return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Update() error = %v", err)
	}
	if called {
		t.Fatal("mutator called for canceled context")
	}

	const updates = 24
	var wg sync.WaitGroup
	errCh := make(chan error, updates)
	for i := 0; i < updates; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- store.Update(context.Background(), func(cfg *Config) error {
				cfg.App.SyncConcurrency++
				return nil
			})
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent Update() error = %v", err)
		}
	}
	if got, want := store.Snapshot().App.SyncConcurrency, 4+updates; got != want {
		t.Fatalf("sync concurrency = %d, want %d", got, want)
	}
}

func validTestConfig() Config {
	cfg := Default()
	cfg.Targets = []TargetConfig{{
		ID:          "target-1",
		Name:        "Target",
		Type:        "newapi",
		BaseURL:     "https://target.example.com",
		AccessToken: "target-token",
	}}
	cfg.Upstreams = []UpstreamConfig{{
		ID:      "source-1",
		Name:    "Source",
		Type:    "generic",
		BaseURL: "https://source.example.com",
		APIKey:  "source-key",
	}}
	return cfg
}

func writeValidConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf(`app:
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
  - id: source-1
    name: Source
    type: generic
    base_url: https://source.example.com
    api_key: source-key
%s`, "")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
