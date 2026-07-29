package app

import (
	"context"
	"strings"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/mapping"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

// SyncService creates a request-scoped sync service so both the source-bound
// mapping store and configured concurrency are explicit for every operation.
type SyncService struct {
	repository *mapping.Repository
}

func NewSyncService(repository *mapping.Repository) *SyncService {
	return &SyncService{repository: repository}
}

func (s *SyncService) Sync(
	ctx context.Context,
	sourceID string,
	concurrency int,
	request syncservice.BatchRequest,
) (syncservice.BatchResult, error) {
	if ctx == nil {
		return syncservice.BatchResult{}, ErrContextRequired
	}
	if s == nil || s.repository == nil {
		return syncservice.BatchResult{}, ErrDependenciesIncomplete
	}
	if strings.TrimSpace(sourceID) == "" {
		return syncservice.BatchResult{}, syncservice.ErrInvalidRequest
	}
	service := syncservice.NewService(
		s.repository.ForSource(sourceID),
		syncservice.Options{Concurrency: concurrency},
	)
	return service.Sync(ctx, request)
}

func (s *SyncService) SyncUnits(
	ctx context.Context,
	sourceID string,
	concurrency int,
	request syncservice.MultiRequest,
) (syncservice.MultiResult, error) {
	if ctx == nil {
		return syncservice.MultiResult{}, ErrContextRequired
	}
	if s == nil || s.repository == nil {
		return syncservice.MultiResult{}, ErrDependenciesIncomplete
	}
	if strings.TrimSpace(sourceID) == "" {
		return syncservice.MultiResult{}, syncservice.ErrInvalidRequest
	}
	service := syncservice.NewService(
		s.repository.ForSource(sourceID),
		syncservice.Options{Concurrency: concurrency},
	)
	return service.SyncUnits(ctx, request)
}

var _ api.SyncService = (*SyncService)(nil)
