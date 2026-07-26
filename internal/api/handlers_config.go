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
	writeSuccess(c, http.StatusOK, gin.H{"status": "ok", "version": s.deps.Version})
}

func (s *server) getConfig(c *gin.Context) {
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	writeSuccess(c, http.StatusOK, redactConfig(s.deps.Config.Snapshot()))
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
	if err := s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
		if _, ok := findUpstream(cfg, upstream.ID); ok {
			return errInvalidInput
		}
		cfg.Upstreams = append(cfg.Upstreams, upstream)
		return nil
	}); err != nil {
		respondDependencyError(c, err, invalidRequestError)
		return
	}
	c.Header("Location", "/api/v1/upstreams/"+upstream.ID)
	writeSuccess(c, http.StatusCreated, redactUpstream(upstream))
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
	err = s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
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
		upstream.Name = name
		upstream.BaseURL = baseURL
		updated = *upstream
		return nil
	})
	if errors.Is(err, errMutationNotFound) {
		writeFailure(c, http.StatusNotFound, "upstream_not_found")
		return
	}
	if err != nil {
		respondDependencyError(c, err, invalidRequestError)
		return
	}
	writeSuccess(c, http.StatusOK, redactUpstream(updated))
}

func (s *server) deleteUpstream(c *gin.Context) {
	upstreamID := c.Param("upstream_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(upstreamID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	err := s.deps.Config.Update(c.Request.Context(), func(cfg *config.Config) error {
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
		if request.ManagementKey != "" || request.ProxyAPIKey.set || (request.AccessToken == "" && request.APIKey == "") ||
			(request.AccessToken != "" && validateCredential(request.AccessToken) != nil) ||
			(request.APIKey != "" && validateCredential(request.APIKey) != nil) {
			return config.UpstreamConfig{}, errInvalidInput
		}
		upstream.AccessToken = request.AccessToken
		upstream.APIKey = request.APIKey
		if request.UserID.set {
			upstream.UserID = request.UserID.value
		}
	case "cliproxyapi":
		if request.UserID.set || request.AccessToken != "" || (request.ManagementKey == "" && request.APIKey == "") ||
			(request.ManagementKey != "" && validateCredential(request.ManagementKey) != nil) ||
			(request.APIKey != "" && validateCredential(request.APIKey) != nil) || validateOptionalProxyCredential(request.ProxyAPIKey) != nil {
			return config.UpstreamConfig{}, errInvalidInput
		}
		upstream.ManagementKey = request.ManagementKey
		upstream.APIKey = request.APIKey
		upstream.ProxyAPIKey = request.ProxyAPIKey.value
	case "sub2api":
		if request.UserID.set || request.AccessToken != "" || request.ManagementKey != "" || request.ProxyAPIKey.set || validateCredential(request.APIKey) != nil {
			return config.UpstreamConfig{}, errInvalidInput
		}
		upstream.APIKey = request.APIKey
	default:
		return config.UpstreamConfig{}, errInvalidInput
	}
	return upstream, nil
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
		if managementKey.set || proxyAPIKey.set {
			return errInvalidInput
		}
		if accessToken.set {
			upstream.AccessToken = accessToken.value
		}
		if apiKey.set {
			upstream.APIKey = apiKey.value
		}
	case "cliproxyapi":
		if accessToken.set {
			return errInvalidInput
		}
		if managementKey.set {
			upstream.ManagementKey = managementKey.value
		}
		if apiKey.set {
			upstream.APIKey = apiKey.value
		}
		if proxyAPIKey.set {
			upstream.ProxyAPIKey = proxyAPIKey.value
		}
	case "sub2api":
		if accessToken.set || managementKey.set || proxyAPIKey.set {
			return errInvalidInput
		}
		if apiKey.set {
			upstream.APIKey = apiKey.value
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
