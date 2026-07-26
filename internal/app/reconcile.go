package app

import (
	"context"
	"errors"
	"time"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

// ReconcileOnce checks exactly the target set present in one config snapshot.
// Each result intentionally drops the original error and platform response.
func (a *Application) ReconcileOnce(ctx context.Context) []ReconcileResult {
	if a == nil || ctx == nil || a.closed.Load() || isNilDependency(a.deps.Config) ||
		isNilDependency(a.deps.Adapters) || isNilDependency(a.deps.Reconcile) || a.runtime == nil {
		return nil
	}
	targets := a.deps.Config.Snapshot().Targets
	results := make([]ReconcileResult, 0, len(targets))
	for _, targetConfig := range targets {
		result := ReconcileResult{TargetID: targetConfig.ID, Code: ReconcileUpstreamFailure}
		target, _, err := a.deps.Adapters.ResolveTarget(ctx, targetConfig)
		if err == nil {
			_, err = a.runtime.CheckAndRecord(ctx, a.deps.Reconcile, targetConfig.ID, target)
		}
		result.Code = classifyReconcileError(err)
		results = append(results, result)
	}
	return results
}

// RunReconcile runs synchronously. Callers own the goroutine and cancellation.
// Waiting starts only after each complete pass, so passes cannot overlap.
func (a *Application) RunReconcile(ctx context.Context) error {
	if ctx == nil {
		return ErrContextRequired
	}
	if a == nil || a.closed.Load() || isNilDependency(a.deps.Config) ||
		isNilDependency(a.deps.Adapters) || isNilDependency(a.deps.Reconcile) || a.runtime == nil {
		if a != nil && a.closed.Load() {
			return ErrApplicationClosed
		}
		return ErrDependenciesIncomplete
	}
	if !a.running.CompareAndSwap(false, true) {
		return ErrReconcileRunnerActive
	}
	defer a.running.Store(false)

	for {
		a.ReconcileOnce(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		interval := time.Duration(a.deps.Config.Snapshot().App.ReconcileInterval)
		if interval <= 0 {
			return ErrInvalidReconcileInterval
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func classifyReconcileError(err error) ReconcileCode {
	switch {
	case err == nil:
		return ReconcileOK
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, api.ErrUpstreamTimeout):
		return ReconcileUpstreamTimeout
	case errors.Is(err, platform.ErrIncompatibleTarget), errors.Is(err, api.ErrIncompatibleTarget):
		return ReconcileIncompatibleTarget
	default:
		return ReconcileUpstreamFailure
	}
}
