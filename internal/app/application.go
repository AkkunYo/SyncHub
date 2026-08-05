package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/discovery"
	"github.com/AkkunYo/SyncHub/internal/mapping"
	"github.com/AkkunYo/SyncHub/internal/modelcatalog"
	"github.com/AkkunYo/SyncHub/internal/probe"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
)

type Options struct {
	ConfigPath         string
	Version            string
	BuildDate          string
	HTTPClient         *http.Client
	Runtime            *api.Runtime
	RequestIDGenerator func() string
}

// Application owns the config lock and the shared runtime state. HTTP and
// process lifecycle concerns remain with the command package.
type Application struct {
	deps    api.Dependencies
	runtime *api.Runtime

	closeFn   func() error
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
	running   atomic.Bool
}

func New(options Options) (_ *Application, resultErr error) {
	configPath := strings.TrimSpace(options.ConfigPath)
	if configPath == "" {
		return nil, ErrConfigPathRequired
	}
	store, err := config.Open(configPath)
	if err != nil {
		return nil, err
	}
	assembled := false
	defer func() {
		if !assembled {
			_ = store.Close()
		}
	}()

	mappings := mapping.NewRepository(store)
	discoveryService := discovery.NewService()
	reconcileService := reconcile.NewService(mappings)
	resolver := NewAdapterResolver(store, options.HTTPClient)
	syncService := NewSyncService(mappings)
	modelService := modelcatalog.NewService(store, options.HTTPClient, probe.NewService(options.HTTPClient))
	runtimeState := options.Runtime
	if runtimeState == nil {
		runtimeState = api.NewRuntime()
	}

	// Constructors perform local validation only. Preflight catches adapter
	// configuration errors while the deferred close still owns the file lock.
	snapshot := store.Snapshot()
	for _, target := range snapshot.Targets {
		if _, _, err := resolver.ResolveTarget(context.Background(), target); err != nil {
			return nil, errors.New("initialize target adapter: invalid configuration")
		}
	}
	for _, upstream := range snapshot.Upstreams {
		if _, err := resolver.ResolveUpstream(context.Background(), upstream); err != nil {
			return nil, errors.New("initialize upstream adapter: invalid configuration")
		}
	}

	application := &Application{
		deps: api.Dependencies{
			Config: store, Adapters: resolver, Discovery: discoveryService,
			Sync: syncService, Mappings: mappings, Reconcile: reconcileService, Models: modelService,
			Version: strings.TrimSpace(options.Version), BuildDate: strings.TrimSpace(options.BuildDate), RequestIDGenerator: options.RequestIDGenerator,
		},
		runtime: runtimeState,
		closeFn: store.Close,
	}
	assembled = true
	return application, nil
}

func (a *Application) Dependencies() api.Dependencies {
	if a == nil {
		return api.Dependencies{}
	}
	return a.deps
}

func (a *Application) Runtime() *api.Runtime {
	if a == nil {
		return nil
	}
	return a.runtime
}

func (a *Application) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		if a.closeFn != nil {
			a.closeErr = a.closeFn()
		}
	})
	return a.closeErr
}
