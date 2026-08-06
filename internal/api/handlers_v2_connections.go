package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/gin-gonic/gin"
)

var errTargetConnectionChanged = errors.New("target connection changed during validation")

func (s *server) testTargetConnection(c *gin.Context) {
	targetID := c.Param("target_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(targetID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	targetConfig, ok := targetByID(s.deps.Config.Snapshot(), targetID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	}
	target, capabilities, err := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	if capabilities.Platform != targetConfig.Type || len(capabilities.Providers) == 0 {
		respondDependencyError(c, ErrUpstreamFailure, upstreamFailure)
		return
	}
	channels, err := target.ListChannels(c.Request.Context())
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	validatedAt := time.Now().UTC()
	persistedCapabilities := copyValidationCapabilities(capabilities)
	err = s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
		index, exists := findTarget(cfg, targetID)
		if !exists {
			return errMutationNotFound
		}
		current := &cfg.Targets[index]
		if !sameTargetConnection(*current, targetConfig) {
			return errTargetConnectionChanged
		}
		current.ValidationStatus = config.TargetValidationVerified
		current.ValidatedAt = &validatedAt
		current.ValidationCapabilities = copyValidationCapabilities(persistedCapabilities)
		return nil
	})
	switch {
	case errors.Is(err, errMutationNotFound):
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	case errors.Is(err, errTargetConnectionChanged):
		writeFailure(c, http.StatusConflict, "operation_in_progress")
		return
	case err != nil:
		respondDependencyError(c, err, internalError)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{
		"reachable":         true,
		"authenticated":     true,
		"authorized":        true,
		"resource_count":    len(channels),
		"capabilities":      capabilities,
		"validation_status": config.TargetValidationVerified,
		"validated_at":      validatedAt,
	})
}

func sameTargetConnection(left, right config.TargetConfig) bool {
	return left.ID == right.ID && left.Type == right.Type && left.BaseURL == right.BaseURL && left.UserID == right.UserID &&
		left.AccessToken == right.AccessToken && left.ManagementKey == right.ManagementKey && left.APIKey == right.APIKey
}

func (s *server) testUpstreamConnection(c *gin.Context) {
	upstreamConfig, ok := upstreamByID(s.deps.Config.Snapshot(), c.Param("upstream_id"))
	if !ok {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	upstream, err := s.deps.Adapters.ResolveUpstream(c.Request.Context(), upstreamConfig)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	capabilities, err := upstream.Capabilities(c.Request.Context())
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	snapshot, err := s.deps.Discovery.Refresh(c.Request.Context(), upstreamConfig.ID, upstream)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{
		"reachable":      true,
		"authenticated":  true,
		"authorized":     true,
		"resource_count": len(snapshot.Assets),
		"capabilities":   capabilities,
	})
}
