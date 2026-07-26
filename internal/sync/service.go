package sync

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

var (
	ErrInvalidRequest = errors.New("invalid sync request")
	ErrSecretResolve  = errors.New("secret resolution failed")
	ErrMappingPersist = errors.New("mapping persistence failed")
)

type MappingStore interface {
	SaveMappings(ctx context.Context, mappings []platform.SyncMapping) error
}

type Options struct {
	Concurrency int
}

type Service struct {
	store       MappingStore
	concurrency int
}

func NewService(store MappingStore, options Options) *Service {
	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	return &Service{store: store, concurrency: concurrency}
}

type BatchRequest struct {
	Asset    platform.UpstreamAsset
	Source   platform.UpstreamAdapter
	Grant    platform.SecretGrant
	Settings platform.ChannelSettings
	Targets  []TargetRequest
}

type TargetRequest struct {
	ID           string
	Adapter      platform.TargetAdapter
	Capabilities platform.TargetCapabilities
}

type TargetStatus string

const (
	TargetSynced         TargetStatus = "synced"
	TargetFailed         TargetStatus = "failed"
	TargetIncompatible   TargetStatus = "incompatible"
	TargetNeedsReconcile TargetStatus = "needs_reconcile"
)

type TargetResult struct {
	TargetID  string       `json:"target_id"`
	Status    TargetStatus `json:"status"`
	Code      string       `json:"code,omitempty"`
	ChannelID string       `json:"channel_id,omitempty"`
	Retryable bool         `json:"retryable,omitempty"`
}

type BatchResult struct {
	Targets []TargetResult `json:"targets"`
}

type targetJob struct {
	position    int
	resultIndex int
	request     TargetRequest
	mode        platform.SyncMode
}

type targetOutcome struct {
	position    int
	resultIndex int
	result      TargetResult
	mapping     *platform.SyncMapping
}

func (s *Service) Sync(ctx context.Context, request BatchRequest) (BatchResult, error) {
	result := BatchResult{Targets: make([]TargetResult, len(request.Targets))}
	for i, target := range request.Targets {
		result.Targets[i].TargetID = target.ID
	}
	if len(request.Targets) == 0 {
		return result, nil
	}
	if !request.Asset.Enabled {
		for i := range result.Targets {
			result.Targets[i].Status = TargetIncompatible
			result.Targets[i].Code = "asset_disabled"
		}
		return result, nil
	}

	jobs := selectTargetJobs(request, &result)
	if len(jobs) == 0 {
		return result, nil
	}
	if ctx == nil || s == nil || isNilDependency(s.store) || isNilDependency(request.Source) || strings.TrimSpace(request.Asset.ID) == "" {
		markJobsFailed(jobs, &result, "invalid_request", false)
		return result, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		markJobsFailed(jobs, &result, "context_cancelled", true)
		return result, err
	}

	resolved, err := request.Source.ResolveSecret(ctx, request.Asset.ID, request.Grant)
	defer resolved.Wipe()
	if err != nil || resolved.Kind != request.Asset.Kind || len(resolved.Bytes) == 0 {
		markJobsFailed(jobs, &result, "secret_resolve_failed", true)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, ErrSecretResolve
	}
	baseURL, valid := targetBaseURL(request.Asset.BaseURL, resolved.Metadata["base_url"])
	if !valid {
		markJobsFailed(jobs, &result, "secret_resolve_failed", true)
		return result, ErrSecretResolve
	}
	request.Asset.BaseURL = baseURL

	outcomes := s.runTargets(ctx, request, jobs, resolved.Bytes)
	resolved.Wipe()

	mappings := make([]platform.SyncMapping, 0, len(outcomes))
	createdResultIndexes := make([]int, 0, len(outcomes))
	for _, outcome := range outcomes {
		result.Targets[outcome.resultIndex] = outcome.result
		if outcome.mapping != nil {
			mappings = append(mappings, *outcome.mapping)
			createdResultIndexes = append(createdResultIndexes, outcome.resultIndex)
		}
	}

	if len(mappings) > 0 {
		if err := s.store.SaveMappings(ctx, mappings); err != nil {
			for _, resultIndex := range createdResultIndexes {
				result.Targets[resultIndex].Status = TargetNeedsReconcile
				result.Targets[resultIndex].Code = "mapping_persist_failed"
				result.Targets[resultIndex].Retryable = true
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

func targetBaseURL(discovered, resolved string) (string, bool) {
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return discovered, true
	}
	parsed, err := url.Parse(resolved)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.Hostname() == "" {
		return "", false
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", false
	}
	return resolved, true
}

func selectTargetJobs(request BatchRequest, result *BatchResult) []targetJob {
	jobs := make([]targetJob, 0, len(request.Targets))
	for i, target := range request.Targets {
		mode, err := platform.SelectSyncMode(request.Asset, target.Capabilities)
		if err != nil {
			result.Targets[i].Status = TargetIncompatible
			result.Targets[i].Code = "incompatible_target"
			continue
		}
		if strings.TrimSpace(target.ID) == "" || isNilDependency(target.Adapter) {
			result.Targets[i].Status = TargetFailed
			result.Targets[i].Code = "invalid_target"
			continue
		}
		jobs = append(jobs, targetJob{
			position:    len(jobs),
			resultIndex: i,
			request:     target,
			mode:        mode,
		})
	}
	return jobs
}

func markJobsFailed(jobs []targetJob, result *BatchResult, code string, retryable bool) {
	for _, job := range jobs {
		result.Targets[job.resultIndex].Status = TargetFailed
		result.Targets[job.resultIndex].Code = code
		result.Targets[job.resultIndex].Retryable = retryable
	}
}

func (s *Service) runTargets(ctx context.Context, request BatchRequest, jobs []targetJob, secret []byte) []targetOutcome {
	workerCount := s.concurrency
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}

	jobQueue := make(chan targetJob, len(jobs))
	outcomeQueue := make(chan targetOutcome, len(jobs))
	for _, job := range jobs {
		jobQueue <- job
	}
	close(jobQueue)

	for i := 0; i < workerCount; i++ {
		go func() {
			for job := range jobQueue {
				outcomeQueue <- syncTarget(ctx, request, job, secret)
			}
		}()
	}

	outcomes := make([]targetOutcome, len(jobs))
	for range jobs {
		outcome := <-outcomeQueue
		outcomes[outcome.position] = outcome
	}
	return outcomes
}

func syncTarget(ctx context.Context, request BatchRequest, job targetJob, secret []byte) targetOutcome {
	result := TargetResult{TargetID: job.request.ID}
	outcome := targetOutcome{position: job.position, resultIndex: job.resultIndex, result: result}
	if ctxErr := ctx.Err(); ctxErr != nil {
		outcome.result.Status = TargetFailed
		outcome.result.Code = "context_cancelled"
		outcome.result.Retryable = true
		return outcome
	}

	targetSecret := append([]byte(nil), secret...)
	defer wipeBytes(targetSecret)
	input := platform.CreateChannelInput{
		AssetID:  request.Asset.ID,
		Mode:     job.mode,
		Name:     request.Asset.Name,
		Provider: request.Asset.Provider,
		RawType:  request.Asset.RawType,
		BaseURL:  request.Asset.BaseURL,
		Secret:   targetSecret,
		Models:   append([]string(nil), request.Settings.Models...),
		Group:    request.Settings.Group,
		Priority: request.Settings.Priority,
		Weight:   request.Settings.Weight,
	}
	channel, err := job.request.Adapter.CreateChannel(ctx, input)
	if err != nil {
		outcome.result.Status = TargetFailed
		outcome.result.Code = "target_create_failed"
		outcome.result.Retryable = true
		if ctx.Err() != nil {
			outcome.result.Code = "context_cancelled"
		}
		return outcome
	}
	if strings.TrimSpace(channel.ID) == "" {
		outcome.result.Status = TargetFailed
		outcome.result.Code = "target_create_failed"
		outcome.result.Retryable = true
		return outcome
	}

	outcome.result.Status = TargetSynced
	outcome.result.ChannelID = channel.ID
	outcome.mapping = &platform.SyncMapping{
		UpstreamAssetID: request.Asset.ID,
		TargetID:        job.request.ID,
		TargetChannelID: channel.ID,
		SourceProvider:  request.Asset.Provider,
		AssetKind:       request.Asset.Kind,
		Snapshot:        platform.SnapshotFromChannel(channel),
	}
	return outcome
}

func wipeBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
