package cliproxyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

const maxResponseBytes = 8 << 20

type Config struct {
	SourceID               string
	BaseURL                string
	ManagementKey          string
	ProxyAPIKey            string
	UseManagementKeyHeader bool
	RequestTimeout         time.Duration
}

type Source struct {
	config  Config
	client  *http.Client
	catalog *platform.ProviderCatalog

	mu      sync.RWMutex
	records map[string]assetRecord
}

type assetRecord struct {
	authID         string
	authIndex      string
	name           string
	rawProvider    string
	kind           platform.AssetKind
	enabled        bool
	secretReadable bool
}

type authFilesResponse struct {
	Files []authFileResponse `json:"files"`
}

type authFileResponse struct {
	ID            string `json:"id"`
	AuthIndex     string `json:"auth_index"`
	AuthIndexDash string `json:"auth-index"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Provider      string `json:"provider"`
	Status        string `json:"status"`
	Disabled      bool   `json:"disabled"`
	Unavailable   bool   `json:"unavailable"`
	RuntimeOnly   bool   `json:"runtime_only"`
}

type authModelsResponse struct {
	Models []struct {
		ID string `json:"id"`
	} `json:"models"`
}

type secretMatch struct {
	value   string
	baseURL string
	models  []string
}

func NewSource(cfg Config, client *http.Client) (*Source, error) {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.ManagementKey = strings.TrimSpace(cfg.ManagementKey)
	cfg.ProxyAPIKey = strings.TrimSpace(cfg.ProxyAPIKey)
	if cfg.SourceID == "" {
		return nil, errors.New("source id is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("base URL must be an absolute HTTP(S) URL")
	}
	if cfg.ManagementKey == "" {
		return nil, errors.New("management key is required")
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 15 * time.Second
	}
	if client == nil {
		client = &http.Client{}
	}
	return &Source{
		config:  cfg,
		client:  client,
		catalog: platform.DefaultCatalog(),
		records: make(map[string]assetRecord),
	}, nil
}

func (s *Source) Capabilities(context.Context) (platform.SourceCapabilities, error) {
	return platform.SourceCapabilities{
		AssetKinds:       []platform.AssetKind{platform.AssetStaticAPIKey, platform.AssetOAuthFile, platform.AssetProxyKey},
		SecretResolution: true,
	}, nil
}

func (s *Source) ListAssets(ctx context.Context, _ platform.PageCursor) (platform.AssetPage, error) {
	var response authFilesResponse
	if err := s.doJSON(ctx, http.MethodGet, s.config.BaseURL+"/v0/management/auth-files", &response); err != nil {
		return platform.AssetPage{}, err
	}

	assetCapacity := len(response.Files)
	if s.config.ProxyAPIKey != "" {
		assetCapacity++
	}
	assets := make([]platform.UpstreamAsset, 0, assetCapacity)
	records := make(map[string]assetRecord, len(response.Files))
	for _, file := range response.Files {
		rawProvider := strings.ToLower(strings.TrimSpace(file.Provider))
		if rawProvider == "" {
			rawProvider = strings.ToLower(strings.TrimSpace(file.Type))
		}
		descriptor := s.catalog.FromCLIProxyAPI(rawProvider)
		authID := strings.TrimSpace(file.ID)
		name := strings.TrimSpace(file.Name)
		if authID == "" {
			authID = rawProvider + ":" + name
		}
		if authID == ":" {
			continue
		}
		if name == "" {
			name = authID
		}
		models, err := s.listModels(ctx, name)
		if err != nil {
			return platform.AssetPage{}, err
		}
		kind := platform.AssetOAuthFile
		if file.RuntimeOnly {
			kind = platform.AssetStaticAPIKey
		}
		status := strings.ToLower(strings.TrimSpace(file.Status))
		enabled := !file.Disabled && !file.Unavailable && status != "disabled" && status != "error"
		metadata := map[string]string{}
		if kind == platform.AssetOAuthFile {
			metadata["schema_version"] = "cpa-auth-v1"
		}
		if descriptor.DiscoveryOnly {
			metadata["discovery_only"] = "true"
		}
		assetID := platform.CLIProxyAssetID(s.config.SourceID, authID)
		secretReadable := enabled && !descriptor.DiscoveryOnly
		assets = append(assets, platform.UpstreamAsset{
			ID:             assetID,
			SourceID:       s.config.SourceID,
			SourceType:     "cliproxyapi",
			Provider:       descriptor.ID,
			RawType:        rawProvider,
			Kind:           kind,
			Name:           name,
			Models:         models,
			Enabled:        enabled,
			SecretReadable: secretReadable,
			Metadata:       metadata,
		})
		authIndex := strings.TrimSpace(file.AuthIndex)
		if authIndex == "" {
			authIndex = strings.TrimSpace(file.AuthIndexDash)
		}
		records[assetID] = assetRecord{
			authID:         authID,
			authIndex:      authIndex,
			name:           name,
			rawProvider:    rawProvider,
			kind:           kind,
			enabled:        enabled,
			secretReadable: secretReadable,
		}
	}
	if s.config.ProxyAPIKey != "" {
		assets = append(assets, platform.UpstreamAsset{
			ID:             s.proxyAssetID(),
			SourceID:       s.config.SourceID,
			SourceType:     "cliproxyapi",
			Provider:       platform.ProviderOpenAI,
			RawType:        "openai-compatible",
			Kind:           platform.AssetProxyKey,
			Name:           "CLIProxyAPI OpenAI-Compatible Proxy",
			BaseURL:        s.config.BaseURL,
			Models:         []string{},
			Enabled:        true,
			SecretReadable: true,
			Metadata:       map[string]string{},
		})
	}

	s.mu.Lock()
	s.records = records
	s.mu.Unlock()
	return platform.AssetPage{Assets: assets}, nil
}

func (s *Source) listModels(ctx context.Context, name string) ([]string, error) {
	requestURL := s.config.BaseURL + "/v0/management/auth-files/models?" + url.Values{"name": []string{name}}.Encode()
	var response authModelsResponse
	if err := s.doJSON(ctx, http.MethodGet, requestURL, &response); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(response.Models))
	seen := make(map[string]struct{}, len(response.Models))
	for _, model := range response.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	return models, nil
}

func (s *Source) ResolveSecret(ctx context.Context, assetID string, grant platform.SecretGrant) (platform.ResolvedSecret, error) {
	if ctx == nil {
		return platform.ResolvedSecret{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return platform.ResolvedSecret{}, err
	}
	if assetID == s.proxyAssetID() {
		if s.config.ProxyAPIKey == "" {
			return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
		}
		return platform.ResolvedSecret{
			Kind:        platform.AssetProxyKey,
			Bytes:       []byte(s.config.ProxyAPIKey),
			ContentType: "text/plain",
			Metadata:    map[string]string{"base_url": s.config.BaseURL},
		}, nil
	}
	s.mu.RLock()
	record, exists := s.records[assetID]
	s.mu.RUnlock()
	if !exists {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	if !record.enabled {
		return platform.ResolvedSecret{}, platform.ErrAssetDisabled
	}
	if !record.secretReadable {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	switch record.kind {
	case platform.AssetOAuthFile:
		if !grant.AllowAuthFile {
			return platform.ResolvedSecret{}, platform.ErrSecretGrantRequired
		}
		return s.downloadAuthFile(ctx, record)
	case platform.AssetStaticAPIKey:
		return s.resolveStaticKey(ctx, record)
	default:
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
}

func (s *Source) proxyAssetID() string {
	return s.config.SourceID + ":proxy:openai-compatible"
}

func (s *Source) downloadAuthFile(ctx context.Context, record assetRecord) (platform.ResolvedSecret, error) {
	requestURL := s.config.BaseURL + "/v0/management/auth-files/download?" + url.Values{"name": []string{record.name}}.Encode()
	response, err := s.do(ctx, http.MethodGet, requestURL)
	if err != nil {
		return platform.ResolvedSecret{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		wipe(body)
		return platform.ResolvedSecret{}, errors.New("CLIProxyAPI returned an invalid auth file")
	}
	return platform.ResolvedSecret{
		Kind:        platform.AssetOAuthFile,
		Bytes:       body,
		ContentType: response.Header.Get("Content-Type"),
		Metadata:    map[string]string{"schema_version": "cpa-auth-v1", "auth_id": record.authID},
	}, nil
}

func (s *Source) resolveStaticKey(ctx context.Context, record assetRecord) (platform.ResolvedSecret, error) {
	endpoint, rootField := staticEndpoint(record.rawProvider)
	if endpoint == "" || record.authIndex == "" {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	var response map[string]json.RawMessage
	if err := s.doJSON(ctx, http.MethodGet, s.config.BaseURL+"/v0/management/"+endpoint, &response); err != nil {
		return platform.ResolvedSecret{}, err
	}
	raw, exists := response[rootField]
	if !exists {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return platform.ResolvedSecret{}, errors.New("CLIProxyAPI returned an invalid static-key response")
	}
	match, ok := findSecretByAuthIndex(value, record.authIndex, secretMatch{})
	if !ok || strings.TrimSpace(match.value) == "" {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	metadata := map[string]string{}
	if match.baseURL != "" {
		metadata["base_url"] = match.baseURL
	}
	if len(match.models) > 0 {
		metadata["models"] = strings.Join(match.models, ",")
	}
	return platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: []byte(match.value), Metadata: metadata}, nil
}

func staticEndpoint(provider string) (endpoint, rootField string) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini", "aistudio":
		return "gemini-api-key", "gemini-api-key"
	case "interactions", "google-interactions", "gemini-interactions":
		return "interactions-api-key", "interactions-api-key"
	case "claude", "anthropic":
		return "claude-api-key", "claude-api-key"
	case "codex":
		return "codex-api-key", "codex-api-key"
	case "xai":
		return "xai-api-key", "xai-api-key"
	case "vertex":
		return "vertex-api-key", "vertex-api-key"
	case "openai", "openai-compatibility":
		return "openai-compatibility", "openai-compatibility"
	default:
		if platform.IsCLIProxyOpenAICompatibleProvider(provider) {
			return "openai-compatibility", "openai-compatibility"
		}
		return "", ""
	}
}

func findSecretByAuthIndex(value any, wanted string, inherited secretMatch) (secretMatch, bool) {
	switch current := value.(type) {
	case []any:
		for _, child := range current {
			if result, ok := findSecretByAuthIndex(child, wanted, inherited); ok {
				return result, true
			}
		}
	case map[string]any:
		next := inherited
		if baseURL, ok := current["base-url"].(string); ok && strings.TrimSpace(baseURL) != "" {
			next.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		}
		if models, ok := current["models"].([]any); ok {
			next.models = normalizeModels(models)
		}
		authIndex, _ := current["auth-index"].(string)
		if strings.TrimSpace(authIndex) == wanted {
			if apiKey, ok := current["api-key"].(string); ok {
				next.value = apiKey
				return next, true
			}
		}
		for key, child := range current {
			if key == "headers" || key == "api-key" {
				continue
			}
			if result, ok := findSecretByAuthIndex(child, wanted, next); ok {
				return result, true
			}
		}
	}
	return secretMatch{}, false
}

func normalizeModels(values []any) []string {
	models := make([]string, 0, len(values))
	for _, value := range values {
		switch model := value.(type) {
		case string:
			if model = strings.TrimSpace(model); model != "" {
				models = append(models, model)
			}
		case map[string]any:
			name, _ := model["name"].(string)
			if name = strings.TrimSpace(name); name != "" {
				models = append(models, name)
			}
		}
	}
	return models
}

func (s *Source) doJSON(ctx context.Context, method, requestURL string, destination any) error {
	response, err := s.do(ctx, method, requestURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("CLIProxyAPI returned an invalid response")
	}
	return nil
}

func (s *Source) do(ctx context.Context, method, requestURL string) (*http.Response, error) {
	requestCtx, cancel := context.WithTimeout(ctx, s.config.RequestTimeout)
	request, err := http.NewRequestWithContext(requestCtx, method, requestURL, nil)
	if err != nil {
		cancel()
		return nil, errors.New("failed to create CLIProxyAPI request")
	}
	request.Header.Set("Accept", "application/json")
	if s.config.UseManagementKeyHeader {
		request.Header.Set("X-Management-Key", s.config.ManagementKey)
	} else {
		request.Header.Set("Authorization", "Bearer "+s.config.ManagementKey)
	}
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, errors.New("CLIProxyAPI request failed")
	}
	response.Body = &cancelOnCloseReadCloser{ReadCloser: response.Body, cancel: cancel}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		_ = response.Body.Close()
		return nil, fmt.Errorf("CLIProxyAPI request returned status %d", response.StatusCode)
	}
	return response, nil
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

func wipe(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
