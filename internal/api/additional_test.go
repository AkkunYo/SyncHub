package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/discovery"
	"github.com/AkkunYo/SyncHub/internal/mapping"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/platform/cliproxyapi"
	"github.com/AkkunYo/SyncHub/internal/platform/newapi"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestNewAPIUserIDConfigCRUDAndRedaction(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)

	for _, item := range []struct {
		path string
		body string
		id   string
		want float64
	}{
		{path: "/api/v1/targets", id: "target-user", want: 17, body: `{"id":"target-user","name":"Target User","type":"newapi","base_url":"https://target-user.example.com","access_token":"REPLACE_WITH_TARGET_TOKEN","user_id":17}`},
		{path: "/api/v1/upstreams", id: "source-user", want: 23, body: `{"id":"source-user","name":"Source User","type":"newapi","base_url":"https://source-user.example.com","access_token":"REPLACE_WITH_SOURCE_TOKEN","user_id":23}`},
	} {
		recorder, envelope := request(t, router, http.MethodPost, item.path, item.body, "application/json")
		if recorder.Code != http.StatusCreated {
			t.Fatalf("POST %s status=%d body=%s", item.path, recorder.Code, recorder.Body.String())
		}
		data := dataObject(t, envelope)
		if data["id"] != item.id || data["user_id"] != item.want || strings.Contains(recorder.Body.String(), "REPLACE_WITH") || strings.Contains(recorder.Body.String(), "access_token") {
			t.Fatalf("created redacted config = %#v, body=%s", data, recorder.Body.String())
		}
	}

	for _, item := range []struct {
		path string
		want float64
	}{
		{path: "/api/v1/targets/target-user", want: 17},
		{path: "/api/v1/upstreams/source-user", want: 23},
	} {
		body := `{"name":"Renamed","base_url":"https://renamed.example.com"}`
		recorder, envelope := request(t, router, http.MethodPut, item.path, body, "application/json")
		if recorder.Code != http.StatusOK || dataObject(t, envelope)["user_id"] != item.want {
			t.Fatalf("omitted user_id update status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}

	clear := `{"name":"Renamed","base_url":"https://renamed.example.com","user_id":0}`
	recorder, envelope := request(t, router, http.MethodPut, "/api/v1/targets/target-user", clear, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear user_id status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got, exists := dataObject(t, envelope)["user_id"]; exists && got != float64(0) {
		t.Fatalf("cleared user_id = %#v", got)
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/config", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET config status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	encoded := recorder.Body.String()
	if strings.Contains(encoded, "REPLACE_WITH") || strings.Contains(encoded, "access_token") {
		t.Fatalf("GET config leaked credentials: %s", encoded)
	}
	data := dataObject(t, envelope)
	var upstreamUserID any
	for _, raw := range data["upstreams"].([]any) {
		entry := raw.(map[string]any)
		if entry["id"] == "source-user" {
			upstreamUserID = entry["user_id"]
		}
	}
	if upstreamUserID != float64(23) {
		t.Fatalf("GET config source user_id = %#v", upstreamUserID)
	}

	invalid := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/v1/targets", body: `{"id":"negative","name":"Negative","type":"newapi","base_url":"https://negative.example.com","access_token":"x","user_id":-1}`},
		{method: http.MethodPost, path: "/api/v1/targets", body: `{"id":"cpa-user","name":"CPA","type":"cliproxyapi","base_url":"https://cpa.example.com","management_key":"x","user_id":1}`},
		{method: http.MethodPost, path: "/api/v1/upstreams", body: `{"id":"sub-user","name":"Sub","type":"sub2api","base_url":"https://sub.example.com","api_key":"x","user_id":1}`},
		{method: http.MethodPut, path: "/api/v1/upstreams/source-user", body: `{"name":"Source User","base_url":"https://source-user.example.com","user_id":-1}`},
	}
	for _, item := range invalid {
		recorder, envelope := request(t, router, item.method, item.path, item.body, "application/json")
		if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
			t.Fatalf("invalid user_id %s %s status=%d body=%s", item.method, item.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestConfigurationSupportsPersistedCredentialVariants(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)

	requests := []struct {
		path string
		body string
	}{
		{
			path: "/api/v1/targets",
			body: `{"id":"target-cpa","name":"CPA target","type":"cliproxyapi","base_url":"https://cpa-target.example.com","management_key":"cpa-target-management"}`,
		},
		{
			path: "/api/v1/upstreams",
			body: `{"id":"source-new-key","name":"New API token source","type":"newapi","base_url":"https://new-source.example.com","access_token":"new-source-key"}`,
		},
		{
			path: "/api/v1/upstreams",
			body: `{"id":"source-generic-key","name":"Generic key source","type":"generic","base_url":"https://generic-source.example.com/v1","api_key":"cpa-source-key"}`,
		},
	}
	for _, item := range requests {
		recorder, envelope := request(t, router, http.MethodPost, item.path, item.body, "application/json")
		if recorder.Code != http.StatusCreated || envelope["success"] != true {
			t.Fatalf("POST %s status=%d body=%s", item.path, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "cpa-target-management") ||
			strings.Contains(recorder.Body.String(), "new-source-key") ||
			strings.Contains(recorder.Body.String(), "cpa-source-key") ||
			strings.Contains(recorder.Body.String(), "api_key") {
			t.Fatalf("credential variant leaked: %s", recorder.Body.String())
		}
	}

	targetUpdate := `{"name":"CPA target updated","base_url":"https://cpa-target-2.example.com","management_key":"new-management-key"}`
	recorder, _ := request(t, router, http.MethodPut, "/api/v1/targets/target-cpa", targetUpdate, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("target update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	upstreamUpdate := `{"name":"New API source updated","base_url":"https://new-source-2.example.com","access_token":"replacement-access"}`
	recorder, _ = request(t, router, http.MethodPut, "/api/v1/upstreams/source-new-key", upstreamUpdate, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("upstream update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if target, ok := targetByID(env.store.cfg, "target-cpa"); !ok || target.ManagementKey != "new-management-key" {
		t.Fatalf("target credential update = %#v, found=%v", target, ok)
	}
	rejectTargetAPIKey := `{"name":"CPA target updated","base_url":"https://cpa-target-2.example.com","api_key":"must-not-be-used"}`
	recorder, envelope := request(t, router, http.MethodPut, "/api/v1/targets/target-cpa", rejectTargetAPIKey, "application/json")
	if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
		t.Fatalf("CPA target api_key status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if upstream, ok := upstreamByID(env.store.cfg, "source-new-key"); !ok || upstream.AccessToken != "replacement-access" || upstream.APIKey != "" {
		t.Fatalf("upstream credential update = %#v, found=%v", upstream, ok)
	}
}

func TestConfigurationValidationAndStoreFailures(t *testing.T) {
	invalidCreates := []struct {
		path string
		body string
	}{
		{path: "/api/v1/targets", body: `{"id":"bad","name":"Bad","type":"unknown","base_url":"https://target.example.com","access_token":"x"}`},
		{path: "/api/v1/targets", body: `{"id":"bad","name":"Bad","type":"newapi","base_url":"ftp://target.example.com","access_token":"x"}`},
		{path: "/api/v1/targets", body: `{"id":"bad","name":"Bad","type":"newapi","base_url":"https://target.example.com","management_key":"wrong-kind"}`},
		{path: "/api/v1/upstreams", body: `{"id":"bad","name":"Bad","type":"sub2api","base_url":"https://source.example.com"}`},
		{path: "/api/v1/upstreams", body: `{"id":"bad","name":"Bad","type":"cliproxyapi","base_url":"https://source.example.com","access_token":"wrong-kind"}`},
	}
	for _, item := range invalidCreates {
		recorder, envelope := request(t, newTestEnvironment().router(t), http.MethodPost, item.path, item.body, "application/json")
		if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
			t.Fatalf("POST %s status=%d body=%s", item.path, recorder.Code, recorder.Body.String())
		}
	}

	invalidApps := []string{
		`{"host":"bad host","port":8888,"reconcile_interval":"5m","request_timeout":"15s","sync_concurrency":4}`,
		`{"host":"127.0.0.1","port":0,"reconcile_interval":"5m","request_timeout":"15s","sync_concurrency":4}`,
		`{"host":"127.0.0.1","port":8888,"reconcile_interval":"0s","request_timeout":"15s","sync_concurrency":4}`,
		`{"host":"127.0.0.1","port":8888,"reconcile_interval":"5m","request_timeout":"bad","sync_concurrency":4}`,
		`{"host":"127.0.0.1","port":8888,"reconcile_interval":"5m","request_timeout":"15s","sync_concurrency":0}`,
	}
	for _, body := range invalidApps {
		recorder, envelope := request(t, newTestEnvironment().router(t), http.MethodPut, "/api/v1/config/app", body, "application/json")
		if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" {
			t.Fatalf("invalid app status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}

	t.Run("missing resources", func(t *testing.T) {
		env := newTestEnvironment()
		router := env.router(t)
		for _, item := range []struct {
			method string
			path   string
			body   string
			code   string
		}{
			{method: http.MethodPut, path: "/api/v1/targets/missing", body: `{"name":"Missing","base_url":"https://missing.example.com"}`, code: "target_not_found"},
			{method: http.MethodDelete, path: "/api/v1/targets/missing", code: "target_not_found"},
			{method: http.MethodPut, path: "/api/v1/upstreams/missing", body: `{"name":"Missing","base_url":"https://missing.example.com"}`, code: "upstream_not_found"},
			{method: http.MethodDelete, path: "/api/v1/upstreams/missing", code: "upstream_not_found"},
		} {
			recorder, envelope := request(t, router, item.method, item.path, item.body, map[bool]string{true: "application/json"}[item.body != ""])
			if recorder.Code != http.StatusNotFound || errorCode(t, envelope) != item.code {
				t.Fatalf("%s %s status=%d body=%s", item.method, item.path, recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("atomic store failure", func(t *testing.T) {
		env := newTestEnvironment()
		env.store.updateErr = errors.New("config failure " + testSecret)
		body := `{"host":"127.0.0.1","port":9000,"reconcile_interval":"5m","request_timeout":"15s","sync_concurrency":4}`
		recorder, envelope := request(t, env.router(t), http.MethodPut, "/api/v1/config/app", body, "application/json")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, envelope) != "internal_error" || strings.Contains(recorder.Body.String(), testSecret) {
			t.Fatalf("store failure status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestConcreteAdapterChannelNotFoundErrorsMapToContract(t *testing.T) {
	tests := []struct {
		name   string
		method string
		err    error
	}{
		{name: "New API update", method: http.MethodPut, err: newapi.ErrChannelNotFound},
		{name: "CLIProxyAPI delete", method: http.MethodDelete, err: cliproxyapi.ErrChannelNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnvironment()
			target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
			body := ""
			contentType := ""
			if test.method == http.MethodPut {
				target.updateErr = test.err
				body = `{"name":"channel","base_url":"","models":["gpt-4.1"],"group":"default","priority":0,"weight":100,"enabled":true}`
				contentType = "application/json"
			} else {
				target.deleteErr = test.err
			}
			recorder, envelope := request(t, env.router(t), test.method, "/api/v1/targets/target-a/channels/42", body, contentType)
			if recorder.Code != http.StatusNotFound || errorCode(t, envelope) != "channel_not_found" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestMappingPersistenceFailureMarksMatrixNeedsReconcile(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{name: "update", method: http.MethodPut},
		{name: "delete", method: http.MethodDelete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnvironment()
			mapping := platform.SyncMapping{
				UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42",
				Snapshot: platform.ChannelSnapshot{Models: []string{"old"}, Group: "default", Weight: 100},
			}
			env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{mapping}
			env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
			target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
			target.updateOut = platform.Channel{ID: "42", Name: "channel", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true}
			body := ""
			contentType := ""
			if test.method == http.MethodPut {
				env.mappings.updateErr = errors.New("persist failed")
				body = `{"name":"channel","base_url":"","models":["gpt-4.1"],"group":"default","priority":0,"weight":100,"enabled":true}`
				contentType = "application/json"
			} else {
				env.mappings.deleteErr = errors.New("persist failed")
			}
			router := env.router(t)
			recorder, envelope := request(t, router, test.method, "/api/v1/targets/target-a/channels/42", body, contentType)
			if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" {
				t.Fatalf("mutation status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			recorder, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("matrix status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			cell := dataObject(t, envelope)["rows"].([]any)[0].(map[string]any)["cells"].([]any)[0].(map[string]any)
			if cell["status"] != "needs_reconcile" {
				t.Fatalf("matrix cell=%#v", cell)
			}
		})
	}
}

func TestRequestIDGeneratorPanicIsSafelyRecovered(t *testing.T) {
	env := newTestEnvironment()
	deps := env.dependencies()
	deps.RequestIDGenerator = func() string { panic("request id " + testSecret) }
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("request-id panic escaped middleware: %v", recovered)
			}
		}()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	}()
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), testSecret) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if errorCode(t, envelope) != "internal_error" || envelope["request_id"] == "" {
		t.Fatalf("envelope=%#v", envelope)
	}
}

func TestOperationDependencyFailuresAndBoundaries(t *testing.T) {
	t.Run("list mapping failure", func(t *testing.T) {
		env := newTestEnvironment()
		env.mappings.listErr = errors.New("mapping failure " + testSecret)
		recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/targets/target-a/channels", "", "")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, envelope) != "internal_error" || strings.Contains(recorder.Body.String(), testSecret) {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("target resolver failure", func(t *testing.T) {
		env := newTestEnvironment()
		resolved := env.resolver.targets["target-a"]
		resolved.err = errors.New("resolver failure")
		env.resolver.targets["target-a"] = resolved
		recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/targets/target-a/channels", "", "")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, envelope) != "internal_error" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("mapping read after remote update", func(t *testing.T) {
		env := newTestEnvironment()
		env.mappings.listErr = errors.New("mapping read failed " + testSecret)
		target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
		target.updateOut = platform.Channel{ID: "42", Name: "updated", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true}
		body := `{"name":"updated","base_url":"","models":["gpt-4.1"],"group":"default","priority":0,"weight":100,"enabled":true}`
		recorder, envelope := request(t, env.router(t), http.MethodPut, "/api/v1/targets/target-a/channels/42", body, "application/json")
		if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" || strings.Contains(recorder.Body.String(), testSecret) {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("refresh upstream failure and timeout", func(t *testing.T) {
		for _, item := range []struct {
			err    error
			status int
			code   string
		}{
			{err: errors.New("platform failure"), status: http.StatusBadGateway, code: "upstream_failure"},
			{err: context.DeadlineExceeded, status: http.StatusGatewayTimeout, code: "upstream_timeout"},
		} {
			env := newTestEnvironment()
			env.fakeDisc.refreshErr = item.err
			recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/upstreams/source-a/refresh", "", "")
			if recorder.Code != item.status || errorCode(t, envelope) != item.code {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("sync request-level secret failure", func(t *testing.T) {
		env := newTestEnvironment()
		env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{{
			UnitID: "u-1", AssetID: "source-a:channel:7:key:0", TargetID: "target-a", Status: syncservice.TargetFailed,
			Code: "secret_unavailable", EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{},
		}}}
		env.syncer.err = platform.ErrSecretGrantRequired
		body := staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", 100)
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
		if recorder.Code != http.StatusOK || dataObject(t, envelope)["units"].([]any)[0].(map[string]any)["code"] != "secret_unavailable" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("reconcile timeout", func(t *testing.T) {
		env := newTestEnvironment()
		env.reconciler.checkErr = context.DeadlineExceeded
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")
		if recorder.Code != http.StatusGatewayTimeout || errorCode(t, envelope) != "upstream_timeout" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("accept repository failure", func(t *testing.T) {
		env := newTestEnvironment()
		env.mappings.listErr = errors.New("repository failure")
		body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/targets/target-a/drift/accept", body, "application/json")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, envelope) != "internal_error" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestBatchSyncTargetResolverFailuresRemainPerTarget(t *testing.T) {
	t.Run("one target fails and another syncs", func(t *testing.T) {
		env := newTestEnvironment()
		env.store.cfg.Targets = append(env.store.cfg.Targets, config.TargetConfig{
			ID: "target-b", Name: "Target B", Type: "newapi", BaseURL: "https://target-b.example.com", AccessToken: "target-b-credential",
		})
		resolvedA := env.resolver.targets["target-a"]
		resolvedA.err = errors.New("resolver detail " + testSecret)
		env.resolver.targets["target-a"] = resolvedA
		env.resolver.targets["target-b"] = targetResolution{
			adapter: &fakeTarget{},
			capabilities: platform.TargetCapabilities{Platform: "newapi", Providers: map[string]platform.ProviderCapability{
				platform.ProviderOpenAI: {Modes: []platform.SyncMode{platform.SyncModeStaticKey}},
			}},
		}
		env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{{UnitID: "u-b", AssetID: "source-a:channel:7:key:0", TargetID: "target-b", Status: syncservice.TargetSynced, ChannelID: "84", EffectiveModels: []string{"gpt-4.1"}, ExcludedModels: []string{}, Warnings: []string{}}}}
		body := `{"upstream_id":"source-a","units":[{"unit_id":"u-a","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}},{"unit_id":"u-b","asset_id":"source-a:channel:7:key:0","target_id":"target-b","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}]}`
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
		if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), testSecret) || strings.Contains(recorder.Body.String(), "resolver detail") {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		results := dataObject(t, envelope)["units"].([]any)
		if len(results) != 2 || results[0].(map[string]any)["target_id"] != "target-a" || results[0].(map[string]any)["status"] != "failed" ||
			results[1].(map[string]any)["target_id"] != "target-b" || results[1].(map[string]any)["status"] != "synced" {
			t.Fatalf("results=%#v", results)
		}
		if env.syncer.calls != 1 || len(env.syncer.multiRequest.Units) != 1 || env.syncer.multiRequest.Units[0].Target.ID != "target-b" {
			t.Fatalf("sync request=%#v calls=%d", env.syncer.multiRequest.Units, env.syncer.calls)
		}
	})

	t.Run("all targets fail resolution", func(t *testing.T) {
		env := newTestEnvironment()
		resolved := env.resolver.targets["target-a"]
		resolved.err = context.DeadlineExceeded
		env.resolver.targets["target-a"] = resolved
		body := staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", 100)
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		result := dataObject(t, envelope)["units"].([]any)[0].(map[string]any)
		if result["status"] != "failed" || result["code"] != "upstream_timeout" || result["retryable"] != true {
			t.Fatalf("result=%#v", result)
		}
		if env.syncer.calls != 0 {
			t.Fatalf("sync service called %d times", env.syncer.calls)
		}
	})
}

func TestPendingNeedsReconcilePreventsDuplicateSyncAndCanBeDeleted(t *testing.T) {
	env := newTestEnvironment()
	env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{}
	env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{{
		UnitID: "u-1", AssetID: "source-a:channel:7:key:0", TargetID: "target-a", Status: syncservice.TargetNeedsReconcile,
		Code: "mapping_persist_failed", ChannelID: "provisional-42", Retryable: true, EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{},
	}}}
	env.syncer.err = syncservice.ErrMappingPersist
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.channels = []platform.Channel{{ID: "provisional-42", Name: "provisional", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true}}
	router := env.router(t)
	body := staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", 100)

	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/sync", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("first sync status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	first := dataObject(t, envelope)["units"].([]any)[0].(map[string]any)
	if first["status"] != "needs_reconcile" || first["channel_id"] != "provisional-42" {
		t.Fatalf("first result=%#v", first)
	}

	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	cell := matrixCellFromEnvelope(t, envelope)
	if cell["status"] != "needs_reconcile" || cell["channel_id"] != "provisional-42" {
		t.Fatalf("pending matrix cell=%#v", cell)
	}

	_, envelope = request(t, router, http.MethodGet, "/api/v1/targets/target-a/channels", "", "")
	channel := dataObject(t, envelope)["channels"].([]any)[0].(map[string]any)
	if channel["managed"] != true || channel["upstream_asset_id"] != "source-a:channel:7:key:0" {
		t.Fatalf("provisional channel=%#v", channel)
	}

	recorder, envelope = request(t, router, http.MethodPost, "/api/v1/sync", body, "application/json")
	if recorder.Code != http.StatusOK || env.syncer.calls != 1 {
		t.Fatalf("repeat sync status=%d calls=%d body=%s", recorder.Code, env.syncer.calls, recorder.Body.String())
	}
	repeated := dataObject(t, envelope)["units"].([]any)[0].(map[string]any)
	if repeated["status"] != "needs_reconcile" || repeated["channel_id"] != "provisional-42" {
		t.Fatalf("repeat result=%#v", repeated)
	}

	recorder, _ = request(t, router, http.MethodDelete, "/api/v1/targets/target-a/channels/provisional-42", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	cell = matrixCellFromEnvelope(t, envelope)
	if cell["status"] != "unsynced" {
		t.Fatalf("deleted pending state remains: %#v", cell)
	}
}

func TestDependencyErrorClassification(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{err: nil, status: 500, code: "internal_error"},
		{err: context.Canceled, status: 504, code: "upstream_timeout"},
		{err: ErrUpstreamTimeout, status: 504, code: "upstream_timeout"},
		{err: ErrTargetNotFound, status: 404, code: "target_not_found"},
		{err: ErrUpstreamNotFound, status: 404, code: "upstream_not_found"},
		{err: ErrAssetNotFound, status: 404, code: "asset_not_found"},
		{err: ErrChannelNotFound, status: 404, code: "channel_not_found"},
		{err: mapping.ErrMappingNotFound, status: 404, code: "channel_not_found"},
		{err: ErrResourceInUse, status: 409, code: "resource_in_use"},
		{err: platform.ErrIncompatibleTarget, status: 409, code: "incompatible_target"},
		{err: syncservice.ErrMappingPersist, status: 409, code: "needs_reconcile"},
		{err: platform.ErrSecretUnavailable, status: 422, code: "secret_unavailable"},
		{err: platform.ErrAssetDisabled, status: 422, code: "secret_unavailable"},
		{err: syncservice.ErrSecretResolve, status: 422, code: "secret_unavailable"},
		{err: ErrUpstreamFailure, status: 502, code: "upstream_failure"},
		{err: errors.New("unknown"), status: 500, code: "internal_error"},
	}
	for _, test := range tests {
		got := classifyError(test.err, internalError)
		if got.status != test.status || got.code != test.code {
			t.Fatalf("classifyError(%v) = %#v", test.err, got)
		}
	}
}

func TestTargetResultNormalization(t *testing.T) {
	tests := []struct {
		input     syncservice.TargetResult
		status    syncservice.TargetStatus
		code      string
		retryable bool
		channelID string
	}{
		{input: syncservice.TargetResult{Status: syncservice.TargetSynced, Code: "raw", Retryable: true, ChannelID: "42"}, status: syncservice.TargetSynced, channelID: "42"},
		{input: syncservice.TargetResult{Status: syncservice.TargetIncompatible}, status: syncservice.TargetIncompatible, code: "incompatible_target"},
		{input: syncservice.TargetResult{Status: syncservice.TargetIncompatible, Code: "asset_disabled", Retryable: true}, status: syncservice.TargetIncompatible, code: "secret_unavailable"},
		{input: syncservice.TargetResult{Status: syncservice.TargetNeedsReconcile, ChannelID: "42"}, status: syncservice.TargetNeedsReconcile, code: "needs_reconcile", retryable: true, channelID: "42"},
		{input: syncservice.TargetResult{Status: syncservice.TargetFailed, Code: "secret_resolve_failed", Retryable: true}, status: syncservice.TargetFailed, code: "secret_unavailable"},
		{input: syncservice.TargetResult{Status: syncservice.TargetFailed, Code: "context_cancelled"}, status: syncservice.TargetFailed, code: "upstream_timeout", retryable: true},
		{input: syncservice.TargetResult{Status: syncservice.TargetFailed, Code: "incompatible_target", Retryable: true}, status: syncservice.TargetFailed, code: "incompatible_target"},
		{input: syncservice.TargetResult{Status: syncservice.TargetFailed, Code: "private-upstream-code"}, status: syncservice.TargetFailed, code: "upstream_failure", retryable: true},
		{input: syncservice.TargetResult{Status: "unexpected", Code: testSecret, ChannelID: "private"}, status: syncservice.TargetFailed, code: "upstream_failure", retryable: true},
	}
	for _, test := range tests {
		got := normalizeTargetResult(test.input)
		if got.Status != test.status || got.Code != test.code || got.Retryable != test.retryable || got.ChannelID != test.channelID {
			t.Fatalf("normalizeTargetResult(%#v) = %#v", test.input, got)
		}
	}
}

func TestValidationBoundariesAndGeneratedRequestID(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "localhost", "api.example.com", "api.example.com."} {
		if err := validateHost(host); err != nil {
			t.Fatalf("validateHost(%q) = %v", host, err)
		}
	}
	for _, host := range []string{"", " bad", "bad host", "-bad.example", strings.Repeat("a", 254)} {
		if err := validateHost(host); err == nil {
			t.Fatalf("validateHost(%q) succeeded", host)
		}
	}
	if value, err := normalizeBaseURL("", true); err != nil || value != "" {
		t.Fatalf("empty optional base URL = %q, %v", value, err)
	}
	for _, value := range []string{"ftp://example.com", "https://user@example.com", "https://example.com?token=x", "relative/path"} {
		if _, err := normalizeBaseURL(value, false); err == nil {
			t.Fatalf("normalizeBaseURL(%q) succeeded", value)
		}
	}
	for _, duration := range []string{"", " 1s", "-1s", "not-duration"} {
		if _, err := parsePositiveDuration(duration); err == nil {
			t.Fatalf("parsePositiveDuration(%q) succeeded", duration)
		}
	}
	if err := validateText("line\nbreak", 100, true); err == nil {
		t.Fatal("control character was accepted")
	}
	if _, err := normalizeModels(make([]string, 257)); err == nil {
		t.Fatal("oversized model collection was accepted")
	}
	if _, err := normalizeModels([]string{"ok", "bad\nmodel"}); err == nil {
		t.Fatal("control character in model was accepted")
	}

	requestID := generateRequestID()
	if !validRequestID.MatchString(requestID) || !strings.HasPrefix(requestID, "req_") {
		t.Fatalf("generated request id = %q", requestID)
	}
	env := newTestEnvironment()
	deps := env.dependencies()
	deps.RequestIDGenerator = nil
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if !validRequestID.MatchString(recorder.Header().Get("X-Request-ID")) {
		t.Fatalf("header request id = %q", recorder.Header().Get("X-Request-ID"))
	}
}

func TestOptionalStringAndUnknownFailureCode(t *testing.T) {
	var optional optionalString
	if err := json.Unmarshal([]byte("null"), &optional); err != nil || !optional.set || !optional.null {
		t.Fatalf("null optional = %#v, %v", optional, err)
	}
	if err := json.Unmarshal([]byte("123"), &optional); err == nil {
		t.Fatal("numeric credential was accepted")
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("request_id", testRequestID)
	writeFailure(context, http.StatusTeapot, "private_code")
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "private_code") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestReconcileReportUpdatesRuntimeStates(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42"}
	env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{mapping}
	expectedModels := []string{"old-model"}
	actualModels := []string{"new-model"}
	env.reconciler.report = reconcile.Report{TargetID: "target-a", Mappings: []reconcile.MappingState{{
		Mapping: mapping,
		Status:  reconcile.StatusDrifted,
		Drift: map[string]reconcile.FieldDrift{
			"models":   {Expected: expectedModels, Actual: actualModels},
			"group":    {Expected: "old-group", Actual: "new-group"},
			"priority": {Expected: 1, Actual: 2},
			"weight":   {Expected: 100, Actual: 80},
		},
	}}}
	router := env.router(t)
	request(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")
	expectedModels[0] = "tampered-expected"
	actualModels[0] = "tampered-actual"
	recorder, envelope := request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	cell := matrixCellFromEnvelope(t, envelope)
	if cell["status"] != "drifted" {
		t.Fatalf("drifted cell=%#v", cell)
	}
	differences := differencesByField(t, cell)
	if len(differences) != 4 || differences["weight"]["expected"] != float64(100) || differences["weight"]["actual"] != float64(80) {
		t.Fatalf("differences=%#v", differences)
	}
	models := differences["models"]
	if models["expected"].([]any)[0] != "old-model" || models["actual"].([]any)[0] != "new-model" {
		t.Fatalf("model difference was not deep-copied: %#v", models)
	}

	env.reconciler.checkErr = errors.New("temporary reconcile failure")
	recorder, _ = request(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("failed reconcile status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	cell = matrixCellFromEnvelope(t, envelope)
	if cell["status"] != "drifted" || len(differencesByField(t, cell)) != 4 {
		t.Fatalf("failed reconcile overwrote last report: %#v", cell)
	}
	env.reconciler.checkErr = nil

	env.reconciler.report.Mappings[0].Status = reconcile.StatusRemoved
	env.reconciler.report.Mappings[0].Drift = nil
	request(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")
	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	cell = matrixCellFromEnvelope(t, envelope)
	if cell["status"] != "synced" {
		t.Fatalf("removed report did not clear runtime state: %#v", cell)
	}
	if _, exists := cell["differences"]; exists {
		t.Fatalf("removed cell retained differences: %#v", cell)
	}
}

func TestHTTPReconcileRetainsPendingUntilCapturedChannelDisappears(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0",
		TargetID:        "target-a",
		TargetChannelID: "42",
	}
	env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{mapping}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
	env.mappings.updateErr = errors.New("mapping persistence failed")
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.channels = []platform.Channel{{
		ID: "42", Name: "live", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true,
	}}
	env.reconciler.checkFn = func(ctx context.Context, targetID string, target platform.TargetAdapter) (reconcile.Report, error) {
		if _, err := target.ListChannels(ctx); err != nil {
			return reconcile.Report{TargetID: targetID}, err
		}
		return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{}}, nil
	}
	router := env.router(t)

	updateBody := `{"name":"live","base_url":"","models":["gpt-4.1"],"group":"default","priority":0,"weight":100,"enabled":true}`
	recorder, envelope := request(t, router, http.MethodPut, "/api/v1/targets/target-a/channels/42", updateBody, "application/json")
	if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" {
		t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	env.mappings.updateErr = nil

	before := target.listCalls
	recorder, _ = request(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("present-channel reconcile status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if target.listCalls != before+1 {
		t.Fatalf("present-channel ListChannels calls=%d, want %d", target.listCalls, before+1)
	}
	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	if cell := matrixCellFromEnvelope(t, envelope); cell["status"] != "needs_reconcile" || cell["channel_id"] != "42" {
		t.Fatalf("live provisional channel was cleared: %#v", cell)
	}

	target.channels = nil
	before = target.listCalls
	recorder, _ = request(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("missing-channel reconcile status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if target.listCalls != before+1 {
		t.Fatalf("missing-channel ListChannels calls=%d, want %d", target.listCalls, before+1)
	}
	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	if cell := matrixCellFromEnvelope(t, envelope); cell["status"] != "synced" {
		t.Fatalf("missing provisional channel was retained: %#v", cell)
	}
}

func TestSuccessfulSyncAndAcceptDriftClearDifferences(t *testing.T) {
	for _, operation := range []string{"sync", "accept"} {
		t.Run(operation, func(t *testing.T) {
			env := newTestEnvironment()
			mapping := platform.SyncMapping{UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42"}
			env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{mapping}
			env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
			env.reconciler.report = reconcile.Report{TargetID: "target-a", Mappings: []reconcile.MappingState{{
				Mapping: mapping, Status: reconcile.StatusDrifted,
				Drift: map[string]reconcile.FieldDrift{"weight": {Expected: 100, Actual: 80}},
			}}}
			target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
			target.channels = []platform.Channel{{ID: "42", Name: "live", Models: []string{"gpt-4.1"}, Group: "default", Weight: 80, Enabled: true}}
			router := env.router(t)
			request(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", "", "")

			if operation == "sync" {
				body := staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", 100)
				recorder, _ := request(t, router, http.MethodPost, "/api/v1/sync", body, "application/json")
				if recorder.Code != http.StatusOK {
					t.Fatalf("sync status=%d body=%s", recorder.Code, recorder.Body.String())
				}
			} else {
				body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`
				recorder, _ := request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/accept", body, "application/json")
				if recorder.Code != http.StatusOK {
					t.Fatalf("accept status=%d body=%s", recorder.Code, recorder.Body.String())
				}
			}

			_, envelope := request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
			cell := matrixCellFromEnvelope(t, envelope)
			if cell["status"] != "synced" {
				t.Fatalf("cell=%#v", cell)
			}
			if _, exists := cell["differences"]; exists {
				t.Fatalf("successful %s retained differences: %#v", operation, cell)
			}
		})
	}
}

func TestZeroWeightIsAcceptedAndForwarded(t *testing.T) {
	env := newTestEnvironment()
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.updateOut = platform.Channel{ID: "42", Name: "zero", Models: []string{"gpt-4.1"}, Group: "default", Weight: 0, Enabled: true}
	router := env.router(t)
	updateBody := `{"name":"zero","base_url":"","models":["gpt-4.1"],"group":"default","priority":0,"weight":0,"enabled":true}`
	recorder, _ := request(t, router, http.MethodPut, "/api/v1/targets/target-a/channels/42", updateBody, "application/json")
	if recorder.Code != http.StatusOK || target.updated.Weight != 0 {
		t.Fatalf("update status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), target.updated)
	}

	syncBody := staticSyncBody("u-1", "source-a", "source-a:channel:7:key:0", "target-a", 0)
	recorder, _ = request(t, router, http.MethodPost, "/api/v1/sync", syncBody, "application/json")
	if recorder.Code != http.StatusOK || env.syncer.multiRequest.Units[0].Settings.Weight != 0 {
		t.Fatalf("sync status=%d body=%s request=%#v", recorder.Code, recorder.Body.String(), env.syncer.multiRequest.Units)
	}
}

func matrixCellFromEnvelope(t *testing.T, envelope map[string]any) map[string]any {
	t.Helper()
	return dataObject(t, envelope)["rows"].([]any)[0].(map[string]any)["cells"].([]any)[0].(map[string]any)
}

func differencesByField(t *testing.T, cell map[string]any) map[string]map[string]any {
	t.Helper()
	items, ok := cell["differences"].([]any)
	if !ok {
		t.Fatalf("differences=%#v", cell["differences"])
	}
	result := make(map[string]map[string]any, len(items))
	for _, item := range items {
		difference := item.(map[string]any)
		result[difference["field"].(string)] = difference
	}
	return result
}

func TestMatrixWithoutSnapshotAndResolverFailure(t *testing.T) {
	t.Run("without snapshot", func(t *testing.T) {
		env := newTestEnvironment()
		env.fakeDisc.snapshots = map[string]discovery.Snapshot{}
		recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		data := dataObject(t, envelope)
		if data["refreshed"] != false || len(data["rows"].([]any)) != 0 {
			t.Fatalf("matrix=%#v", data)
		}
	})

	t.Run("resolver failure", func(t *testing.T) {
		env := newTestEnvironment()
		resolved := env.resolver.targets["target-a"]
		resolved.err = errors.New("resolver failure")
		env.resolver.targets["target-a"] = resolved
		recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, envelope) != "internal_error" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestAppAcceptsMaximumDocumentedRuntimeValues(t *testing.T) {
	env := newTestEnvironment()
	body := `{"host":"0.0.0.0","port":65535,"reconcile_interval":"24h","request_timeout":"1m","sync_concurrency":1024}`
	recorder, envelope := request(t, env.router(t), http.MethodPut, "/api/v1/config/app", body, "application/json")
	if recorder.Code != http.StatusOK || dataObject(t, envelope)["sync_concurrency"] != float64(1024) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if time.Duration(env.store.cfg.App.ReconcileInterval) != 24*time.Hour {
		t.Fatalf("app=%#v", env.store.cfg.App)
	}
}
