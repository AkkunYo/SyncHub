package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/gin-gonic/gin"
)

func (s *server) listUpstreamKeys(c *gin.Context) {
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
	writeSuccess(c, http.StatusOK, gin.H{"keys": keys})
}

func (s *server) createUpstreamKey(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[upstreamKeyCreateRequest](c)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := validateUpstreamKeyCreate(request)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstreamID := c.Param("upstream_id")
	err = s.models.MutateKey(c.Request.Context(), upstreamID, key.ID, func() error {
		return s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
			upstreamIndex, ok := findUpstream(cfg, upstreamID)
			if !ok {
				return errMutationNotFound
			}
			upstream := &cfg.Upstreams[upstreamIndex]
			if upstream.Type != "generic" {
				return errUnsupportedMutation
			}
			if _, ok := findGenericKey(upstream, key.ID); ok {
				return errDuplicateMutation
			}
			upstream.Keys = append(upstream.Keys, key)
			return nil
		})
	})
	if writeKeyMutationError(c, err) {
		return
	}
	c.Header("Location", "/api/v1/upstreams/"+upstreamID+"/keys/"+key.ID)
	writeSuccess(c, http.StatusCreated, redactUpstreamKey(key))
}

func (s *server) updateUpstreamKey(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[upstreamKeyUpdateRequest](c)
	if err != nil || (!request.Name.set && !request.APIKey.set && !request.Enabled.set && !request.Models.set) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if request.Name.set && (request.Name.null || validateText(request.Name.value, 200, false) != nil) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if request.APIKey.set && (request.APIKey.null || validateCredential(request.APIKey.value) != nil) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	models, err := normalizeOptionalKeyModels(request.Models)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstreamID, keyID := c.Param("upstream_id"), c.Param("key_id")
	var updated config.GenericKeyConfig
	err = s.models.MutateKey(c.Request.Context(), upstreamID, keyID, func() error {
		return s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
			upstreamIndex, ok := findUpstream(cfg, upstreamID)
			if !ok {
				return errMutationNotFound
			}
			upstream := &cfg.Upstreams[upstreamIndex]
			if upstream.Type != "generic" {
				return errUnsupportedMutation
			}
			keyIndex, ok := findGenericKey(upstream, keyID)
			if !ok {
				return errMutationNotFound
			}
			key := &upstream.Keys[keyIndex]
			if request.Name.set {
				key.Name = strings.TrimSpace(request.Name.value)
			}
			if request.APIKey.set {
				key.APIKey = request.APIKey.value
			}
			if request.Enabled.set {
				key.Enabled = request.Enabled.value
			}
			if request.Models.set {
				key.Models = models
			}
			updated = *key
			updated.Models = append([]string(nil), key.Models...)
			return nil
		})
	})
	if writeKeyMutationError(c, err) {
		return
	}
	writeSuccess(c, http.StatusOK, redactUpstreamKey(updated))
}

func (s *server) deleteUpstreamKey(c *gin.Context) {
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstreamID, keyID := c.Param("upstream_id"), c.Param("key_id")
	err := s.models.MutateKey(c.Request.Context(), upstreamID, keyID, func() error {
		return s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
			upstreamIndex, ok := findUpstream(cfg, upstreamID)
			if !ok {
				return errMutationNotFound
			}
			upstream := &cfg.Upstreams[upstreamIndex]
			if upstream.Type != "generic" {
				return errUnsupportedMutation
			}
			keyIndex, ok := findGenericKey(upstream, keyID)
			if !ok {
				return errMutationNotFound
			}
			assetID := upstream.ID + ":key:" + keyID
			legacyAssetID := upstream.ID + ":endpoint"
			for _, mapping := range upstream.SyncMappings {
				if mapping.UpstreamAssetID == assetID || (keyID == config.DefaultGenericKeyID && mapping.UpstreamAssetID == legacyAssetID) ||
					(len(upstream.Keys) == 1 && mapping.UpstreamAssetID == legacyAssetID) {
					return ErrResourceInUse
				}
			}
			upstream.Keys = append(upstream.Keys[:keyIndex], upstream.Keys[keyIndex+1:]...)
			return nil
		})
	})
	if writeKeyMutationError(c, err) {
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"deleted": true})
}

var (
	errUnsupportedMutation = errors.New("configuration resource does not support this operation")
	errDuplicateMutation   = errors.New("configuration resource already exists")
)

func writeKeyMutationError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errMutationNotFound):
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
	case errors.Is(err, errUnsupportedMutation):
		writeFailure(c, http.StatusUnprocessableEntity, "unsupported_capability")
	case errors.Is(err, errDuplicateMutation):
		writeFailure(c, http.StatusConflict, "resource_conflict")
	case errors.Is(err, ErrResourceInUse):
		writeFailure(c, http.StatusConflict, "resource_in_use")
	default:
		respondDependencyError(c, err, invalidRequestError)
	}
	return true
}

func validateUpstreamKeyCreate(request upstreamKeyCreateRequest) (config.GenericKeyConfig, error) {
	if validateIdentifier(request.ID) != nil || validateCredential(request.APIKey) != nil {
		return config.GenericKeyConfig{}, errInvalidInput
	}
	name, err := normalizeRequiredText(request.Name, 200)
	if err != nil {
		return config.GenericKeyConfig{}, errInvalidInput
	}
	models, err := normalizeKeyModels(request.Models)
	if err != nil {
		return config.GenericKeyConfig{}, errInvalidInput
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return config.GenericKeyConfig{
		ID: strings.TrimSpace(request.ID), Name: name, APIKey: request.APIKey,
		Enabled: enabled, Models: models,
	}, nil
}

func normalizeOptionalKeyModels(models optionalStringSlice) ([]string, error) {
	if !models.set {
		return nil, nil
	}
	return normalizeKeyModels(models.value)
}

func normalizeKeyModels(models []string) ([]string, error) {
	if len(models) > 1024 {
		return nil, errInvalidInput
	}
	result := make([]string, len(models))
	seen := make(map[string]struct{}, len(models))
	for i, model := range models {
		model = strings.TrimSpace(model)
		if validateText(model, 512, false) != nil {
			return nil, errInvalidInput
		}
		if _, ok := seen[model]; ok {
			return nil, errInvalidInput
		}
		seen[model] = struct{}{}
		result[i] = model
	}
	if models == nil {
		return nil, nil
	}
	return result, nil
}

func findGenericKey(upstream *config.UpstreamConfig, keyID string) (int, bool) {
	for i := range upstream.Keys {
		if upstream.Keys[i].ID == keyID {
			return i, true
		}
	}
	return -1, false
}
