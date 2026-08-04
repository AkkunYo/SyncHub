package generic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestMultiKeySourceDiscoversIndependentAssetsAndSkipsDisabledKeys(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		mu.Lock()
		requests[authorization]++
		mu.Unlock()
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.URL.RawQuery != "" {
			t.Errorf("model request = %s %s", r.Method, r.URL.String())
		}
		switch authorization {
		case "Bearer primary-secret":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"gpt-primary"}]}`)
		case "Bearer backup-secret":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"gpt-backup"}]}`)
		case "Bearer disabled-secret":
			t.Error("disabled key made a model request")
		default:
			t.Errorf("unexpected Authorization header %q", authorization)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewMultiKeySource(MultiKeyConfig{
		SourceID: "shared-a", Name: "Shared A", BaseURL: server.URL + "/v1", RequestTimeout: time.Second,
		Keys: []KeyConfig{
			{ID: DefaultKeyID, Name: "Primary", APIKey: "primary-secret", Enabled: true},
			{ID: "backup", Name: "Backup", APIKey: "backup-secret", Enabled: true},
			{ID: "disabled", Name: "Disabled", APIKey: "disabled-secret", Enabled: false, Models: []string{"manual-disabled"}},
		},
	}, server.Client())
	if err != nil {
		t.Fatalf("NewMultiKeySource() error = %v", err)
	}
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if page.HasMore || len(page.Assets) != 3 {
		t.Fatalf("page = %#v", page)
	}
	primary := assetByID(t, page, "shared-a:endpoint")
	assertGenericAsset(t, primary, "Primary", []string{"gpt-primary"}, true)
	backup := assetByID(t, page, "shared-a:key:backup")
	assertGenericAsset(t, backup, "Backup", []string{"gpt-backup"}, true)
	disabled := assetByID(t, page, "shared-a:key:disabled")
	assertGenericAsset(t, disabled, "Disabled", []string{"manual-disabled"}, false)
	if disabled.Metadata["disabled"] != "true" {
		t.Fatalf("disabled metadata = %#v", disabled.Metadata)
	}
	if primary.Metadata["key_id"] != DefaultKeyID || backup.Metadata["key_id"] != "backup" {
		t.Fatalf("key metadata = %#v, %#v", primary.Metadata, backup.Metadata)
	}

	mu.Lock()
	defer mu.Unlock()
	if requests["Bearer primary-secret"] != 1 || requests["Bearer backup-secret"] != 1 || requests["Bearer disabled-secret"] != 0 {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestMultiKeySourceIsolatesFailuresAndUsesSafeModelFallbacks(t *testing.T) {
	t.Parallel()

	const responseCanary = "upstream-body-canary"
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		mu.Lock()
		requests[authorization]++
		attempt := requests[authorization]
		mu.Unlock()
		switch authorization {
		case "Bearer healthy-secret":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"healthy-model"}]}`)
		case "Bearer cached-secret":
			if attempt == 1 {
				_, _ = fmt.Fprint(w, `{"data":[{"id":"cached-model"}]}`)
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprint(w, responseCanary+" cached-secret")
		case "Bearer manual-secret":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, responseCanary+" manual-secret")
		case "Bearer empty-secret":
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, responseCanary+" empty-secret")
		default:
			t.Errorf("unexpected Authorization header %q", authorization)
		}
	}))
	t.Cleanup(server.Close)

	cfg := MultiKeyConfig{
		SourceID: "shared-a", Name: "Shared A", BaseURL: server.URL, RequestTimeout: time.Second,
		Keys: []KeyConfig{
			{ID: DefaultKeyID, Name: "Healthy", APIKey: "healthy-secret", Enabled: true},
			{ID: "cached", Name: "Cached", APIKey: "cached-secret", Enabled: true},
			{ID: "manual", Name: "Manual", APIKey: "manual-secret", Enabled: true, Models: []string{"manual-model"}},
			{ID: "empty", Name: "Empty", APIKey: "empty-secret", Enabled: true},
		},
	}
	source, err := NewMultiKeySource(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Keys[2].Models[0] = "caller-mutation"

	first, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("first ListAssets() error = %v", err)
	}
	assetByID(t, first, "shared-a:key:cached").Models[0] = "returned-mutation"
	manual := assetByID(t, first, "shared-a:key:manual")
	assertGenericAsset(t, manual, "Manual", []string{"manual-model"}, true)
	assertFallbackMetadata(t, manual, "unauthenticated", "manual", responseCanary)
	empty := assetByID(t, first, "shared-a:key:empty")
	assertGenericAsset(t, empty, "Empty", []string{}, false)
	assertFallbackMetadata(t, empty, "forbidden", "none", responseCanary)

	second, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("second ListAssets() error = %v", err)
	}
	assertGenericAsset(t, assetByID(t, second, "shared-a:endpoint"), "Healthy", []string{"healthy-model"}, true)
	cached := assetByID(t, second, "shared-a:key:cached")
	assertGenericAsset(t, cached, "Cached", []string{"cached-model"}, true)
	assertFallbackMetadata(t, cached, "request_failed", "cache", responseCanary)
	cached.Models[0] = "second-mutation"
	cached.Metadata["error_code"] = "mutated"

	third, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("third ListAssets() error = %v", err)
	}
	cached = assetByID(t, third, "shared-a:key:cached")
	if !reflect.DeepEqual(cached.Models, []string{"cached-model"}) || cached.Metadata["error_code"] != "request_failed" {
		t.Fatalf("cached result shared caller storage: %#v", cached)
	}
}

func TestMultiKeySourceResolvesExactSecretsAndReturnsCopies(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	source, err := NewMultiKeySource(MultiKeyConfig{
		SourceID: "shared-a", Name: "Shared A", BaseURL: server.URL, RequestTimeout: time.Second,
		Keys: []KeyConfig{
			{ID: DefaultKeyID, Name: "Primary", APIKey: "primary-secret", Enabled: true},
			{ID: "backup", Name: "Backup", APIKey: "backup-secret", Enabled: true},
			{ID: "disabled", Name: "Disabled", APIKey: "disabled-secret", Enabled: false},
		},
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		assetID string
		want    string
		wantErr error
	}{
		{assetID: "shared-a:endpoint", want: "primary-secret"},
		{assetID: "shared-a:key:backup", want: "backup-secret"},
		{assetID: "shared-a:key:default", wantErr: platform.ErrSecretUnavailable},
		{assetID: "shared-a:key:backup:extra", wantErr: platform.ErrSecretUnavailable},
		{assetID: "other:endpoint", wantErr: platform.ErrSecretUnavailable},
		{assetID: "shared-a:key:disabled", wantErr: platform.ErrAssetDisabled},
	}
	for _, test := range tests {
		secret, resolveErr := source.ResolveSecret(context.Background(), test.assetID, platform.SecretGrant{})
		if !errors.Is(resolveErr, test.wantErr) {
			t.Fatalf("ResolveSecret(%q) error = %v, want %v", test.assetID, resolveErr, test.wantErr)
		}
		if test.want == "" {
			continue
		}
		if string(secret.Bytes) != test.want || secret.Kind != platform.AssetProxyKey {
			t.Fatalf("ResolveSecret(%q) = %#v", test.assetID, secret)
		}
		secret.Bytes[0] = 'X'
		again, err := source.ResolveSecret(context.Background(), test.assetID, platform.SecretGrant{})
		if err != nil || string(again.Bytes) != test.want {
			t.Fatalf("second ResolveSecret(%q) = %#v, %v", test.assetID, again, err)
		}
		secret.Wipe()
		again.Wipe()
	}
}

func TestMultiKeySourceValidatesConfigurationWithoutLeakingSecrets(t *testing.T) {
	t.Parallel()

	const secretCanary = "validation-secret-canary"
	valid := MultiKeyConfig{
		SourceID: "shared-a", Name: "Shared A", BaseURL: "https://source.example.com", RequestTimeout: time.Second,
		Keys: []KeyConfig{{ID: DefaultKeyID, Name: "Primary", APIKey: secretCanary, Enabled: true}},
	}
	tests := []struct {
		name   string
		mutate func(*MultiKeyConfig)
	}{
		{name: "missing keys", mutate: func(cfg *MultiKeyConfig) { cfg.Keys = nil }},
		{name: "missing key id", mutate: func(cfg *MultiKeyConfig) { cfg.Keys[0].ID = "" }},
		{name: "invalid key id", mutate: func(cfg *MultiKeyConfig) { cfg.Keys[0].ID = "bad:id" }},
		{name: "missing key name", mutate: func(cfg *MultiKeyConfig) { cfg.Keys[0].Name = "" }},
		{name: "missing key secret", mutate: func(cfg *MultiKeyConfig) { cfg.Keys[0].APIKey = " " }},
		{name: "invalid manual model", mutate: func(cfg *MultiKeyConfig) { cfg.Keys[0].Models = []string{"bad\nmodel"} }},
		{name: "duplicate key id", mutate: func(cfg *MultiKeyConfig) { cfg.Keys = append(cfg.Keys, cfg.Keys[0]) }},
		{name: "duplicate key name", mutate: func(cfg *MultiKeyConfig) {
			cfg.Keys = append(cfg.Keys, KeyConfig{ID: "backup", Name: "PRIMARY", APIKey: "backup-secret", Enabled: true})
		}},
		{name: "invalid timeout", mutate: func(cfg *MultiKeyConfig) { cfg.RequestTimeout = 0 }},
		{name: "credential in url", mutate: func(cfg *MultiKeyConfig) { cfg.BaseURL = "https://user:pass@source.example.com" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			cfg.Keys = append([]KeyConfig(nil), valid.Keys...)
			test.mutate(&cfg)
			_, err := NewMultiKeySource(cfg, nil)
			if err == nil {
				t.Fatal("NewMultiKeySource() error = nil")
			}
			if strings.Contains(err.Error(), secretCanary) {
				t.Fatalf("validation error leaked secret: %v", err)
			}
		})
	}
}

func assetByID(t *testing.T, page platform.AssetPage, assetID string) platform.UpstreamAsset {
	t.Helper()
	for _, asset := range page.Assets {
		if asset.ID == assetID {
			return asset
		}
	}
	t.Fatalf("asset %q not found in %#v", assetID, page.Assets)
	return platform.UpstreamAsset{}
}

func assertGenericAsset(t *testing.T, asset platform.UpstreamAsset, name string, models []string, available bool) {
	t.Helper()
	if asset.Name != name || asset.SourceID != "shared-a" || asset.SourceType != sourceType || asset.Provider != platform.ProviderOpenAI || asset.RawType != rawType || asset.Kind != platform.AssetProxyKey {
		t.Fatalf("asset identity = %#v", asset)
	}
	if !reflect.DeepEqual(asset.Models, models) || asset.Enabled != available || asset.SecretReadable != available {
		t.Fatalf("asset availability = %#v, want models %#v available %v", asset, models, available)
	}
}

func assertFallbackMetadata(t *testing.T, asset platform.UpstreamAsset, code, source, forbidden string) {
	t.Helper()
	if asset.Metadata["stale"] != "true" || asset.Metadata["error_code"] != code || asset.Metadata["models_source"] != source {
		t.Fatalf("fallback metadata = %#v", asset.Metadata)
	}
	for key, value := range asset.Metadata {
		if strings.Contains(key, forbidden) || strings.Contains(value, forbidden) || strings.Contains(value, "secret") {
			t.Fatalf("metadata leaked upstream data: %#v", asset.Metadata)
		}
	}
}
