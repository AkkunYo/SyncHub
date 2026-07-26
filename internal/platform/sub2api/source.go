package sub2api

import (
	"bytes"
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

	"github.com/AkkunYo/SyncHub/internal/platform"
)

const (
	maxResponseBytes = 8 << 20
	maxPageSize      = 1000
	sourceType       = "sub2api"
)

type Config struct {
	SourceID       string
	BaseURL        string
	APIKey         string
	PageSize       int
	RequestTimeout time.Duration
}

type Source struct {
	config Config
	client *http.Client

	mu      sync.RWMutex
	records map[string]assetRecord
}

type assetRecord struct {
	accountID      int64
	rawProvider    string
	accountType    string
	provider       string
	kind           platform.AssetKind
	enabled        bool
	secretReadable bool
}

type accountListEnvelope struct {
	Code *int                 `json:"code"`
	Data *accountPageResponse `json:"data"`
}

type accountPageResponse struct {
	Items    []accountResponse `json:"items"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
	Pages    int               `json:"pages"`
}

type accountResponse struct {
	ID                int64             `json:"id"`
	Name              string            `json:"name"`
	Platform          string            `json:"platform"`
	Type              string            `json:"type"`
	Credentials       accountMetadata   `json:"credentials"`
	CredentialsStatus map[string]bool   `json:"credentials_status"`
	Status            string            `json:"status"`
	Schedulable       *bool             `json:"schedulable"`
	Priority          int               `json:"priority"`
	Concurrency       int               `json:"concurrency"`
	RateMultiplier    float64           `json:"rate_multiplier"`
	LoadFactor        *int              `json:"load_factor"`
	GroupIDs          []int64           `json:"group_ids"`
	Groups            []accountGroupDTO `json:"groups"`
}

type accountMetadata struct {
	BaseURL  string `json:"base_url"`
	TierID   string `json:"tier_id"`
	AuthMode string `json:"auth_mode"`
	Location string `json:"location"`
}

type accountGroupDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type modelListEnvelope struct {
	Code *int             `json:"code"`
	Data *[]modelResponse `json:"data"`
}

type modelResponse struct {
	ID string `json:"id"`
}

type secretExportEnvelope struct {
	Code *int                  `json:"code"`
	Data *secretExportResponse `json:"data"`
}

type secretExportResponse struct {
	Accounts []secretAccountResponse `json:"accounts"`
}

type secretAccountResponse struct {
	Platform    string                  `json:"platform"`
	Type        string                  `json:"type"`
	Credentials secretAccountCredential `json:"credentials"`
}

type secretAccountCredential struct {
	APIKey mutableSecret `json:"api_key"`
}

type mutableSecret []byte

func (s *mutableSecret) UnmarshalJSON(data []byte) error {
	wipe(*s)
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = append((*s)[:0], value...)
	return nil
}

func NewSource(cfg Config, client *http.Client) (*Source, error) {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.SourceID == "" {
		return nil, errors.New("source id is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("base URL must be an absolute credential-free HTTP(S) URL")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("admin API key is required")
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 100
	}
	if cfg.PageSize > maxPageSize {
		cfg.PageSize = maxPageSize
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
		records: make(map[string]assetRecord),
	}, nil
}

func (s *Source) Capabilities(context.Context) (platform.SourceCapabilities, error) {
	return platform.SourceCapabilities{
		AssetKinds:       []platform.AssetKind{platform.AssetStaticAPIKey, platform.AssetOAuthFile},
		SecretResolution: true,
	}, nil
}

func (s *Source) ListAssets(ctx context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	if ctx == nil {
		return platform.AssetPage{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return platform.AssetPage{}, err
	}
	page := cursor.Page
	if page < 1 {
		page = 1
	}
	pageSize := cursor.PageSize
	if pageSize < 1 {
		pageSize = s.config.PageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))
	requestURL := s.config.BaseURL + "/api/v1/admin/accounts?" + query.Encode()
	var response accountListEnvelope
	if err := s.doJSON(ctx, http.MethodGet, requestURL, &response); err != nil {
		return platform.AssetPage{}, err
	}
	if !businessSuccess(response.Code) {
		return platform.AssetPage{}, errors.New("Sub2Api account listing was rejected")
	}
	if response.Data == nil || response.Data.Total < 0 || response.Data.Pages < 0 {
		return platform.AssetPage{}, errors.New("Sub2Api returned invalid account pagination")
	}
	data := response.Data
	responsePage := data.Page
	if responsePage < 1 {
		responsePage = page
	}
	if responsePage != page {
		return platform.AssetPage{}, errors.New("Sub2Api returned invalid account pagination")
	}
	responsePageSize := data.PageSize
	if responsePageSize < 1 {
		responsePageSize = pageSize
	}

	assets := make([]platform.UpstreamAsset, 0, len(data.Items))
	records := make(map[string]assetRecord, len(data.Items))
	for i := range data.Items {
		account := data.Items[i]
		if account.ID <= 0 {
			return platform.AssetPage{}, errors.New("Sub2Api returned an account without a stable id")
		}
		_, providerKnown := normalizeProvider(account.Platform)
		var models []string
		if providerKnown {
			var err error
			models, err = s.listModels(ctx, account.ID)
			if err != nil {
				return platform.AssetPage{}, err
			}
		}
		asset, record := s.normalizeAccount(account, models)
		assets = append(assets, asset)
		records[asset.ID] = record
	}

	hasMore := false
	if data.Pages > 0 {
		hasMore = responsePage < data.Pages
	} else if data.Total > 0 {
		hasMore = int64(responsePage)*int64(responsePageSize) < data.Total
	}
	if hasMore && len(data.Items) == 0 {
		return platform.AssetPage{}, errors.New("Sub2Api returned invalid account pagination")
	}

	s.mu.Lock()
	for id, record := range records {
		s.records[id] = record
	}
	s.mu.Unlock()

	result := platform.AssetPage{Assets: assets, HasMore: hasMore}
	if hasMore {
		result.Next = platform.PageCursor{Page: responsePage + 1, PageSize: responsePageSize}
	}
	return result, nil
}

func (s *Source) normalizeAccount(account accountResponse, models []string) (platform.UpstreamAsset, assetRecord) {
	rawProvider := strings.ToLower(strings.TrimSpace(account.Platform))
	accountType := strings.ToLower(strings.TrimSpace(account.Type))
	provider, providerKnown := normalizeProvider(rawProvider)
	kind, supportedType := normalizeAccountType(accountType)
	discoveryReason := ""
	if !providerKnown {
		discoveryReason = "unknown_provider"
	} else if !supportedType {
		discoveryReason = "unsupported_account_type"
	}
	if discoveryReason != "" {
		provider = platform.ProviderUnknown
	}

	schedulable := true
	if account.Schedulable != nil {
		schedulable = *account.Schedulable
	}
	status := strings.ToLower(strings.TrimSpace(account.Status))
	enabled := status == "active" && schedulable
	secretReadable := providerKnown && supportedType && account.CredentialsStatus["has_api_key"]

	metadata := map[string]string{
		"account_id":      strconv.FormatInt(account.ID, 10),
		"account_type":    accountType,
		"raw_provider":    rawProvider,
		"status":          status,
		"schedulable":     strconv.FormatBool(schedulable),
		"priority":        strconv.Itoa(account.Priority),
		"concurrency":     strconv.Itoa(account.Concurrency),
		"rate_multiplier": strconv.FormatFloat(account.RateMultiplier, 'f', -1, 64),
	}
	if account.LoadFactor != nil {
		metadata["load_factor"] = strconv.Itoa(*account.LoadFactor)
	}
	if value := strings.TrimSpace(account.Credentials.TierID); value != "" {
		metadata["tier_id"] = value
	}
	if value := strings.TrimSpace(account.Credentials.AuthMode); value != "" {
		metadata["auth_mode"] = value
	}
	if value := strings.TrimSpace(account.Credentials.Location); value != "" {
		metadata["location"] = value
	}
	if groupIDs := normalizeGroupIDs(account.GroupIDs); groupIDs != "" {
		metadata["group_ids"] = groupIDs
	}
	if groups := normalizeGroupNames(account.Groups); groups != "" {
		metadata["groups"] = groups
	}
	if discoveryReason != "" {
		metadata["discovery_only"] = "true"
		metadata["discovery_reason"] = discoveryReason
	}

	name := strings.TrimSpace(account.Name)
	if name == "" {
		name = "Sub2Api account " + strconv.FormatInt(account.ID, 10)
	}
	assetID := s.config.SourceID + ":key:" + strconv.FormatInt(account.ID, 10)
	asset := platform.UpstreamAsset{
		ID:             assetID,
		SourceID:       s.config.SourceID,
		SourceType:     sourceType,
		Provider:       provider,
		RawType:        accountType,
		Kind:           kind,
		Name:           name,
		BaseURL:        sanitizeMetadataURL(account.Credentials.BaseURL),
		Models:         append([]string(nil), models...),
		Enabled:        enabled,
		SecretReadable: secretReadable,
		Metadata:       metadata,
	}
	record := assetRecord{
		accountID:      account.ID,
		rawProvider:    rawProvider,
		accountType:    accountType,
		provider:       provider,
		kind:           kind,
		enabled:        enabled,
		secretReadable: secretReadable,
	}
	return asset, record
}

func (s *Source) listModels(ctx context.Context, accountID int64) ([]string, error) {
	requestURL := s.config.BaseURL + "/api/v1/admin/accounts/" + strconv.FormatInt(accountID, 10) + "/models"
	var response modelListEnvelope
	if err := s.doJSON(ctx, http.MethodGet, requestURL, &response); err != nil {
		return nil, err
	}
	if !businessSuccess(response.Code) {
		return nil, errors.New("Sub2Api model listing was rejected")
	}
	if response.Data == nil {
		return nil, errors.New("Sub2Api returned an invalid model response")
	}
	seen := make(map[string]struct{}, len(*response.Data))
	models := make([]string, 0, len(*response.Data))
	for _, model := range *response.Data {
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
	sort.Strings(models)
	return models, nil
}

func (s *Source) ResolveSecret(ctx context.Context, assetID string, _ platform.SecretGrant) (platform.ResolvedSecret, error) {
	if ctx == nil {
		return platform.ResolvedSecret{}, errors.New("context is required")
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
	if !record.secretReadable || record.kind != platform.AssetStaticAPIKey {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}

	query := url.Values{}
	query.Set("ids", strconv.FormatInt(record.accountID, 10))
	query.Set("include_proxies", "false")
	requestURL := s.config.BaseURL + "/api/v1/admin/accounts/data?" + query.Encode()
	var response secretExportEnvelope
	err := s.doJSON(ctx, http.MethodGet, requestURL, &response)
	defer response.wipe()
	if err != nil {
		return platform.ResolvedSecret{}, err
	}
	if !businessSuccess(response.Code) || response.Data == nil || len(response.Data.Accounts) != 1 {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	account := &response.Data.Accounts[0]
	if strings.ToLower(strings.TrimSpace(account.Platform)) != record.rawProvider ||
		strings.ToLower(strings.TrimSpace(account.Type)) != record.accountType {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}
	if len(bytes.TrimSpace(account.Credentials.APIKey)) == 0 {
		return platform.ResolvedSecret{}, platform.ErrSecretUnavailable
	}

	secret := []byte(account.Credentials.APIKey)
	account.Credentials.APIKey = nil
	return platform.ResolvedSecret{
		Kind:        platform.AssetStaticAPIKey,
		Bytes:       secret,
		ContentType: "text/plain",
		Metadata: map[string]string{
			"account_id":   strconv.FormatInt(record.accountID, 10),
			"account_type": record.accountType,
			"provider":     record.provider,
		},
	}, nil
}

func (s *Source) doJSON(ctx context.Context, method, requestURL string, destination any) error {
	requestCtx, cancel := context.WithTimeout(ctx, s.config.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, requestURL, nil)
	if err != nil {
		return errors.New("failed to create Sub2Api request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-api-key", s.config.APIKey)
	response, err := s.client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return requestCtx.Err()
		}
		return errors.New("Sub2Api request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Sub2Api request returned status %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	defer wipe(body)
	if err != nil {
		if requestCtx.Err() != nil {
			return requestCtx.Err()
		}
		return errors.New("Sub2Api returned an invalid response")
	}
	if len(body) > maxResponseBytes {
		return errors.New("Sub2Api returned an oversized response")
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return errors.New("Sub2Api returned an invalid response")
	}
	return nil
}

func businessSuccess(code *int) bool {
	return code != nil && *code == 0
}

func normalizeProvider(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "openai":
		return platform.ProviderOpenAI, true
	case "anthropic":
		return platform.ProviderAnthropic, true
	case "gemini":
		return platform.ProviderGemini, true
	case "antigravity":
		return platform.ProviderAntigravity, true
	default:
		return platform.ProviderUnknown, false
	}
}

func normalizeAccountType(raw string) (platform.AssetKind, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "apikey", "upstream":
		return platform.AssetStaticAPIKey, true
	case "oauth", "setup-token", "service_account":
		return platform.AssetOAuthFile, false
	default:
		return platform.AssetStaticAPIKey, false
	}
}

func sanitizeMetadataURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func normalizeGroupIDs(values []int64) string {
	ids := append([]int64(nil), values...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, 0, len(ids))
	var previous int64
	for i, id := range ids {
		if id <= 0 || (i > 0 && id == previous) {
			continue
		}
		parts = append(parts, strconv.FormatInt(id, 10))
		previous = id
	}
	return strings.Join(parts, ",")
}

func normalizeGroupNames(groups []accountGroupDTO) string {
	names := make([]string, 0, len(groups))
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (response *secretExportEnvelope) wipe() {
	if response == nil || response.Data == nil {
		return
	}
	for i := range response.Data.Accounts {
		wipe(response.Data.Accounts[i].Credentials.APIKey)
		response.Data.Accounts[i].Credentials.APIKey = nil
	}
}

func wipe(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
