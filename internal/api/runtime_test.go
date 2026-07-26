package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
)

type runtimeTestTarget struct {
	channels []platform.Channel
	calls    atomic.Int64
}

func (t *runtimeTestTarget) ListChannels(context.Context) ([]platform.Channel, error) {
	t.calls.Add(1)
	channels := make([]platform.Channel, len(t.channels))
	for i, channel := range t.channels {
		channel.Models = append([]string(nil), channel.Models...)
		channels[i] = channel
	}
	return channels, nil
}

func (*runtimeTestTarget) CreateChannel(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("unexpected create")
}

func (*runtimeTestTarget) UpdateChannel(context.Context, string, platform.UpdateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("unexpected update")
}

func (*runtimeTestTarget) DeleteChannel(context.Context, string) error {
	return errors.New("unexpected delete")
}

type runtimeTestReconciler struct {
	calls atomic.Int64
}

func (r *runtimeTestReconciler) Check(ctx context.Context, targetID string, target platform.TargetAdapter) (reconcile.Report, error) {
	r.calls.Add(1)
	if _, err := target.ListChannels(ctx); err != nil {
		return reconcile.Report{TargetID: targetID}, err
	}
	return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{}}, nil
}

func (*runtimeTestReconciler) AcceptDrift(context.Context, platform.SyncMapping, platform.Channel) error {
	return errors.New("unexpected accept")
}

func TestRuntimeCheckAndRecordIsSharedWithRouterAndConcurrent(t *testing.T) {
	env := newTestEnvironment()
	mapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7:key:0",
		TargetID:        "target-a",
		TargetChannelID: "42",
	}
	env.store.cfg.Upstreams[0].SyncMappings = []config.SyncMapping{mapping}
	env.mappings.byTarget["target-a"] = []platform.SyncMapping{mapping}
	env.mappings.updateErr = errors.New("mapping persistence failed")
	runtimeState := NewRuntime()
	router, err := NewRouterWithRuntime(env.dependencies(), runtimeState)
	if err != nil {
		t.Fatalf("NewRouterWithRuntime() error = %v", err)
	}

	updateBody := `{"name":"live","base_url":"","models":["gpt-4.1"],"group":"default","priority":0,"weight":100,"enabled":true}`
	recorder, envelope := request(t, router, http.MethodPut, "/api/v1/targets/target-a/channels/42", updateBody, "application/json")
	if recorder.Code != http.StatusConflict || errorCode(t, envelope) != "needs_reconcile" {
		t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	reconciler := &runtimeTestReconciler{}
	target := &runtimeTestTarget{channels: []platform.Channel{{
		ID: "42", Name: "live", Models: []string{"gpt-4.1"}, Group: "default", Weight: 100, Enabled: true,
	}}}
	const checks = 32
	errCh := make(chan error, checks*2)
	var wait sync.WaitGroup
	for range checks {
		wait.Add(2)
		go func() {
			defer wait.Done()
			report, checkErr := runtimeState.CheckAndRecord(context.Background(), reconciler, "target-a", target)
			if checkErr != nil {
				errCh <- fmt.Errorf("CheckAndRecord() error = %w", checkErr)
				return
			}
			if report.TargetID != "target-a" {
				errCh <- fmt.Errorf("CheckAndRecord() target = %q", report.TargetID)
			}
		}()
		go func() {
			defer wait.Done()
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/matrix?upstream_id=source-a", nil))
			if recorder.Code != http.StatusOK {
				errCh <- fmt.Errorf("matrix status = %d", recorder.Code)
			}
		}()
	}
	wait.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := reconciler.calls.Load(); got != checks {
		t.Fatalf("Check() calls = %d, want %d", got, checks)
	}
	if got := target.calls.Load(); got != checks {
		t.Fatalf("ListChannels() calls = %d, want %d", got, checks)
	}
	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	if cell := matrixCellFromEnvelope(t, envelope); cell["status"] != "needs_reconcile" || cell["channel_id"] != "42" {
		t.Fatalf("shared runtime did not retain live provisional channel: %#v", cell)
	}

	missingTarget := &runtimeTestTarget{}
	if _, err := runtimeState.CheckAndRecord(context.Background(), reconciler, "target-a", missingTarget); err != nil {
		t.Fatalf("missing-channel CheckAndRecord() error = %v", err)
	}
	if got := missingTarget.calls.Load(); got != 1 {
		t.Fatalf("missing-channel ListChannels() calls = %d, want 1", got)
	}
	_, envelope = request(t, router, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", "", "")
	if cell := matrixCellFromEnvelope(t, envelope); cell["status"] != "synced" {
		t.Fatalf("shared runtime retained missing provisional channel: %#v", cell)
	}
}

func TestNewRouterWithRuntimeDefaultsNilState(t *testing.T) {
	env := newTestEnvironment()
	router, err := NewRouterWithRuntime(env.dependencies(), nil)
	if err != nil {
		t.Fatalf("NewRouterWithRuntime(nil) error = %v", err)
	}
	recorder, _ := request(t, router, http.MethodGet, "/api/v1/health", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d", recorder.Code)
	}
}
