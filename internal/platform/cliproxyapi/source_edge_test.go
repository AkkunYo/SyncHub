package cliproxyapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestSourceValidatesConfigAndReportsCapabilities(t *testing.T) {
	t.Parallel()

	tests := []Config{
		{BaseURL: "https://cpa.example.com", ManagementKey: "key"},
		{SourceID: "source", BaseURL: "/relative", ManagementKey: "key"},
		{SourceID: "source", BaseURL: "https://cpa.example.com"},
	}
	for _, cfg := range tests {
		if source, err := NewSource(cfg, nil); err == nil {
			_ = source
			t.Fatalf("NewSource(%#v) unexpectedly succeeded", cfg)
		}
	}
	source, err := NewSource(Config{SourceID: "source", BaseURL: "https://cpa.example.com", ManagementKey: "key"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := source.Capabilities(context.Background())
	if err != nil || !capabilities.SecretResolution || len(capabilities.AssetKinds) != 3 {
		t.Fatalf("Capabilities() = %#v, %v", capabilities, err)
	}
}

func TestSourceUsesManagementHeaderAndRejectsDisabledAsset(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Management-Key"); got != "management-secret" {
			t.Errorf("X-Management-Key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization unexpectedly set to %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[{"id":"disabled-gemini","auth_index":"disabled-index","name":"gemini-disabled","provider":"gemini","status":"disabled","disabled":true,"runtime_only":true}]}`)
		case "/v0/management/auth-files/models":
			_, _ = fmt.Fprint(w, `{"models":[]}`)
		default:
			t.Errorf("disabled asset triggered unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	source, err := NewSource(Config{
		SourceID:               "source",
		BaseURL:                server.URL,
		ManagementKey:          "management-secret",
		UseManagementKeyHeader: true,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil || len(page.Assets) != 1 || page.Assets[0].Enabled {
		t.Fatalf("ListAssets() = %#v, %v", page, err)
	}
	_, err = source.ResolveSecret(context.Background(), page.Assets[0].ID, platform.SecretGrant{})
	if !errors.Is(err, platform.ErrAssetDisabled) {
		t.Fatalf("ResolveSecret(disabled) error = %v", err)
	}
}

func TestSourceResolvesNestedOpenAICompatibilityKey(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[{"id":"compat-stable","auth-index":"compat-index","name":"custom-provider","provider":"openai-compatible-custom-provider","status":"ready","runtime_only":true}]}`)
		case "/v0/management/auth-files/models":
			_, _ = fmt.Fprint(w, `{"models":[{"id":"upstream-model"},{"id":"upstream-model"}]}`)
		case "/v0/management/openai-compatibility":
			_, _ = fmt.Fprint(w, `{"openai-compatibility":[{"name":"custom-provider","base-url":"https://compat.example.com/v1/","models":[{"name":"upstream-model","alias":"public-model"}],"api-key-entries":[{"api-key":"compat-secret","auth-index":"compat-index"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	source, err := NewSource(Config{SourceID: "source", BaseURL: server.URL, ManagementKey: "key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Assets[0].Models) != 1 {
		t.Fatalf("models were not deduplicated: %#v", page.Assets[0].Models)
	}
	secret, err := source.ResolveSecret(context.Background(), page.Assets[0].ID, platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if string(secret.Bytes) != "compat-secret" || secret.Metadata["base_url"] != "https://compat.example.com/v1" || secret.Metadata["models"] != "upstream-model" {
		t.Fatalf("resolved secret = %#v", secret)
	}
}

func TestSourceResolvesGeminiInteractionsKeyFromRealProvider(t *testing.T) {
	t.Parallel()

	var staticReads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = fmt.Fprint(w, `{"files":[{"id":"interactions-stable","auth_index":"interactions-index","name":"interactions-apikey","provider":"gemini-interactions","status":"ready","runtime_only":true}]}`)
		case "/v0/management/auth-files/models":
			_, _ = fmt.Fprint(w, `{"models":[{"id":"gemini-3.1-flash-lite"}]}`)
		case "/v0/management/interactions-api-key":
			staticReads++
			_, _ = fmt.Fprint(w, `{"interactions-api-key":[{"api-key":"interactions-secret","base-url":"https://generativelanguage.googleapis.com","auth-index":"interactions-index"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "source", BaseURL: server.URL, ManagementKey: "key"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Assets) != 1 || page.Assets[0].Provider != platform.ProviderGemini || !page.Assets[0].SecretReadable {
		t.Fatalf("interactions asset = %#v", page.Assets)
	}
	secret, err := source.ResolveSecret(context.Background(), page.Assets[0].ID, platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if string(secret.Bytes) != "interactions-secret" || staticReads != 1 {
		t.Fatalf("resolved secret = %q, reads = %d", secret.Bytes, staticReads)
	}
}

func TestSourceRedactsNonSuccessResponsesAndHonorsTimeout(t *testing.T) {
	t.Parallel()

	t.Run("non-success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "management-secret should remain private", http.StatusForbidden)
		}))
		t.Cleanup(server.Close)
		source, err := NewSource(Config{SourceID: "source", BaseURL: server.URL, ManagementKey: "management-secret"}, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		_, err = source.ListAssets(context.Background(), platform.PageCursor{})
		if err == nil || strings.Contains(err.Error(), "management-secret") {
			t.Fatalf("ListAssets() error = %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		t.Cleanup(server.Close)
		source, err := NewSource(Config{SourceID: "source", BaseURL: server.URL, ManagementKey: "key", RequestTimeout: 20 * time.Millisecond}, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		_, err = source.ListAssets(context.Background(), platform.PageCursor{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ListAssets(timeout) error = %v", err)
		}
	})
}

func TestStaticEndpointProviderMatrix(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"gemini":               "gemini-api-key",
		"aistudio":             "gemini-api-key",
		"interactions":         "interactions-api-key",
		"claude":               "claude-api-key",
		"codex":                "codex-api-key",
		"xai":                  "xai-api-key",
		"vertex":               "vertex-api-key",
		"openai-compatibility": "openai-compatibility",
	}
	for provider, want := range tests {
		endpoint, root := staticEndpoint(provider)
		if endpoint != want || root != want {
			t.Fatalf("staticEndpoint(%q) = %q, %q", provider, endpoint, root)
		}
	}
	if endpoint, root := staticEndpoint("future-plugin"); endpoint != "" || root != "" {
		t.Fatalf("unknown static endpoint = %q, %q", endpoint, root)
	}
}
