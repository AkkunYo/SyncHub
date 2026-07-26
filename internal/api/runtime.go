package api

import (
	"context"
	"errors"
	"sync"

	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
)

// Runtime stores transient reconciliation state shared by the management API
// and background reconciliation workers.
type Runtime struct {
	mu             sync.RWMutex
	drifted        map[runtimeKey][]matrixDifference
	needsReconcile map[runtimeKey]pendingReconcile
}

// NewRuntime creates an initialized reconciliation runtime.
func NewRuntime() *Runtime {
	return &Runtime{
		drifted:        make(map[runtimeKey][]matrixDifference),
		needsReconcile: make(map[runtimeKey]pendingReconcile),
	}
}

// CheckAndRecord executes one reconciliation check and atomically records its
// report using the same live channel snapshot consumed by that check.
func (r *Runtime) CheckAndRecord(
	ctx context.Context,
	service ReconcileService,
	targetID string,
	target platform.TargetAdapter,
) (reconcile.Report, error) {
	if r == nil || isNilDependency(service) || isNilDependency(target) {
		return reconcile.Report{TargetID: targetID}, errors.New("reconcile runtime dependencies are incomplete")
	}

	snapshotTarget := &reconcileSnapshotTarget{TargetAdapter: target}
	report, err := service.Check(ctx, targetID, snapshotTarget)
	report.TargetID = targetID
	if err != nil {
		return report, err
	}
	channels, complete := snapshotTarget.snapshot()
	r.recordReconcileReport(report, channels, complete)
	return report, nil
}

func (r *Runtime) recordReconcileReport(report reconcile.Report, channels []platform.Channel, complete bool) {
	liveChannels := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		liveChannels[channel.ID] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitialized()
	for key := range r.drifted {
		if key.targetID == report.TargetID {
			delete(r.drifted, key)
		}
	}
	if complete {
		for key, pending := range r.needsReconcile {
			if key.targetID != report.TargetID {
				continue
			}
			if _, exists := liveChannels[pending.channelID]; !exists {
				delete(r.needsReconcile, key)
			}
		}
	}
	for _, state := range report.Mappings {
		key := runtimeKey{assetID: state.Mapping.UpstreamAssetID, targetID: report.TargetID}
		if state.Status == reconcile.StatusDrifted {
			if differences := differencesFromDrift(state.Drift); len(differences) != 0 {
				r.drifted[key] = differences
			}
		}
	}
}

func (r *Runtime) matrixState(key runtimeKey) (pendingReconcile, bool, []matrixDifference) {
	r.mu.RLock()
	pending, needsReconcile := r.needsReconcile[key]
	differences := cloneDifferences(r.drifted[key])
	r.mu.RUnlock()
	return pending, needsReconcile, differences
}

func (r *Runtime) clear(key runtimeKey) {
	r.mu.Lock()
	r.ensureInitialized()
	delete(r.drifted, key)
	delete(r.needsReconcile, key)
	r.mu.Unlock()
}

func (r *Runtime) markNeedsReconcile(key runtimeKey, channelID string) {
	r.mu.Lock()
	r.ensureInitialized()
	r.needsReconcile[key] = pendingReconcile{channelID: channelID}
	r.mu.Unlock()
}

func (r *Runtime) pendingState(key runtimeKey) (pendingReconcile, bool) {
	r.mu.RLock()
	pending, exists := r.needsReconcile[key]
	r.mu.RUnlock()
	return pending, exists
}

func (r *Runtime) pendingMappings(targetID string) map[string]platform.SyncMapping {
	result := make(map[string]platform.SyncMapping)
	r.mu.RLock()
	for key, pending := range r.needsReconcile {
		if key.targetID == targetID && pending.channelID != "" {
			result[pending.channelID] = platform.SyncMapping{
				UpstreamAssetID: key.assetID, TargetID: targetID, TargetChannelID: pending.channelID,
			}
		}
	}
	r.mu.RUnlock()
	return result
}

func (r *Runtime) clearPendingChannel(targetID, channelID string) {
	r.mu.Lock()
	r.ensureInitialized()
	for key, pending := range r.needsReconcile {
		if key.targetID == targetID && pending.channelID == channelID {
			delete(r.needsReconcile, key)
			delete(r.drifted, key)
		}
	}
	r.mu.Unlock()
}

func (r *Runtime) ensureInitialized() {
	if r.drifted == nil {
		r.drifted = make(map[runtimeKey][]matrixDifference)
	}
	if r.needsReconcile == nil {
		r.needsReconcile = make(map[runtimeKey]pendingReconcile)
	}
}

type reconcileSnapshotTarget struct {
	platform.TargetAdapter

	once sync.Once
	mu   sync.RWMutex

	channels []platform.Channel
	listErr  error
	listed   bool
}

func (t *reconcileSnapshotTarget) ListChannels(ctx context.Context) ([]platform.Channel, error) {
	t.once.Do(func() {
		channels, err := t.TargetAdapter.ListChannels(ctx)
		t.mu.Lock()
		t.channels = cloneRuntimeChannels(channels)
		t.listErr = err
		t.listed = true
		t.mu.Unlock()
	})
	t.mu.RLock()
	channels := cloneRuntimeChannels(t.channels)
	err := t.listErr
	t.mu.RUnlock()
	return channels, err
}

func (t *reconcileSnapshotTarget) snapshot() ([]platform.Channel, bool) {
	t.mu.RLock()
	channels := cloneRuntimeChannels(t.channels)
	complete := t.listed && t.listErr == nil
	t.mu.RUnlock()
	return channels, complete
}

func cloneRuntimeChannels(channels []platform.Channel) []platform.Channel {
	if channels == nil {
		return nil
	}
	result := make([]platform.Channel, len(channels))
	for i, channel := range channels {
		channel.Models = append([]string(nil), channel.Models...)
		result[i] = channel
	}
	return result
}
