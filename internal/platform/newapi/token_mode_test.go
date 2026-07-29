package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

var (
	_ platform.GroupCatalogProvider = (*Source)(nil)
	_ platform.BatchSecretResolver  = (*Source)(nil)
)

func TestTokenModeListsPaginatedUserTokensWithoutAdminEndpoints(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/token/" {
			t.Errorf("unexpected token-mode request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("size"); got != "2" {
			t.Errorf("size = %q, want 2", got)
		}
		if got := r.URL.Query().Get("page_size"); got != "" {
			t.Errorf("page_size must be absent, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("p") {
		case "1":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"page":1,"page_size":2,"total":4,"items":[`+
				`{"id":42,"name":"vip-ready","key":"sk-abcd****wxyz","status":1,"group":"vip","remain_quota":100,"used_quota":9,"unlimited_quota":false,"expired_time":4102444800,"model_limits_enabled":true,"model_limits":"gpt-4o, gpt-4o-mini"},`+
				`{"id":43,"name":"disabled","key":"sk-disabled****","status":0,"group":"default","remain_quota":100,"used_quota":0,"unlimited_quota":false,"expired_time":-1,"model_limits_enabled":false,"model_limits":""}`+
				`]}}`)
		case "2":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"page":2,"page_size":2,"total":4,"items":[`+
				`{"id":44,"name":"expired","key":"sk-expired****","status":1,"group":"default","remain_quota":100,"used_quota":0,"unlimited_quota":false,"expired_time":1},`+
				`{"id":45,"name":"exhausted","key":"sk-empty****","status":1,"group":"default","remain_quota":0,"used_quota":100,"unlimited_quota":false,"expired_time":-1}`+
				`]}}`)
		case "3":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"page":3,"page_size":2,"total":4,"items":[]}}`)
		default:
			http.Error(w, "bad page", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	source := newTokenSource(t, server, 2)
	first, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err != nil {
		t.Fatalf("ListAssets(first) error = %v", err)
	}
	if !first.HasMore || first.Next.Page != 2 || len(first.Assets) != 2 {
		t.Fatalf("first page = %#v", first)
	}
	ready := first.Assets[0]
	if ready.ID != "source-token:token:42" || ready.Kind != platform.AssetProxyKey || ready.Provider != platform.ProviderOpenAI || ready.RawType != "newapi-token" {
		t.Fatalf("ready token identity = %#v", ready)
	}
	if ready.BaseURL != server.URL || !ready.Enabled || !ready.SecretReadable {
		t.Fatalf("ready token availability = %#v", ready)
	}
	if !reflect.DeepEqual(ready.Models, []string{"gpt-4o", "gpt-4o-mini"}) {
		t.Fatalf("ready models = %#v", ready.Models)
	}
	if ready.Metadata["masked_key"] != "sk-abcd****wxyz" || ready.Metadata["upstream_group"] != "vip" || ready.Metadata["remain_quota"] != "100" {
		t.Fatalf("ready metadata = %#v", ready.Metadata)
	}
	if first.Assets[1].Enabled || first.Assets[1].SecretReadable {
		t.Fatalf("disabled token is usable: %#v", first.Assets[1])
	}

	second, err := source.ListAssets(context.Background(), first.Next)
	if err != nil {
		t.Fatalf("ListAssets(second) error = %v", err)
	}
	if !second.HasMore || second.Next.Page != 3 || len(second.Assets) != 2 {
		t.Fatalf("second page = %#v", second)
	}
	for _, asset := range second.Assets {
		if asset.Enabled || asset.SecretReadable {
			t.Fatalf("unavailable token is usable: %#v", asset)
		}
	}
	third, err := source.ListAssets(context.Background(), second.Next)
	if err != nil {
		t.Fatalf("ListAssets(third) error = %v", err)
	}
	if third.HasMore || len(third.Assets) != 0 {
		t.Fatalf("terminal page = %#v", third)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("request count = %d, want 3 token pages", got)
	}

	encoded, err := json.Marshal(append(append(first.Assets, second.Assets...), third.Assets...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "dashboard-user-token") {
		t.Fatalf("asset JSON leaked management token: %s", encoded)
	}
}

func TestTokenModeRejectsUnsuccessfulTokenPage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":false,"message":"rejected"}`)
	}))
	t.Cleanup(server.Close)

	source := newTokenSource(t, server, 100)
	if _, err := source.ListAssets(context.Background(), platform.PageCursor{}); err == nil {
		t.Fatal("ListAssets() error = nil, want unsuccessful envelope failure")
	}
}

func TestTokenModeDiscoversVerifiedModelsForEveryAuthorizedGroup(t *testing.T) {
	t.Parallel()

	var modelGroups []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":1,"group":"vip"}}`)
		case "/api/user/self/groups":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"vip":{"ratio":1.5,"desc":"VIP"},"auto":{"ratio":"自动","desc":"Auto route"},"default":{"ratio":1,"desc":"Default"}}}`)
		case "/api/user/models":
			group := r.URL.Query().Get("group")
			modelGroups = append(modelGroups, group)
			switch group {
			case "auto":
				_, _ = fmt.Fprint(w, `{"success":true,"data":["gpt-4o-mini","gpt-4o","gpt-4o"]}`)
			case "default":
				_, _ = fmt.Fprint(w, `{"success":true,"data":[]}`)
			case "vip":
				_, _ = fmt.Fprint(w, `{"success":true,"data":["gpt-4o"]}`)
			default:
				http.Error(w, "unknown group", http.StatusBadRequest)
			}
		default:
			t.Errorf("unexpected group request: %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newTokenSource(t, server, 100)
	catalog, err := source.GroupCatalog(context.Background())
	if err != nil {
		t.Fatalf("GroupCatalog() error = %v", err)
	}
	if catalog.SourceID != "source-token" || catalog.DefaultGroup != "vip" {
		t.Fatalf("catalog identity = %#v", catalog)
	}
	if got := groupNames(catalog.Groups); !reflect.DeepEqual(got, []string{"auto", "default", "vip"}) {
		t.Fatalf("group order = %#v", got)
	}
	if !reflect.DeepEqual(modelGroups, []string{"auto", "default", "vip"}) {
		t.Fatalf("model request order = %#v", modelGroups)
	}
	auto := catalog.Groups[0]
	if !auto.Auto || auto.RatioKnown || auto.Ratio != 0 || !auto.ModelsVerified || !reflect.DeepEqual(auto.Models, []string{"gpt-4o", "gpt-4o-mini"}) {
		t.Fatalf("auto group = %#v", auto)
	}
	defaultGroup := catalog.Groups[1]
	if !defaultGroup.RatioKnown || defaultGroup.Ratio != 1 || !defaultGroup.ModelsVerified || defaultGroup.Models == nil || len(defaultGroup.Models) != 0 {
		t.Fatalf("default group = %#v", defaultGroup)
	}
	vip := catalog.Groups[2]
	if vip.Description != "VIP" || !vip.RatioKnown || vip.Ratio != 1.5 || !vip.ModelsVerified || !reflect.DeepEqual(vip.Models, []string{"gpt-4o"}) {
		t.Fatalf("vip group = %#v", vip)
	}
}

func TestTokenModeGroupCatalogFailsOnIncompleteCoreData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
		code int
		want error
	}{
		{name: "groups rate limited", path: "/api/user/self/groups", code: http.StatusTooManyRequests, want: platform.ErrRateLimited},
		{name: "models invalid JSON", path: "/api/user/models", code: http.StatusOK, body: `{"success":`},
		{name: "models unsuccessful", path: "/api/user/models", code: http.StatusOK, body: `{"success":false}`},
		{name: "models server failure", path: "/api/user/models", code: http.StatusBadGateway},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/user/self" {
					_, _ = fmt.Fprint(w, `{"success":true,"data":{"role":1,"group":"default"}}`)
					return
				}
				if r.URL.Path == "/api/user/self/groups" && test.path != r.URL.Path {
					_, _ = fmt.Fprint(w, `{"success":true,"data":{"default":{"ratio":1,"desc":"Default"}}}`)
					return
				}
				if r.URL.Path != test.path {
					http.NotFound(w, r)
					return
				}
				if test.name == "groups rate limited" {
					w.Header().Set("Retry-After", "11")
				}
				w.WriteHeader(test.code)
				if test.body != "" {
					_, _ = fmt.Fprint(w, test.body)
				}
			}))
			t.Cleanup(server.Close)

			source := newTokenSource(t, server, 100)
			_, err := source.GroupCatalog(context.Background())
			if err == nil {
				t.Fatal("GroupCatalog() error = nil")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("GroupCatalog() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestTokenModeResolvesOneSecretBatchAndPrefixesKeys(t *testing.T) {
	t.Parallel()

	var batchCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			_, _ = fmt.Fprint(w, tokenListJSON(42, 43, 44))
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/batch/keys":
			batchCalls.Add(1)
			var request struct {
				IDs []int `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode batch request: %v", err)
			}
			if !reflect.DeepEqual(request.IDs, []int{42, 43, 44}) {
				t.Errorf("batch ids = %#v", request.IDs)
			}
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"keys":{"42":"rawTokenA","43":"sk-already-prefixed"}}}`)
		default:
			t.Errorf("unexpected secret request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newTokenSource(t, server, 100)
	discoverTokenRecords(t, source)
	if got := source.MaxSecretBatchSize(); got != 100 {
		t.Fatalf("MaxSecretBatchSize() = %d, want 100", got)
	}
	resolved, err := source.ResolveSecrets(context.Background(), []string{
		"source-token:token:42", "source-token:token:43", "source-token:token:44",
	}, platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecrets() error = %v", err)
	}
	if got := string(resolved["source-token:token:42"].Bytes); got != "sk-rawTokenA" {
		t.Fatalf("token 42 key = %q", got)
	}
	if got := string(resolved["source-token:token:43"].Bytes); got != "sk-already-prefixed" {
		t.Fatalf("token 43 key = %q", got)
	}
	if _, exists := resolved["source-token:token:44"]; exists {
		t.Fatal("missing upstream key must remain absent")
	}
	if got := batchCalls.Load(); got != 1 {
		t.Fatalf("batch calls = %d, want 1", got)
	}
	for id, secret := range resolved {
		secret.Wipe()
		resolved[id] = secret
	}
}

func TestTokenModeResolveSecretUsesOneElementBatch(t *testing.T) {
	t.Parallel()

	var gotIDs []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/token/" {
			_, _ = fmt.Fprint(w, tokenListJSON(42))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/token/batch/keys" {
			var request struct {
				IDs []int `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			gotIDs = append([]int(nil), request.IDs...)
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"keys":{"42":"singleRaw"}}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	source := newTokenSource(t, server, 100)
	discoverTokenRecords(t, source)
	secret, err := source.ResolveSecret(context.Background(), "source-token:token:42", platform.SecretGrant{})
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if string(secret.Bytes) != "sk-singleRaw" || !reflect.DeepEqual(gotIDs, []int{42}) {
		t.Fatalf("secret=%q ids=%#v", secret.Bytes, gotIDs)
	}
	secret.Wipe()
}

func TestTokenModeRejectsInvalidSecretBatchesWithoutHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/token/" {
			_, _ = fmt.Fprint(w, tokenListJSON(42))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	source := newTokenSource(t, server, 100)
	discoverTokenRecords(t, source)
	baseline := requests.Load()
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "source-token:token:" + strconv.Itoa(1000+i)
	}
	tests := []struct {
		name string
		ids  []string
	}{
		{name: "empty"},
		{name: "duplicate", ids: []string{"source-token:token:42", "source-token:token:42"}},
		{name: "unknown", ids: []string{"source-token:token:999"}},
		{name: "oversized", ids: tooMany},
	}
	for _, test := range tests {
		if _, err := source.ResolveSecrets(context.Background(), test.ids, platform.SecretGrant{}); err == nil {
			t.Errorf("ResolveSecrets(%s) error = nil", test.name)
		}
	}
	if got := requests.Load(); got != baseline {
		t.Fatalf("invalid batches made %d HTTP requests", got-baseline)
	}
}

func TestTokenModeSecretBatchPreservesRateLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/token/" {
			_, _ = fmt.Fprint(w, tokenListJSON(42))
			return
		}
		w.Header().Set("Retry-After", "29")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	source := newTokenSource(t, server, 100)
	discoverTokenRecords(t, source)
	_, err := source.ResolveSecrets(context.Background(), []string{"source-token:token:42"}, platform.SecretGrant{})
	if !errors.Is(err, platform.ErrRateLimited) {
		t.Fatalf("ResolveSecrets() error = %v, want rate limited", err)
	}
	var rateLimitErr *platform.RateLimitError
	if !errors.As(err, &rateLimitErr) || rateLimitErr.RetryAfter != 29*time.Second {
		t.Fatalf("rate limit error = %#v", err)
	}
}

func newTokenSource(t *testing.T, server *httptest.Server, pageSize int) *Source {
	t.Helper()
	source, err := NewSource(Config{
		SourceID: "source-token", BaseURL: server.URL, AccessToken: "dashboard-user-token", PageSize: pageSize, DiscoveryMode: "token",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func discoverTokenRecords(t *testing.T, source *Source) {
	t.Helper()
	if _, err := source.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
		t.Fatalf("discover token records: %v", err)
	}
}

func tokenListJSON(ids ...int) string {
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, fmt.Sprintf(`{"id":%d,"name":"token-%d","key":"sk-mask****","status":1,"group":"default","remain_quota":100,"used_quota":0,"unlimited_quota":false,"expired_time":4102444800}`, id, id))
	}
	return fmt.Sprintf(`{"success":true,"data":{"page":1,"page_size":100,"total":%d,"items":[%s]}}`, len(ids), strings.Join(items, ","))
}

func groupNames(groups []platform.UpstreamGroup) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.Name)
	}
	return names
}
