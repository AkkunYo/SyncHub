package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/discovery"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/reconcile"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
	"github.com/gin-gonic/gin"
)

func (s *server) listChannels(c *gin.Context) {
	targetID := c.Param("target_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(targetID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	cfg := s.deps.Config.Snapshot()
	targetConfig, ok := targetByID(cfg, targetID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	}
	target, _, err := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	channels, err := target.ListChannels(c.Request.Context())
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	mappings, err := s.deps.Mappings.ListMappings(c.Request.Context(), targetID)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	mappingByChannel := make(map[string]*platform.SyncMapping, len(mappings))
	for i := range mappings {
		mapping := mappings[i]
		if _, exists := mappingByChannel[mapping.TargetChannelID]; !exists {
			mappingByChannel[mapping.TargetChannelID] = &mapping
		}
	}
	for channelID, mapping := range s.pendingMappings(targetID) {
		if _, exists := mappingByChannel[channelID]; !exists {
			pendingMapping := mapping
			mappingByChannel[channelID] = &pendingMapping
		}
	}
	result := make([]managedChannel, len(channels))
	for i, channel := range channels {
		result[i] = toManagedChannel(channel, mappingByChannel[channel.ID])
	}
	writeSuccess(c, http.StatusOK, gin.H{"channels": result})
}

func (s *server) updateChannel(c *gin.Context) {
	targetID := c.Param("target_id")
	channelID := c.Param("channel_id")
	if validateNoQuery(c) != nil || validateIdentifier(targetID) != nil || validateIdentifier(channelID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[channelUpdateRequest](c)
	if err != nil || request.Enabled == nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	name, err := normalizeRequiredText(request.Name, 200)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	baseURL, err := normalizeBaseURL(request.BaseURL, true)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	models, err := normalizeModels(request.Models)
	if err != nil || validateText(strings.TrimSpace(request.Group), 128, false) != nil || validatePriorityAndWeight(request.Priority, request.Weight) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	cfg := s.deps.Config.Snapshot()
	targetConfig, ok := targetByID(cfg, targetID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	}
	target, _, err := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	updated, err := target.UpdateChannel(c.Request.Context(), channelID, platform.UpdateChannelInput{
		Name: name, BaseURL: baseURL, Models: models, Group: strings.TrimSpace(request.Group),
		Priority: request.Priority, Weight: request.Weight, Enabled: *request.Enabled,
	})
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	if updated.ID != channelID {
		respondDependencyError(c, ErrUpstreamFailure, upstreamFailure)
		return
	}
	mappings, err := s.deps.Mappings.ListMappings(c.Request.Context(), targetID)
	if err != nil {
		s.markChannelNeedsReconcile(cfg, targetID, channelID)
		writeFailure(c, http.StatusConflict, "needs_reconcile")
		return
	}
	var managedMapping *platform.SyncMapping
	for i := range mappings {
		if mappings[i].TargetChannelID == channelID {
			mapping := mappings[i]
			mapping.Snapshot = platform.SnapshotFromChannel(updated)
			if err := s.deps.Mappings.UpdateMapping(c.Request.Context(), mapping); err != nil {
				s.markNeedsReconcile(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: targetID}, channelID)
				writeFailure(c, http.StatusConflict, "needs_reconcile")
				return
			}
			managedMapping = &mapping
			key := runtimeKey{assetID: mapping.UpstreamAssetID, targetID: targetID}
			s.clearRuntimeState(key)
			break
		}
	}
	writeSuccess(c, http.StatusOK, toManagedChannel(updated, managedMapping))
}

func (s *server) deleteChannel(c *gin.Context) {
	targetID := c.Param("target_id")
	channelID := c.Param("channel_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(targetID) != nil || validateIdentifier(channelID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	cfg := s.deps.Config.Snapshot()
	targetConfig, ok := targetByID(cfg, targetID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	}
	target, _, err := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	if err := target.DeleteChannel(c.Request.Context(), channelID); err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	s.clearPendingChannel(targetID, channelID)
	mappings, err := s.deps.Mappings.ListMappings(c.Request.Context(), targetID)
	if err != nil {
		s.markChannelNeedsReconcile(cfg, targetID, channelID)
		writeFailure(c, http.StatusConflict, "needs_reconcile")
		return
	}
	remove := make([]platform.SyncMapping, 0, 1)
	for _, mapping := range mappings {
		if mapping.TargetChannelID == channelID {
			remove = append(remove, mapping)
		}
	}
	if len(remove) != 0 {
		if err := s.deps.Mappings.DeleteMappings(c.Request.Context(), remove); err != nil {
			for _, mapping := range remove {
				s.markNeedsReconcile(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: targetID}, channelID)
			}
			writeFailure(c, http.StatusConflict, "needs_reconcile")
			return
		}
		for _, mapping := range remove {
			s.clearRuntimeState(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: targetID})
		}
	}
	writeSuccess(c, http.StatusOK, gin.H{"deleted": true})
}

func (s *server) refreshUpstream(c *gin.Context) {
	upstreamID := c.Param("upstream_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(upstreamID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstreamConfig, ok := upstreamByID(s.deps.Config.Snapshot(), upstreamID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	adapter, err := s.deps.Adapters.ResolveUpstream(c.Request.Context(), upstreamConfig)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	snapshot, err := s.deps.Discovery.Refresh(c.Request.Context(), upstreamID, adapter)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	writeSuccess(c, http.StatusOK, snapshotData(snapshot, true))
}

func (s *server) listAssets(c *gin.Context) {
	upstreamID := c.Param("upstream_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(upstreamID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, ok := upstreamByID(s.deps.Config.Snapshot(), upstreamID); !ok {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	snapshot, refreshed := s.deps.Discovery.Snapshot(upstreamID)
	if !refreshed {
		snapshot = discovery.Snapshot{SourceID: upstreamID, Assets: []platform.UpstreamAsset{}}
	}
	writeSuccess(c, http.StatusOK, snapshotData(snapshot, refreshed))
}

func snapshotData(snapshot discovery.Snapshot, refreshed bool) gin.H {
	assets := snapshot.Assets
	if assets == nil {
		assets = []platform.UpstreamAsset{}
	}
	return gin.H{"source_id": snapshot.SourceID, "assets": assets, "refreshed": refreshed}
}

func (s *server) batchSync(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[syncRequest](c)
	if err != nil || validateIdentifier(request.UpstreamID) != nil || validateIdentifier(request.AssetID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	models, err := normalizeModels(request.Settings.Models)
	if err != nil || validateText(strings.TrimSpace(request.Settings.Group), 128, false) != nil ||
		validatePriorityAndWeight(request.Settings.Priority, request.Settings.Weight) != nil ||
		validateText(request.Grant.SecurityProof, 4096, true) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	targetIDs, err := normalizeTargetIDs(request.TargetIDs)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	cfg := s.deps.Config.Snapshot()
	upstreamConfig, ok := upstreamByID(cfg, request.UpstreamID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	snapshot, refreshed := s.deps.Discovery.Snapshot(request.UpstreamID)
	if !refreshed {
		writeFailure(c, http.StatusNotFound, "asset_not_found")
		return
	}
	asset, ok := assetByID(snapshot, request.AssetID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "asset_not_found")
		return
	}
	source, err := s.deps.Adapters.ResolveUpstream(c.Request.Context(), upstreamConfig)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	keys := make([]runtimeKey, len(targetIDs))
	for i, targetID := range targetIDs {
		keys[i] = runtimeKey{assetID: request.AssetID, targetID: targetID}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].assetID == keys[j].assetID {
			return keys[i].targetID < keys[j].targetID
		}
		return keys[i].assetID < keys[j].assetID
	})
	unlock := s.lockTuples(keys)
	defer unlock()

	targetRequests := make([]syncservice.TargetRequest, 0, len(targetIDs))
	targetAdapters := make(map[string]platform.TargetAdapter, len(targetIDs))
	resolutionFailures := make([]syncservice.TargetResult, 0)
	pendingResults := make([]syncservice.TargetResult, 0)
	for _, targetID := range targetIDs {
		targetConfig, exists := targetByID(cfg, targetID)
		if !exists {
			writeFailure(c, http.StatusNotFound, "target_not_found")
			return
		}
		key := runtimeKey{assetID: request.AssetID, targetID: targetID}
		if pending, exists := s.pendingState(key); exists {
			pendingResults = append(pendingResults, syncservice.TargetResult{
				TargetID: targetID, Status: syncservice.TargetNeedsReconcile,
				Code: "needs_reconcile", ChannelID: pending.channelID, Retryable: true,
			})
			continue
		}
		adapter, capabilities, resolveErr := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
		if resolveErr != nil || isNilDependency(adapter) {
			resolutionFailures = append(resolutionFailures, targetResolutionFailure(targetID, resolveErr))
			continue
		}
		targetAdapters[targetID] = adapter
		targetRequests = append(targetRequests, syncservice.TargetRequest{ID: targetID, Adapter: adapter, Capabilities: capabilities})
	}

	grant := platform.SecretGrant{SecurityProof: request.Grant.SecurityProof, AllowAuthFile: request.Grant.AllowAuthFile}
	request.Grant.SecurityProof = ""
	batch := syncservice.BatchRequest{
		Asset: asset, Source: source, Grant: grant,
		Settings: platform.ChannelSettings{Models: models, Group: strings.TrimSpace(request.Settings.Group), Priority: request.Settings.Priority, Weight: request.Settings.Weight},
		Targets:  targetRequests,
	}
	result := syncservice.BatchResult{}
	var syncErr error
	if len(targetRequests) != 0 {
		result, syncErr = s.deps.Sync.Sync(c.Request.Context(), request.UpstreamID, cfg.App.SyncConcurrency, batch)
	}
	batch.Grant.SecurityProof = ""
	grant.SecurityProof = ""
	if len(result.Targets) == 0 && len(resolutionFailures) == 0 && len(pendingResults) == 0 && syncErr != nil {
		respondDependencyError(c, syncErr, upstreamFailure)
		return
	}
	result.Targets = append(result.Targets, resolutionFailures...)
	result.Targets = append(result.Targets, pendingResults...)
	result = normalizeBatchResult(targetIDs, result)
	for _, targetResult := range result.Targets {
		key := runtimeKey{assetID: request.AssetID, targetID: targetResult.TargetID}
		if targetResult.Status == syncservice.TargetNeedsReconcile {
			s.markNeedsReconcile(key, targetResult.ChannelID)
			if target := targetAdapters[targetResult.TargetID]; target != nil {
				_, _ = target.ListChannels(c.Request.Context())
			}
		} else if targetResult.Status == syncservice.TargetSynced {
			s.clearRuntimeState(key)
		}
	}
	writeSuccess(c, http.StatusOK, result)
}

func (s *server) matrix(c *gin.Context) {
	if requireEmptyBody(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	values, err := queryValues(c, "upstream_id")
	if err != nil || len(values) != 1 {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstreamID := values.Get("upstream_id")
	if validateIdentifier(upstreamID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	cfg := s.deps.Config.Snapshot()
	upstream, ok := upstreamByID(cfg, upstreamID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	snapshot, refreshed := s.deps.Discovery.Snapshot(upstreamID)
	targets := make([]matrixTarget, len(cfg.Targets))
	capabilities := make(map[string]platform.TargetCapabilities, len(cfg.Targets))
	for i, target := range cfg.Targets {
		targets[i] = matrixTarget{ID: target.ID, Name: target.Name, Type: target.Type, BaseURL: target.BaseURL}
		if refreshed && len(snapshot.Assets) != 0 {
			_, targetCapabilities, resolveErr := s.deps.Adapters.ResolveTarget(c.Request.Context(), target)
			if resolveErr != nil {
				respondDependencyError(c, resolveErr, internalError)
				return
			}
			capabilities[target.ID] = targetCapabilities
		}
	}
	mappings := make(map[runtimeKey]platform.SyncMapping, len(upstream.SyncMappings))
	for _, mapping := range upstream.SyncMappings {
		mappings[runtimeKey{assetID: mapping.UpstreamAssetID, targetID: mapping.TargetID}] = mapping
	}
	rows := make([]matrixRow, 0, len(snapshot.Assets))
	if refreshed {
		for _, asset := range snapshot.Assets {
			row := matrixRow{Asset: asset, Cells: make([]matrixCell, len(cfg.Targets))}
			for i, target := range cfg.Targets {
				key := runtimeKey{assetID: asset.ID, targetID: target.ID}
				cell := matrixCell{TargetID: target.ID, Status: "unsynced"}
				if mapping, exists := mappings[key]; exists {
					cell.Status = "synced"
					cell.ChannelID = mapping.TargetChannelID
				}
				pending, needsReconcile, differences := s.runtime.matrixState(key)
				switch {
				case needsReconcile:
					cell.Status = "needs_reconcile"
					if pending.channelID != "" {
						cell.ChannelID = pending.channelID
					}
				case len(differences) != 0 && cell.Status == "synced":
					cell.Status = "drifted"
					cell.Differences = differences
				case cell.Status == "unsynced":
					if !asset.Enabled {
						cell.Status = "incompatible"
					} else if _, selectErr := platform.SelectSyncMode(asset, capabilities[target.ID]); selectErr != nil {
						cell.Status = "incompatible"
					}
				}
				row.Cells[i] = cell
			}
			rows = append(rows, row)
		}
	}
	writeSuccess(c, http.StatusOK, matrixResponse{UpstreamID: upstreamID, Refreshed: refreshed, Targets: targets, Rows: rows})
}

func (s *server) reconcileTarget(c *gin.Context) {
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
	target, _, err := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	report, err := s.runtime.CheckAndRecord(c.Request.Context(), s.deps.Reconcile, targetID, target)
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	writeSuccess(c, http.StatusOK, report)
}

func (s *server) acceptDrift(c *gin.Context) {
	targetID := c.Param("target_id")
	if validateNoQuery(c) != nil || validateIdentifier(targetID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[acceptDriftRequest](c)
	if err != nil || validateIdentifier(request.UpstreamAssetID) != nil || validateIdentifier(request.ChannelID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	targetConfig, ok := targetByID(s.deps.Config.Snapshot(), targetID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	}
	mappings, err := s.deps.Mappings.ListMappings(c.Request.Context(), targetID)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	var mapping *platform.SyncMapping
	for i := range mappings {
		if mappings[i].UpstreamAssetID == request.UpstreamAssetID && mappings[i].TargetChannelID == request.ChannelID {
			candidate := mappings[i]
			mapping = &candidate
			break
		}
	}
	if mapping == nil {
		writeFailure(c, http.StatusNotFound, "channel_not_found")
		return
	}
	target, _, err := s.deps.Adapters.ResolveTarget(c.Request.Context(), targetConfig)
	if err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	channels, err := target.ListChannels(c.Request.Context())
	if err != nil {
		respondDependencyError(c, err, upstreamFailure)
		return
	}
	var current *platform.Channel
	for i := range channels {
		if channels[i].ID == request.ChannelID {
			channel := channels[i]
			current = &channel
			break
		}
	}
	if current == nil {
		writeFailure(c, http.StatusNotFound, "channel_not_found")
		return
	}
	if err := s.deps.Reconcile.AcceptDrift(c.Request.Context(), *mapping, *current); err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	mapping.Snapshot = platform.SnapshotFromChannel(*current)
	s.clearRuntimeState(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: targetID})
	writeSuccess(c, http.StatusOK, gin.H{"mapping": *mapping})
}

func targetByID(cfg config.Config, targetID string) (config.TargetConfig, bool) {
	for _, target := range cfg.Targets {
		if target.ID == targetID {
			return target, true
		}
	}
	return config.TargetConfig{}, false
}

func upstreamByID(cfg config.Config, upstreamID string) (config.UpstreamConfig, bool) {
	for _, upstream := range cfg.Upstreams {
		if upstream.ID == upstreamID {
			return upstream, true
		}
	}
	return config.UpstreamConfig{}, false
}

func assetByID(snapshot discovery.Snapshot, assetID string) (platform.UpstreamAsset, bool) {
	for _, asset := range snapshot.Assets {
		if asset.ID == assetID {
			return asset, true
		}
	}
	return platform.UpstreamAsset{}, false
}

func normalizeTargetIDs(targetIDs []string) ([]string, error) {
	if len(targetIDs) == 0 || len(targetIDs) > 128 {
		return nil, errInvalidInput
	}
	result := make([]string, 0, len(targetIDs))
	seen := make(map[string]struct{}, len(targetIDs))
	for _, targetID := range targetIDs {
		if validateIdentifier(targetID) != nil {
			return nil, errInvalidInput
		}
		if _, exists := seen[targetID]; exists {
			continue
		}
		seen[targetID] = struct{}{}
		result = append(result, targetID)
	}
	return result, nil
}

func normalizeBatchResult(targetIDs []string, result syncservice.BatchResult) syncservice.BatchResult {
	byTarget := make(map[string]syncservice.TargetResult, len(result.Targets))
	for _, item := range result.Targets {
		if _, exists := byTarget[item.TargetID]; exists {
			continue
		}
		item = normalizeTargetResult(item)
		byTarget[item.TargetID] = item
	}
	normalized := syncservice.BatchResult{Targets: make([]syncservice.TargetResult, len(targetIDs))}
	for i, targetID := range targetIDs {
		item, ok := byTarget[targetID]
		if !ok {
			item = syncservice.TargetResult{TargetID: targetID, Status: syncservice.TargetFailed, Code: "upstream_failure", Retryable: true}
		}
		item.TargetID = targetID
		normalized.Targets[i] = item
	}
	return normalized
}

func normalizeTargetResult(result syncservice.TargetResult) syncservice.TargetResult {
	switch result.Status {
	case syncservice.TargetSynced:
		result.Code = ""
		result.Retryable = false
	case syncservice.TargetIncompatible:
		if result.Code == "asset_disabled" {
			result.Code = "secret_unavailable"
		} else {
			result.Code = "incompatible_target"
		}
		result.Retryable = false
	case syncservice.TargetNeedsReconcile:
		if strings.TrimSpace(result.ChannelID) == "" {
			result.Status = syncservice.TargetFailed
			result.Code = "upstream_failure"
			result.Retryable = true
		} else {
			result.Code = "needs_reconcile"
			result.Retryable = true
		}
	case syncservice.TargetFailed:
		switch result.Code {
		case "secret_resolve_failed":
			result.Code = "secret_unavailable"
			result.Retryable = false
		case "context_cancelled":
			result.Code = "upstream_timeout"
			result.Retryable = true
		case "upstream_timeout":
			result.Retryable = true
		case "incompatible_target":
			result.Code = "incompatible_target"
			result.Retryable = false
		default:
			result.Code = "upstream_failure"
			result.Retryable = true
		}
	default:
		result.Status = syncservice.TargetFailed
		result.Code = "upstream_failure"
		result.ChannelID = ""
		result.Retryable = true
	}
	return result
}

func targetResolutionFailure(targetID string, err error) syncservice.TargetResult {
	result := syncservice.TargetResult{
		TargetID:  targetID,
		Status:    syncservice.TargetFailed,
		Code:      "upstream_failure",
		Retryable: true,
	}
	if descriptor := classifyError(err, upstreamFailure); descriptor.code == "upstream_timeout" {
		result.Code = "upstream_timeout"
	}
	return result
}

func (s *server) clearRuntimeState(key runtimeKey) {
	s.runtime.clear(key)
}

func (s *server) markNeedsReconcile(key runtimeKey, channelID string) {
	s.runtime.markNeedsReconcile(key, channelID)
}

func (s *server) markChannelNeedsReconcile(cfg config.Config, targetID, channelID string) {
	for _, upstream := range cfg.Upstreams {
		for _, mapping := range upstream.SyncMappings {
			if mapping.TargetID == targetID && mapping.TargetChannelID == channelID {
				s.markNeedsReconcile(runtimeKey{assetID: mapping.UpstreamAssetID, targetID: targetID}, channelID)
			}
		}
	}
}

func (s *server) pendingState(key runtimeKey) (pendingReconcile, bool) {
	return s.runtime.pendingState(key)
}

func (s *server) pendingMappings(targetID string) map[string]platform.SyncMapping {
	return s.runtime.pendingMappings(targetID)
}

func (s *server) clearPendingChannel(targetID, channelID string) {
	s.runtime.clearPendingChannel(targetID, channelID)
}

func differencesFromDrift(drift map[string]reconcile.FieldDrift) []matrixDifference {
	fields := []string{"models", "group", "priority", "weight"}
	differences := make([]matrixDifference, 0, len(drift))
	for _, field := range fields {
		value, exists := drift[field]
		if !exists {
			continue
		}
		expected, expectedOK := safeDifferenceValue(field, value.Expected)
		actual, actualOK := safeDifferenceValue(field, value.Actual)
		if !expectedOK || !actualOK {
			continue
		}
		differences = append(differences, matrixDifference{Field: field, Expected: expected, Actual: actual})
	}
	return differences
}

func safeDifferenceValue(field string, value any) (any, bool) {
	switch field {
	case "models":
		models, ok := value.([]string)
		if !ok {
			return nil, false
		}
		return append([]string(nil), models...), true
	case "group":
		group, ok := value.(string)
		return group, ok
	case "priority", "weight":
		number, ok := value.(int)
		return number, ok
	default:
		return nil, false
	}
}

func cloneDifferences(differences []matrixDifference) []matrixDifference {
	if len(differences) == 0 {
		return nil
	}
	result := make([]matrixDifference, 0, len(differences))
	for _, difference := range differences {
		expected, expectedOK := safeDifferenceValue(difference.Field, difference.Expected)
		actual, actualOK := safeDifferenceValue(difference.Field, difference.Actual)
		if !expectedOK || !actualOK {
			continue
		}
		result = append(result, matrixDifference{Field: difference.Field, Expected: expected, Actual: actual})
	}
	return result
}
