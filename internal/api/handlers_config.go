package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/gin-gonic/gin"
)

var errMutationNotFound = errors.New("configuration resource not found")

func (s *server) health(c *gin.Context) {
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"status": "ok", "version": s.deps.Version, "build_date": s.deps.BuildDate})
}

func (s *server) getConfig(c *gin.Context) {
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	cfg := s.deps.Config.Snapshot()
	writeSuccess(c, http.StatusOK, redactConfigWithModeStatus(cfg, s.deps.Adapters))
}

func (s *server) updateApp(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[appUpdateRequest](c)
	if err != nil || validateHost(request.Host) != nil || request.Port < 1 || request.Port > 65535 || request.SyncConcurrency < 1 || request.SyncConcurrency > 1024 {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	reconcileInterval, err := parsePositiveDuration(request.ReconcileInterval)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	requestTimeout, err := parsePositiveDuration(request.RequestTimeout)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	next := config.AppConfig{
		Host: request.Host, Port: request.Port,
		ReconcileInterval: config.Duration(reconcileInterval), RequestTimeout: config.Duration(requestTimeout),
		SyncConcurrency: request.SyncConcurrency,
	}
	if err := s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
		cfg.App = next
		return nil
	}); err != nil {
		respondDependencyError(c, err, internalError)
		return
	}
	writeSuccess(c, http.StatusOK, redactConfig(config.Config{App: next}).App)
}

func (s *server) createTarget(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[targetCreateRequest](c)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	target, err := validateTargetCreate(request)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
		if _, ok := findTarget(cfg, target.ID); ok {
			return errInvalidInput
		}
		cfg.Targets = append(cfg.Targets, target)
		return nil
	}); err != nil {
		respondDependencyError(c, err, invalidRequestError)
		return
	}
	c.Header("Location", "/api/v1/targets/"+target.ID)
	writeSuccess(c, http.StatusCreated, redactTarget(target))
}

func (s *server) updateTarget(c *gin.Context) {
	targetID := c.Param("target_id")
	if validateNoQuery(c) != nil || validateIdentifier(targetID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[targetUpdateRequest](c)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	name, err := normalizeRequiredText(request.Name, 200)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	baseURL, err := normalizeBaseURL(request.BaseURL, false)
	if err != nil || validateOptionalCredentials(request.AccessToken, request.ManagementKey, request.APIKey) != nil ||
		(request.UserID.set && request.UserID.value < 0) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	var updated config.TargetConfig
	err = s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
		index, ok := findTarget(cfg, targetID)
		if !ok {
			return errMutationNotFound
		}
		target := &cfg.Targets[index]
		if err := applyTargetCredentials(target, request.AccessToken, request.ManagementKey, request.APIKey); err != nil {
			return err
		}
		if err := applyTargetUserID(target, request.UserID); err != nil {
			return err
		}
		target.Name = name
		target.BaseURL = baseURL
		updated = *target
		return nil
	})
	if errors.Is(err, errMutationNotFound) {
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	}
	if err != nil {
		respondDependencyError(c, err, invalidRequestError)
		return
	}
	writeSuccess(c, http.StatusOK, redactTarget(updated))
}

func (s *server) deleteTarget(c *gin.Context) {
	targetID := c.Param("target_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(targetID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	err := s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
		index, ok := findTarget(cfg, targetID)
		if !ok {
			return errMutationNotFound
		}
		for _, upstream := range cfg.Upstreams {
			for _, mapping := range upstream.SyncMappings {
				if mapping.TargetID == targetID {
					return ErrResourceInUse
				}
			}
		}
		cfg.Targets = append(cfg.Targets[:index], cfg.Targets[index+1:]...)
		return nil
	})
	switch {
	case errors.Is(err, errMutationNotFound):
		writeFailure(c, http.StatusNotFound, "target_not_found")
		return
	case errors.Is(err, ErrResourceInUse):
		writeFailure(c, http.StatusConflict, "resource_in_use")
		return
	case err != nil:
		respondDependencyError(c, err, internalError)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"deleted": true})
}

func (s *server) createUpstream(c *gin.Context) {
	if validateNoQuery(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[upstreamCreateRequest](c)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	upstream, err := validateUpstreamCreate(request)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.models.MutateUpstream(c.Request.Context(), upstream.ID, func() error {
		return s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
			if _, ok := findUpstream(cfg, upstream.ID); ok {
				return errInvalidInput
			}
			cfg.Upstreams = append(cfg.Upstreams, upstream)
			return nil
		})
	}); err != nil {
		respondDependencyError(c, err, invalidRequestError)
		return
	}
	c.Header("Location", "/api/v1/upstreams/"+upstream.ID)
	writeSuccess(c, http.StatusCreated, redactUpstreamWithModeStatus(upstream, s.deps.Adapters))
}

func (s *server) updateUpstream(c *gin.Context) {
	upstreamID := c.Param("upstream_id")
	if validateNoQuery(c) != nil || validateIdentifier(upstreamID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	request, err := decodeStrictJSON[upstreamUpdateRequest](c)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	name, err := normalizeRequiredText(request.Name, 200)
	if err != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	baseURL, err := normalizeBaseURL(request.BaseURL, false)
	if err != nil || validateOptionalCredentials(request.AccessToken, request.ManagementKey, request.APIKey) != nil ||
		validateOptionalProxyCredential(request.ProxyAPIKey) != nil ||
		(request.UserID.set && request.UserID.value < 0) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	var updated config.UpstreamConfig
	err = s.models.MutateUpstream(c.Request.Context(), upstreamID, func() error {
		return s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
			index, ok := findUpstream(cfg, upstreamID)
			if !ok {
				return errMutationNotFound
			}
			upstream := &cfg.Upstreams[index]
			if err := applyUpstreamCredentials(upstream, request.AccessToken, request.ManagementKey, request.APIKey, request.ProxyAPIKey); err != nil {
				return err
			}
			if err := applyUpstreamUserID(upstream, request.UserID); err != nil {
				return err
			}
			if err := applyUpstreamDiscoverySettings(upstream, request.DiscoveryMode, request.ManageTokens); err != nil {
				return err
			}
			upstream.Name = name
			upstream.BaseURL = baseURL
			updated = *upstream
			return nil
		})
	})
	if errors.Is(err, errMutationNotFound) {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	if err != nil {
		respondDependencyError(c, err, invalidRequestError)
		return
	}
	writeSuccess(c, http.StatusOK, redactUpstreamWithModeStatus(updated, s.deps.Adapters))
}

func (s *server) deleteUpstream(c *gin.Context) {
	upstreamID := c.Param("upstream_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(upstreamID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	err := s.models.MutateUpstream(c.Request.Context(), upstreamID, func() error {
		return s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
			index, ok := findUpstream(cfg, upstreamID)
			if !ok {
				return errMutationNotFound
			}
			if len(cfg.Upstreams[index].SyncMappings) != 0 {
				return ErrResourceInUse
			}
			cfg.Upstreams = append(cfg.Upstreams[:index], cfg.Upstreams[index+1:]...)
			return nil
		})
	})
	switch {
	case errors.Is(err, errMutationNotFound):
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	case errors.Is(err, ErrResourceInUse):
		writeFailure(c, http.StatusConflict, "resource_in_use")
		return
	case err != nil:
		respondDependencyError(c, err, internalError)
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"deleted": true})
}

func validateTargetCreate(request targetCreateRequest) (config.TargetConfig, error) {
	if validateIdentifier(request.ID) != nil {
		return config.TargetConfig{}, errInvalidInput
	}
	name, err := normalizeRequiredText(request.Name, 200)
	if err != nil {
		return config.TargetConfig{}, err
	}
	typeName := strings.ToLower(strings.TrimSpace(request.Type))
	baseURL, err := normalizeBaseURL(request.BaseURL, false)
	if err != nil {
		return config.TargetConfig{}, err
	}
	if request.UserID.set && request.UserID.value < 0 {
		return config.TargetConfig{}, errInvalidInput
	}
	target := config.TargetConfig{ID: request.ID, Name: name, Type: typeName, BaseURL: baseURL}
	switch typeName {
	case "newapi":
		if validateCredential(request.AccessToken) != nil || request.ManagementKey != "" || request.APIKey != "" {
			return config.TargetConfig{}, errInvalidInput
		}
		target.AccessToken = request.AccessToken
		if request.UserID.set {
			target.UserID = request.UserID.value
		}
	case "cliproxyapi":
		if request.UserID.set || request.AccessToken != "" || request.APIKey != "" || validateCredential(request.ManagementKey) != nil {
			return config.TargetConfig{}, errInvalidInput
		}
		target.ManagementKey = request.ManagementKey
	default:
		return config.TargetConfig{}, errInvalidInput
	}
	return target, nil
}

func validateUpstreamCreate(request upstreamCreateRequest) (config.UpstreamConfig, error) {
	if validateIdentifier(request.ID) != nil {
		return config.UpstreamConfig{}, errInvalidInput
	}
	name, err := normalizeRequiredText(request.Name, 200)
	if err != nil {
		return config.UpstreamConfig{}, err
	}
	typeName := strings.ToLower(strings.TrimSpace(request.Type))
	baseURL, err := normalizeBaseURL(request.BaseURL, false)
	if err != nil {
		return config.UpstreamConfig{}, err
	}
	if request.UserID.set && request.UserID.value < 0 {
		return config.UpstreamConfig{}, errInvalidInput
	}
	upstream := config.UpstreamConfig{ID: request.ID, Name: name, Type: typeName, BaseURL: baseURL, SyncMappings: []config.SyncMapping{}}
	switch typeName {
	case "newapi":
		if request.ManagementKey != "" || request.APIKey != "" || len(request.Keys) != 0 || request.ProxyAPIKey.set ||
			validateCredential(request.AccessToken) != nil {
			return config.UpstreamConfig{}, errInvalidInput
		}
		upstream.AccessToken = request.AccessToken
		mode, err := normalizeDiscoveryMode(request.DiscoveryMode)
		if err != nil {
			return config.UpstreamConfig{}, err
		}
		upstream.DiscoveryMode = mode
		upstream.ManageTokens = request.ManageTokens
		if request.UserID.set {
			upstream.UserID = request.UserID.value
		}
	case "generic":
		if request.UserID.set || request.AccessToken != "" || request.ManagementKey != "" || request.ProxyAPIKey.set || request.DiscoveryMode != "" || request.ManageTokens ||
			(request.APIKey != "" && len(request.Keys) != 0) || (request.APIKey == "" && len(request.Keys) == 0) {
			return config.UpstreamConfig{}, errInvalidInput
		}
		if request.APIKey != "" {
			if validateCredential(request.APIKey) != nil {
				return config.UpstreamConfig{}, errInvalidInput
			}
			upstream.Keys = []config.GenericKeyConfig{{
				ID: config.DefaultGenericKeyID, Name: "Default", APIKey: request.APIKey, Enabled: true,
			}}
		} else {
			upstream.Keys = make([]config.GenericKeyConfig, len(request.Keys))
			for i, keyRequest := range request.Keys {
				key, err := validateUpstreamKeyCreate(keyRequest)
				if err != nil {
					return config.UpstreamConfig{}, err
				}
				upstream.Keys[i] = key
			}
		}
	default:
		return config.UpstreamConfig{}, errInvalidInput
	}
	return upstream, nil
}

func normalizeDiscoveryMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return config.DiscoveryModeToken, nil
	}
	switch value {
	case config.DiscoveryModeToken:
		return value, nil
	default:
		return "", errInvalidInput
	}
}

func applyUpstreamDiscoverySettings(upstream *config.UpstreamConfig, mode optionalString, manage optionalBool) error {
	if upstream.Type != "newapi" {
		if mode.set || manage.set {
			return errInvalidInput
		}
		return nil
	}
	if mode.set {
		if mode.null {
			return errInvalidInput
		}
		normalized, err := normalizeDiscoveryMode(mode.value)
		if err != nil {
			return err
		}
		upstream.DiscoveryMode = normalized
	}
	if manage.set {
		upstream.ManageTokens = manage.value
	}
	return nil
}

func validateOptionalCredentials(values ...optionalString) error {
	for _, value := range values {
		if value.set && (value.null || validateCredential(value.value) != nil) {
			return errInvalidInput
		}
	}
	return nil
}

func validateOptionalProxyCredential(value optionalString) error {
	if !value.set {
		return nil
	}
	if value.null {
		return errInvalidInput
	}
	if value.value == "" {
		return nil
	}
	return validateCredential(value.value)
}

func applyTargetCredentials(target *config.TargetConfig, accessToken, managementKey, apiKey optionalString) error {
	switch target.Type {
	case "newapi":
		if managementKey.set || apiKey.set {
			return errInvalidInput
		}
		if accessToken.set {
			target.AccessToken = accessToken.value
		}
	case "cliproxyapi":
		if accessToken.set || apiKey.set {
			return errInvalidInput
		}
		if managementKey.set {
			target.ManagementKey = managementKey.value
		}
	default:
		return errInvalidInput
	}
	return nil
}

func applyTargetUserID(target *config.TargetConfig, userID optionalInt) error {
	if !userID.set {
		return nil
	}
	if target.Type != "newapi" || userID.value < 0 {
		return errInvalidInput
	}
	target.UserID = userID.value
	return nil
}

func applyUpstreamCredentials(upstream *config.UpstreamConfig, accessToken, managementKey, apiKey, proxyAPIKey optionalString) error {
	switch upstream.Type {
	case "newapi":
		if managementKey.set || apiKey.set || proxyAPIKey.set {
			return errInvalidInput
		}
		if accessToken.set {
			upstream.AccessToken = accessToken.value
		}
	case "generic":
		if accessToken.set || managementKey.set || proxyAPIKey.set {
			return errInvalidInput
		}
		if apiKey.set {
			keyIndex := -1
			for i := range upstream.Keys {
				if upstream.Keys[i].ID == config.DefaultGenericKeyID {
					keyIndex = i
					break
				}
			}
			if keyIndex == -1 && len(upstream.Keys) == 1 {
				keyIndex = 0
			}
			if keyIndex == -1 {
				return errInvalidInput
			}
			upstream.Keys[keyIndex].APIKey = apiKey.value
		}
	default:
		return errInvalidInput
	}
	return nil
}

func applyUpstreamUserID(upstream *config.UpstreamConfig, userID optionalInt) error {
	if !userID.set {
		return nil
	}
	if upstream.Type != "newapi" || userID.value < 0 {
		return errInvalidInput
	}
	upstream.UserID = userID.value
	return nil
}

func findTarget(cfg *config.Config, id string) (int, bool) {
	for i := range cfg.Targets {
		if cfg.Targets[i].ID == id {
			return i, true
		}
	}
	return -1, false
}

func findUpstream(cfg *config.Config, id string) (int, bool) {
	for i := range cfg.Upstreams {
		if cfg.Upstreams[i].ID == id {
			return i, true
		}
	}
	return -1, false
}
