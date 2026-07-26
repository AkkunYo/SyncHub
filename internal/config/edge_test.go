package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateAdditionalBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"reconcile interval", func(c *Config) { c.App.ReconcileInterval = 0 }, "reconcile_interval"},
		{"request timeout", func(c *Config) { c.App.RequestTimeout = 0 }, "request_timeout"},
		{"target id", func(c *Config) { c.Targets[0].ID = "" }, "target[0].id"},
		{"target name", func(c *Config) { c.Targets[0].Name = "" }, "target[0].name"},
		{"target cpa credential", func(c *Config) {
			c.Targets[0].Type = "cliproxyapi"
			c.Targets[0].ManagementKey = ""
			c.Targets[0].APIKey = ""
		}, "management_key"},
		{"upstream id", func(c *Config) { c.Upstreams[0].ID = "" }, "upstream[0].id"},
		{"upstream name", func(c *Config) { c.Upstreams[0].Name = "" }, "upstream[0].name"},
		{"upstream newapi credential", func(c *Config) {
			c.Upstreams[0].Type = "newapi"
			c.Upstreams[0].APIKey = ""
			c.Upstreams[0].AccessToken = ""
		}, "access_token"},
		{"upstream sub2api credential", func(c *Config) { c.Upstreams[0].APIKey = "" }, "api_key"},
		{"non-http url", func(c *Config) { c.Targets[0].BaseURL = "ftp://target.example.com" }, "http"},
		{"url user info", func(c *Config) { c.Targets[0].BaseURL = "https://user:pass@target.example.com" }, "user"},
		{"missing mapping asset", func(c *Config) {
			c.Upstreams[0].SyncMappings = []SyncMapping{{TargetID: "target-1", TargetChannelID: "1"}}
		}, "upstream_asset_id"},
		{"missing mapping channel", func(c *Config) {
			c.Upstreams[0].SyncMappings = []SyncMapping{{UpstreamAssetID: "asset", TargetID: "target-1"}}
		}, "target_channel_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validTestConfig()
			tt.mutate(&cfg)
			err := Validate(&cfg)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestDurationFormattingAndDecodeErrors(t *testing.T) {
	t.Parallel()

	if got := Duration(3 * time.Second).String(); got != "3s" {
		t.Fatalf("Duration.String() = %q", got)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	invalid := `app:
  host: 127.0.0.1
  port: 8888
  reconcile_interval: definitely-not-a-duration
  request_timeout: 15s
  sync_concurrency: 4
targets: []
upstreams: []
`
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("Load(invalid duration) error = %v", err)
	}
}

func TestStoreRejectsBrokenInputAndClosedUpdates(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("unknown_field: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(path); err == nil {
		_ = store.Close()
		t.Fatal("Open(unknown field) unexpectedly succeeded")
	}
	if err := os.WriteFile(path, []byte(`app:
  host: 127.0.0.1
  port: 8888
  reconcile_interval: 5m
  request_timeout: 15s
  sync_concurrency: 4
targets: []
upstreams: []
`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() after failed Open error = %v", err)
	}
	if err := store.Update(context.Background(), nil); err == nil {
		t.Fatal("Update(nil mutator) unexpectedly succeeded")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := store.Update(context.Background(), func(*Config) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Update(closed) error = %v", err)
	}

	var nilStore *Store
	if snapshot := nilStore.Snapshot(); snapshot.App.Port != 0 {
		t.Fatalf("nil Snapshot() = %#v", snapshot)
	}
	if err := nilStore.Update(context.Background(), func(*Config) error { return nil }); err == nil {
		t.Fatal("nil Store.Update() unexpectedly succeeded")
	}
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil Store.Close() error = %v", err)
	}
}

func TestOpenFailsWhenParentPathIsAFileAndReleasesClaim(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "config.yaml")
	if store, err := Open(path); err == nil {
		_ = store.Close()
		t.Fatal("Open() unexpectedly succeeded")
	}
	if store, err := Open(path); err == nil {
		_ = store.Close()
		t.Fatal("second Open() unexpectedly succeeded")
	} else if strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("failed Open retained process lock: %v", err)
	}
}
