package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestServiceSanitizesSecretResolutionFailureAndWipesReturnedBytes(t *testing.T) {
	t.Parallel()

	secretBytes := []byte("placeholder-sensitive-value")
	source := &fakeSource{
		secret: platform.ResolvedSecret{Bytes: secretBytes},
		err:    errors.New("provider exposed placeholder-sensitive-value"),
	}
	store := &fakeMappingStore{}
	var createCalls atomic.Int32
	target := &fakeTarget{id: "compatible", create: func(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
		createCalls.Add(1)
		return platform.Channel{}, nil
	}}

	result, err := NewService(store, Options{Concurrency: 2}).Sync(context.Background(), BatchRequest{
		Asset:  enabledStaticAsset(),
		Source: source,
		Targets: []TargetRequest{
			{ID: "compatible", Adapter: target, Capabilities: staticKeyCapabilities()},
			{ID: "incompatible", Adapter: target, Capabilities: platform.TargetCapabilities{}},
		},
	})
	if !errors.Is(err, ErrSecretResolve) {
		t.Fatalf("Sync() error = %v, want ErrSecretResolve", err)
	}
	if strings.Contains(err.Error(), "placeholder-sensitive-value") {
		t.Fatalf("resolution error leaked secret: %v", err)
	}
	if createCalls.Load() != 0 {
		t.Fatalf("CreateChannel calls = %d, want 0", createCalls.Load())
	}
	if source.resolveCalls.Load() != 1 {
		t.Fatalf("ResolveSecret calls = %d, want 1", source.resolveCalls.Load())
	}
	assertTargetResult(t, result, "compatible", TargetFailed, "secret_resolve_failed")
	assertTargetResult(t, result, "incompatible", TargetIncompatible, "incompatible_target")
	assertWiped(t, secretBytes)
	assertStoreCalls(t, store, 0)
}

func TestServiceHonorsCancellationBeforeSecretResolution(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &fakeSource{secret: platform.ResolvedSecret{Bytes: []byte("unused-placeholder")}}
	store := &fakeMappingStore{}

	result, err := NewService(store, Options{Concurrency: 1}).Sync(ctx, BatchRequest{
		Asset:   enabledStaticAsset(),
		Source:  source,
		Targets: []TargetRequest{{ID: "target", Adapter: &fakeTarget{id: "target"}, Capabilities: staticKeyCapabilities()}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync() error = %v, want context.Canceled", err)
	}
	if source.resolveCalls.Load() != 0 {
		t.Fatalf("ResolveSecret calls = %d, want 0", source.resolveCalls.Load())
	}
	assertTargetResult(t, result, "target", TargetFailed, "context_cancelled")
	assertStoreCalls(t, store, 0)
}

func TestServiceNormalizesNonPositiveConcurrencyAndIsolatesTargetSecrets(t *testing.T) {
	t.Parallel()

	for _, concurrency := range []int{0, -3} {
		concurrency := concurrency
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			t.Parallel()

			secretBytes := []byte("per-target-placeholder")
			source := &fakeSource{secret: platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: secretBytes}}
			store := &fakeMappingStore{}
			var active atomic.Int32
			var maximum atomic.Int32
			var capturedMu sync.Mutex
			var captured [][]byte

			newTarget := func(id string) *fakeTarget {
				return &fakeTarget{id: id, create: func(_ context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
					current := active.Add(1)
					defer active.Add(-1)
					updateMaximum(&maximum, current)
					if got := string(input.Secret); got != "per-target-placeholder" {
						return platform.Channel{}, fmt.Errorf("secret copy was shared: %q", got)
					}
					capturedMu.Lock()
					captured = append(captured, input.Secret)
					capturedMu.Unlock()
					input.Secret[0] = 'X'
					time.Sleep(5 * time.Millisecond)
					return platform.Channel{ID: id + "-channel"}, nil
				}}
			}

			result, err := NewService(store, Options{Concurrency: concurrency}).Sync(context.Background(), BatchRequest{
				Asset:  enabledStaticAsset(),
				Source: source,
				Targets: []TargetRequest{
					{ID: "first", Adapter: newTarget("first"), Capabilities: staticKeyCapabilities()},
					{ID: "second", Adapter: newTarget("second"), Capabilities: staticKeyCapabilities()},
				},
			})
			if err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			if got := maximum.Load(); got != 1 {
				t.Fatalf("maximum concurrency = %d, want 1", got)
			}
			assertTargetResult(t, result, "first", TargetSynced, "")
			assertTargetResult(t, result, "second", TargetSynced, "")
			assertWiped(t, secretBytes)
			capturedMu.Lock()
			defer capturedMu.Unlock()
			if len(captured) != 2 {
				t.Fatalf("captured secrets = %d, want 2", len(captured))
			}
			for _, secret := range captured {
				assertWiped(t, secret)
			}
		})
	}
}

func TestServiceDoesNotExposeTargetErrorsOrPersistFailures(t *testing.T) {
	t.Parallel()

	const sensitive = "target-sensitive-placeholder"
	store := &fakeMappingStore{}
	source := &fakeSource{secret: platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: []byte(sensitive)}}
	target := &fakeTarget{id: "target", create: func(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
		return platform.Channel{}, errors.New("target returned " + sensitive)
	}}

	result, err := NewService(store, Options{Concurrency: 1}).Sync(context.Background(), BatchRequest{
		Asset:   enabledStaticAsset(),
		Source:  source,
		Targets: []TargetRequest{{ID: "target", Adapter: target, Capabilities: staticKeyCapabilities()}},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", result), sensitive) {
		t.Fatalf("target result leaked sensitive error: %#v", result)
	}
	assertTargetResult(t, result, "target", TargetFailed, "target_create_failed")
	assertStoreCalls(t, store, 0)
}

func TestServiceRejectsMissingDependenciesWithoutExternalCalls(t *testing.T) {
	t.Parallel()

	t.Run("typed nil source", func(t *testing.T) {
		var source *fakeSource
		store := &fakeMappingStore{}
		var createCalls atomic.Int32
		target := &fakeTarget{id: "target", create: func(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
			createCalls.Add(1)
			return platform.Channel{}, nil
		}}

		result, err := NewService(store, Options{Concurrency: 1}).Sync(context.Background(), BatchRequest{
			Asset:   enabledStaticAsset(),
			Source:  source,
			Targets: []TargetRequest{{ID: "target", Adapter: target, Capabilities: staticKeyCapabilities()}},
		})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Sync() error = %v, want ErrInvalidRequest", err)
		}
		if createCalls.Load() != 0 {
			t.Fatalf("CreateChannel calls = %d, want 0", createCalls.Load())
		}
		assertTargetResult(t, result, "target", TargetFailed, "invalid_request")
	})

	t.Run("typed nil mapping store", func(t *testing.T) {
		var store *fakeMappingStore
		source := &fakeSource{secret: platform.ResolvedSecret{Bytes: []byte("unused-placeholder")}}

		result, err := NewService(store, Options{Concurrency: 1}).Sync(context.Background(), BatchRequest{
			Asset:   enabledStaticAsset(),
			Source:  source,
			Targets: []TargetRequest{{ID: "target", Adapter: &fakeTarget{id: "target"}, Capabilities: staticKeyCapabilities()}},
		})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Sync() error = %v, want ErrInvalidRequest", err)
		}
		if source.resolveCalls.Load() != 0 {
			t.Fatalf("ResolveSecret calls = %d, want 0", source.resolveCalls.Load())
		}
		assertTargetResult(t, result, "target", TargetFailed, "invalid_request")
	})
}

func TestServiceSkipsNilTargetsAndNoOpBatchesWithoutResolvingSecrets(t *testing.T) {
	t.Parallel()

	t.Run("nil target", func(t *testing.T) {
		source := &fakeSource{secret: platform.ResolvedSecret{Bytes: []byte("unused-placeholder")}}
		store := &fakeMappingStore{}
		result, err := NewService(store, Options{Concurrency: 1}).Sync(context.Background(), BatchRequest{
			Asset:   enabledStaticAsset(),
			Source:  source,
			Targets: []TargetRequest{{ID: "target", Capabilities: staticKeyCapabilities()}},
		})
		if err != nil {
			t.Fatalf("Sync() error = %v", err)
		}
		if source.resolveCalls.Load() != 0 {
			t.Fatalf("ResolveSecret calls = %d, want 0", source.resolveCalls.Load())
		}
		assertTargetResult(t, result, "target", TargetFailed, "invalid_target")
		assertStoreCalls(t, store, 0)
	})

	t.Run("empty target list", func(t *testing.T) {
		result, err := NewService(nil, Options{}).Sync(context.Background(), BatchRequest{Asset: enabledStaticAsset()})
		if err != nil {
			t.Fatalf("Sync() error = %v", err)
		}
		if len(result.Targets) != 0 {
			t.Fatalf("targets = %#v, want empty", result.Targets)
		}
	})
}

func TestServiceCancellationDuringTargetCreationWipesAllSecretBuffers(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secretBytes := []byte("cancellation-placeholder")
	source := &fakeSource{secret: platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: secretBytes}}
	store := &fakeMappingStore{}
	started := make(chan struct{})
	var targetSecret []byte
	target := &fakeTarget{id: "target", create: func(ctx context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
		targetSecret = input.Secret
		close(started)
		<-ctx.Done()
		return platform.Channel{}, errors.New("adapter included cancellation-placeholder")
	}}

	done := make(chan struct{})
	var result BatchResult
	var err error
	go func() {
		defer close(done)
		result, err = NewService(store, Options{Concurrency: 1}).Sync(ctx, BatchRequest{
			Asset:   enabledStaticAsset(),
			Source:  source,
			Targets: []TargetRequest{{ID: "target", Adapter: target, Capabilities: staticKeyCapabilities()}},
		})
	}()
	<-started
	cancel()
	<-done

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync() error = %v, want context.Canceled", err)
	}
	assertTargetResult(t, result, "target", TargetFailed, "context_cancelled")
	assertWiped(t, secretBytes)
	assertWiped(t, targetSecret)
	assertStoreCalls(t, store, 0)
}

func TestServiceRejectsInvalidResolvedSecretBeforeTargetCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		secret platform.ResolvedSecret
	}{
		{
			name: "kind mismatch",
			secret: platform.ResolvedSecret{
				Kind:  platform.AssetOAuthFile,
				Bytes: []byte("kind-mismatch-sensitive-placeholder"),
			},
		},
		{
			name: "empty secret",
			secret: platform.ResolvedSecret{
				Kind: platform.AssetStaticAPIKey,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			secretBytes := test.secret.Bytes
			source := &fakeSource{secret: test.secret}
			store := &fakeMappingStore{}
			var createCalls atomic.Int32
			target := &fakeTarget{id: "target", create: func(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
				createCalls.Add(1)
				return platform.Channel{ID: "unexpected"}, nil
			}}

			result, err := NewService(store, Options{Concurrency: 2}).Sync(context.Background(), BatchRequest{
				Asset:  enabledStaticAsset(),
				Source: source,
				Targets: []TargetRequest{
					{ID: "first", Adapter: target, Capabilities: staticKeyCapabilities()},
					{ID: "second", Adapter: target, Capabilities: staticKeyCapabilities()},
				},
			})
			if !errors.Is(err, ErrSecretResolve) {
				t.Fatalf("Sync() error = %v, want ErrSecretResolve", err)
			}
			for _, sensitive := range []string{string(platform.AssetOAuthFile), "kind-mismatch-sensitive-placeholder"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("Sync() error leaked resolved secret detail %q: %v", sensitive, err)
				}
			}
			if createCalls.Load() != 0 {
				t.Fatalf("CreateChannel calls = %d, want 0", createCalls.Load())
			}
			assertTargetResult(t, result, "first", TargetFailed, "secret_resolve_failed")
			assertTargetResult(t, result, "second", TargetFailed, "secret_resolve_failed")
			assertStoreCalls(t, store, 0)
			assertWiped(t, secretBytes)
		})
	}
}

func TestServiceUsesValidatedResolvedBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		resolvedURL string
		wantBaseURL string
	}{
		{name: "resolved override", resolvedURL: "https://resolved.example.com/v1", wantBaseURL: "https://resolved.example.com/v1"},
		{name: "empty metadata retains discovery URL", resolvedURL: "", wantBaseURL: "https://discovered.example.com/v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			secretBytes := []byte("base-url-sensitive-placeholder")
			source := &fakeSource{secret: platform.ResolvedSecret{
				Kind: platform.AssetStaticAPIKey, Bytes: secretBytes,
				Metadata: map[string]string{"base_url": test.resolvedURL},
			}}
			store := &fakeMappingStore{}
			var capturedSecret []byte
			target := &fakeTarget{id: "target", create: func(_ context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
				if input.BaseURL != test.wantBaseURL {
					t.Errorf("CreateChannel BaseURL = %q, want %q", input.BaseURL, test.wantBaseURL)
				}
				capturedSecret = input.Secret
				return platform.Channel{ID: "created"}, nil
			}}
			asset := enabledStaticAsset()
			asset.BaseURL = "https://discovered.example.com/v1"

			result, err := NewService(store, Options{Concurrency: 1}).Sync(context.Background(), BatchRequest{
				Asset: asset, Source: source,
				Targets: []TargetRequest{{ID: "target", Adapter: target, Capabilities: staticKeyCapabilities()}},
			})
			if err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			assertTargetResult(t, result, "target", TargetSynced, "")
			assertStoreCalls(t, store, 1)
			assertWiped(t, secretBytes)
			assertWiped(t, capturedSecret)
		})
	}
}

func TestServiceRejectsInvalidResolvedBaseURLBeforeTargetCalls(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{
		"/relative/base-url-sensitive-placeholder",
		"ftp://base-url-sensitive-placeholder.example.com/v1",
		"https://user:base-url-sensitive-placeholder@example.com/v1",
		"https:///base-url-sensitive-placeholder",
	} {
		baseURL := baseURL
		t.Run(baseURL, func(t *testing.T) {
			t.Parallel()

			secretBytes := []byte("resolved-secret-sensitive-placeholder")
			source := &fakeSource{secret: platform.ResolvedSecret{
				Kind: platform.AssetStaticAPIKey, Bytes: secretBytes,
				Metadata: map[string]string{"base_url": baseURL},
			}}
			store := &fakeMappingStore{}
			var createCalls atomic.Int32
			target := &fakeTarget{id: "target", create: func(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
				createCalls.Add(1)
				return platform.Channel{ID: "unexpected"}, nil
			}}

			result, err := NewService(store, Options{Concurrency: 1}).Sync(context.Background(), BatchRequest{
				Asset: enabledStaticAsset(), Source: source,
				Targets: []TargetRequest{{ID: "target", Adapter: target, Capabilities: staticKeyCapabilities()}},
			})
			if !errors.Is(err, ErrSecretResolve) {
				t.Fatalf("Sync() error = %v, want ErrSecretResolve", err)
			}
			for _, sensitive := range []string{baseURL, string(platform.AssetStaticAPIKey), "resolved-secret-sensitive-placeholder"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("Sync() error leaked resolved detail %q: %v", sensitive, err)
				}
			}
			if createCalls.Load() != 0 {
				t.Fatalf("CreateChannel calls = %d, want 0", createCalls.Load())
			}
			assertTargetResult(t, result, "target", TargetFailed, "secret_resolve_failed")
			assertStoreCalls(t, store, 0)
			assertWiped(t, secretBytes)
		})
	}
}

func enabledStaticAsset() platform.UpstreamAsset {
	return platform.UpstreamAsset{
		ID:         "source:channel:1",
		SourceID:   "source",
		SourceType: "newapi",
		Provider:   platform.ProviderOpenAI,
		Kind:       platform.AssetStaticAPIKey,
		Name:       "source channel",
		Enabled:    true,
	}
}

func staticKeyCapabilities() platform.TargetCapabilities {
	return platform.TargetCapabilities{
		Platform: "newapi",
		Providers: map[string]platform.ProviderCapability{
			platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
		},
	}
}

func updateMaximum(maximum *atomic.Int32, current int32) {
	for {
		old := maximum.Load()
		if current <= old || maximum.CompareAndSwap(old, current) {
			return
		}
	}
}

func assertWiped(t *testing.T, secret []byte) {
	t.Helper()
	for i, value := range secret {
		if value != 0 {
			t.Fatalf("secret byte %d was not wiped", i)
		}
	}
}

func assertStoreCalls(t *testing.T, store *fakeMappingStore, want int) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.calls != want {
		t.Fatalf("SaveMappings calls = %d, want %d", store.calls, want)
	}
}
