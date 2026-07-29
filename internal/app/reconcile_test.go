package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
)

func TestReconcileOnceUsesCompleteTargetSnapshotAndIsolatesSafeFailures(t *testing.T) {
	t.Parallel()

	const secretCanary = "test-upstream-response-secret"
	cfg := config.Default()
	for _, id := range []string{"target-failed", "target-ok", "target-incompatible", "target-timeout"} {
		cfg.Targets = append(cfg.Targets, config.TargetConfig{ID: id, Type: "newapi"})
	}
	store := &memoryConfigStore{cfg: cfg}
	okTarget := &staticTarget{}
	failedTarget := &staticTarget{listErr: fmt.Errorf("remote body contained %s", secretCanary)}
	resolver := &fakeResolver{resolveTarget: func(_ context.Context, target config.TargetConfig) (platform.TargetAdapter, platform.TargetCapabilities, error) {
		switch target.ID {
		case "target-failed":
			return failedTarget, platform.TargetCapabilities{}, nil
		case "target-ok":
			return okTarget, platform.TargetCapabilities{}, nil
		case "target-incompatible":
			return nil, platform.TargetCapabilities{}, platform.ErrIncompatibleTarget
		case "target-timeout":
			return nil, platform.TargetCapabilities{}, context.DeadlineExceeded
		default:
			return nil, platform.TargetCapabilities{}, errors.New("unexpected target")
		}
	}}
	check := &fakeReconcileService{}
	runtimeState := api.NewRuntime()
	application := &Application{
		deps:    api.Dependencies{Config: store, Adapters: resolver, Reconcile: check},
		runtime: runtimeState,
	}

	results := application.ReconcileOnce(context.Background())
	want := []ReconcileResult{
		{TargetID: "target-failed", Code: ReconcileUpstreamFailure},
		{TargetID: "target-ok", Code: ReconcileOK},
		{TargetID: "target-incompatible", Code: ReconcileIncompatibleTarget},
		{TargetID: "target-timeout", Code: ReconcileUpstreamTimeout},
	}
	if len(results) != len(want) {
		t.Fatalf("results = %#v, want %#v", results, want)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("results[%d] = %#v, want %#v", i, results[i], want[i])
		}
	}
	if got := resolver.targetCalls.Load(); got != int32(len(cfg.Targets)) {
		t.Fatalf("ResolveTarget calls = %d, want %d", got, len(cfg.Targets))
	}
	if got := check.calls.Load(); got != 2 {
		t.Fatalf("reconcile checks = %d, want 2", got)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("json.Marshal(results) error = %v", err)
	}
	if containsBytes(encoded, []byte(secretCanary)) {
		t.Fatalf("reconcile results leaked upstream response: %s", encoded)
	}
}

func TestRunReconcileStartsImmediatelyDoesNotOverlapAndCancelsPromptly(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.App.ReconcileInterval = config.Duration(5 * time.Millisecond)
	cfg.Targets = []config.TargetConfig{{ID: "target-a", Type: "newapi"}}
	store := &memoryConfigStore{cfg: cfg}
	target := &staticTarget{}
	resolver := &fakeResolver{resolveTarget: func(context.Context, config.TargetConfig) (platform.TargetAdapter, platform.TargetCapabilities, error) {
		return target, platform.TargetCapabilities{}, nil
	}}
	check := newSlowReconcileService(25 * time.Millisecond)
	application := &Application{
		deps:    api.Dependencies{Config: store, Adapters: resolver, Reconcile: check},
		runtime: api.NewRuntime(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	startedAt := time.Now()
	go func() { done <- application.RunReconcile(ctx) }()

	select {
	case <-check.started:
		if elapsed := time.Since(startedAt); elapsed > 200*time.Millisecond {
			t.Fatalf("first reconcile did not start promptly: %s", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first reconcile did not start immediately")
	}
	if err := application.RunReconcile(context.Background()); !errors.Is(err, ErrReconcileRunnerActive) {
		t.Fatalf("second runner error = %v, want ErrReconcileRunnerActive", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-check.started:
		case <-time.After(time.Second):
			t.Fatal("periodic reconcile did not run again")
		}
	}
	if maximum := check.maximum.Load(); maximum != 1 {
		t.Fatalf("maximum overlapping checks = %d, want 1", maximum)
	}

	cancelledAt := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunReconcile() error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(cancelledAt); elapsed > 500*time.Millisecond {
			t.Fatalf("cancellation took %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("RunReconcile() did not exit after cancellation")
	}
}

func TestRunReconcileValidatesBoundaries(t *testing.T) {
	t.Parallel()

	var nilApplication *Application
	if results := nilApplication.ReconcileOnce(context.Background()); len(results) != 0 {
		t.Fatalf("nil ReconcileOnce() = %#v, want empty", results)
	}
	if err := nilApplication.RunReconcile(context.Background()); !errors.Is(err, ErrDependenciesIncomplete) {
		t.Fatalf("nil RunReconcile() error = %v, want ErrDependenciesIncomplete", err)
	}

	cfg := config.Default()
	cfg.App.ReconcileInterval = 0
	cfg.Targets = []config.TargetConfig{{ID: "target-a", Type: "newapi"}}
	check := &fakeReconcileService{}
	application := &Application{
		deps: api.Dependencies{
			Config: &memoryConfigStore{cfg: cfg},
			Adapters: &fakeResolver{resolveTarget: func(context.Context, config.TargetConfig) (platform.TargetAdapter, platform.TargetCapabilities, error) {
				return &staticTarget{}, platform.TargetCapabilities{}, nil
			}},
			Reconcile: check,
		},
		runtime: api.NewRuntime(),
	}
	if err := application.RunReconcile(nil); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("RunReconcile(nil) error = %v, want ErrContextRequired", err)
	}
	if err := application.RunReconcile(context.Background()); !errors.Is(err, ErrInvalidReconcileInterval) {
		t.Fatalf("non-positive interval error = %v, want ErrInvalidReconcileInterval", err)
	}
	if check.calls.Load() != 1 {
		t.Fatalf("startup reconcile calls = %d, want 1", check.calls.Load())
	}
}

type fakeResolver struct {
	resolveTarget   func(context.Context, config.TargetConfig) (platform.TargetAdapter, platform.TargetCapabilities, error)
	targetCalls     atomic.Int32
	upstreamCalls   atomic.Int32
	resolveUpstream func(context.Context, config.UpstreamConfig) (platform.UpstreamAdapter, error)
}

func (r *fakeResolver) ResolveTarget(ctx context.Context, cfg config.TargetConfig) (platform.TargetAdapter, platform.TargetCapabilities, error) {
	r.targetCalls.Add(1)
	return r.resolveTarget(ctx, cfg)
}

func (r *fakeResolver) ResolveUpstream(ctx context.Context, cfg config.UpstreamConfig) (platform.UpstreamAdapter, error) {
	r.upstreamCalls.Add(1)
	if r.resolveUpstream == nil {
		return nil, errors.New("upstream resolver not configured")
	}
	return r.resolveUpstream(ctx, cfg)
}

func (r *fakeResolver) DiscoveryModeStatus(config.UpstreamConfig) platform.DiscoveryModeStatus {
	return platform.DiscoveryModeStatus{EffectiveMode: "unresolved", Status: "unresolved"}
}

type staticTarget struct {
	channels []platform.Channel
	listErr  error
}

func (t *staticTarget) ListChannels(context.Context) ([]platform.Channel, error) {
	return append([]platform.Channel(nil), t.channels...), t.listErr
}

func (t *staticTarget) CreateChannel(context.Context, platform.CreateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("not implemented in test")
}

func (t *staticTarget) UpdateChannel(context.Context, string, platform.UpdateChannelInput) (platform.Channel, error) {
	return platform.Channel{}, errors.New("not implemented in test")
}

func (t *staticTarget) DeleteChannel(context.Context, string) error {
	return errors.New("not implemented in test")
}

type fakeReconcileService struct {
	calls atomic.Int32
}

func (s *fakeReconcileService) Check(ctx context.Context, targetID string, target platform.TargetAdapter) (reconcile.Report, error) {
	s.calls.Add(1)
	_, err := target.ListChannels(ctx)
	return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{}}, err
}

func (s *fakeReconcileService) AcceptDrift(context.Context, platform.SyncMapping, platform.Channel) error {
	return nil
}

type slowReconcileService struct {
	delay   time.Duration
	started chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
	calls   atomic.Int32
	mu      sync.Mutex
}

func newSlowReconcileService(delay time.Duration) *slowReconcileService {
	return &slowReconcileService{delay: delay, started: make(chan struct{}, 16)}
}

func (s *slowReconcileService) Check(ctx context.Context, targetID string, target platform.TargetAdapter) (reconcile.Report, error) {
	s.calls.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maximum.Load()
		if active <= maximum || s.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	s.started <- struct{}{}
	select {
	case <-ctx.Done():
		return reconcile.Report{TargetID: targetID}, ctx.Err()
	case <-time.After(s.delay):
	}
	_, err := target.ListChannels(ctx)
	return reconcile.Report{TargetID: targetID, Mappings: []reconcile.MappingState{}}, err
}

func (s *slowReconcileService) AcceptDrift(context.Context, platform.SyncMapping, platform.Channel) error {
	return nil
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		matched := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
