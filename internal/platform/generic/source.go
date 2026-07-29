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
)

var (
	ErrUnauthenticated = errors.New("generic upstream credential is invalid or expired")
	ErrForbidden       = errors.New("generic upstream credential is forbidden")
)

type Config struct {
	SourceID       string
	Name           string
	BaseURL        string
	APIKey         string
	RequestTimeout time.Duration
}

type Source struct {
	config    Config
	client    *http.Client
	modelsURL string
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
	if strings.TrimSpace(assetID) != s.config.SourceID+":endpoint" {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	return platform.ResolvedSecret{
		Kind:        platform.AssetProxyKey,
		Bytes:       append([]byte(nil), s.config.APIKey...),
		ContentType: "text/plain",
		Metadata:    map[string]string{},
	}, nil
}

func (s *Source) listModels(ctx context.Context) ([]string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, s.config.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, s.modelsURL, nil)
	if err != nil {
		return nil, errors.New("failed to create generic upstream request")
	}
	request.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("generic upstream request timed out: %w", requestCtx.Err())
		}
		return nil, errors.New("generic upstream request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		return nil, classifyStatus(response.StatusCode, response.Header.Get("Retry-After"), time.Now())
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, errors.New("generic upstream returned an invalid response")
	}
	defer wipe(body)
	var payload modelListResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.Data == nil {
		return nil, errors.New("generic upstream returned an invalid response")
	}
	seen := make(map[string]struct{}, len(*payload.Data))
	models := make([]string, 0, len(*payload.Data))
	for _, item := range *payload.Data {
		model := strings.TrimSpace(item.ID)
		if !validModelID(model) {
			return nil, errors.New("generic upstream returned an invalid response")
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
		return errors.New("generic upstream request failed")
	}
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
