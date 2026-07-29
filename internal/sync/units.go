package sync

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

// UnitRequest is one existing upstream asset being written to one target.
// UpstreamGroup is required for New API token assets and is kept distinct from
// Settings.Group, which is the target platform's routing group.
type UnitRequest struct {
	UnitID        string
	Asset         platform.UpstreamAsset
	Target        TargetRequest
	UpstreamGroup *platform.UpstreamGroup
	Settings      platform.ChannelSettings
}

type MultiRequest struct {
	Source platform.UpstreamAdapter
	Grant  platform.SecretGrant
	Units  []UnitRequest
}

type UnitResult struct {
	UnitID            string       `json:"unit_id"`
	AssetID           string       `json:"asset_id"`
	TargetID          string       `json:"target_id"`
	UpstreamGroup     string       `json:"upstream_group,omitempty"`
	Status            TargetStatus `json:"status"`
	Code              string       `json:"code,omitempty"`
	ChannelID         string       `json:"channel_id,omitempty"`
	Retryable         bool         `json:"retryable,omitempty"`
	RetryAfterSeconds int64        `json:"retry_after_seconds,omitempty"`
	EffectiveModels   []string     `json:"effective_models"`
	ExcludedModels    []string     `json:"excluded_models"`
	Warnings          []string     `json:"warnings"`
}

type MultiResult struct {
	Units []UnitResult `json:"units"`
}

type unitJob struct {
	index         int
	unitID        string
	mode          platform.SyncMode
	asset         platform.UpstreamAsset
	target        TargetRequest
	settings      platform.ChannelSettings
	groupSnapshot *platform.UpstreamGroupSnapshot
	excluded      []string
	warnings      []string
}

type unitOutcome struct {
	index   int
	result  UnitResult
	mapping *platform.SyncMapping
}

// SyncUnits performs a valid multi-unit request with per-unit failures. Only
// malformed request structure, cancellation, and mapping persistence are
// returned as request-level errors.
func (s *Service) SyncUnits(ctx context.Context, request MultiRequest) (MultiResult, error) {
	result := MultiResult{Units: make([]UnitResult, len(request.Units))}
	for i, unit := range request.Units {
		result.Units[i] = newUnitResult(unit)
	}
	if len(request.Units) == 0 {
		return result, nil
	}
	if ctx == nil || s == nil || isNilDependency(s.store) || isNilDependency(request.Source) || !validUnitIdentities(request.Units) {
		markAllUnitsInvalid(&result)
		return result, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		markAllUnitsCancelled(&result)
		return result, err
	}

	jobs := make([]unitJob, 0, len(request.Units))
	for index, unit := range request.Units {
		job, code := prepareUnitJob(index, unit)
		if code != "" {
			setUnitFailure(&result.Units[index], code, false)
			if code == "asset_disabled" || code == "incompatible_target" {
				result.Units[index].Status = TargetIncompatible
			}
			continue
		}
		result.Units[index].EffectiveModels = append([]string{}, job.settings.Models...)
		result.Units[index].ExcludedModels = append([]string{}, job.excluded...)
		result.Units[index].Warnings = append([]string{}, job.warnings...)
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return result, nil
	}

	secrets, resolvedValues, err := resolveUnitSecrets(ctx, request.Source, request.Grant, jobs, &result)
	defer wipeResolvedValues(resolvedValues)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, err
	}

	runnable := make([]unitJob, 0, len(jobs))
	for _, job := range jobs {
		if result.Units[job.index].Status != "" {
			continue
		}
		resolved, ok := secrets[job.asset.ID]
		if !ok || resolved.Kind != job.asset.Kind || len(resolved.Bytes) == 0 {
			setUnitFailure(&result.Units[job.index], "secret_unavailable", false)
			continue
		}
		baseURL, valid := targetBaseURL(job.asset.BaseURL, resolved.Metadata["base_url"])
		if !valid {
			setUnitFailure(&result.Units[job.index], "secret_resolve_failed", true)
			continue
		}
		job.asset.BaseURL = baseURL
		runnable = append(runnable, job)
	}

	outcomes := s.runUnitTargets(ctx, runnable, secrets)
	mappings := make([]platform.SyncMapping, 0, len(outcomes))
	createdIndexes := make([]int, 0, len(outcomes))
	for _, outcome := range outcomes {
		result.Units[outcome.index] = outcome.result
		if outcome.mapping != nil {
			mappings = append(mappings, *outcome.mapping)
			createdIndexes = append(createdIndexes, outcome.index)
		}
	}
	if len(mappings) > 0 {
		if err := s.store.SaveMappings(ctx, mappings); err != nil {
			for _, index := range createdIndexes {
				result.Units[index].Status = TargetNeedsReconcile
				result.Units[index].Code = "mapping_persist_failed"
				result.Units[index].Retryable = true
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, errors.Join(ErrMappingPersist, ctxErr)
			}
			return result, ErrMappingPersist
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func validUnitIdentities(units []UnitRequest) bool {
	unitIDs := make(map[string]struct{}, len(units))
	tuples := make(map[string]struct{}, len(units))
	for _, unit := range units {
		unitID := strings.TrimSpace(unit.UnitID)
		assetID := strings.TrimSpace(unit.Asset.ID)
		targetID := strings.TrimSpace(unit.Target.ID)
		if unitID == "" || assetID == "" || targetID == "" {
			return false
		}
		if _, exists := unitIDs[unitID]; exists {
			return false
		}
		unitIDs[unitID] = struct{}{}
		tuple := assetID + "\x00" + targetID
		if _, exists := tuples[tuple]; exists {
			return false
		}
		tuples[tuple] = struct{}{}
	}
	return true
}

func prepareUnitJob(index int, unit UnitRequest) (unitJob, string) {
	job := unitJob{index: index, unitID: unit.UnitID, asset: unit.Asset, target: unit.Target, settings: unit.Settings}
	if !unit.Asset.Enabled {
		return job, "asset_disabled"
	}
	if strings.TrimSpace(unit.Target.ID) == "" || isNilDependency(unit.Target.Adapter) {
		return job, "invalid_target"
	}
	mode, err := platform.SelectSyncMode(unit.Asset, unit.Target.Capabilities)
	if err != nil {
		return job, "incompatible_target"
	}
	job.mode = mode
	job.settings.Models = dedupeModels(unit.Settings.Models)
	if unit.Asset.RawType != "newapi-token" {
		return job, ""
	}

	fixedGroup := strings.TrimSpace(unit.Asset.Metadata["upstream_group"])
	if fixedGroup == "" || unit.UpstreamGroup == nil || strings.TrimSpace(unit.UpstreamGroup.Name) == "" {
		return job, "group_required"
	}
	if strings.TrimSpace(unit.UpstreamGroup.Name) != fixedGroup {
		return job, "group_mismatch"
	}
	effective, excluded, warnings := modelsForGroup(job.settings.Models, unit.Asset.Models, *unit.UpstreamGroup)
	if len(effective) == 0 {
		return job, "models_out_of_group"
	}
	job.settings.Models = effective
	job.excluded = excluded
	job.warnings = warnings
	job.groupSnapshot = &platform.UpstreamGroupSnapshot{
		Group:          fixedGroup,
		Ratio:          unit.UpstreamGroup.Ratio,
		RatioKnown:     unit.UpstreamGroup.RatioKnown,
		Models:         append([]string(nil), unit.UpstreamGroup.Models...),
		ModelsVerified: unit.UpstreamGroup.ModelsVerified,
	}
	return job, ""
}

func modelsForGroup(requested, assetLimits []string, group platform.UpstreamGroup) ([]string, []string, []string) {
	requested = dedupeModels(requested)
	groupModels := dedupeModels(group.Models)
	assetLimits = dedupeModels(assetLimits)
	warnings := make([]string, 0, 2)
	var effective []string
	var excluded []string
	if group.ModelsVerified {
		if len(requested) == 0 {
			effective = append([]string(nil), groupModels...)
		} else {
			effective, excluded = intersectModels(requested, groupModels)
		}
	} else {
		warnings = append(warnings, "models_unverified")
		if len(requested) != 0 {
			effective = append([]string(nil), requested...)
		} else {
			effective = append([]string(nil), groupModels...)
		}
	}
	if len(assetLimits) != 0 {
		var removed []string
		effective, removed = intersectModels(effective, assetLimits)
		excluded = appendUnique(excluded, removed...)
	}
	if len(excluded) != 0 {
		warnings = append(warnings, "models_out_of_group")
	}
	return effective, excluded, warnings
}

func intersectModels(values, allowed []string) ([]string, []string) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, model := range allowed {
		allowedSet[model] = struct{}{}
	}
	included := make([]string, 0, len(values))
	excluded := make([]string, 0)
	for _, model := range values {
		if _, ok := allowedSet[model]; ok {
			included = append(included, model)
		} else {
			excluded = append(excluded, model)
		}
	}
	return included, excluded
}

func dedupeModels(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		model := strings.TrimSpace(value)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	return result
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	result := make([]string, 0, len(values)+len(additions))
	for _, value := range append(append([]string(nil), values...), additions...) {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func resolveUnitSecrets(
	ctx context.Context,
	source platform.UpstreamAdapter,
	grant platform.SecretGrant,
	jobs []unitJob,
	result *MultiResult,
) (map[string]platform.ResolvedSecret, []platform.ResolvedSecret, error) {
	assetOrder := make([]string, 0, len(jobs))
	assetKinds := make(map[string]platform.AssetKind, len(jobs))
	seen := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		if _, exists := seen[job.asset.ID]; !exists {
			seen[job.asset.ID] = struct{}{}
			assetOrder = append(assetOrder, job.asset.ID)
			assetKinds[job.asset.ID] = job.asset.Kind
		}
	}
	secrets := make(map[string]platform.ResolvedSecret, len(assetOrder))
	resolvedValues := make([]platform.ResolvedSecret, 0, len(assetOrder))

	if batch, ok := source.(platform.BatchSecretResolver); ok && !isNilDependency(batch) && usesBatchSecretResolution(jobs) {
		maxSize := batch.MaxSecretBatchSize()
		if maxSize <= 0 {
			markAssetsFailed(assetOrder, jobs, result, "secret_resolve_failed", true, 0)
			return secrets, resolvedValues, nil
		}
		for start := 0; start < len(assetOrder); start += maxSize {
			end := start + maxSize
			if end > len(assetOrder) {
				end = len(assetOrder)
			}
			ids := assetOrder[start:end]
			resolved, err := batch.ResolveSecrets(ctx, ids, grant)
			for _, value := range resolved {
				resolvedValues = append(resolvedValues, value)
			}
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					markAssetsFailed(assetOrder[start:], jobs, result, "context_cancelled", true, 0)
					return secrets, resolvedValues, ctxErr
				}
				if errors.Is(err, platform.ErrRateLimited) {
					retryAfter := retryAfterSeconds(err)
					markAssetsFailed(assetOrder[start:], jobs, result, "rate_limited", true, retryAfter)
					break
				}
				markAssetsFailed(ids, jobs, result, "secret_resolve_failed", true, 0)
				continue
			}
			for _, id := range ids {
				value, exists := resolved[id]
				if !exists || value.Kind != assetKinds[id] || len(value.Bytes) == 0 {
					markAssetsFailed([]string{id}, jobs, result, "secret_unavailable", false, 0)
					continue
				}
				secrets[id] = value
			}
		}
		return secrets, resolvedValues, nil
	}

	for _, id := range assetOrder {
		value, err := source.ResolveSecret(ctx, id, grant)
		resolvedValues = append(resolvedValues, value)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				markAssetsFailed([]string{id}, jobs, result, "context_cancelled", true, 0)
				return secrets, resolvedValues, ctxErr
			}
			if errors.Is(err, platform.ErrRateLimited) {
				markAssetsFailed([]string{id}, jobs, result, "rate_limited", true, retryAfterSeconds(err))
			} else {
				markAssetsFailed([]string{id}, jobs, result, "secret_resolve_failed", true, 0)
			}
			continue
		}
		if value.Kind != assetKinds[id] || len(value.Bytes) == 0 {
			markAssetsFailed([]string{id}, jobs, result, "secret_unavailable", false, 0)
			continue
		}
		secrets[id] = value
	}
	return secrets, resolvedValues, nil
}

func usesBatchSecretResolution(jobs []unitJob) bool {
	if len(jobs) == 0 {
		return false
	}
	for _, job := range jobs {
		if job.asset.RawType != "newapi-token" {
			return false
		}
	}
	return true
}

func markAssetsFailed(assetIDs []string, jobs []unitJob, result *MultiResult, code string, retryable bool, retryAfter int64) {
	set := make(map[string]struct{}, len(assetIDs))
	for _, id := range assetIDs {
		set[id] = struct{}{}
	}
	for _, job := range jobs {
		if _, matches := set[job.asset.ID]; !matches || result.Units[job.index].Status != "" {
			continue
		}
		setUnitFailure(&result.Units[job.index], code, retryable)
		result.Units[job.index].RetryAfterSeconds = retryAfter
	}
}

func retryAfterSeconds(err error) int64 {
	var rateLimit *platform.RateLimitError
	if !errors.As(err, &rateLimit) || rateLimit.RetryAfter <= 0 {
		return 0
	}
	return int64((rateLimit.RetryAfter + time.Second - 1) / time.Second)
}

func wipeResolvedValues(values []platform.ResolvedSecret) {
	for i := range values {
		values[i].Wipe()
	}
}

func (s *Service) runUnitTargets(ctx context.Context, jobs []unitJob, secrets map[string]platform.ResolvedSecret) []unitOutcome {
	if len(jobs) == 0 {
		return nil
	}
	workerCount := s.concurrency
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}
	queue := make(chan unitJob, len(jobs))
	outcomes := make(chan unitOutcome, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	for i := 0; i < workerCount; i++ {
		go func() {
			for job := range queue {
				outcomes <- syncUnitTarget(ctx, job, secrets[job.asset.ID].Bytes)
			}
		}()
	}
	result := make([]unitOutcome, len(jobs))
	positions := make(map[int]int, len(jobs))
	for position, job := range jobs {
		positions[job.index] = position
	}
	for range jobs {
		outcome := <-outcomes
		result[positions[outcome.index]] = outcome
	}
	return result
}

func syncUnitTarget(ctx context.Context, job unitJob, secret []byte) unitOutcome {
	upstreamGroup := ""
	if job.groupSnapshot != nil {
		upstreamGroup = job.groupSnapshot.Group
	}
	result := UnitResult{
		UnitID: job.unitID, AssetID: job.asset.ID, TargetID: job.target.ID, UpstreamGroup: upstreamGroup,
		EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{},
	}
	result.EffectiveModels = append([]string{}, job.settings.Models...)
	result.ExcludedModels = append([]string{}, job.excluded...)
	result.Warnings = append([]string{}, job.warnings...)
	outcome := unitOutcome{index: job.index, result: result}
	if err := ctx.Err(); err != nil {
		setUnitFailure(&outcome.result, "context_cancelled", true)
		return outcome
	}
	targetSecret := append([]byte(nil), secret...)
	defer wipeBytes(targetSecret)
	channel, err := job.target.Adapter.CreateChannel(ctx, platform.CreateChannelInput{
		AssetID: job.asset.ID, Mode: job.mode, Name: job.asset.Name, Provider: job.asset.Provider,
		RawType: job.asset.RawType, BaseURL: job.asset.BaseURL, Secret: targetSecret,
		Models: append([]string(nil), job.settings.Models...), Group: job.settings.Group,
		Priority: job.settings.Priority, Weight: job.settings.Weight,
	})
	if err != nil || strings.TrimSpace(channel.ID) == "" {
		setUnitFailure(&outcome.result, "target_create_failed", true)
		if ctx.Err() != nil {
			outcome.result.Code = "context_cancelled"
		}
		return outcome
	}
	outcome.result.Status = TargetSynced
	outcome.result.ChannelID = channel.ID
	outcome.mapping = &platform.SyncMapping{
		UpstreamAssetID: job.asset.ID,
		TargetID:        job.target.ID,
		TargetChannelID: channel.ID,
		SourceProvider:  job.asset.Provider,
		AssetKind:       job.asset.Kind,
		Snapshot:        platform.SnapshotFromChannel(channel),
		UpstreamGroup:   cloneGroupSnapshot(job.groupSnapshot),
	}
	return outcome
}

func cloneGroupSnapshot(source *platform.UpstreamGroupSnapshot) *platform.UpstreamGroupSnapshot {
	if source == nil {
		return nil
	}
	result := *source
	result.Models = append([]string(nil), source.Models...)
	return &result
}

func newUnitResult(unit UnitRequest) UnitResult {
	group := ""
	if unit.UpstreamGroup != nil {
		group = strings.TrimSpace(unit.UpstreamGroup.Name)
	}
	return UnitResult{
		UnitID: unit.UnitID, AssetID: unit.Asset.ID, TargetID: unit.Target.ID, UpstreamGroup: group,
		EffectiveModels: []string{}, ExcludedModels: []string{}, Warnings: []string{},
	}
}

func setUnitFailure(result *UnitResult, code string, retryable bool) {
	result.Status = TargetFailed
	result.Code = code
	result.Retryable = retryable
}

func markAllUnitsInvalid(result *MultiResult) {
	for i := range result.Units {
		setUnitFailure(&result.Units[i], "invalid_request", false)
	}
}

func markAllUnitsCancelled(result *MultiResult) {
	for i := range result.Units {
		setUnitFailure(&result.Units[i], "context_cancelled", true)
	}
}
