package sub2api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

var _ platform.UpstreamAdapter = (*Source)(nil)

func TestNewSourceValidatesConfigurationAndReportsCapabilities(t *testing.T) {
	valid := Config{
		SourceID:       " source-main ",
		BaseURL:        "https://sub2api.example.test/",
		APIKey:         testAdminKey(),
		PageSize:       5000,
		RequestTimeout: time.Second,
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing source id", mutate: func(cfg *Config) { cfg.SourceID = " " }},
		{name: "missing base URL", mutate: func(cfg *Config) { cfg.BaseURL = "" }},
		{name: "non HTTP base URL", mutate: func(cfg *Config) { cfg.BaseURL = "ftp://example.test" }},
		{name: "base URL with user info", mutate: func(cfg *Config) { cfg.BaseURL = "https://user:pass@example.test" }},
		{name: "base URL with query", mutate: func(cfg *Config) { cfg.BaseURL = "https://example.test?key=value" }},
		{name: "missing admin API key", mutate: func(cfg *Config) { cfg.APIKey = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if _, err := NewSource(cfg, nil); err == nil {
				t.Fatal("NewSource() error = nil, want configuration error")
			}
		})
	}

	source, err := NewSource(valid, nil)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}
	capabilities, err := source.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	wantKinds := []platform.AssetKind{platform.AssetStaticAPIKey, platform.AssetOAuthFile}
	if !reflect.DeepEqual(capabilities.AssetKinds, wantKinds) || !capabilities.SecretResolution {
		t.Fatalf("Capabilities() = %#v, want kinds %v with secret resolution", capabilities, wantKinds)
	}
}

func TestSourceListsPaginatedMetadataWithoutSecrets(t *testing.T) {
	adminKey := testAdminKey()
	listedSecret := strings.Join([]string{"listed", "value", "must", "not", "escape"}, "-")
	oauthValue := strings.Join([]string{"oauth", "value", "must", "not", "escape"}, "-")
	queryValue := strings.Join([]string{"query", "value", "must", "not", "escape"}, "-")
	var modelCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != adminKey {
			http.Error(w, "missing management authentication", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			if r.Method != http.MethodGet || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("page_size") != "3" {
				http.Error(w, "unexpected list request", http.StatusBadRequest)
				return
			}
			writeEnvelope(t, w, map[string]any{
				"items": []any{
					map[string]any{
						"id": 17, "name": "Primary OpenAI", "platform": "openai", "type": "apikey",
						"credentials": map[string]any{
							"base_url":      "https://api.example.test/v1?token=" + queryValue,
							"model_mapping": map[string]string{"ignored": "metadata"},
							"api_key":       listedSecret,
						},
						"credentials_status": map[string]bool{"has_api_key": true},
						"status":             "active", "schedulable": true, "priority": 7, "concurrency": 2,
						"rate_multiplier": 1.25, "group_ids": []int64{5},
						"groups": []any{map[string]any{"id": 5, "name": "Core", "platform": "openai"}},
					},
					map[string]any{
						"id": 18, "name": "Future Key", "platform": "future-provider", "type": "api-token",
						"credentials":        map[string]any{"api_key": listedSecret},
						"credentials_status": map[string]bool{"has_api_key": true},
						"status":             "active", "schedulable": true,
					},
					map[string]any{
						"id": 19, "name": "OAuth Account", "platform": "anthropic", "type": "oauth",
						"credentials":        map[string]any{"access_token": oauthValue},
						"credentials_status": map[string]bool{"has_access_token": true},
						"status":             "active", "schedulable": true,
					},
				},
				"total": 5, "page": 1, "page_size": 3, "pages": 2,
			})
		case "/api/v1/admin/accounts/17/models":
			modelCalls.Add(1)
			writeEnvelope(t, w, []any{
				map[string]any{"id": "z-model"},
				map[string]any{"id": "a-model"},
				map[string]any{"id": "a-model"},
			})
		case "/api/v1/admin/accounts/19/models":
			modelCalls.Add(1)
			writeEnvelope(t, w, []any{map[string]any{"id": "claude-model"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := mustNewTestSource(t, server.URL, adminKey, time.Second)
	page, err := source.ListAssets(context.Background(), platform.PageCursor{Page: 1, PageSize: 3})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if !page.HasMore || page.Next.Page != 2 || page.Next.PageSize != 3 {
		t.Fatalf("ListAssets() pagination = %#v, want next page 2", page)
	}
	if len(page.Assets) != 3 {
		t.Fatalf("ListAssets() returned %d assets, want 3", len(page.Assets))
	}

	primary := page.Assets[0]
	if primary.ID != "source-main:key:17" || primary.SourceID != "source-main" || primary.SourceType != "sub2api" {
		t.Fatalf("primary identity = %#v", primary)
	}
	if primary.Provider != platform.ProviderOpenAI || primary.RawType != "apikey" || primary.Kind != platform.AssetStaticAPIKey {
		t.Fatalf("primary type normalization = %#v", primary)
	}
	if primary.BaseURL != "https://api.example.test/v1" {
		t.Fatalf("primary BaseURL = %q, want query-free URL", primary.BaseURL)
	}
	if !reflect.DeepEqual(primary.Models, []string{"a-model", "z-model"}) {
		t.Fatalf("primary Models = %v", primary.Models)
	}
	if !primary.Enabled || !primary.SecretReadable {
		t.Fatalf("primary availability = enabled:%v readable:%v", primary.Enabled, primary.SecretReadable)
	}
	for key, want := range map[string]string{
		"account_id": "17", "account_type": "apikey", "raw_provider": "openai",
		"status": "active", "schedulable": "true", "priority": "7", "concurrency": "2",
		"rate_multiplier": "1.25", "group_ids": "5", "groups": "Core",
	} {
		if got := primary.Metadata[key]; got != want {
			t.Errorf("primary.Metadata[%q] = %q, want %q", key, got, want)
		}
	}

	future := page.Assets[1]
	if future.Provider != platform.ProviderUnknown || future.RawType != "api-token" || future.SecretReadable {
		t.Fatalf("future asset must be discovery-only: %#v", future)
	}
	if future.Metadata["discovery_only"] != "true" {
		t.Fatalf("future metadata = %#v, want discovery_only", future.Metadata)
	}

	oauth := page.Assets[2]
	if oauth.Provider != platform.ProviderUnknown || oauth.Kind != platform.AssetOAuthFile || oauth.SecretReadable {
		t.Fatalf("OAuth asset must be discovery-only: %#v", oauth)
	}
	if !reflect.DeepEqual(oauth.Models, []string{"claude-model"}) {
		t.Fatalf("OAuth metadata models = %v", oauth.Models)
	}
	if modelCalls.Load() != 2 {
		t.Fatalf("model calls = %d, want 2 known-provider accounts", modelCalls.Load())
	}

	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("json.Marshal(page) error = %v", err)
	}
	for _, forbidden := range []string{adminKey, listedSecret, oauthValue, queryValue} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("metadata JSON leaked forbidden value %q: %s", forbidden, raw)
		}
	}
}

func TestSourceResolvesSupportedAccountSecretAndWipes(t *testing.T) {
	adminKey := testAdminKey()
	secretValue := strings.Join([]string{"upstream", "test", "value"}, "-") + "\nline-two"
	oauthValue := strings.Join([]string{"unrelated", "oauth", "value"}, "-")
	var exportCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != adminKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			writeEnvelope(t, w, accountPage([]any{map[string]any{
				"id": 41, "name": "Compatible Upstream", "platform": "openai", "type": "upstream",
				"credentials":        map[string]any{"base_url": "https://upstream.example.test"},
				"credentials_status": map[string]bool{"has_api_key": true},
				"status":             "active", "schedulable": true,
			}}, 1, 1, 100))
		case "/api/v1/admin/accounts/41/models":
			writeEnvelope(t, w, []any{map[string]any{"id": "gpt-test"}})
		case "/api/v1/admin/accounts/data":
			exportCalls.Add(1)
			if r.URL.Query().Get("ids") != "41" || r.URL.Query().Get("include_proxies") != "false" {
				http.Error(w, "unexpected export scope", http.StatusBadRequest)
				return
			}
			writeEnvelope(t, w, map[string]any{
				"exported_at": time.Now().UTC().Format(time.RFC3339),
				"proxies":     []any{},
				"accounts": []any{map[string]any{
					"name": "Compatible Upstream", "platform": "openai", "type": "upstream",
					"credentials": map[string]any{"api_key": secretValue, "refresh_token": oauthValue},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := mustNewTestSource(t, server.URL, adminKey, time.Second)
	if _, err := source.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	secret, err := source.ResolveSecret(context.Background(), "source-main:key:41", platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if secret.Kind != platform.AssetStaticAPIKey || string(secret.Bytes) != secretValue {
		t.Fatalf("ResolveSecret() = kind %q value %q", secret.Kind, secret.Bytes)
	}
	if secret.Metadata["account_id"] != "41" || secret.Metadata["account_type"] != "upstream" {
		t.Fatalf("secret metadata = %#v", secret.Metadata)
	}
	if exportCalls.Load() != 1 {
		t.Fatalf("export calls = %d, want 1", exportCalls.Load())
	}
	raw, marshalErr := json.Marshal(secret)
	if marshalErr != nil {
		t.Fatalf("json.Marshal(secret) error = %v", marshalErr)
	}
	if strings.Contains(string(raw), secretValue) || strings.Contains(string(raw), oauthValue) {
		t.Fatalf("ResolvedSecret JSON leaked secret material: %s", raw)
	}

	secret.Wipe()
	for i, value := range secret.Bytes {
		if value != 0 {
			t.Fatalf("secret byte %d was not wiped", i)
		}
	}
}

func TestSourceRefusesDiscoveryOnlyMissingAndDisabledAssetsWithoutExport(t *testing.T) {
	adminKey := testAdminKey()
	var exportCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			writeEnvelope(t, w, accountPage([]any{
				map[string]any{"id": 51, "name": "OAuth", "platform": "anthropic", "type": "oauth", "credentials_status": map[string]bool{"has_access_token": true}, "status": "active", "schedulable": true},
				map[string]any{"id": 52, "name": "Unknown", "platform": "future", "type": "apikey", "credentials_status": map[string]bool{"has_api_key": true}, "status": "active", "schedulable": true},
				map[string]any{"id": 53, "name": "Inactive", "platform": "gemini", "type": "apikey", "credentials_status": map[string]bool{"has_api_key": true}, "status": "inactive", "schedulable": true},
				map[string]any{"id": 54, "name": "No Key", "platform": "openai", "type": "apikey", "credentials_status": map[string]bool{}, "status": "active", "schedulable": true},
			}, 4, 1, 100))
		case "/api/v1/admin/accounts/51/models", "/api/v1/admin/accounts/53/models", "/api/v1/admin/accounts/54/models":
			writeEnvelope(t, w, []any{})
		case "/api/v1/admin/accounts/data":
			exportCalls.Add(1)
			http.Error(w, "must not export", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := mustNewTestSource(t, server.URL, adminKey, time.Second)
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if len(page.Assets) != 4 {
		t.Fatalf("assets = %d, want 4", len(page.Assets))
	}
	if page.Assets[0].Provider != platform.ProviderUnknown || page.Assets[0].Metadata["discovery_only"] != "true" {
		t.Fatalf("OAuth asset is not discovery-only: %#v", page.Assets[0])
	}
	if page.Assets[1].Provider != platform.ProviderUnknown || page.Assets[1].Metadata["discovery_only"] != "true" {
		t.Fatalf("unknown provider asset is not discovery-only: %#v", page.Assets[1])
	}

	checks := []struct {
		id   string
		want error
	}{
		{id: "source-main:key:51", want: platform.ErrSecretUnavailable},
		{id: "source-main:key:52", want: platform.ErrSecretUnavailable},
		{id: "source-main:key:53", want: platform.ErrAssetDisabled},
		{id: "source-main:key:54", want: platform.ErrSecretUnavailable},
		{id: "source-main:key:999", want: platform.ErrSecretUnavailable},
	}
	for _, check := range checks {
		if _, err := source.ResolveSecret(context.Background(), check.id, platform.SecretGrant{}); !errors.Is(err, check.want) {
			t.Errorf("ResolveSecret(%q) error = %v, want %v", check.id, err, check.want)
		}
	}
	if exportCalls.Load() != 0 {
		t.Fatalf("discovery-only resolution made %d export calls", exportCalls.Load())
	}
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success", "data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func accountPage(items []any, total int64, page, pageSize int) map[string]any {
	pages := 1
	if pageSize > 0 && total > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize, "pages": pages}
}

func mustNewTestSource(t *testing.T, baseURL, adminKey string, timeout time.Duration) *Source {
	t.Helper()
	source, err := NewSource(Config{
		SourceID:       "source-main",
		BaseURL:        baseURL,
		APIKey:         adminKey,
		PageSize:       100,
		RequestTimeout: timeout,
	}, nil)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}
	return source
}

func testAdminKey() string {
	return strings.Join([]string{"admin", "test", strconv.Itoa(29)}, "-")
}
