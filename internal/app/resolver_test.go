package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/platform/cliproxyapi"
	"github.com/AkkunYo/SyncHub/internal/platform/newapi"
	"github.com/AkkunYo/SyncHub/internal/platform/sub2api"
)

func TestAdapterResolverConstructsSupportedAdapters(t *testing.T) {
	t.Parallel()

	store := &memoryConfigStore{cfg: config.Default()}
	resolver := NewAdapterResolver(store, &http.Client{})
	ctx := context.Background()

	targetTests := []struct {
		name     string
		config   config.TargetConfig
		wantType any
		platform string
	}{
		{
			name: "new api",
			config: config.TargetConfig{
				ID: "new-target", Type: "newapi", BaseURL: "https://new.example.test", AccessToken: "test-console-token",
			},
			wantType: (*newapi.Target)(nil), platform: "newapi",
		},
		{
			name: "CLIProxyAPI",
			config: config.TargetConfig{
				ID: "cpa-target", Type: "cliproxyapi", BaseURL: "https://cpa.example.test", ManagementKey: "test-management-key",
			},
			wantType: (*cliproxyapi.Target)(nil), platform: "cliproxyapi",
		},
	}
	for _, test := range targetTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			adapter, capabilities, err := resolver.ResolveTarget(ctx, test.config)
			if err != nil {
				t.Fatalf("ResolveTarget() error = %v", err)
			}
			if got, want := reflect.TypeOf(adapter), reflect.TypeOf(test.wantType); got != want {
				t.Fatalf("adapter type = %v, want %v", got, want)
			}
			if capabilities.Platform != test.platform {
				t.Fatalf("capabilities platform = %q, want %q", capabilities.Platform, test.platform)
			}
		})
	}

	upstreamTests := []struct {
		name     string
		config   config.UpstreamConfig
		wantType any
	}{
		{
			name: "new api",
			config: config.UpstreamConfig{
				ID: "new-source", Type: "newapi", BaseURL: "https://new.example.test", AccessToken: "test-console-token", DiscoveryMode: "channel",
			},
			wantType: (*newapi.Source)(nil),
		},
		{
			name: "CLIProxyAPI",
			config: config.UpstreamConfig{
				ID: "cpa-source", Type: "cliproxyapi", BaseURL: "https://cpa.example.test", ManagementKey: "test-management-key",
			},
			wantType: (*cliproxyapi.Source)(nil),
		},
		{
			name: "Sub2Api",
			config: config.UpstreamConfig{
				ID: "sub-source", Type: "sub2api", BaseURL: "https://sub.example.test", APIKey: "test-admin-key",
			},
			wantType: (*sub2api.Source)(nil),
		},
	}
	for _, test := range upstreamTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			adapter, err := resolver.ResolveUpstream(ctx, test.config)
			if err != nil {
				t.Fatalf("ResolveUpstream() error = %v", err)
			}
			if got, want := reflect.TypeOf(adapter), reflect.TypeOf(test.wantType); got != want {
				t.Fatalf("adapter type = %v, want %v", got, want)
			}
		})
	}
}

func TestAdapterResolverRejectsSub2ApiTargetAndInvalidInputs(t *testing.T) {
	t.Parallel()

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, nil)
	_, _, err := resolver.ResolveTarget(context.Background(), config.TargetConfig{
		ID: "sub-target", Type: "sub2api", BaseURL: "https://sub.example.test", APIKey: "test-admin-key",
	})
	if !errors.Is(err, platform.ErrIncompatibleTarget) {
		t.Fatalf("ResolveTarget() error = %v, want ErrIncompatibleTarget", err)
	}

	if _, _, err := resolver.ResolveTarget(nil, config.TargetConfig{}); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("ResolveTarget(nil) error = %v, want ErrContextRequired", err)
	}
	if _, err := resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{Type: "unknown"}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("ResolveUpstream(unknown) error = %v, want ErrUnsupportedPlatform", err)
	}
	var nilResolver *AdapterResolver
	if _, err := nilResolver.ResolveUpstream(context.Background(), config.UpstreamConfig{}); !errors.Is(err, ErrDependenciesIncomplete) {
		t.Fatalf("nil resolver error = %v, want ErrDependenciesIncomplete", err)
	}
}

func TestAdapterResolverUsesInjectedClientConfiguredTimeoutAndAdminAuth(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.App.RequestTimeout = config.Duration(20 * time.Millisecond)
	store := &memoryConfigStore{cfg: cfg}
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer test-console-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("New-Api-User"); got != "" {
			t.Errorf("New-Api-User must be absent, got %q", got)
		}
		body := `{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":100}}`
		if request.URL.Path == "/api/user/self" {
			body = `{"success":true,"data":{"role":100,"group":"default"}}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	resolver := NewAdapterResolver(store, client)
	adapter, err := resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{
		ID: "new-source", Type: "newapi", BaseURL: "https://new.example.test", AccessToken: "test-console-token", DiscoveryMode: "channel",
	})
	if err != nil {
		t.Fatalf("ResolveUpstream() error = %v", err)
	}
	if _, err := adapter.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("transport calls = %d, want 3 (probe: user/self + channel; listing: channel)", calls.Load())
	}

	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	target, _, err := resolver.ResolveTarget(context.Background(), config.TargetConfig{
		ID: "new-target", Type: "newapi", BaseURL: "https://new.example.test", AccessToken: "test-console-token",
	})
	if err != nil {
		t.Fatalf("ResolveTarget() error = %v", err)
	}
	started := time.Now()
	_, err = target.ListChannels(context.Background())
	if err == nil {
		t.Fatal("ListChannels() error = nil, want timeout")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("configured request timeout took %s", elapsed)
	}
}

func TestAdapterResolverErrorsDoNotExposeCredentials(t *testing.T) {
	t.Parallel()

	const canary = "test-secret-canary-value"
	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, nil)
	_, err := resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{
		ID: "bad-source", Type: "sub2api", BaseURL: "https://sub.example.test/?leak=" + canary, APIKey: canary,
	})
	if err == nil {
		t.Fatal("ResolveUpstream() error = nil, want invalid configuration")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error leaked credential: %v", err)
	}
}

func TestAdapterResolverReusesCPAUpstreamDiscoveryStateForSecretResolution(t *testing.T) {
	t.Parallel()

	const (
		managementKey = "REPLACE_WITH_CPA_MANAGEMENT_KEY"
		assetID       = "source-cpa:cpa:gemini-stable-id"
		secretValue   = "REPLACE_WITH_DISCOVERED_GEMINI_KEY"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Management-Key"); got != managementKey {
			t.Errorf("X-Management-Key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = io.WriteString(w, `{"files":[{"id":"gemini-stable-id","auth_index":"stable-index","name":"gemini:apikey","provider":"gemini","status":"ready","runtime_only":true}]}`)
		case "/v0/management/auth-files/models":
			_, _ = io.WriteString(w, `{"models":[{"id":"gemini-2.5-pro"}]}`)
		case "/v0/management/gemini-api-key":
			_, _ = io.WriteString(w, `{"gemini-api-key":[{"api-key":"`+secretValue+`","auth-index":"stable-index"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, server.Client())
	cfg := config.UpstreamConfig{
		ID: "source-cpa", Type: "cliproxyapi", BaseURL: server.URL, ManagementKey: managementKey,
	}
	discoverySource, err := resolver.ResolveUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ResolveUpstream(discovery) error = %v", err)
	}
	page, err := discoverySource.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil || len(page.Assets) != 1 || page.Assets[0].ID != assetID {
		t.Fatalf("ListAssets() = %#v, %v", page, err)
	}

	syncSource, err := resolver.ResolveUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ResolveUpstream(sync) error = %v", err)
	}
	if syncSource != discoverySource {
		t.Fatal("same upstream configuration returned a new source and lost discovery state")
	}
	secret, err := syncSource.ResolveSecret(context.Background(), assetID, platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	defer secret.Wipe()
	if got := string(secret.Bytes); got != secretValue {
		t.Fatalf("ResolveSecret() = %q", got)
	}
}

func TestAdapterResolverConcurrentReuseAndConfigurationReplacement(t *testing.T) {
	t.Parallel()

	const (
		credentialA = "REPLACE_WITH_CPA_KEY_A"
		credentialB = "REPLACE_WITH_CPA_KEY_B"
	)
	newServer := func(wantCredential, authID string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Management-Key"); got != wantCredential {
				http.Error(w, "invalid management authentication", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v0/management/auth-files":
				_, _ = io.WriteString(w, `{"files":[{"id":"`+authID+`","auth_index":"index-`+authID+`","name":"gemini:apikey","provider":"gemini","status":"ready","runtime_only":true}]}`)
			case "/v0/management/auth-files/models":
				_, _ = io.WriteString(w, `{"models":[]}`)
			default:
				http.NotFound(w, r)
			}
		}))
	}
	serverA := newServer(credentialA, "asset-a")
	serverB := newServer(credentialB, "asset-b")
	t.Cleanup(serverA.Close)
	t.Cleanup(serverB.Close)

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, serverA.Client())
	cfgA := config.UpstreamConfig{ID: "shared-source", Type: "cliproxyapi", BaseURL: serverA.URL, ManagementKey: credentialA}
	first, err := resolver.ResolveUpstream(context.Background(), cfgA)
	if err != nil {
		t.Fatal(err)
	}

	const concurrentResolvers = 32
	resolved := make(chan platform.UpstreamAdapter, concurrentResolvers)
	errs := make(chan error, concurrentResolvers)
	var wait sync.WaitGroup
	for range concurrentResolvers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			adapter, resolveErr := resolver.ResolveUpstream(context.Background(), cfgA)
			resolved <- adapter
			errs <- resolveErr
		}()
	}
	wait.Wait()
	close(resolved)
	close(errs)
	for resolveErr := range errs {
		if resolveErr != nil {
			t.Fatalf("concurrent ResolveUpstream() error = %v", resolveErr)
		}
	}
	for adapter := range resolved {
		if adapter != first {
			t.Fatal("concurrent same-configuration resolution returned different sources")
		}
	}

	pageA, err := first.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil || len(pageA.Assets) != 1 {
		t.Fatalf("source A ListAssets() = %#v, %v", pageA, err)
	}
	cfgB := config.UpstreamConfig{ID: "shared-source", Type: "cliproxyapi", BaseURL: serverB.URL, ManagementKey: credentialB}
	second, err := resolver.ResolveUpstream(context.Background(), cfgB)
	if err != nil {
		t.Fatalf("ResolveUpstream(changed config) error = %v", err)
	}
	if second == first {
		t.Fatal("BaseURL and effective credential change reused the old source")
	}
	if _, err := second.ResolveSecret(context.Background(), pageA.Assets[0].ID, platform.SecretGrant{}); !errors.Is(err, platform.ErrSecretUnavailable) {
		t.Fatalf("replacement source inherited prior records: %v", err)
	}
	pageB, err := second.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil || len(pageB.Assets) != 1 || pageB.Assets[0].ID == pageA.Assets[0].ID {
		t.Fatalf("source B ListAssets() = %#v, %v", pageB, err)
	}

	typeChanged, err := resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{
		ID: "shared-source", Type: "newapi", BaseURL: serverB.URL, AccessToken: credentialB,
	})
	if err != nil {
		t.Fatalf("ResolveUpstream(type changed) error = %v", err)
	}
	if typeChanged == second || reflect.TypeOf(typeChanged) != reflect.TypeOf((*newapi.Source)(nil)) {
		t.Fatalf("type change did not replace cached source: %T", typeChanged)
	}

	const secretCanary = "REPLACE_WITH_PRIVATE_CACHE_KEY"
	_, err = resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{
		ID: "shared-source", Type: "cliproxyapi", BaseURL: "ftp://example.test/" + secretCanary, ManagementKey: secretCanary,
	})
	if err == nil {
		t.Fatal("invalid replacement configuration was accepted")
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Fatalf("replacement error leaked credential: %v", err)
	}
}

func TestAdapterResolverIncludesCPAProxyKeyInCacheIdentity(t *testing.T) {
	t.Parallel()

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, nil)
	cfg := config.UpstreamConfig{
		ID: "source-cpa", Type: "cliproxyapi", BaseURL: "https://cpa.example.test",
		ManagementKey: "REPLACE_WITH_CPA_MANAGEMENT_KEY", ProxyAPIKey: "REPLACE_WITH_CPA_PROXY_KEY_A",
	}
	if formatted := fmt.Sprintf("%#v", newUpstreamIdentity(cfg, time.Second)); strings.Contains(formatted, cfg.ProxyAPIKey) {
		t.Fatalf("upstream identity retained proxy key plaintext: %s", formatted)
	}
	first, err := resolver.ResolveUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := resolver.ResolveUpstream(context.Background(), cfg); err != nil || again != first {
		t.Fatalf("unchanged proxy key ResolveUpstream() = %T, %v; want cached adapter", again, err)
	}
	cfg.ProxyAPIKey = "REPLACE_WITH_CPA_PROXY_KEY_B"
	second, err := resolver.ResolveUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("proxy_api_key change reused cached CPA source")
	}
}

func TestAdapterResolverPropagatesNewAPIUserIDAndIncludesItInSourceIdentity(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	headers := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("New-Api-User"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/user/self" {
			_, _ = io.WriteString(w, `{"success":true,"data":{"role":100,"group":"default"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":100}}`)
	}))
	t.Cleanup(server.Close)
	store := &memoryConfigStore{cfg: config.Config{App: config.AppConfig{RequestTimeout: config.Duration(time.Second)}}}
	resolver := NewAdapterResolver(store, server.Client())

	targetConfig := config.TargetConfig{ID: "target-a", Type: "newapi", BaseURL: server.URL, AccessToken: "REPLACE_WITH_TARGET_TOKEN"}
	setResolverUserIDForTest(t, &targetConfig, 31)
	target, _, err := resolver.ResolveTarget(context.Background(), targetConfig)
	if err != nil {
		t.Fatalf("ResolveTarget() error = %v", err)
	}
	if _, err := target.ListChannels(context.Background()); err != nil {
		t.Fatalf("target.ListChannels() error = %v", err)
	}

	upstreamConfig := config.UpstreamConfig{ID: "source-a", Type: "newapi", BaseURL: server.URL, AccessToken: "REPLACE_WITH_SOURCE_TOKEN", DiscoveryMode: "channel"}
	setResolverUserIDForTest(t, &upstreamConfig, 41)
	first, err := resolver.ResolveUpstream(context.Background(), upstreamConfig)
	if err != nil {
		t.Fatalf("ResolveUpstream(first) error = %v", err)
	}
	if _, err := first.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatalf("first.ListAssets() error = %v", err)
	}
	setResolverUserIDForTest(t, &upstreamConfig, 42)
	second, err := resolver.ResolveUpstream(context.Background(), upstreamConfig)
	if err != nil {
		t.Fatalf("ResolveUpstream(second) error = %v", err)
	}
	if first == second {
		t.Fatal("user_id change reused cached New API source")
	}
	if _, err := second.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatalf("second.ListAssets() error = %v", err)
	}
	mu.Lock()
	got := append([]string(nil), headers...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"31", "41", "41", "41", "42", "42", "42"}) {
		t.Fatalf("New-Api-User headers = %#v", got)
	}
}

func setResolverUserIDForTest(t *testing.T, config any, userID int) {
	t.Helper()
	field := reflect.ValueOf(config).Elem().FieldByName("UserID")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Int {
		t.Fatal("resolver configuration is missing an integer UserID field")
	}
	field.SetInt(int64(userID))
}

type memoryConfigStore struct {
	mu  sync.RWMutex
	cfg config.Config
}

func (s *memoryConfigStore) Snapshot() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *memoryConfigStore) Update(ctx context.Context, mutate func(*config.Config) error) error {
	if ctx == nil {
		return ErrContextRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	if err := mutate(&next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

var _ api.ConfigStore = (*memoryConfigStore)(nil)
