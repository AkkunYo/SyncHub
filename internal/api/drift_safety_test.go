package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

func TestAcceptDriftVerifiesLiveStateBeforeClearingDrift(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0",
		TargetID:        "target-a",
		TargetChannelID: "42",
		Snapshot:        platform.ChannelSnapshot{Models: []string{"gpt-4.1"}, Group: "default", Weight: 100},
	}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	acceptedChannel := platform.Channel{ID: "42", Name: "live", Models: []string{"gpt-4.1"}, Group: "default", Weight: 80, Enabled: true}
	target.channels = []platform.Channel{acceptedChannel}
	acceptedMapping := mapping
	acceptedMapping.Snapshot = platform.SnapshotFromChannel(acceptedChannel)
	env.reconciler.checkFn = func(ctx context.Context, targetID string, checked platform.TargetAdapter) (reconcile.Report, error) {
		changed := acceptedChannel
		changed.Weight = 70
		target.channels = []platform.Channel{changed}
		channels, err := checked.ListChannels(ctx)
		if err != nil {
			t.Fatalf("verification ListChannels() error = %v", err)
		}
		if targetID != "target-a" || len(channels) != 1 || channels[0].Weight != 70 {
			t.Fatalf("verification target=%q channels=%#v", targetID, channels)
		}
		return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{{
			Mapping: acceptedMapping,
			Status:  reconcile.StatusDrifted,
			Drift:   map[string]reconcile.FieldDrift{"weight": {Expected: 80, Actual: 70}},
		}}}, nil
	}
	runtimeState := NewRuntime()
	runtimeState.recordReconcileReport(reconcile.Report{TargetID: "target-a", Mappings: []reconcile.MappingState{{
		Mapping: mapping,
		Status:  reconcile.StatusDrifted,
		Drift:   map[string]reconcile.FieldDrift{"weight": {Expected: 100, Actual: 80}},
	}}}, []platform.Channel{acceptedChannel}, true)
	router, err := NewRouterWithRuntime(env.dependencies(), runtimeState)
	if err != nil {
		t.Fatalf("NewRouterWithRuntime() error = %v", err)
	}

	body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/accept", body, "application/json")
	if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if env.reconciler.acceptCalls != 1 || env.reconciler.checkCalls != 1 || target.listCalls != 2 {
		t.Fatalf("accept=%d check=%d list=%d", env.reconciler.acceptCalls, env.reconciler.checkCalls, target.listCalls)
	}
	pending, needsReconcile, differences := runtimeState.matrixState(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: mapping.TargetID})
	if !needsReconcile || pending.channelID != "42" || len(differences) != 1 || differences[0].Field != "weight" ||
		differences[0].Expected != 80 || differences[0].Actual != 70 {
		t.Fatalf("runtime pending=%#v needs=%v differences=%#v", pending, needsReconcile, differences)
	}
}

func TestAcceptDriftMarksNeedsReconcileWhenVerificationOmitsMapping(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0", TargetID: "target-a", TargetChannelID: "42",
		Snapshot: platform.ChannelSnapshot{Models: []string{"gpt-4.1"}, Group: "default", Weight: 100},
	}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
	target.channels = []platform.Channel{{ID: "42", Name: "live", Models: []string{"gpt-4.1"}, Group: "default", Weight: 80, Enabled: true}}
	env.reconciler.checkFn = func(ctx context.Context, targetID string, checked platform.TargetAdapter) (reconcile.Report, error) {
		if _, err := checked.ListChannels(ctx); err != nil {
			return reconcile.Report{TargetID: targetID}, err
		}
		return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{}}, nil
	}
	runtimeState := NewRuntime()
	router, err := NewRouterWithRuntime(env.dependencies(), runtimeState)
	if err != nil {
		t.Fatalf("NewRouterWithRuntime() error = %v", err)
	}

	body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`
	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/accept", body, "application/json")
	if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	pending, needsReconcile, differences := runtimeState.matrixState(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: mapping.TargetID})
	if !needsReconcile || pending.channelID != "42" || len(differences) != 0 {
		t.Fatalf("runtime pending=%#v needs=%v differences=%#v", pending, needsReconcile, differences)
	}
}

func TestDriftMutationsShareSyncTupleLockAcrossReadWriteVerify(t *testing.T) {
	for _, operation := range []string{"accept", "restore"} {
		t.Run(operation, func(t *testing.T) {
			env := newTestEnvironment()
			mapping := platform.SyncMapping{
				UpstreamAssetID: "source-a:channel:7:key:0",
				TargetID:        "target-a",
				TargetChannelID: "42",
				Snapshot:        platform.ChannelSnapshot{Models: []string{"gpt-4.1"}, Group: "default", Weight: 100},
			}
			env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
			target := env.resolver.targets["target-a"].adapter.(*fakeTarget)
			current := platform.Channel{ID: "42", Name: "live", Models: []string{"gpt-4.1"}, Group: "default", Weight: 80, Enabled: true}
			target.channels = []platform.Channel{current}
			if operation == "restore" {
				restored := current
				restored.Weight = 100
				target.updateFn = func(_ context.Context, _ string, _ platform.UpdateChannelInput) (platform.Channel, error) {
					target.channels = []platform.Channel{restored}
					return restored, nil
				}
			}

			mappingReadEntered := make(chan struct{})
			mappingReadRelease := make(chan struct{})
			verificationEntered := make(chan struct{})
			verificationRelease := make(chan struct{})
			var releaseReadOnce sync.Once
			var releaseVerificationOnce sync.Once
			releaseRead := func() { releaseReadOnce.Do(func() { close(mappingReadRelease) }) }
			releaseVerification := func() { releaseVerificationOnce.Do(func() { close(verificationRelease) }) }
			t.Cleanup(releaseRead)
			t.Cleanup(releaseVerification)

			env.reconciler.checkFn = func(ctx context.Context, targetID string, checked platform.TargetAdapter) (reconcile.Report, error) {
				channels, err := checked.ListChannels(ctx)
				if err != nil {
					return reconcile.Report{TargetID: targetID}, err
				}
				close(verificationEntered)
				select {
				case <-verificationRelease:
				case <-ctx.Done():
					return reconcile.Report{TargetID: targetID}, ctx.Err()
				}
				verified := mapping
				if operation == "accept" {
					verified.Snapshot = platform.SnapshotFromChannel(channels[0])
				}
				return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{{Mapping: verified, Status: reconcile.StatusSynced}}}, nil
			}

			deps := env.dependencies()
			deps.Mappings = &gatedMappingRepository{
				MappingRepository: deps.Mappings,
				entered:           mappingReadEntered,
				release:           mappingReadRelease,
			}
			router, err := NewRouter(deps)
			if err != nil {
				t.Fatalf("NewRouter() error = %v", err)
			}

			syncEntered := make(chan struct{})
			env.syncer.multiFn = func(_ context.Context, _ string, _ int, request syncservice.MultiRequest) (syncservice.MultiResult, error) {
				close(syncEntered)
				unit := request.Units[0]
				return syncservice.MultiResult{Units: []syncservice.UnitResult{{
					UnitID: unit.UnitID, AssetID: unit.Asset.ID, TargetID: unit.Target.ID,
					Status: syncservice.TargetSynced, ChannelID: "42",
					EffectiveModels: []string{"gpt-4.1"}, ExcludedModels: []string{}, Warnings: []string{},
				}}}, nil
			}

			body := `{"upstream_asset_id":"source-a:channel:7:key:0","channel_id":"42"}`
			driftDone := serveAsync(router, http.MethodPost, "/api/v1/targets/target-a/drift/"+operation, body)
			waitForSignal(t, mappingReadEntered, "drift mapping read")
			syncDone := serveAsync(router, http.MethodPost, "/api/v1/sync", staticSyncBody("u-1", "source-a", mapping.UpstreamAssetID, "target-a", 100))
			assertNoSignal(t, syncEntered, "sync entered while drift mapping read was blocked")

			releaseRead()
			waitForSignal(t, verificationEntered, "drift verification")
			if operation == "accept" && env.reconciler.acceptCalls != 1 {
				t.Fatalf("AcceptDrift calls=%d", env.reconciler.acceptCalls)
			}
			if operation == "restore" && target.updateCalls != 1 {
				t.Fatalf("UpdateChannel calls=%d", target.updateCalls)
			}
			assertNoSignal(t, syncEntered, "sync entered before drift verification completed")

			releaseVerification()
			driftResponse := waitForResponse(t, driftDone, "drift response")
			if driftResponse.Code != http.StatusOK {
				t.Fatalf("drift status=%d body=%s", driftResponse.Code, driftResponse.Body.String())
			}
			waitForSignal(t, syncEntered, "sync after drift unlock")
			syncResponse := waitForResponse(t, syncDone, "sync response")
			if syncResponse.Code != http.StatusOK {
				t.Fatalf("sync status=%d body=%s", syncResponse.Code, syncResponse.Body.String())
			}
		})
	}
}

type gatedMappingRepository struct {
	MappingRepository
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *gatedMappingRepository) ListMappings(ctx context.Context, targetID string) ([]platform.SyncMapping, error) {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.MappingRepository.ListMappings(ctx, targetID)
}

func serveAsync(router http.Handler, method, path, body string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		done <- recorder
	}()
	return done
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func assertNoSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal(message)
	case <-time.After(100 * time.Millisecond):
	}
}

func waitForResponse(t *testing.T, done <-chan *httptest.ResponseRecorder, name string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-done:
		return response
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}
