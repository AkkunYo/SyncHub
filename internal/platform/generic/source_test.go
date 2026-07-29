package generic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

var _ platform.UpstreamAdapter = (*Source)(nil)

func TestSourceDiscoversOneOpenAICompatibleEndpoint(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{"", "/v1"} {
		suffix := suffix
		t.Run("base path "+suffix, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.URL.RawQuery != "" {
					t.Errorf("model request = %s %s", r.Method, r.URL.String())
				}
				if got := r.Header.Get("Authorization"); got != "Bearer shared-test-key" {
					t.Errorf("Authorization = %q", got)
				}
				if got := r.Header.Get("Accept"); got != "application/json" {
					t.Errorf("Accept = %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":" gpt-4o-mini "},{"id":"gpt-4o"},{"id":"gpt-4o"}]}`)
			}))
			t.Cleanup(server.Close)

			source, err := NewSource(Config{
				SourceID: "shared-a", Name: "Shared A", BaseURL: server.URL + suffix,
				APIKey: "shared-test-key", RequestTimeout: time.Second,
			}, server.Client())
			if err != nil {
				t.Fatalf("NewSource() error = %v", err)
			}
			capabilities, err := source.Capabilities(context.Background())
			if err != nil {
				t.Fatalf("Capabilities() error = %v", err)
			}
			if !capabilities.SecretResolution || capabilities.GroupCatalog || !reflect.DeepEqual(capabilities.AssetKinds, []platform.AssetKind{platform.AssetProxyKey}) {
				t.Fatalf("capabilities = %#v", capabilities)
			}

			page, err := source.ListAssets(context.Background(), platform.PageCursor{})
			if err != nil {
				t.Fatalf("ListAssets() error = %v", err)
			}
			if page.HasMore || len(page.Assets) != 1 {
				t.Fatalf("page = %#v", page)
			}
			asset := page.Assets[0]
			if asset.ID != "shared-a:endpoint" || asset.SourceID != "shared-a" || asset.SourceType != "generic" || asset.Name != "Shared A" {
				t.Fatalf("asset identity = %#v", asset)
			}
			if asset.Kind != platform.AssetProxyKey || asset.Provider != platform.ProviderOpenAI || asset.RawType != "generic-openai" {
				t.Fatalf("asset shape = %#v", asset)
			}
			if asset.BaseURL != server.URL+suffix || !asset.Enabled || !asset.SecretReadable || !reflect.DeepEqual(asset.Models, []string{"gpt-4o", "gpt-4o-mini"}) {
				t.Fatalf("asset availability = %#v", asset)
			}
			encoded, err := json.Marshal(asset)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "shared-test-key") {
				t.Fatalf("asset leaked shared key: %s", encoded)
			}
		})
	}
}

func TestSourcePublishesDisabledAssetForEmptyModelList(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	}))
	t.Cleanup(server.Close)
	source := newTestSource(t, server)
	page, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatal(err)
	}
	asset := page.Assets[0]
	if asset.Enabled || asset.SecretReadable || asset.Models == nil || len(asset.Models) != 0 {
		t.Fatalf("empty asset = %#v", asset)
	}
}

func TestSourceResolvesOnlyTheFixedEndpointSecretAndReturnsCopies(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"gpt-4o"}]}`)
	}))
	t.Cleanup(server.Close)
	source := newTestSource(t, server)

	secret, err := source.ResolveSecret(context.Background(), "shared-a:endpoint", platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if secret.Kind != platform.AssetProxyKey || string(secret.Bytes) != "shared-test-key" {
		t.Fatalf("secret metadata = %#v", secret)
	}
	secret.Bytes[0] = 'X'
	again, err := source.ResolveSecret(context.Background(), "shared-a:endpoint", platform.SecretGrant{})
	if err != nil || string(again.Bytes) != "shared-test-key" {
		t.Fatalf("second secret = %#v, %v", again, err)
	}
	secret.Wipe()
	again.Wipe()

	if _, err := source.ResolveSecret(context.Background(), "shared-a:other", platform.SecretGrant{}); !errors.Is(err, platform.ErrSecretUnavailable) {
		t.Fatalf("unknown asset error = %v", err)
	}
}

func TestSourceClassifiesAndSanitizesModelFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		want       error
	}{
		{name: "unauthenticated", status: http.StatusUnauthorized, body: `{"secret":"response-secret"}`, want: ErrUnauthenticated},
		{name: "forbidden", status: http.StatusForbidden, body: `{"secret":"response-secret"}`, want: ErrForbidden},
		{name: "rate limited", status: http.StatusTooManyRequests, retryAfter: "12", want: platform.ErrRateLimited},
		{name: "server failure", status: http.StatusBadGateway, body: `{"secret":"response-secret"}`},
		{name: "invalid json", status: http.StatusOK, body: `{"data":`},
		{name: "missing data", status: http.StatusOK, body: `{"object":"list"}`},
		{name: "invalid model", status: http.StatusOK, body: `{"data":[{"id":""}]}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			t.Cleanup(server.Close)
			source := newTestSource(t, server)
			_, err := source.ListAssets(context.Background(), platform.PageCursor{})
			if err == nil {
				t.Fatal("ListAssets() error = nil")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), "response-secret") || strings.Contains(err.Error(), "shared-test-key") {
				t.Fatalf("error leaked secret: %v", err)
			}
			if test.name == "rate limited" {
				var rate *platform.RateLimitError
				if !errors.As(err, &rate) || rate.RetryAfter != 12*time.Second {
					t.Fatalf("rate limit = %#v", rate)
				}
			}
		})
	}
}

func TestSourceHonorsTimeoutAndRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	source, err := NewSource(Config{SourceID: "shared-a", Name: "Shared A", BaseURL: server.URL, APIKey: "shared-test-key", RequestTimeout: 10 * time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.ListAssets(context.Background(), platform.PageCursor{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}

	invalid := []Config{
		{Name: "name", BaseURL: server.URL, APIKey: "key", RequestTimeout: time.Second},
		{SourceID: "id", BaseURL: server.URL, APIKey: "key", RequestTimeout: time.Second},
		{SourceID: "id", Name: "name", BaseURL: "ftp://example.com", APIKey: "key", RequestTimeout: time.Second},
		{SourceID: "id", Name: "name", BaseURL: "https://user@example.com", APIKey: "key", RequestTimeout: time.Second},
		{SourceID: "id", Name: "name", BaseURL: "https://example.com?key=value", APIKey: "key", RequestTimeout: time.Second},
		{SourceID: "id", Name: "name", BaseURL: server.URL, RequestTimeout: time.Second},
		{SourceID: "id", Name: "name", BaseURL: server.URL, APIKey: "key"},
	}
	for i, cfg := range invalid {
		if _, err := NewSource(cfg, server.Client()); err == nil {
			t.Fatalf("invalid config %d accepted: %#v", i, cfg)
		}
	}
}

func newTestSource(t *testing.T, server *httptest.Server) *Source {
	t.Helper()
	source, err := NewSource(Config{
		SourceID: "shared-a", Name: "Shared A", BaseURL: server.URL,
		APIKey: "shared-test-key", RequestTimeout: time.Second,
	}, server.Client())
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}
	return source
}
