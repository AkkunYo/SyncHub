package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

const maxResponseBytes = 8 << 20

type Config struct {
	SourceID       string
	BaseURL        string
	AccessToken    string
	UserID         int
	PageSize       int
	RequestTimeout time.Duration
	DiscoveryMode  string
}

type discoveryMode int

const (
	modeUnresolved discoveryMode = iota
	modeChannel
	modeToken
)

type Source struct {
	config    Config
	transport transport
	catalog   *platform.ProviderCatalog

	mu           sync.RWMutex
	records      map[string]assetRecord
	resolvedMode discoveryMode
	userRole     int
}

type assetRecord struct {
	channelID      int
	keyIndex       *int
	enabled        bool
	secretReadable bool
}

type channelListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items    []channelResponse `json:"items"`
		Total    int               `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"page_size"`
	} `json:"data"`
}

type channelResponse struct {
	ID          int    `json:"id"`
	Type        int    `json:"type"`
	Name        string `json:"name"`
	Status      int    `json:"status"`
	BaseURL     string `json:"base_url"`
	Models      string `json:"models"`
	Group       string `json:"group"`
	Priority    int    `json:"priority"`
	Weight      int    `json:"weight"`
	ChannelInfo struct {
		IsMultiKey         bool        `json:"is_multi_key"`
		MultiKeySize       int         `json:"multi_key_size"`
		MultiKeyStatusList map[int]int `json:"multi_key_status_list"`
	} `json:"channel_info"`
}

type channelKeyResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Key string `json:"key"`
	} `json:"data"`
}

type userSelfResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Role  int    `json:"role"`
		Group string `json:"group"`
	} `json:"data"`
}

func NewSource(cfg Config, client *http.Client) (*Source, error) {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.AccessToken = strings.TrimSpace(cfg.AccessToken)
	cfg.DiscoveryMode = strings.ToLower(strings.TrimSpace(cfg.DiscoveryMode))
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

	var initial discoveryMode
	switch cfg.DiscoveryMode {
	case config.DiscoveryModeToken:
		initial = modeToken
	default:
		initial = modeUnresolved
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
		catalog:      platform.DefaultCatalog(),
		records:      make(map[string]assetRecord),
		resolvedMode: initial,
	}, nil
}

func (s *Source) Capabilities(ctx context.Context) (platform.SourceCapabilities, error) {
	mode, err := s.ensureMode(ctx)
	if err != nil {
		return platform.SourceCapabilities{}, err
	}
	switch mode {
	case modeToken:
		return platform.SourceCapabilities{AssetKinds: []platform.AssetKind{platform.AssetProxyKey}, SecretResolution: true}, nil
	default:
		return platform.SourceCapabilities{AssetKinds: []platform.AssetKind{platform.AssetStaticAPIKey}, SecretResolution: true}, nil
	}
}

func (s *Source) ListAssets(ctx context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	mode, err := s.ensureMode(ctx)
	if err != nil {
		return platform.AssetPage{}, err
	}
	if mode == modeToken {
		return platform.AssetPage{}, errors.New("token mode listing not yet implemented")
	}
	return s.listChannelAssets(ctx, cursor)
}

func (s *Source) ensureMode(ctx context.Context) (discoveryMode, error) {
	s.mu.RLock()
	mode := s.resolvedMode
	s.mu.RUnlock()
	if mode != modeUnresolved {
		return mode, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolvedMode != modeUnresolved {
		return s.resolvedMode, nil
	}

	resolved, role, err := s.probe(ctx)
	if err != nil {
		return modeUnresolved, err
	}
	s.resolvedMode = resolved
	s.userRole = role
	return resolved, nil
}

func (s *Source) probe(ctx context.Context) (discoveryMode, int, error) {
	var self userSelfResponse
	if err := s.transport.get(ctx, "/api/user/self", "", &self); err != nil {
		if s.config.DiscoveryMode == config.DiscoveryModeChannel {
			return modeUnresolved, 0, fmt.Errorf("%w: cannot verify admin role", ErrInsufficientPrivilege)
		}
		return modeToken, 0, nil
	}
	if !self.Success {
		if s.config.DiscoveryMode == config.DiscoveryModeChannel {
			return modeUnresolved, 0, fmt.Errorf("%w: /api/user/self rejected", ErrInsufficientPrivilege)
		}
		return modeToken, 0, nil
	}

	role := self.Data.Role
	if role < 10 {
		if s.config.DiscoveryMode == config.DiscoveryModeChannel {
			return modeUnresolved, role, fmt.Errorf("%w: role %d is below admin threshold", ErrInsufficientPrivilege, role)
		}
		return modeToken, role, nil
	}

	query := url.Values{}
	query.Set("p", "1")
	query.Set("page_size", "1")
	var channelProbe channelListResponse
	if err := s.transport.get(ctx, "/api/channel/", query.Encode(), &channelProbe); err != nil {
		if errors.Is(err, ErrInsufficientPrivilege) {
			if s.config.DiscoveryMode == config.DiscoveryModeChannel {
				return modeUnresolved, role, fmt.Errorf("%w: channel listing rejected despite role %d", ErrInsufficientPrivilege, role)
			}
			return modeToken, role, nil
		}
		return modeUnresolved, role, err
	}
	if !channelProbe.Success {
		if s.config.DiscoveryMode == config.DiscoveryModeChannel {
			return modeUnresolved, role, fmt.Errorf("%w: channel listing was not successful", ErrInsufficientPrivilege)
		}
		return modeToken, role, nil
	}

	return modeChannel, role, nil
}

func (s *Source) listChannelAssets(ctx context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
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
	query.Set("page_size", strconv.Itoa(pageSize))
	var response channelListResponse
	if err := s.transport.get(ctx, "/api/channel/", query.Encode(), &response); err != nil {
		return platform.AssetPage{}, err
	}
	if !response.Success {
		return platform.AssetPage{}, errors.New("New API channel listing was rejected")
	}

	assets := make([]platform.UpstreamAsset, 0, len(response.Data.Items))
	records := make(map[string]assetRecord)
	for _, channel := range response.Data.Items {
		channelAssets, channelRecords := s.normalizeChannel(channel)
		assets = append(assets, channelAssets...)
		for id, record := range channelRecords {
			records[id] = record
		}
	}
	s.mu.Lock()
	for id, record := range records {
		s.records[id] = record
	}
	s.mu.Unlock()

	total := response.Data.Total
	responsePage := response.Data.Page
	if responsePage < 1 {
		responsePage = page
	}
	responsePageSize := response.Data.PageSize
	if responsePageSize < 1 {
		responsePageSize = pageSize
	}
	hasMore := responsePage*responsePageSize < total
	result := platform.AssetPage{Assets: assets, HasMore: hasMore}
	if hasMore {
		result.Next = platform.PageCursor{Page: responsePage + 1, PageSize: responsePageSize}
	}
	return result, nil
}

func (s *Source) normalizeChannel(channel channelResponse) ([]platform.UpstreamAsset, map[string]assetRecord) {
	descriptor := s.catalog.FromNewAPI(channel.Type)
	models := splitCSV(channel.Models)
	baseURL := strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
	metadata := map[string]string{
		"channel_id": strconv.Itoa(channel.ID),
		"group":      strings.TrimSpace(channel.Group),
		"priority":   strconv.Itoa(channel.Priority),
		"weight":     strconv.Itoa(channel.Weight),
	}
	if descriptor.DiscoveryOnly {
		metadata["discovery_only"] = "true"
	}
	newAsset := func(id string, index *int, enabled bool) platform.UpstreamAsset {
		assetMetadata := cloneMetadata(metadata)
		name := strings.TrimSpace(channel.Name)
		if index != nil {
			assetMetadata["key_index"] = strconv.Itoa(*index)
			name += " #" + strconv.Itoa(*index+1)
		}
		return platform.UpstreamAsset{
			ID:             id,
			SourceID:       s.config.SourceID,
			SourceType:     "newapi",
			Provider:       descriptor.ID,
			RawType:        descriptor.RawType,
			Kind:           platform.AssetStaticAPIKey,
			Name:           name,
			BaseURL:        baseURL,
			Models:         append([]string(nil), models...),
			Enabled:        enabled,
			SecretReadable: enabled && !descriptor.DiscoveryOnly,
			Metadata:       assetMetadata,
		}
	}

	records := make(map[string]assetRecord)
	channelEnabled := channel.Status == 1
	if !channel.ChannelInfo.IsMultiKey {
		id := platform.ChannelAssetID(s.config.SourceID, strconv.Itoa(channel.ID), nil)
		asset := newAsset(id, nil, channelEnabled)
		records[id] = assetRecord{channelID: channel.ID, enabled: channelEnabled, secretReadable: asset.SecretReadable}
		return []platform.UpstreamAsset{asset}, records
	}

	assets := make([]platform.UpstreamAsset, 0, channel.ChannelInfo.MultiKeySize)
	for index := 0; index < channel.ChannelInfo.MultiKeySize; index++ {
		status, exists := channel.ChannelInfo.MultiKeyStatusList[index]
		keyEnabled := channelEnabled && (!exists || status == 1)
		keyIndex := index
		id := platform.ChannelAssetID(s.config.SourceID, strconv.Itoa(channel.ID), &keyIndex)
		asset := newAsset(id, &keyIndex, keyEnabled)
		assets = append(assets, asset)
		recordIndex := keyIndex
		records[id] = assetRecord{channelID: channel.ID, keyIndex: &recordIndex, enabled: keyEnabled, secretReadable: asset.SecretReadable}
	}
	return assets, records
}

func (s *Source) ResolveSecret(ctx context.Context, assetID string, grant platform.SecretGrant) (platform.ResolvedSecret, error) {
	proof := strings.TrimSpace(grant.SecurityProof)
	if proof == "" {
		return platform.ResolvedSecret{}, platform.ErrSecretGrantRequired
	}
	record, err := s.resolveRecord(assetID)
	if err != nil {
		return platform.ResolvedSecret{}, err
	}
	if !record.enabled {
		return platform.ResolvedSecret{}, platform.ErrAssetDisabled
	}
	if !record.secretReadable {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}

	var response channelKeyResponse
	if err := s.transport.do(ctx, request{
		method: http.MethodPost,
		path:   "/api/channel/" + strconv.Itoa(record.channelID) + "/key",
		proof:  proof,
	}, &response); err != nil {
		return platform.ResolvedSecret{}, err
	}
	if !response.Success || response.Data.Key == "" {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	secret := strings.TrimSpace(response.Data.Key)
	if record.keyIndex != nil {
		keys, err := parseMultiKeys(secret)
		if err != nil || *record.keyIndex < 0 || *record.keyIndex >= len(keys) {
			return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
		}
		secret = keys[*record.keyIndex]
	}
	if secret == "" {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	metadata := map[string]string{"channel_id": strconv.Itoa(record.channelID)}
	if record.keyIndex != nil {
		metadata["key_index"] = strconv.Itoa(*record.keyIndex)
	}
	return platform.ResolvedSecret{Kind: platform.AssetStaticAPIKey, Bytes: []byte(secret), Metadata: metadata}, nil
}

func (s *Source) resolveRecord(assetID string) (assetRecord, error) {
	s.mu.RLock()
	record, ok := s.records[assetID]
	s.mu.RUnlock()
	if ok {
		return record, nil
	}

	prefix := s.config.SourceID + ":channel:"
	if !strings.HasPrefix(assetID, prefix) {
		return assetRecord{}, platform.ErrSecretUnavailable
	}
	remainder := strings.TrimPrefix(assetID, prefix)
	channelPart := remainder
	var keyIndex *int
	if marker := strings.LastIndex(remainder, ":key:"); marker >= 0 {
		channelPart = remainder[:marker]
		parsedIndex, err := strconv.Atoi(remainder[marker+len(":key:"):])
		if err != nil || parsedIndex < 0 {
			return assetRecord{}, platform.ErrSecretUnavailable
		}
		keyIndex = &parsedIndex
	}
	channelID, err := strconv.Atoi(channelPart)
	if err != nil || channelID <= 0 {
		return assetRecord{}, platform.ErrSecretUnavailable
	}
	return assetRecord{channelID: channelID, keyIndex: keyIndex, enabled: true, secretReadable: true}, nil
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

func cloneMetadata(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
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
