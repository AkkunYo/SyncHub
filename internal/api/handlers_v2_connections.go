package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *server) testTargetConnection(c *gin.Context) {
	targetConfig, ok := targetByID(s.deps.Config.Snapshot(), c.Param("target_id"))
	if !ok {
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	}
	target, capabilities, err := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	channels, err := target.ListChannels(c.Request.Context())
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{
		"reachable":      true,
		"authenticated":  true,
		"authorized":     true,
		"resource_count": len(channels),
		"capabilities":   capabilities,
	})
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
