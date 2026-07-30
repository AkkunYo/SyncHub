package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

const maxResponseBytes = 8 << 20

const maxTokenSecretBatchSize = 100

type Config struct {
	SourceID       string
	BaseURL        string
	AccessToken    string
	UserID         int
	PageSize       int
	RequestTimeout time.Duration
	DiscoveryMode  string
}

type Source struct {
	config    Config
	transport transport

	mu      sync.RWMutex
	records map[string]assetRecord
}

type assetRecord struct {
	tokenID        int
	enabled        bool
	secretReadable bool
}

type tokenListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items    []tokenResponse `json:"items"`
		Total    int             `json:"total"`
		Page     int             `json:"page"`
		PageSize int             `json:"page_size"`
	} `json:"data"`
}

type tokenResponse struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Key                string `json:"key"`
	Status             int    `json:"status"`
	Group              string `json:"group"`
	RemainQuota        int64  `json:"remain_quota"`
	UsedQuota          int64  `json:"used_quota"`
	UnlimitedQuota     bool   `json:"unlimited_quota"`
	ExpiredTime        int64  `json:"expired_time"`
	ModelLimitsEnabled bool   `json:"model_limits_enabled"`
	ModelLimits        string `json:"model_limits"`
}

type userModelsResponse struct {
	Success bool     `json:"success"`
	Data    []string `json:"data"`
}

type tokenBatchKeysResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Keys map[string]string `json:"keys"`
	} `json:"data"`
}

type userSelfResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Group string `json:"group"`
	} `json:"data"`
}

func NewSource(cfg Config, client *http.Client) (*Source, error) {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.AccessToken = strings.TrimSpace(cfg.AccessToken)
	cfg.DiscoveryMode = strings.ToLower(strings.TrimSpace(cfg.DiscoveryMode))
	if cfg.DiscoveryMode == "" {
		cfg.DiscoveryMode = config.DiscoveryModeToken
	}
	if cfg.DiscoveryMode != config.DiscoveryModeToken {
		return nil, errors.New("only ordinary-user token discovery is supported")
	}
	if cfg.SourceID == "" {
		return nil, errors.New("source id is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("base URL must be an absolute HTTP(S) URL")
	}
	if cfg.AccessToken == "" {
		return nil, errors.New("access token is required")
	}
	if cfg.UserID < 0 {
		return nil, errors.New("user id must not be negative")
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 100
	}
	if cfg.PageSize > 100 {
		cfg.PageSize = 100
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 15 * time.Second
	}
	if client == nil {
		client = &http.Client{}
	}

	return &Source{
		config: cfg,
		transport: transport{
			baseURL:        cfg.BaseURL,
			accessToken:    cfg.AccessToken,
			userID:         cfg.UserID,
			requestTimeout: cfg.RequestTimeout,
			client:         client,
		},
		records: make(map[string]assetRecord),
	}, nil
}

func (s *Source) Capabilities(ctx context.Context) (platform.SourceCapabilities, error) {
	if ctx == nil {
		return platform.SourceCapabilities{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return platform.SourceCapabilities{}, err
	}
	return platform.SourceCapabilities{AssetKinds: []platform.AssetKind{platform.AssetProxyKey}, SecretResolution: true, GroupCatalog: true}, nil
}

func (s *Source) ListAssets(ctx context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	return s.listTokenAssets(ctx, cursor)
}

func (s *Source) listTokenAssets(ctx context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	page := cursor.Page
	if page < 1 {
		page = 1
	}
	pageSize := cursor.PageSize
	if pageSize < 1 {
		pageSize = s.config.PageSize
	}
	if pageSize > 100 {
		pageSize = 100
	}

	query := url.Values{}
	query.Set("p", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(pageSize))
	var response tokenListResponse
	if err := s.transport.get(ctx, "/api/token/", query.Encode(), &response); err != nil {
		return platform.AssetPage{}, err
	}
	if !response.Success {
		return platform.AssetPage{}, errors.New("New API token listing was rejected")
	}

	now := time.Now().Unix()
	assets := make([]platform.UpstreamAsset, 0, len(response.Data.Items))
	records := make(map[string]assetRecord, len(response.Data.Items))
	for _, token := range response.Data.Items {
		if token.ID <= 0 {
			return platform.AssetPage{}, errors.New("New API token listing returned an invalid token")
		}
		asset, record := s.normalizeToken(token, now)
		assets = append(assets, asset)
		records[asset.ID] = record
	}
	s.mu.Lock()
	for id, record := range records {
		s.records[id] = record
	}
	s.mu.Unlock()

	responsePage := response.Data.Page
	if responsePage < 1 {
		responsePage = page
	}
	responsePageSize := response.Data.PageSize
	if responsePageSize < 1 {
		responsePageSize = pageSize
	}
	hasMore := len(response.Data.Items) > 0 && (len(response.Data.Items) >= responsePageSize || responsePage*responsePageSize < response.Data.Total)
	result := platform.AssetPage{Assets: assets, HasMore: hasMore}
	if hasMore {
		result.Next = platform.PageCursor{Page: responsePage + 1, PageSize: responsePageSize}
	}
	return result, nil
}

func (s *Source) normalizeToken(token tokenResponse, now int64) (platform.UpstreamAsset, assetRecord) {
	group := strings.TrimSpace(token.Group)
	models := []string{}
	if token.ModelLimitsEnabled {
		models = normalizeStrings(splitCSV(token.ModelLimits))
	}
	expiryValid := token.ExpiredTime == -1 || token.ExpiredTime > now
	quotaValid := token.UnlimitedQuota || token.RemainQuota > 0
	enabled := token.Status == 1 && expiryValid && quotaValid && group != ""
	id := platform.TokenAssetID(s.config.SourceID, token.ID)
	metadata := map[string]string{
		"token_id":             strconv.Itoa(token.ID),
		"masked_key":           strings.TrimSpace(token.Key),
		"upstream_group":       group,
		"status":               strconv.Itoa(token.Status),
		"remain_quota":         strconv.FormatInt(token.RemainQuota, 10),
		"used_quota":           strconv.FormatInt(token.UsedQuota, 10),
		"unlimited_quota":      strconv.FormatBool(token.UnlimitedQuota),
		"expired_time":         strconv.FormatInt(token.ExpiredTime, 10),
		"model_limits_enabled": strconv.FormatBool(token.ModelLimitsEnabled),
	}
	if group == "" {
		metadata["group_required"] = "true"
	}
	asset := platform.UpstreamAsset{
		ID:             id,
		SourceID:       s.config.SourceID,
		SourceType:     "newapi",
		Provider:       platform.ProviderOpenAI,
		RawType:        "newapi-token",
		Kind:           platform.AssetProxyKey,
		Name:           strings.TrimSpace(token.Name),
		BaseURL:        s.config.BaseURL,
		Models:         models,
		Enabled:        enabled,
		SecretReadable: enabled,
		Metadata:       metadata,
	}
	return asset, assetRecord{tokenID: token.ID, enabled: enabled, secretReadable: enabled}
}

func (s *Source) GroupCatalog(ctx context.Context) (platform.GroupCatalog, error) {
	var self userSelfResponse
	if err := s.transport.get(ctx, "/api/user/self", "", &self); err != nil {
		return platform.GroupCatalog{}, err
	}
	if !self.Success {
		return platform.GroupCatalog{}, errors.New("New API user lookup was rejected")
	}

	var groupsResponse userGroupsResponse
	if err := s.transport.get(ctx, "/api/user/self/groups", "", &groupsResponse); err != nil {
		return platform.GroupCatalog{}, err
	}
	if !groupsResponse.Success {
		return platform.GroupCatalog{}, errors.New("New API group listing was rejected")
	}

	names := make([]string, 0, len(groupsResponse.Data))
	groupByName := make(map[string]struct {
		ratio json.RawMessage
		desc  string
	}, len(groupsResponse.Data))
	for rawName, rawGroup := range groupsResponse.Data {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return platform.GroupCatalog{}, errors.New("New API group listing returned an invalid group")
		}
		if _, duplicate := groupByName[name]; duplicate {
			return platform.GroupCatalog{}, errors.New("New API group listing returned duplicate groups")
		}
		names = append(names, name)
		groupByName[name] = struct {
			ratio json.RawMessage
			desc  string
		}{ratio: append(json.RawMessage(nil), rawGroup.Ratio...), desc: strings.TrimSpace(rawGroup.Description)}
	}
	sort.Strings(names)

	groups := make([]platform.UpstreamGroup, 0, len(names))
	for _, name := range names {
		query := url.Values{}
		query.Set("group", name)
		var modelsResponse userModelsResponse
		if err := s.transport.get(ctx, "/api/user/models", query.Encode(), &modelsResponse); err != nil {
			return platform.GroupCatalog{}, err
		}
		if !modelsResponse.Success {
			return platform.GroupCatalog{}, errors.New("New API group model listing was rejected")
		}
		rawGroup := groupByName[name]
		ratio, ratioKnown := parseGroupRatio(rawGroup.ratio)
		groups = append(groups, platform.UpstreamGroup{
			Name:           name,
			Description:    rawGroup.desc,
			Ratio:          ratio,
			RatioKnown:     ratioKnown,
			Models:         normalizeStrings(modelsResponse.Data),
			ModelsVerified: true,
			Auto:           name == "auto" || !ratioKnown,
		})
	}

	return platform.GroupCatalog{
		SourceID:     s.config.SourceID,
		DefaultGroup: strings.TrimSpace(self.Data.Group),
		Groups:       groups,
	}, nil
}

func parseGroupRatio(raw json.RawMessage) (float64, bool) {
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func (s *Source) DiscoveryModeStatus() platform.DiscoveryModeStatus {
	if s == nil {
		return platform.DiscoveryModeStatus{EffectiveMode: "unresolved", Status: "unresolved"}
	}
	return platform.DiscoveryModeStatus{EffectiveMode: "token", Status: "ready"}
}

func (s *Source) ResolveSecret(ctx context.Context, assetID string, grant platform.SecretGrant) (platform.ResolvedSecret, error) {
	resolved, err := s.ResolveSecrets(ctx, []string{assetID}, grant)
	if err != nil {
		return platform.ResolvedSecret{}, err
	}
	secret, ok := resolved[assetID]
	if !ok {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	return secret, nil
}

func (s *Source) MaxSecretBatchSize() int {
	return maxTokenSecretBatchSize
}

func (s *Source) ResolveSecrets(ctx context.Context, assetIDs []string, _ platform.SecretGrant) (map[string]platform.ResolvedSecret, error) {
	if len(assetIDs) < 1 || len(assetIDs) > maxTokenSecretBatchSize {
		return nil, errors.New("New API token secret batch size is invalid")
	}
	ids := make([]int, 0, len(assetIDs))
	seenAssets := make(map[string]struct{}, len(assetIDs))
	seenTokens := make(map[int]struct{}, len(assetIDs))
	s.mu.RLock()
	for _, assetID := range assetIDs {
		if _, duplicate := seenAssets[assetID]; duplicate {
			s.mu.RUnlock()
			return nil, errors.New("New API token secret batch contains duplicate assets")
		}
		seenAssets[assetID] = struct{}{}
		record, ok := s.records[assetID]
		if !ok || record.tokenID <= 0 {
			s.mu.RUnlock()
			return nil, platform.ErrSecretUnavailable
		}
		if !record.enabled {
			s.mu.RUnlock()
			return nil, platform.ErrAssetDisabled
		}
		if !record.secretReadable {
			s.mu.RUnlock()
			return nil, platform.ErrSecretUnavailable
		}
		if _, duplicate := seenTokens[record.tokenID]; duplicate {
			s.mu.RUnlock()
			return nil, errors.New("New API token secret batch contains duplicate token ids")
		}
		seenTokens[record.tokenID] = struct{}{}
		ids = append(ids, record.tokenID)
	}
	s.mu.RUnlock()

	var response tokenBatchKeysResponse
	if err := s.transport.do(ctx, request{
		method: http.MethodPost,
		path:   "/api/token/batch/keys",
		body: struct {
			IDs []int `json:"ids"`
		}{IDs: ids},
	}, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, platform.ErrSecretUnavailable
	}
	defer clearTokenKeyStrings(response.Data.Keys)

	result := make(map[string]platform.ResolvedSecret, len(response.Data.Keys))
	for index, assetID := range assetIDs {
		raw, ok := response.Data.Keys[strconv.Itoa(ids[index])]
		secret := strings.TrimSpace(raw)
		if !ok || secret == "" {
			continue
		}
		if !strings.HasPrefix(secret, "sk-") {
			secret = "sk-" + secret
		}
		result[assetID] = platform.ResolvedSecret{
			Kind:     platform.AssetProxyKey,
			Bytes:    []byte(secret),
			Metadata: map[string]string{"token_id": strconv.Itoa(ids[index])},
		}
	}
	return result, nil
}

func clearTokenKeyStrings(keys map[string]string) {
	for id := range keys {
		keys[id] = ""
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func normalizeStrings(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			unique[trimmed] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func parseMultiKeys(value string) ([]string, error) {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "[") {
		var rawKeys []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &rawKeys); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(rawKeys))
		for _, raw := range rawKeys {
			var stringKey string
			if err := json.Unmarshal(raw, &stringKey); err == nil {
				keys = append(keys, strings.TrimSpace(stringKey))
				continue
			}
			keys = append(keys, strings.TrimSpace(string(raw)))
		}
		return keys, nil
	}
	lines := strings.Split(strings.Trim(value, "\r\n"), "\n")
	keys := make([]string, 0, len(lines))
	for _, line := range lines {
		keys = append(keys, strings.TrimSpace(strings.TrimSuffix(line, "\r")))
	}
	return keys, nil
}
