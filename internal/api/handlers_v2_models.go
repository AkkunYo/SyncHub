package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/modelcatalog"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/probe"
	"github.com/gin-gonic/gin"
)

func (s *server) discoverUpstreamModels(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[modelDiscoveryRequest](c)
	if err != nil || !validDiscoveryKeyIDs(request.KeyIDs) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstream, adapter, ok := s.resolveModelUpstream(c)
	if !ok {
		return
	}
	startedAt := time.Now().UTC()
	task, err := s.models.Discover(c.Request.Context(), upstream, adapter, request.KeyIDs)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	s.tasks.add(discoveryTaskRecord(task, upstream.ID, startedAt, time.Now().UTC()))
	writeSuccess(c, http.StatusAccepted, task)
}

func (s *server) listUpstreamKeyModels(c *gin.Context) {
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstream, ok := upstreamByID(s.deps.Config.Snapshot(), c.Param("upstream_id"))
	if !ok {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	var adapter platform.UpstreamAdapter
	if upstream.Type == "newapi" {
		var err error
		adapter, err = s.deps.Adapters.ResolveUpstream(c.Request.Context(), upstream)
		if err != nil {
			respondDependencyError(c, err, upstreamFailure)
			return
		}
	}
	keys, err := s.models.ListKeys(c.Request.Context(), upstream, adapter)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	keyID := c.Param("key_id")
	if !catalogContainsKey(keys, keyID) {
		writeFailure(c, http.StatusNotFound, "asset_not_found")
		return
	}
	models, exists := s.models.Models(upstream.ID, keyID)
	if !exists {
		writeFailure(c, http.StatusNotFound, "asset_not_found")
		return
	}
	writeSuccess(c, http.StatusOK, models)
}

func (s *server) probeUpstreamKeyModel(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[modelProbeRequest](c)
	protocol := probe.Protocol(strings.TrimSpace(request.Protocol))
	if err != nil || validateText(request.Model, 512, false) != nil || strings.TrimSpace(request.Model) != request.Model ||
		strings.TrimSpace(request.Protocol) != request.Protocol || !validProbeProtocol(protocol) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstream, adapter, ok := s.resolveModelUpstream(c)
	if !ok {
		return
	}
	result, err := s.models.Probe(
		c.Request.Context(), upstream, adapter, c.Param("key_id"), request.Model, protocol,
	)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	writeSuccess(c, http.StatusOK, result)
}

func (s *server) resolveModelUpstream(c *gin.Context) (config.UpstreamConfig, platform.UpstreamAdapter, bool) {
	upstream, ok := upstreamByID(s.deps.Config.Snapshot(), c.Param("upstream_id"))
	if !ok {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return config.UpstreamConfig{}, nil, false
	}
	adapter, err := s.deps.Adapters.ResolveUpstream(c.Request.Context(), upstream)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return config.UpstreamConfig{}, nil, false
	}
	return upstream, adapter, true
}

func validDiscoveryKeyIDs(values []string) bool {
	if len(values) == 0 || len(values) > 100 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if validateIdentifier(value) != nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validProbeProtocol(protocol probe.Protocol) bool {
	switch protocol {
	case probe.ProtocolAuto, probe.ProtocolChatCompletions, probe.ProtocolResponses, probe.ProtocolCompletions:
		return true
	default:
		return false
	}
}

func catalogContainsKey(keys []modelcatalog.Key, keyID string) bool {
	for _, key := range keys {
		if key.ID == keyID {
			return true
		}
	}
	return false
}
