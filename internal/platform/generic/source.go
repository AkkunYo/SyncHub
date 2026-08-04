package generic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

const (
	maxResponseBytes = 8 << 20
	sourceType       = "generic"
	rawType          = "generic-openai"
	maxModelIDBytes  = 512
	DefaultKeyID     = "default"
)

var (
	ErrUnauthenticated = errors.New("generic upstream credential is invalid or expired")
	ErrForbidden       = errors.New("generic upstream credential is forbidden")
	errRequestFailed   = errors.New("generic upstream request failed")
	errInvalidResponse = errors.New("generic upstream returned an invalid response")
)

type Config struct {
	SourceID       string
	Name           string
	BaseURL        string
	APIKey         string
	RequestTimeout time.Duration
}

type KeyConfig struct {
	ID      string
	Name    string
	APIKey  string
	Enabled bool
	Models  []string
}

type MultiKeyConfig struct {
	SourceID       string
	Name           string
	BaseURL        string
	Keys           []KeyConfig
	RequestTimeout time.Duration
}

type Source struct {
	config      Config
	multiConfig *MultiKeyConfig
	client      *http.Client
	modelsURL   string

	cacheMu    sync.RWMutex
	modelCache map[string][]string
}

type modelListResponse struct {
	Data *[]modelResponse `json:"data"`
}

type modelResponse struct {
	ID string `json:"id"`
}

func NewSource(cfg Config, client *http.Client) (*Source, error) {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.SourceID == "" || cfg.Name == "" {
		return nil, errors.New("generic upstream identity is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("generic upstream base URL must be an absolute credential-free HTTP(S) URL")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("generic upstream API key is required")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, errors.New("generic upstream request timeout must be positive")
	}
	if client == nil {
		client = http.DefaultClient
	}
	modelsURL := cfg.BaseURL + "/v1/models"
	if strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1") {
		modelsURL = cfg.BaseURL + "/models"
	}
	return &Source{config: cfg, client: client, modelsURL: modelsURL}, nil
}

func NewMultiKeySource(cfg MultiKeyConfig, client *http.Client) (*Source, error) {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.SourceID == "" || cfg.Name == "" {
		return nil, errors.New("generic upstream identity is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("generic upstream base URL must be an absolute credential-free HTTP(S) URL")
	}
	if len(cfg.Keys) == 0 {
		return nil, errors.New("generic upstream keys are required")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, errors.New("generic upstream request timeout must be positive")
	}

	keys := make([]KeyConfig, len(cfg.Keys))
	keyIDs := make(map[string]struct{}, len(cfg.Keys))
	keyNames := make(map[string]struct{}, len(cfg.Keys))
	for index, input := range cfg.Keys {
		key := input
		key.ID = strings.TrimSpace(key.ID)
		key.Name = strings.TrimSpace(key.Name)
		key.APIKey = strings.TrimSpace(key.APIKey)
		if !validKeyID(key.ID) {
			return nil, fmt.Errorf("generic upstream key %d ID is invalid", index+1)
		}
		if _, exists := keyIDs[key.ID]; exists {
			return nil, fmt.Errorf("generic upstream key %d ID is duplicated", index+1)
		}
		keyIDs[key.ID] = struct{}{}
		if key.Name == "" {
			return nil, fmt.Errorf("generic upstream key %d name is required", index+1)
		}
		nameKey := strings.ToLower(key.Name)
		if _, exists := keyNames[nameKey]; exists {
			return nil, fmt.Errorf("generic upstream key %d name is duplicated", index+1)
		}
		keyNames[nameKey] = struct{}{}
		if key.APIKey == "" {
			return nil, fmt.Errorf("generic upstream key %d API key is required", index+1)
		}
		models, err := normalizeConfiguredModels(key.Models)
		if err != nil {
			return nil, fmt.Errorf("generic upstream key %d models are invalid", index+1)
		}
		key.Models = models
		keys[index] = key
	}
	cfg.Keys = keys
	if client == nil {
		client = http.DefaultClient
	}
	modelsURL := cfg.BaseURL + "/v1/models"
	if strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1") {
		modelsURL = cfg.BaseURL + "/models"
	}
	return &Source{
		config: Config{
			SourceID:       cfg.SourceID,
			Name:           cfg.Name,
			BaseURL:        cfg.BaseURL,
			RequestTimeout: cfg.RequestTimeout,
		},
		multiConfig: &cfg,
		client:      client,
		modelsURL:   modelsURL,
		modelCache:  make(map[string][]string, len(cfg.Keys)),
	}, nil
}

func (s *Source) Capabilities(ctx context.Context) (platform.SourceCapabilities, error) {
	if ctx == nil {
		return platform.SourceCapabilities{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return platform.SourceCapabilities{}, err
	}
	return platform.SourceCapabilities{
		AssetKinds:       []platform.AssetKind{platform.AssetProxyKey},
		SecretResolution: true,
		GroupCatalog:     false,
	}, nil
}

func (s *Source) ListAssets(ctx context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	if ctx == nil {
		return platform.AssetPage{}, errors.New("context is required")
	}
	if cursor != (platform.PageCursor{}) {
		return platform.AssetPage{}, errors.New("generic upstream does not support pagination")
	}
	if s.multiConfig != nil {
		return s.listMultiKeyAssets(ctx)
	}
	models, err := s.listModels(ctx)
	if err != nil {
		return platform.AssetPage{}, err
	}
	available := len(models) > 0
	return platform.AssetPage{Assets: []platform.UpstreamAsset{{
		ID:             s.config.SourceID + ":endpoint",
		SourceID:       s.config.SourceID,
		SourceType:     sourceType,
		Provider:       platform.ProviderOpenAI,
		RawType:        rawType,
		Kind:           platform.AssetProxyKey,
		Name:           s.config.Name,
		BaseURL:        s.config.BaseURL,
		Models:         models,
		Enabled:        available,
		SecretReadable: available,
		Metadata:       map[string]string{},
	}}}, nil
}

func (s *Source) ResolveSecret(ctx context.Context, assetID string, _ platform.SecretGrant) (platform.ResolvedSecret, error) {
	if ctx == nil {
		return platform.ResolvedSecret{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return platform.ResolvedSecret{}, err
	}
	assetID = strings.TrimSpace(assetID)
	if s.multiConfig != nil {
		for _, key := range s.multiConfig.Keys {
			if assetID != s.assetID(key.ID) {
				continue
			}
			if !key.Enabled {
				return platform.ResolvedSecret{}, platform.ErrAssetDisabled
			}
			return resolvedKeySecret(key.APIKey), nil
		}
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	if assetID != s.config.SourceID+":endpoint" {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	return resolvedKeySecret(s.config.APIKey), nil
}

func (s *Source) listModels(ctx context.Context) ([]string, error) {
	return s.listModelsWithKey(ctx, s.config.APIKey, s.config.RequestTimeout)
}

func (s *Source) listMultiKeyAssets(ctx context.Context) (platform.AssetPage, error) {
	assets := make([]platform.UpstreamAsset, 0, len(s.multiConfig.Keys))
	for _, key := range s.multiConfig.Keys {
		if err := ctx.Err(); err != nil {
			return platform.AssetPage{}, err
		}
		metadata := map[string]string{"key_id": key.ID}
		if !key.Enabled {
			metadata["disabled"] = "true"
			assets = append(assets, s.keyAsset(key, nonNilStrings(key.Models), false, metadata))
			continue
		}

		models, err := s.listModelsWithKey(ctx, key.APIKey, s.multiConfig.RequestTimeout)
		if err == nil {
			if err := ctx.Err(); err != nil {
				return platform.AssetPage{}, err
			}
			s.storeCachedModels(key.ID, models)
			assets = append(assets, s.keyAsset(key, nonNilStrings(models), len(models) > 0, metadata))
			continue
		}
		if err := ctx.Err(); err != nil {
			return platform.AssetPage{}, err
		}

		metadata["stale"] = "true"
		metadata["error_code"] = stableModelErrorCode(err)
		models = s.cachedModels(key.ID)
		switch {
		case len(models) > 0:
			metadata["models_source"] = "cache"
		case len(key.Models) > 0:
			models = cloneStrings(key.Models)
			metadata["models_source"] = "manual"
		default:
			models = []string{}
			metadata["models_source"] = "none"
		}
		assets = append(assets, s.keyAsset(key, models, len(models) > 0, metadata))
	}
	return platform.AssetPage{Assets: assets}, nil
}

func (s *Source) listModelsWithKey(ctx context.Context, apiKey string, timeout time.Duration) ([]string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, s.modelsURL, nil)
	if err != nil {
		return nil, errRequestFailed
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("generic upstream request timed out: %w", requestCtx.Err())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("generic upstream request timed out: %w", context.DeadlineExceeded)
		}
		return nil, errRequestFailed
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		return nil, classifyStatus(response.StatusCode, response.Header.Get("Retry-After"), time.Now())
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("generic upstream request timed out: %w", requestCtx.Err())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("generic upstream request timed out: %w", context.DeadlineExceeded)
		}
		return nil, errInvalidResponse
	}
	if len(body) > maxResponseBytes {
		return nil, errInvalidResponse
	}
	defer wipe(body)
	var payload modelListResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.Data == nil {
		return nil, errInvalidResponse
	}
	seen := make(map[string]struct{}, len(*payload.Data))
	models := make([]string, 0, len(*payload.Data))
	for _, item := range *payload.Data {
		model := strings.TrimSpace(item.ID)
		if !validModelID(model) {
			return nil, errInvalidResponse
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func classifyStatus(status int, retryAfter string, now time.Time) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthenticated
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusTooManyRequests:
		return &platform.RateLimitError{RetryAfter: parseRetryAfter(retryAfter, now)}
	default:
		return errRequestFailed
	}
}

func (s *Source) keyAsset(key KeyConfig, models []string, available bool, metadata map[string]string) platform.UpstreamAsset {
	return platform.UpstreamAsset{
		ID:             s.assetID(key.ID),
		SourceID:       s.config.SourceID,
		SourceType:     sourceType,
		Provider:       platform.ProviderOpenAI,
		RawType:        rawType,
		Kind:           platform.AssetProxyKey,
		Name:           key.Name,
		BaseURL:        s.config.BaseURL,
		Models:         cloneStrings(models),
		Enabled:        available,
		SecretReadable: available,
		Metadata:       cloneMetadata(metadata),
	}
}

func (s *Source) assetID(keyID string) string {
	if keyID == DefaultKeyID {
		return s.config.SourceID + ":endpoint"
	}
	return s.config.SourceID + ":key:" + keyID
}

func (s *Source) storeCachedModels(keyID string, models []string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.modelCache[keyID] = cloneStrings(models)
}

func (s *Source) cachedModels(keyID string) []string {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return cloneStrings(s.modelCache[keyID])
}

func stableModelErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return "unauthenticated"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, platform.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errInvalidResponse):
		return "invalid_response"
	default:
		return "request_failed"
	}
}

func resolvedKeySecret(apiKey string) platform.ResolvedSecret {
	return platform.ResolvedSecret{
		Kind:        platform.AssetProxyKey,
		Bytes:       append([]byte(nil), apiKey...),
		ContentType: "text/plain",
		Metadata:    map[string]string{},
	}
}

func normalizeConfiguredModels(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	models := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		model := strings.TrimSpace(value)
		if !validModelID(model) {
			return nil, errInvalidResponse
		}
		if _, exists := seen[model]; exists {
			return nil, errInvalidResponse
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	return models, nil
}

func validKeyID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || index > 0 && (character == '.' || character == '_' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return cloneStrings(values)
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneMetadata(value map[string]string) map[string]string {
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

func validModelID(value string) bool {
	if value == "" || len(value) > maxModelIDBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func wipe(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
