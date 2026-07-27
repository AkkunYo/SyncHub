package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestSourceListsChannelMetadataByPageWithoutReadingKeys(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/user/self" {
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":100,"group":"default"}}`)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/channel/" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer dashboard-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Security-Proof"); got != "" {
			t.Errorf("metadata request leaked security proof %q", got)
		}
		if got := r.Header.Get("New-Api-User"); got != "" {
			t.Errorf("New-Api-User = %q, want omitted", got)
		}
		if got := r.URL.Query().Get("page_size"); got != "2" && got != "1" {
			t.Errorf("page_size = %q", got)
		}

		switch r.URL.Query().Get("p") {
		case "1":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"items":[`+
				`{"id":1,"type":1,"name":"primary","status":1,"base_url":"https://api.openai.com/","models":"gpt-4o, gpt-4.1","group":"default","priority":0,"weight":100,"channel_info":{"is_multi_key":false}},`+
				`{"id":2,"type":14,"name":"claude pool","status":1,"base_url":"https://api.anthropic.com","models":"claude-sonnet-4","group":"default","priority":2,"weight":90,"channel_info":{"is_multi_key":true,"multi_key_size":2,"multi_key_status_list":{"0":1,"1":0}}}`+
				`],"total":3,"page":1,"page_size":2}}`)
		case "2":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"items":[`+
				`{"id":3,"type":999,"name":"future channel","status":1,"models":"future-model","group":"default","priority":0,"weight":100,"channel_info":{"is_multi_key":false}}`+
				`],"total":3,"page":2,"page_size":2}}`)
		default:
			t.Errorf("p = %q", r.URL.Query().Get("p"))
			http.Error(w, "bad page", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{
		SourceID:      "source-a",
		BaseURL:       server.URL,
		AccessToken:   "dashboard-token",
		PageSize:      2,
		DiscoveryMode: "channel",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	first, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets(first) error = %v", err)
	}
	if !first.HasMore || first.Next.Page != 2 || len(first.Assets) != 3 {
		t.Fatalf("first page = %#v", first)
	}
	if got := first.Assets[0]; got.ID != "source-a:channel:1" || got.Provider != platform.ProviderOpenAI || got.Kind != platform.AssetStaticAPIKey || !got.SecretReadable {
		t.Fatalf("single-key asset = %#v", got)
	}
	if got := strings.Join(first.Assets[0].Models, ","); got != "gpt-4o,gpt-4.1" {
		t.Fatalf("models = %q", got)
	}
	if got := first.Assets[1]; got.ID != "source-a:channel:2:key:0" || !got.Enabled || got.Provider != platform.ProviderAnthropic {
		t.Fatalf("enabled multi-key asset = %#v", got)
	}
	if got := first.Assets[2]; got.ID != "source-a:channel:2:key:1" || got.Enabled {
		t.Fatalf("disabled multi-key asset = %#v", got)
	}

	second, err := source.ListAssets(context.Background(), first.Next)
	if err != nil {
		t.Fatalf("ListAssets(second) error = %v", err)
	}
	if second.HasMore || len(second.Assets) != 1 {
		t.Fatalf("second page = %#v", second)
	}
	unknown := second.Assets[0]
	if unknown.Provider != platform.ProviderUnknown || unknown.RawType != "999" || unknown.Metadata["discovery_only"] != "true" {
		t.Fatalf("unknown asset = %#v", unknown)
	}
	encoded, err := json.Marshal(append(first.Assets, second.Assets...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "dashboard-token") || strings.Contains(strings.ToLower(string(encoded)), `"key"`) {
		t.Fatalf("metadata contains secret material: %s", encoded)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("request count = %d, want 4 (probe: user/self + channel/; listing: 2 pages)", got)
	}
}

func TestSourceSendsConfiguredUserIdentityForPaginationAndKeyRead(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("New-Api-User"); got != "47" {
			t.Errorf("New-Api-User = %q, want 47", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer REPLACE_WITH_SOURCE_TOKEN" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/self":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":100,"group":"default"}}`)
		case r.Method == http.MethodGet && r.URL.Query().Get("p") == "1":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"items":[{"id":7,"type":1,"name":"one","status":1,"channel_info":{"is_multi_key":false}}],"total":2,"page":1,"page_size":1}}`)
		case r.Method == http.MethodGet && r.URL.Query().Get("p") == "2":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"items":[{"id":8,"type":1,"name":"two","status":1,"channel_info":{"is_multi_key":false}}],"total":2,"page":2,"page_size":1}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/7/key":
			if got := r.Header.Get("X-Security-Proof"); got != "REPLACE_WITH_SECURITY_PROOF" {
				t.Errorf("X-Security-Proof = %q", got)
			}
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"key":"REPLACE_WITH_ASSET_KEY"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	cfg := Config{SourceID: "source-a", BaseURL: server.URL, AccessToken: "REPLACE_WITH_SOURCE_TOKEN", PageSize: 1, DiscoveryMode: "channel"}
	setNewAPIUserIDForTest(t, &cfg, 47)
	source, err := NewSource(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets(first) error = %v", err)
	}
	if _, err := source.ListAssets(context.Background(), first.Next); err != nil {
		t.Fatalf("ListAssets(second) error = %v", err)
	}
	secret, err := source.ResolveSecret(context.Background(), "source-a:channel:7", platform.SecretGrant{SecurityProof: "REPLACE_WITH_SECURITY_PROOF"})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	secret.Wipe()
}

func setNewAPIUserIDForTest(t *testing.T, config any, userID int) {
	t.Helper()
	field := reflect.ValueOf(config).Elem().FieldByName("UserID")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Int {
		t.Fatal("New API adapter configuration is missing an integer UserID field")
	}
	field.SetInt(int64(userID))
}

func TestSourceRequiresSecurityProofAndResolvesSelectedMultiKey(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/channel/7/key" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer root-session" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Security-Proof"); got != "proof-2fa" {
			t.Errorf("X-Security-Proof = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":true,"data":{"key":"[\"alpha\",\"beta\"]"}}`)
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "source-a", BaseURL: server.URL, AccessToken: "root-session", DiscoveryMode: "channel"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, err = source.ResolveSecret(context.Background(), "source-a:channel:7:key:1", platform.SecretGrant{})
	if !errors.Is(err, platform.ErrSecretGrantRequired) {
		t.Fatalf("ResolveSecret(without proof) error = %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("request count without proof = %d", got)
	}

	secret, err := source.ResolveSecret(context.Background(), "source-a:channel:7:key:1", platform.SecretGrant{SecurityProof: "proof-2fa"})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if got := string(secret.Bytes); got != "beta" {
		t.Fatalf("resolved key = %q", got)
	}
	secret.Wipe()
	for i, b := range secret.Bytes {
		if b != 0 {
			t.Fatalf("secret byte %d not wiped", i)
		}
	}
}

func TestSourceDoesNotExposeSecretsInUpstreamErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid proof proof-sensitive for root-sensitive", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "source-a", BaseURL: server.URL, AccessToken: "root-sensitive", DiscoveryMode: "channel"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.ResolveSecret(context.Background(), "source-a:channel:7", platform.SecretGrant{SecurityProof: "proof-sensitive"})
	if err == nil {
		t.Fatal("ResolveSecret() unexpectedly succeeded")
	}
	if message := err.Error(); strings.Contains(message, "proof-sensitive") || strings.Contains(message, "root-sensitive") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestProbeAutoModeAdminResolvesToChannel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/self":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":10,"group":"default"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":1}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "s", BaseURL: server.URL, AccessToken: "tok", DiscoveryMode: "auto"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	caps, err := source.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if len(caps.AssetKinds) != 1 || caps.AssetKinds[0] != platform.AssetStaticAPIKey {
		t.Fatalf("expected channel mode AssetStaticAPIKey, got %v", caps.AssetKinds)
	}
}

func TestProbeAutoModeCommonUserResolvesToToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/user/self" {
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":1,"group":"default"}}`)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "s", BaseURL: server.URL, AccessToken: "tok", DiscoveryMode: "auto"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	caps, err := source.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if len(caps.AssetKinds) != 1 || caps.AssetKinds[0] != platform.AssetProxyKey {
		t.Fatalf("expected token mode AssetProxyKey, got %v", caps.AssetKinds)
	}
}

func TestProbeAutoModeAdminChannelForbiddenResolvesToToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/self":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":10,"group":"default"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
			http.Error(w, `{"success":false}`, http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "s", BaseURL: server.URL, AccessToken: "tok", DiscoveryMode: "auto"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	caps, err := source.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if len(caps.AssetKinds) != 1 || caps.AssetKinds[0] != platform.AssetProxyKey {
		t.Fatalf("expected token mode, got %v", caps.AssetKinds)
	}
}

func TestProbeChannelModeCommonUserReturnsError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/user/self" {
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":1,"group":"default"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "s", BaseURL: server.URL, AccessToken: "tok", DiscoveryMode: "channel"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Capabilities(context.Background())
	if err == nil {
		t.Fatal("expected error for channel mode with common user")
	}
	if !errors.Is(err, ErrInsufficientPrivilege) {
		t.Fatalf("error = %v, want ErrInsufficientPrivilege", err)
	}
}

func TestProbeTokenModeSkipsProbe(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request in token mode: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "s", BaseURL: server.URL, AccessToken: "tok", DiscoveryMode: "token"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	caps, err := source.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if len(caps.AssetKinds) != 1 || caps.AssetKinds[0] != platform.AssetProxyKey {
		t.Fatalf("expected token mode AssetProxyKey, got %v", caps.AssetKinds)
	}
}

func TestProbeCachesResult(t *testing.T) {
	t.Parallel()

	var probeCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/user/self" {
			probeCount.Add(1)
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":1,"group":"default"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	source, err := NewSource(Config{SourceID: "s", BaseURL: server.URL, AccessToken: "tok", DiscoveryMode: "auto"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := probeCount.Load(); got != 1 {
		t.Fatalf("probe called %d times, want 1 (cached)", got)
	}
}
