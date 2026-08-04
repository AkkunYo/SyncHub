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
	"github.com/AkkunYo/SyncHub/internal/platform/generic"
	"github.com/AkkunYo/SyncHub/internal/platform/newapi"
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
				ID: "new-source", Type: "newapi", BaseURL: "https://new.example.test", AccessToken: "test-console-token", DiscoveryMode: "token",
			},
			wantType: (*newapi.Source)(nil),
		},
		{
			name: "generic",
			config: config.UpstreamConfig{
				ID: "generic-source", Name: "Shared Endpoint", Type: "generic", BaseURL: "https://generic.example.test/v1", APIKey: "test-shared-key",
			},
			wantType: (*generic.Source)(nil),
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

func TestAdapterResolverForwardsGenericNameURLAndAPIKey(t *testing.T) {
	t.Parallel()

	const sharedKey = "REPLACE_WITH_GENERIC_SHARED_KEY"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+sharedKey {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"}]}`)
	}))
	t.Cleanup(server.Close)

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, server.Client())
	adapter, err := resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{
		ID: "generic-source", Name: "Shared Endpoint", Type: "generic",
		BaseURL: server.URL + "/v1", APIKey: sharedKey,
	})
	if err != nil {
		t.Fatalf("ResolveUpstream() error = %v", err)
	}
	page, err := adapter.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if len(page.Assets) != 1 || page.Assets[0].Name != "Shared Endpoint" || page.Assets[0].ID != "generic-source:endpoint" {
		t.Fatalf("generic assets = %#v", page.Assets)
	}
}

func TestAdapterResolverUsesAllGenericKeysAndInvalidatesTheirCacheIdentity(t *testing.T) {
	t.Parallel()

	modelsByAuthorization := map[string]string{
		"Bearer primary-secret": `{"data":[{"id":"primary-model"}]}`,
		"Bearer backup-secret":  `{"data":[{"id":"backup-model"}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, ok := modelsByAuthorization[r.Header.Get("Authorization")]
		if !ok {
			t.Errorf("unexpected Authorization header %q", r.Header.Get("Authorization"))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(server.Close)

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, server.Client())
	cfg := config.UpstreamConfig{
		ID: "generic-source", Name: "Shared Endpoint", Type: "generic", BaseURL: server.URL,
		Keys: []config.GenericKeyConfig{
			{ID: config.DefaultGenericKeyID, Name: "Primary", APIKey: "primary-secret", Enabled: true},
			{ID: "backup", Name: "Backup", APIKey: "backup-secret", Enabled: true},
		},
	}
	first, err := resolver.ResolveUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ResolveUpstream() error = %v", err)
	}
	page, err := first.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if len(page.Assets) != 2 || page.Assets[0].ID != "generic-source:endpoint" || page.Assets[1].ID != "generic-source:key:backup" {
		t.Fatalf("generic assets = %#v", page.Assets)
	}
	if !reflect.DeepEqual(page.Assets[0].Models, []string{"primary-model"}) || !reflect.DeepEqual(page.Assets[1].Models, []string{"backup-model"}) {
		t.Fatalf("generic models = %#v", page.Assets)
	}
	cached, err := resolver.ResolveUpstream(context.Background(), cfg)
	if err != nil || cached != first {
		t.Fatalf("unchanged config did not reuse adapter: adapter=%p error=%v", cached, err)
	}

	mutations := []struct {
		name   string
		mutate func(*config.UpstreamConfig)
	}{
		{name: "key ID", mutate: func(value *config.UpstreamConfig) { value.Keys[1].ID = "backup-2" }},
		{name: "key name", mutate: func(value *config.UpstreamConfig) { value.Keys[1].Name = "Backup 2" }},
		{name: "enabled state", mutate: func(value *config.UpstreamConfig) { value.Keys[1].Enabled = false }},
		{name: "configured models", mutate: func(value *config.UpstreamConfig) { value.Keys[1].Models = []string{"manual-model"} }},
		{name: "credential", mutate: func(value *config.UpstreamConfig) { value.Keys[1].APIKey = "replacement-secret" }},
	}
	previous := first
	for _, test := range mutations {
		nextConfig := cfg
		nextConfig.Keys = append([]config.GenericKeyConfig(nil), cfg.Keys...)
		test.mutate(&nextConfig)
		next, resolveErr := resolver.ResolveUpstream(context.Background(), nextConfig)
		if resolveErr != nil {
			t.Fatalf("ResolveUpstream(%s) error = %v", test.name, resolveErr)
		}
		if next == previous {
			t.Fatalf("%s change reused cached generic source", test.name)
		}
		previous = next
		cfg = nextConfig
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

func TestAdapterResolverUsesInjectedClientConfiguredTimeoutAndUserAuth(t *testing.T) {
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
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	resolver := NewAdapterResolver(store, client)
	adapter, err := resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{
		ID: "new-source", Type: "newapi", BaseURL: "https://new.example.test", AccessToken: "test-console-token", DiscoveryMode: "token",
	})
	if err != nil {
		t.Fatalf("ResolveUpstream() error = %v", err)
	}
	if _, err := adapter.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("transport calls = %d, want 1 token listing call", calls.Load())
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

func TestAdapterResolverModeStatusUsesOnlyMatchingCachedIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"role":1,"group":"default"}}`)
	}))
	t.Cleanup(server.Close)
	store := &memoryConfigStore{cfg: config.Default()}
	resolver := NewAdapterResolver(store, server.Client())
	cfg := config.UpstreamConfig{ID: "source-a", Type: "newapi", BaseURL: server.URL, AccessToken: "test-token", DiscoveryMode: "token"}

	if got := resolver.DiscoveryModeStatus(cfg); got.Status != "ready" || got.EffectiveMode != "token" {
		t.Fatalf("token status = %#v", got)
	}
	adapter, err := resolver.ResolveUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := resolver.DiscoveryModeStatus(cfg); got.Status != "ready" || got.EffectiveMode != "token" {
		t.Fatalf("cached status = %#v", got)
	}

	changed := cfg
	changed.AccessToken = "changed-token"
	if got := resolver.DiscoveryModeStatus(changed); got.Status != "ready" || got.EffectiveMode != "token" {
		t.Fatalf("changed token status = %#v", got)
	}
}

func TestAdapterResolverErrorsDoNotExposeCredentials(t *testing.T) {
	t.Parallel()

	const canary = "test-secret-canary-value"
	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, nil)
	_, err := resolver.ResolveUpstream(context.Background(), config.UpstreamConfig{
		ID: "bad-source", Name: "Bad Source", Type: "generic", BaseURL: "https://sub.example.test/?leak=" + canary, APIKey: canary,
	})
	if err == nil {
		t.Fatal("ResolveUpstream() error = nil, want invalid configuration")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error leaked credential: %v", err)
	}
}

func TestAdapterResolverRejectsCPAUpstreamDiscovery(t *testing.T) {
	t.Parallel()

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, nil)
	cfg := config.UpstreamConfig{
		ID: "source-cpa", Type: "cliproxyapi", BaseURL: "https://cpa.example.test", ManagementKey: "REPLACE_WITH_CPA_MANAGEMENT_KEY",
	}
	discoverySource, err := resolver.ResolveUpstream(context.Background(), cfg)
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("ResolveUpstream(discovery) error = %v, want ErrUnsupportedPlatform", err)
	}
	if discoverySource != nil {
		t.Fatalf("ResolveUpstream(discovery) source = %T, want nil", discoverySource)
	}
}

func TestAdapterResolverRejectsCPAUpstreamCacheConstruction(t *testing.T) {
	t.Parallel()

	resolver := NewAdapterResolver(&memoryConfigStore{cfg: config.Default()}, nil)
	cfgA := config.UpstreamConfig{ID: "shared-source", Type: "cliproxyapi", BaseURL: "https://cpa.example.test", ManagementKey: "REPLACE_WITH_CPA_KEY_A"}
	first, err := resolver.ResolveUpstream(context.Background(), cfgA)
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("ResolveUpstream() error = %v, want ErrUnsupportedPlatform", err)
	}
	if first != nil {
		t.Fatalf("ResolveUpstream() source = %T, want nil", first)
	}
}

func TestAdapterResolverRejectsCPAProxyKeyUpstream(t *testing.T) {
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
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("ResolveUpstream() error = %v, want ErrUnsupportedPlatform", err)
	}
	if first != nil {
		t.Fatalf("ResolveUpstream() source = %T, want nil", first)
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

	upstreamConfig := config.UpstreamConfig{ID: "source-a", Type: "newapi", BaseURL: server.URL, AccessToken: "REPLACE_WITH_SOURCE_TOKEN", DiscoveryMode: "token"}
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
	if !reflect.DeepEqual(got, []string{"31", "41", "42"}) {
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
