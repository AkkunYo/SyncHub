package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/discovery"
	"github.com/AkkunYo/SyncHub/internal/platform"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

func TestGroupsReturnsLatestCompleteCatalogWithoutUnknownRatio(t *testing.T) {
	env := newTestEnvironment()
	env.fakeDisc.snapshots["source-a"] = discovery.Snapshot{
		SourceID: "source-a",
		Assets:   []platform.UpstreamAsset{},
		GroupCatalog: &platform.GroupCatalog{
			SourceID: "source-a",
			Groups: []platform.UpstreamGroup{
				{Name: "auto", Description: "自动调度", RatioKnown: false, Models: []string{"gpt-4o", "gpt-4o-mini"}, ModelsVerified: true, Auto: true},
				{Name: "vip", Description: "VIP", Ratio: 1.5, RatioKnown: true, Models: []string{"gpt-4o"}, ModelsVerified: true},
			},
		},
	}

	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/upstreams/source-a/groups", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	if data["upstream_id"] != "source-a" || data["refreshed"] != true {
		t.Fatalf("data=%#v", data)
	}
	groups := data["groups"].([]any)
	auto := groups[0].(map[string]any)
	if auto["ratio"] != nil || auto["ratio_known"] != false || auto["model_count"] != float64(2) || auto["auto"] != true {
		t.Fatalf("auto group=%#v", auto)
	}
	vip := groups[1].(map[string]any)
	if vip["ratio"] != 1.5 || vip["model_count"] != float64(1) || vip["models_verified"] != true {
		t.Fatalf("vip group=%#v", vip)
	}
}

func TestGroupsWithoutSnapshotReturnsEmptyUnrefreshedCatalog(t *testing.T) {
	env := newTestEnvironment()
	env.fakeDisc.snapshots = map[string]discovery.Snapshot{}
	recorder, envelope := request(t, env.router(t), http.MethodGet, "/api/v1/upstreams/source-a/groups", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	if data["refreshed"] != false || len(data["groups"].([]any)) != 0 {
		t.Fatalf("data=%#v", data)
	}
}

func TestSyncUnitsUsesSnapshotFactsPreservesOrderAndSeparatesGroups(t *testing.T) {
	env := newTestEnvironment()
	token := platform.UpstreamAsset{
		ID: "source-a:token:42", SourceID: "source-a", SourceType: "newapi", Provider: platform.ProviderOpenAI,
		RawType: "newapi-token", Kind: platform.AssetProxyKey, Name: "token", BaseURL: "https://source.example.com",
		Models: []string{"gpt-4o"}, Enabled: true, SecretReadable: true, Metadata: map[string]string{"upstream_group": "vip"},
	}
	snapshot := env.fakeDisc.snapshots["source-a"]
	snapshot.Assets = append(snapshot.Assets, token)
	snapshot.GroupCatalog = &platform.GroupCatalog{SourceID: "source-a", Groups: []platform.UpstreamGroup{{
		Name: "vip", Ratio: 1.5, RatioKnown: true, Models: []string{"gpt-4o"}, ModelsVerified: true,
	}}}
	env.fakeDisc.snapshots["source-a"] = snapshot
	targetBConfig := env.store.cfg.Targets[0]
	targetBConfig.ID, targetBConfig.Name = "target-b", "Target B"
	targetBConfig.BaseURL = "https://target-b.example.com"
	env.store.cfg.Targets = append(env.store.cfg.Targets, targetBConfig)
	env.resolver.targets["target-b"] = env.resolver.targets["target-a"]
	env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{
		{UnitID: "u-token", AssetID: token.ID, TargetID: "target-b", UpstreamGroup: "vip", Status: syncservice.TargetSynced, ChannelID: "42", EffectiveModels: []string{"gpt-4o"}, ExcludedModels: []string{}, Warnings: []string{}},
		{UnitID: "u-static", AssetID: snapshot.Assets[0].ID, TargetID: "target-a", Status: syncservice.TargetSynced, ChannelID: "43", EffectiveModels: []string{"gpt-4.1"}, ExcludedModels: []string{}, Warnings: []string{}},
	}}

	proof := "request-only-proof"
	body := `{"upstream_id":"source-a","units":[` +
		`{"unit_id":"u-token","asset_id":"source-a:token:42","target_id":"target-b","upstream_group":"vip","settings":{"models":["gpt-4o"],"target_group":"paid","priority":1,"weight":90}},` +
		`{"unit_id":"u-static","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}` +
		`],"grant":{"security_proof":"` + proof + `","allow_auth_file":false}}`
	recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), proof) || strings.Contains(recorder.Body.String(), "security_proof") {
		t.Fatalf("response leaked grant: %s", recorder.Body.String())
	}
	if env.syncer.calls != 1 || env.syncer.sourceID != "source-a" || env.syncer.concurrency != 4 || len(env.resolver.upstreamCalls) != 1 {
		t.Fatalf("sync=%#v upstream calls=%v", env.syncer, env.resolver.upstreamCalls)
	}
	if len(env.syncer.multiRequest.Units) != 2 {
		t.Fatalf("units=%#v", env.syncer.multiRequest.Units)
	}
	first := env.syncer.multiRequest.Units[0]
	if first.Asset.ID != token.ID || first.Target.ID != "target-b" || first.UpstreamGroup == nil || first.UpstreamGroup.Name != "vip" || first.Settings.Group != "paid" {
		t.Fatalf("first unit=%#v", first)
	}
	if env.syncer.multiRequest.Units[1].UpstreamGroup != nil || env.syncer.multiRequest.Units[1].Settings.Group != "default" {
		t.Fatalf("second unit=%#v", env.syncer.multiRequest.Units[1])
	}
	units := dataObject(t, envelope)["units"].([]any)
	if units[0].(map[string]any)["unit_id"] != "u-token" || units[1].(map[string]any)["unit_id"] != "u-static" {
		t.Fatalf("response order=%#v", units)
	}
}

func TestSyncUnitsRejectsStructuralAndGroupCatalogErrorsBeforeSideEffects(t *testing.T) {
	validUnit := `{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}`
	tests := []struct {
		name string
		body string
		code string
	}{
		{name: "empty units", body: `{"upstream_id":"source-a","units":[]}`, code: "invalid_request"},
		{name: "duplicate unit id", body: `{"upstream_id":"source-a","units":[` + validUnit + `,{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-b","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}]}`, code: "invalid_request"},
		{name: "duplicate tuple", body: `{"upstream_id":"source-a","units":[` + validUnit + `,{"unit_id":"u-2","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","priority":0,"weight":100}}]}`, code: "invalid_request"},
		{name: "legacy body", body: `{"upstream_id":"source-a","asset_id":"source-a:channel:7:key:0","target_ids":["target-a"]}`, code: "invalid_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnvironment()
			recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", test.body, "application/json")
			if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != test.code {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if env.syncer.calls != 0 || len(env.resolver.upstreamCalls) != 0 {
				t.Fatalf("side effects: sync=%d upstream=%v", env.syncer.calls, env.resolver.upstreamCalls)
			}
		})
	}

	t.Run("more than one thousand units", func(t *testing.T) {
		units := make([]string, 1001)
		for i := range units {
			units[i] = `{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","weight":100}}`
		}
		env := newTestEnvironment()
		body := `{"upstream_id":"source-a","units":[` + strings.Join(units, ",") + `]}`
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
		if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "invalid_request" || env.syncer.calls != 0 {
			t.Fatalf("status=%d calls=%d body=%s", recorder.Code, env.syncer.calls, recorder.Body.String())
		}
	})
}

func TestSyncUnitsReportsGroupRequiredUnknownAndMismatchByContract(t *testing.T) {
	newTokenEnvironment := func() *testEnvironment {
		env := newTestEnvironment()
		token := platform.UpstreamAsset{
			ID: "source-a:token:42", SourceID: "source-a", SourceType: "newapi", Provider: platform.ProviderOpenAI,
			RawType: "newapi-token", Kind: platform.AssetProxyKey, Name: "token", Models: []string{"gpt-4o"}, Enabled: true,
			Metadata: map[string]string{"upstream_group": "vip"},
		}
		env.fakeDisc.snapshots["source-a"] = discovery.Snapshot{
			SourceID: "source-a", Assets: []platform.UpstreamAsset{token},
			GroupCatalog: &platform.GroupCatalog{SourceID: "source-a", Groups: []platform.UpstreamGroup{{Name: "vip", Models: []string{"gpt-4o"}, ModelsVerified: true}, {Name: "default", Models: []string{"gpt-4o"}, ModelsVerified: true}}},
		}
		return env
	}
	t.Run("required", func(t *testing.T) {
		env := newTokenEnvironment()
		body := `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:token:42","target_id":"target-a","settings":{"models":["gpt-4o"],"target_group":"default","weight":100}}]}`
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
		if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "group_required" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("unknown", func(t *testing.T) {
		env := newTokenEnvironment()
		body := `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:token:42","target_id":"target-a","upstream_group":"missing","settings":{"models":["gpt-4o"],"target_group":"default","weight":100}}]}`
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
		if recorder.Code != http.StatusBadRequest || errorCode(t, envelope) != "group_unknown" {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("mismatch is a unit result", func(t *testing.T) {
		env := newTokenEnvironment()
		env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{{
			UnitID: "u-1", AssetID: "source-a:token:42", TargetID: "target-a", UpstreamGroup: "default",
			Status: syncservice.TargetFailed, Code: "group_mismatch", EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{},
		}}}
		body := `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:token:42","target_id":"target-a","upstream_group":"default","settings":{"models":["gpt-4o"],"target_group":"default","weight":100}}]}`
		recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		unit := dataObject(t, envelope)["units"].([]any)[0].(map[string]any)
		if unit["code"] != "group_mismatch" || unit["status"] != "failed" {
			t.Fatalf("unit=%#v", unit)
		}
	})
}

func TestSyncUnitsKeepsRateLimitAndReconcileDetailsInHTTP200(t *testing.T) {
	env := newTestEnvironment()
	env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{
		{UnitID: "u-1", AssetID: "source-a:channel:7:key:0", TargetID: "target-a", Status: syncservice.TargetFailed, Code: "rate_limited", Retryable: true, RetryAfterSeconds: 300, EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{}},
	}}
	env.syncer.err = errors.New("upstream detail " + testSecret)
	body := `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","weight":100}}]}`
	recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/sync", body, "application/json")
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), testSecret) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	unit := dataObject(t, envelope)["units"].([]any)[0].(map[string]any)
	if unit["code"] != "rate_limited" || unit["retryable"] != true || unit["retry_after_seconds"] != float64(300) {
		t.Fatalf("unit=%#v", unit)
	}
}
