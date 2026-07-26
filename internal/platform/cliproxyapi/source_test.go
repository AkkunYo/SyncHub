package cliproxyapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestSourceDiscoversAuthAndStaticAssetsWithoutReadingStaticKeys(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	staticReads := 0
	modelReads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer management-secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[`+
				`{"id":"claude-oauth-1","auth_index":"temporary-1","name":"claude-account.json","provider":"claude","status":"ready","disabled":false,"runtime_only":false},`+
				`{"id":"gemini:apikey:stable","auth_index":"temporary-2","name":"gemini:apikey","provider":"gemini","status":"ready","disabled":false,"runtime_only":true},`+
				`{"id":"plugin-auth-1","auth_index":"temporary-3","name":"plugin.json","provider":"future-plugin","status":"ready","disabled":false,"runtime_only":true}`+
				`]}`)
		case "/v0/management/auth-files/models":
			name := r.URL.Query().Get("name")
			mu.Lock()
			modelReads[name]++
			mu.Unlock()
			switch name {
			case "claude-account.json":
				_, _ = fmt.Fprint(w, `{"models":[{"id":"claude-sonnet-4"}]}`)
			case "gemini:apikey":
				_, _ = fmt.Fprint(w, `{"models":[{"id":"gemini-2.5-pro"},{"id":"gemini-2.5-flash"}]}`)
			default:
				_, _ = fmt.Fprint(w, `{"models":[]}`)
			}
		default:
			if strings.HasPrefix(r.URL.Path, "/v0/management/") {
				mu.Lock()
				staticReads++
				mu.Unlock()
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{
		SourceID:      "source-cpa",
		BaseURL:       server.URL,
		ManagementKey: "management-secret",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if page.HasMore || len(page.Assets) != 3 {
		t.Fatalf("asset page = %#v", page)
	}

	oauth := page.Assets[0]
	if oauth.ID != "source-cpa:cpa:claude-oauth-1" || oauth.Kind != platform.AssetOAuthFile || oauth.Provider != platform.ProviderAnthropic {
		t.Fatalf("OAuth asset = %#v", oauth)
	}
	if oauth.Metadata["schema_version"] != "cpa-auth-v1" || strings.Join(oauth.Models, ",") != "claude-sonnet-4" {
		t.Fatalf("OAuth metadata = %#v", oauth)
	}
	static := page.Assets[1]
	if static.ID != "source-cpa:cpa:gemini:apikey:stable" || static.Kind != platform.AssetStaticAPIKey || static.Provider != platform.ProviderGemini || !static.SecretReadable {
		t.Fatalf("static asset = %#v", static)
	}
	if strings.Join(static.Models, ",") != "gemini-2.5-pro,gemini-2.5-flash" {
		t.Fatalf("static models = %#v", static.Models)
	}
	unknown := page.Assets[2]
	if unknown.Provider != platform.ProviderUnknown || unknown.Metadata["discovery_only"] != "true" || unknown.SecretReadable {
		t.Fatalf("unknown plugin asset = %#v", unknown)
	}
	for _, asset := range page.Assets {
		if _, ok := asset.Metadata["auth_index"]; ok {
			t.Fatalf("ephemeral auth_index leaked into asset metadata: %#v", asset)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if staticReads != 0 {
		t.Fatalf("metadata discovery read %d static-key endpoints", staticReads)
	}
	if modelReads["claude-account.json"] != 1 || modelReads["gemini:apikey"] != 1 {
		t.Fatalf("model reads = %#v", modelReads)
	}
}

func TestSourceResolvesStaticKeyOnlyDuringExplicitSecretRequest(t *testing.T) {
	t.Parallel()

	var staticReads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer management-secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[{"id":"gemini-stable-id","auth_index":"select-me","name":"gemini:apikey","provider":"gemini","status":"ready","disabled":false,"runtime_only":true}]}`)
		case "/v0/management/auth-files/models":
			_, _ = fmt.Fprint(w, `{"models":[{"id":"gemini-2.5-pro"}]}`)
		case "/v0/management/gemini-api-key":
			staticReads++
			_, _ = fmt.Fprint(w, `{"gemini-api-key":[`+
				`{"api-key":"not-this-one","base-url":"https://wrong.example.com","auth-index":"other"},`+
				`{"api-key":"gemini-secret","base-url":"https://generativelanguage.googleapis.com","models":[{"name":"gemini-2.5-pro","alias":"gemini-pro"}],"auth-index":"select-me"}`+
				`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "source-cpa", BaseURL: server.URL, ManagementKey: "management-secret"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatal(err)
	}
	if staticReads != 0 {
		t.Fatalf("static endpoint read during discovery")
	}
	secret, err := source.ResolveSecret(context.Background(), "source-cpa:cpa:gemini-stable-id", platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if string(secret.Bytes) != "gemini-secret" || secret.Metadata["base_url"] != "https://generativelanguage.googleapis.com" {
		t.Fatalf("resolved secret = %#v", secret)
	}
	if staticReads != 1 {
		t.Fatalf("static endpoint reads = %d", staticReads)
	}
}

func TestSourceRequiresExplicitCompatibleGrantBeforeDownloadingAuthFile(t *testing.T) {
	t.Parallel()

	var downloads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[{"id":"claude-oauth-1","auth_index":"temporary","name":"claude-account.json","provider":"claude","status":"ready","disabled":false,"runtime_only":false}]}`)
		case "/v0/management/auth-files/models":
			_, _ = fmt.Fprint(w, `{"models":[]}`)
		case "/v0/management/auth-files/download":
			downloads++
			if got := r.URL.Query().Get("name"); got != "claude-account.json" {
				t.Errorf("download name = %q", got)
			}
			_, _ = fmt.Fprint(w, `{"type":"claude","access_token":"oauth-sensitive"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "source-cpa", BaseURL: server.URL, ManagementKey: "management-secret"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatal(err)
	}
	assetID := "source-cpa:cpa:claude-oauth-1"
	_, err = source.ResolveSecret(context.Background(), assetID, platform.SecretGrant{})
	if !errors.Is(err, platform.ErrSecretGrantRequired) || downloads != 0 {
		t.Fatalf("ResolveSecret(without grant) = %v, downloads %d", err, downloads)
	}
	secret, err := source.ResolveSecret(context.Background(), assetID, platform.SecretGrant{AllowAuthFile: true})
	if err != nil {
		t.Fatalf("ResolveSecret(with grant) error = %v", err)
	}
	if string(secret.Bytes) != `{"type":"claude","access_token":"oauth-sensitive"}` || downloads != 1 {
		t.Fatalf("downloaded secret = %q, downloads %d", secret.Bytes, downloads)
	}

	_, err = source.ResolveSecret(context.Background(), "source-cpa:cpa:missing", platform.SecretGrant{AllowAuthFile: true})
	if !errors.Is(err, platform.ErrSecretUnavailable) {
		t.Fatalf("ResolveSecret(missing) error = %v", err)
	}

	requestURL, err := url.Parse(server.URL + "/v0/management/auth-files/download")
	if err != nil || requestURL.Host == "" {
		t.Fatalf("test server URL invalid: %v", err)
	}
}

func TestSourceDiscoversAndResolvesConfiguredProxyAssetWithoutListingAPIKeys(t *testing.T) {
	t.Parallel()

	const (
		managementKey = "REPLACE_WITH_CPA_MANAGEMENT_KEY"
		proxyKey      = "REPLACE_WITH_CPA_PROXY_KEY"
		assetID       = "source-cpa:proxy:openai-compatible"
	)
	var authFileReads int
	var apiKeyReads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+managementKey {
			t.Errorf("management Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			authFileReads++
			_, _ = fmt.Fprint(w, `{"files":[]}`)
		case "/v0/management/api-keys":
			apiKeyReads++
			http.Error(w, "proxy key endpoint must not be called", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{
		SourceID: "source-cpa", BaseURL: server.URL, ManagementKey: managementKey, ProxyAPIKey: proxyKey,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	t.Run("nil context", func(t *testing.T) {
		if _, err := source.ResolveSecret(nil, assetID, platform.SecretGrant{}); err == nil || strings.Contains(err.Error(), proxyKey) {
			t.Fatalf("ResolveSecret(nil context) error = %v", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := source.ResolveSecret(cancelled, assetID, platform.SecretGrant{}); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), proxyKey) {
			t.Fatalf("ResolveSecret(cancelled context) error = %v", err)
		}
	})
	capabilities, err := source.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundProxyKind := false
	for _, kind := range capabilities.AssetKinds {
		foundProxyKind = foundProxyKind || kind == platform.AssetProxyKey
	}
	if !foundProxyKind {
		t.Fatalf("source capabilities omit AssetProxyKey: %#v", capabilities)
	}
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Assets) != 1 {
		t.Fatalf("proxy asset page = %#v", page)
	}
	asset := page.Assets[0]
	if asset.ID != assetID || asset.Kind != platform.AssetProxyKey || asset.Provider != platform.ProviderOpenAI ||
		asset.BaseURL != server.URL || !asset.Enabled || !asset.SecretReadable {
		t.Fatalf("proxy asset = %#v", asset)
	}
	secret, err := source.ResolveSecret(context.Background(), assetID, platform.SecretGrant{})
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Wipe()
	if secret.Kind != platform.AssetProxyKey || string(secret.Bytes) != proxyKey || secret.ContentType != "text/plain" ||
		len(secret.Metadata) != 1 || secret.Metadata["base_url"] != server.URL {
		t.Fatalf("resolved proxy secret = %#v", secret)
	}
	if _, err := source.ResolveSecret(context.Background(), "source-cpa:proxy:other", platform.SecretGrant{}); !errors.Is(err, platform.ErrSecretUnavailable) {
		t.Fatalf("ResolveSecret(other proxy ID) error = %v", err)
	}

	withoutProxy, err := NewSource(Config{SourceID: "source-cpa", BaseURL: server.URL, ManagementKey: managementKey}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err = withoutProxy.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil || len(page.Assets) != 0 {
		t.Fatalf("ListAssets(without proxy key) = %#v, %v", page, err)
	}
	if _, err := withoutProxy.ResolveSecret(context.Background(), assetID, platform.SecretGrant{}); !errors.Is(err, platform.ErrSecretUnavailable) {
		t.Fatalf("ResolveSecret(without proxy key) error = %v", err)
	}
	if apiKeyReads != 0 || authFileReads != 2 {
		t.Fatalf("management reads: auth-files=%d api-keys=%d", authFileReads, apiKeyReads)
	}
	if _, err := NewSource(Config{
		SourceID: "source-cpa", BaseURL: "/invalid/" + proxyKey, ManagementKey: managementKey, ProxyAPIKey: proxyKey,
	}, nil); err == nil || strings.Contains(err.Error(), proxyKey) {
		t.Fatalf("NewSource(invalid) error leaked proxy key: %v", err)
	}
}
