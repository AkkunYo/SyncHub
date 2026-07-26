package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/config"
)

func TestNewAssemblesConsumableDependenciesAndCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	path := createConfigPath(t, config.Default())
	runtimeState := api.NewRuntime()
	application, err := New(Options{
		ConfigPath:         path,
		Version:            "test-version",
		HTTPClient:         &http.Client{},
		Runtime:            runtimeState,
		RequestIDGenerator: func() string { return "test-request-id" },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if application.Runtime() != runtimeState {
		t.Fatal("Runtime() did not return the injected shared runtime")
	}
	dependencies := application.Dependencies()
	if dependencies.Config == nil || dependencies.Adapters == nil || dependencies.Discovery == nil ||
		dependencies.Sync == nil || dependencies.Mappings == nil || dependencies.Reconcile == nil {
		t.Fatalf("Dependencies() incomplete: %#v", dependencies)
	}
	if dependencies.Version != "test-version" || dependencies.RequestIDGenerator == nil {
		t.Fatalf("Dependencies() metadata = %#v", dependencies)
	}
	if _, err := api.NewRouterWithRuntime(dependencies, application.Runtime()); err != nil {
		t.Fatalf("management router rejected assembled dependencies: %v", err)
	}

	if err := application.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	reopened, err := config.Open(path)
	if err != nil {
		t.Fatalf("config lock was not released: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened store: %v", err)
	}
}

func TestNewReleasesConfigLockWhenAssemblyFails(t *testing.T) {
	t.Parallel()

	const credentialCanary = "test-lock-secret-canary"
	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{
		ID: "sub-source", Name: "Sub source", Type: "sub2api",
		BaseURL: "https://sub.example.test/?marker=" + credentialCanary,
		APIKey:  credentialCanary,
	}}
	path := createConfigPath(t, cfg)
	application, err := New(Options{ConfigPath: path})
	if err == nil {
		if application != nil {
			_ = application.Close()
		}
		t.Fatal("New() error = nil, want adapter validation failure")
	}
	if application != nil {
		t.Fatal("New() returned a partial application")
	}
	if strings.Contains(err.Error(), credentialCanary) {
		t.Fatalf("New() error leaked credential: %v", err)
	}

	reopened, reopenErr := config.Open(path)
	if reopenErr != nil {
		t.Fatalf("failed assembly retained config lock: %v", reopenErr)
	}
	if closeErr := reopened.Close(); closeErr != nil {
		t.Fatalf("close reopened store: %v", closeErr)
	}
}

func TestApplicationNilAndCloseErrorBoundaries(t *testing.T) {
	t.Parallel()

	if application, err := New(Options{}); !errors.Is(err, ErrConfigPathRequired) || application != nil {
		t.Fatalf("New(empty) = (%v, %v), want nil ErrConfigPathRequired", application, err)
	}
	var nilApplication *Application
	if dependencies := nilApplication.Dependencies(); dependencies.Config != nil {
		t.Fatalf("nil Dependencies() = %#v", dependencies)
	}
	if nilApplication.Runtime() != nil {
		t.Fatal("nil Runtime() must return nil")
	}
	if err := nilApplication.Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}

	wantErr := errors.New("test close failure")
	var calls atomic.Int32
	application := &Application{closeFn: func() error {
		calls.Add(1)
		return wantErr
	}}
	if err := application.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close() error = %v, want %v", err, wantErr)
	}
	if err := application.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("second Close() error = %v, want %v", err, wantErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", calls.Load())
	}
}

func createConfigPath(t *testing.T, cfg config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	store, err := config.Open(path)
	if err != nil {
		t.Fatalf("config.Open() error = %v", err)
	}
	if err := store.Update(context.Background(), func(current *config.Config) error {
		*current = cfg
		return nil
	}); err != nil {
		_ = store.Close()
		t.Fatalf("config update error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("config close error = %v", err)
	}
	return path
}
