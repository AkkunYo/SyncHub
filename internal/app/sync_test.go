package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/mapping"
	"github.com/AkkunYo/SyncHub/internal/platform"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

func TestSyncServiceBindsMappingsToSourceAndUsesRequestedConcurrency(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	for _, id := range []string{"target-a", "target-b", "target-c"} {
		cfg.Targets = append(cfg.Targets, config.TargetConfig{
			ID: id, Name: id, Type: "newapi", BaseURL: "https://target.example.test/" + id, AccessToken: "test-console-token",
		})
	}
	for _, id := range []string{"source-a", "source-b"} {
		cfg.Upstreams = append(cfg.Upstreams, config.UpstreamConfig{
			ID: id, Name: id, Type: "sub2api", BaseURL: "https://source.example.test/" + id, APIKey: "test-admin-key",
		})
	}
	path := createConfigPath(t, cfg)
	store, err := config.Open(path)
	if err != nil {
		t.Fatalf("config.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repository := mapping.NewRepository(store)
	service := NewSyncService(repository)
	probe := &concurrencyProbe{}
	targets := make([]syncservice.TargetRequest, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets = append(targets, syncservice.TargetRequest{
			ID:      target.ID,
			Adapter: &probeTarget{id: target.ID, probe: probe, delay: 25 * time.Millisecond},
			Capabilities: platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
				platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
			}},
		})
	}
	secretBytes := []byte("test-resolved-secret")
	request := syncservice.BatchRequest{
		Asset: platform.UpstreamAsset{
			ID: "source-b:key:asset-1", SourceID: "source-b", SourceType: "sub2api", Provider: platform.ProviderOpenAI,
			Kind: platform.AssetStaticAPIKey, Name: "test asset", Models: []string{"gpt-test"}, Enabled: true, SecretReadable: true,
		},
		Source:   &fakeUpstream{secret: secretBytes},
		Settings: platform.ChannelSettings{Models: []string{"gpt-test"}, Group: "default", Weight: 100},
		Targets:  targets,
	}
	result, err := service.Sync(context.Background(), "source-b", 2, request)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(result.Targets) != len(targets) {
		t.Fatalf("result targets = %d, want %d", len(result.Targets), len(targets))
	}
	for _, target := range result.Targets {
		if target.Status != syncservice.TargetSynced {
			t.Fatalf("target %q status = %q", target.TargetID, target.Status)
		}
	}
	if got := probe.maximum.Load(); got != 2 {
		t.Fatalf("maximum target concurrency = %d, want 2", got)
	}
	for i, value := range secretBytes {
		if value != 0 {
			t.Fatalf("resolved secret byte %d was not wiped", i)
		}
	}

	snapshot := store.Snapshot()
	if len(snapshot.Upstreams[0].SyncMappings) != 0 {
		t.Fatalf("source-a mappings = %#v, want none", snapshot.Upstreams[0].SyncMappings)
	}
	if got := len(snapshot.Upstreams[1].SyncMappings); got != len(targets) {
		t.Fatalf("source-b mappings = %d, want %d", got, len(targets))
	}
}

func TestSyncServiceRejectsNilAndBlankSourceWithoutPanic(t *testing.T) {
	t.Parallel()

	request := syncservice.BatchRequest{Targets: []syncservice.TargetRequest{{ID: "target-a"}}}
	if _, err := NewSyncService(nil).Sync(context.Background(), "source-a", 1, request); !errors.Is(err, ErrDependenciesIncomplete) {
		t.Fatalf("nil repository error = %v, want ErrDependenciesIncomplete", err)
	}

	cfg := config.Default()
	path := createConfigPath(t, cfg)
	store, err := config.Open(path)
	if err != nil {
		t.Fatalf("config.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := NewSyncService(mapping.NewRepository(store))
	if _, err := service.Sync(nil, "source-a", 1, request); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("nil context error = %v, want ErrContextRequired", err)
	}
	if _, err := service.Sync(context.Background(), " ", 1, request); !errors.Is(err, syncservice.ErrInvalidRequest) {
		t.Fatalf("blank source error = %v, want ErrInvalidRequest", err)
	}
}

type fakeUpstream struct {
	secret []byte
}

func (f *fakeUpstream) Capabilities(context.Context) (platform.SourceCapabilities, error) {
	return platform.SourceCapabilities{AssetKinds: []platform.AssetKind{platform.AssetStaticAPIKey}, SecretResolution: true}, nil
}

func (f *fakeUpstream) ListAssets(context.Context, platform.PageCursor) (platform.AssetPage, error) {
	return platform.AssetPage{}, nil
}

func (f *fakeUpstream) ResolveSecret(context.Context, string, platform.SecretGrant) (platform.ResolvedSecret, error) {
	return platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: f.secret}, nil
}

type concurrencyProbe struct {
	active  atomic.Int32
	maximum atomic.Int32
}

func (p *concurrencyProbe) enter() {
	active := p.active.Add(1)
	for {
		maximum := p.maximum.Load()
		if active <= maximum || p.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
}

func (p *concurrencyProbe) leave() {
	p.active.Add(-1)
}

type probeTarget struct {
	id    string
	probe *concurrencyProbe
	delay time.Duration

	mu       sync.Mutex
	channels []platform.Channel
}

func (t *probeTarget) ListChannels(context.Context) ([]platform.Channel, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]platform.Channel(nil), t.channels...), nil
}

func (t *probeTarget) CreateChannel(ctx context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
	t.probe.enter()
	defer t.probe.leave()
	select {
	case <-ctx.Done():
		return platform.Channel{}, ctx.Err()
	case <-time.After(t.delay):
	}
	channel := platform.Channel{
		ID: fmt.Sprintf("%s-channel", t.id), Name: input.Name, Provider: input.Provider, BaseURL: input.BaseURL,
		Models: append([]string(nil), input.Models...), Group: input.Group, Priority: input.Priority, Weight: input.Weight, Enabled: true,
	}
	t.mu.Lock()
	t.channels = append(t.channels, channel)
	t.mu.Unlock()
	return channel, nil
}

func (t *probeTarget) UpdateChannel(context.Context, string, platform.UpdateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("not implemented in test")
}

func (t *probeTarget) DeleteChannel(context.Context, string) error {
	return errors.New("not implemented in test")
}
