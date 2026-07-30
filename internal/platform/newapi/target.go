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
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

var (
	ErrChannelNotFound      = errors.New("New API target channel not found")
	ErrChannelIDUnavailable = errors.New("New API created a channel but did not expose its id")
)

const newAPITypeVertexAI = 41

type TargetConfig struct {
	TargetID       string
	BaseURL        string
	AccessToken    string
	UserID         int
	PageSize       int
	RequestTimeout time.Duration
}

type Target struct {
	config    TargetConfig
	transport transport
	catalog   *platform.ProviderCatalog
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

type targetMutationResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

type targetChannelResponse struct {
	Success bool            `json:"success"`
	Data    channelResponse `json:"data"`
}

type createChannelRequest struct {
	Mode         string               `json:"mode"`
	MultiKeyMode string               `json:"multi_key_mode,omitempty"`
	Channel      createChannelPayload `json:"channel"`
}

type createChannelPayload struct {
	Type     int    `json:"type"`
	Key      string `json:"key"`
	Status   int    `json:"status"`
	Name     string `json:"name"`
	Weight   int    `json:"weight"`
	BaseURL  string `json:"base_url"`
	Models   string `json:"models"`
	Group    string `json:"group"`
	Priority int    `json:"priority"`
}

type updateChannelPayload struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Weight   int    `json:"weight"`
	BaseURL  string `json:"base_url"`
	Models   string `json:"models"`
	Group    string `json:"group"`
	Priority int    `json:"priority"`
}

var canonicalNewAPIType = map[string]int{
	platform.ProviderOpenAI:      1,
	platform.ProviderAzure:       3,
	platform.ProviderOllama:      4,
	platform.ProviderPaLM:        11,
	platform.ProviderAnthropic:   14,
	platform.ProviderBaidu:       15,
	platform.ProviderZhipu:       16,
	platform.ProviderQwen:        17,
	platform.ProviderXunfei:      18,
	platform.ProviderOpenRouter:  20,
	platform.ProviderTencent:     23,
	platform.ProviderGemini:      24,
	platform.ProviderMoonshot:    25,
	platform.ProviderPerplexity:  27,
	platform.ProviderAWS:         33,
	platform.ProviderCohere:      34,
	platform.ProviderMiniMax:     35,
	platform.ProviderDify:        37,
	platform.ProviderJina:        38,
	platform.ProviderCloudflare:  39,
	platform.ProviderSiliconFlow: 40,
	platform.ProviderMistral:     42,
	platform.ProviderDeepSeek:    43,
	platform.ProviderVolcEngine:  45,
	platform.ProviderXAI:         48,
	platform.ProviderCoze:        49,
	platform.ProviderReplicate:   56,
	platform.ProviderCodex:       57,
	platform.ProviderVertexAI:    newAPITypeVertexAI,

	// These source variants have explicit New API equivalents.
	platform.ProviderAIStudio: 24,
	platform.ProviderKimi:     25,
}

var proxyCapableProviders = func() map[string]struct{} {
	providers := make(map[string]struct{}, len(canonicalNewAPIType)+4)
	for provider := range canonicalNewAPIType {
		providers[provider] = struct{}{}
	}
	providers[platform.ProviderAntigravity] = struct{}{}
	providers[platform.ProviderKiro] = struct{}{}
	providers[platform.ProviderVertexAI] = struct{}{}
	providers[platform.ProviderVertex] = struct{}{}
	return providers
}()

func NewTarget(cfg TargetConfig, client *http.Client) (*Target, error) {
	cfg.TargetID = strings.TrimSpace(cfg.TargetID)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.AccessToken = strings.TrimSpace(cfg.AccessToken)
	if cfg.TargetID == "" {
		return nil, errors.New("target id is required")
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
	return &Target{
		config: cfg,
		transport: transport{
			baseURL:        cfg.BaseURL,
			accessToken:    cfg.AccessToken,
			userID:         cfg.UserID,
			requestTimeout: cfg.RequestTimeout,
			client:         client,
		},
		catalog: platform.DefaultCatalog(),
	}, nil
}

func (t *Target) Capabilities() platform.TargetCapabilities {
	providers := make(map[string]platform.ProviderCapability, len(proxyCapableProviders))
	for provider := range proxyCapableProviders {
		modes := []platform.SyncMode{platform.SyncModeProxyEndpoint}
		if _, supportsStatic := canonicalNewAPIType[provider]; supportsStatic {
			modes = append([]platform.SyncMode{platform.SyncModeStaticKey}, modes...)
		}
		providers[provider] = platform.ProviderCapability{Modes: modes}
	}
	return platform.TargetCapabilities{Platform: "newapi", Providers: providers}
}

func (t *Target) ListChannels(ctx context.Context) ([]platform.Channel, error) {
	channels, err := t.listAllChannels(ctx)
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func (t *Target) listAllChannels(ctx context.Context) ([]platform.Channel, error) {
	channels := make([]platform.Channel, 0)
	seen := make(map[string]struct{})
	for requestedPage := 1; ; requestedPage++ {
		query := url.Values{}
		query.Set("p", strconv.Itoa(requestedPage))
		query.Set("page_size", strconv.Itoa(t.config.PageSize))

		var response channelListResponse
		if err := t.transport.do(ctx, request{method: http.MethodGet, path: "/api/channel/", query: query.Encode()}, &response); err != nil {
			return nil, err
		}
		if !response.Success {
			return nil, errors.New("New API target request was rejected")
		}
		responsePage := response.Data.Page
		if responsePage == 0 {
			responsePage = requestedPage
		}
		if responsePage != requestedPage || response.Data.Total < 0 {
			return nil, errors.New("New API returned invalid target pagination")
		}
		if response.Data.Total < len(channels)+len(response.Data.Items) {
			return nil, errors.New("New API returned inconsistent target pagination")
		}

		for _, item := range response.Data.Items {
			if item.ID <= 0 {
				return nil, errors.New("New API returned an invalid target channel")
			}
			id := strconv.Itoa(item.ID)
			if _, duplicate := seen[id]; duplicate {
				return nil, errors.New("New API returned duplicate target channels")
			}
			seen[id] = struct{}{}
			channels = append(channels, t.normalizeChannel(item))
		}

		if len(channels) == response.Data.Total {
			return channels, nil
		}
		if len(response.Data.Items) == 0 {
			return nil, errors.New("New API returned incomplete target pagination")
		}
	}
}

func (t *Target) normalizeChannel(item channelResponse) platform.Channel {
	descriptor := t.catalog.FromNewAPI(item.Type)
	return platform.Channel{
		ID:       strconv.Itoa(item.ID),
		Name:     strings.TrimSpace(item.Name),
		Provider: descriptor.ID,
		RawType:  strconv.Itoa(item.Type),
		BaseURL:  strings.TrimRight(strings.TrimSpace(item.BaseURL), "/"),
		Models:   splitCSV(item.Models),
		Group:    strings.TrimSpace(item.Group),
		Priority: item.Priority,
		Weight:   item.Weight,
		Enabled:  item.Status == 1,
	}
}

func (t *Target) CreateChannel(ctx context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
	rawType, err := newAPITypeForInput(input)
	if err != nil {
		return platform.Channel{}, err
	}
	if len(input.Secret) == 0 {
		return platform.Channel{}, platform.ErrSecretUnavailable
	}
	baseURL, err := normalizeTargetChannelBaseURL(input.BaseURL, input.Mode == platform.SyncModeProxyEndpoint)
	if err != nil {
		return platform.Channel{}, err
	}
	models := normalizeTargetModels(input.Models)
	name := strings.TrimSpace(input.Name)
	group := normalizeTargetGroup(input.Group)

	before, err := t.listAllChannels(ctx)
	if err != nil {
		return platform.Channel{}, err
	}
	beforeIDs := make(map[string]struct{}, len(before))
	for _, channel := range before {
		beforeIDs[channel.ID] = struct{}{}
	}

	key, mode, err := encodeTargetSecret(input.Secret, rawType)
	if err != nil {
		return platform.Channel{}, err
	}
	createReq := createChannelRequest{
		Mode: mode,
		Channel: createChannelPayload{
			Type: rawType, Key: key, Status: 1, Name: name, Weight: input.Weight,
			BaseURL: baseURL, Models: strings.Join(models, ","), Group: group, Priority: input.Priority,
		},
	}
	if mode == "multi_to_single" {
		createReq.MultiKeyMode = "polling"
	}

	var response targetMutationResponse
	err = t.transport.do(ctx, request{method: http.MethodPost, path: "/api/channel/", body: createReq}, &response)
	createReq.Channel.Key = ""
	key = ""
	if err != nil {
		return platform.Channel{}, err
	}
	if !response.Success {
		wipeTargetBytes(response.Data)
		return platform.Channel{}, errors.New("New API target request was rejected")
	}

	id := mutationChannelID(response.Data)
	wipeTargetBytes(response.Data)
	response.Data = nil
	if id > 0 {
		return t.channelFromInput(strconv.Itoa(id), rawType, name, baseURL, models, group, input), nil
	}
	return t.waitForCreatedChannel(ctx, beforeIDs, rawType, name)
}

func (t *Target) channelFromInput(id string, rawType int, name, baseURL string, models []string, group string, input platform.CreateChannelInput) platform.Channel {
	descriptor := t.catalog.FromNewAPI(rawType)
	return platform.Channel{
		ID: id, Name: name, Provider: descriptor.ID, RawType: strconv.Itoa(rawType), BaseURL: baseURL,
		Models: append([]string(nil), models...), Group: group, Priority: input.Priority, Weight: input.Weight, Enabled: true,
	}
}

func (t *Target) waitForCreatedChannel(ctx context.Context, before map[string]struct{}, rawType int, name string) (platform.Channel, error) {
	waitLimit := t.config.RequestTimeout
	if waitLimit > 2*time.Second {
		waitLimit = 2 * time.Second
	}
	deadline := time.Now().Add(waitLimit)
	for {
		channels, err := t.listAllChannels(ctx)
		if err != nil {
			return platform.Channel{}, err
		}
		matches := make([]platform.Channel, 0, 1)
		for _, channel := range channels {
			if _, existed := before[channel.ID]; existed || channel.RawType != strconv.Itoa(rawType) {
				continue
			}
			if name != "" && channel.Name != name {
				continue
			}
			matches = append(matches, channel)
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return platform.Channel{}, ErrChannelIDUnavailable
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return platform.Channel{}, ErrChannelIDUnavailable
		}
		pause := 50 * time.Millisecond
		if remaining < pause {
			pause = remaining
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return platform.Channel{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (t *Target) UpdateChannel(ctx context.Context, id string, input platform.UpdateChannelInput) (platform.Channel, error) {
	numericID, err := parseTargetChannelID(id)
	if err != nil {
		return platform.Channel{}, err
	}
	baseURL, err := normalizeTargetChannelBaseURL(input.BaseURL, false)
	if err != nil {
		return platform.Channel{}, err
	}
	payload := updateChannelPayload{
		ID: numericID, Name: strings.TrimSpace(input.Name), Weight: input.Weight, BaseURL: baseURL,
		Models: strings.Join(normalizeTargetModels(input.Models), ","), Group: normalizeTargetGroup(input.Group), Priority: input.Priority,
	}
	var response targetMutationResponse
	if err := t.transport.do(ctx, request{method: http.MethodPut, path: "/api/channel/", body: payload}, &response); err != nil {
		return platform.Channel{}, err
	}
	if !response.Success {
		wipeTargetBytes(response.Data)
		return platform.Channel{}, errors.New("New API target request was rejected")
	}
	updatedChannel, hasUpdatedChannel := t.channelFromMutation(response.Data, numericID)
	wipeTargetBytes(response.Data)
	response.Data = nil

	status := 2
	if input.Enabled {
		status = 1
	}
	var statusResponse targetMutationResponse
	statusPath := "/api/channel/" + strconv.Itoa(numericID) + "/status"
	if err := t.transport.do(ctx, request{method: http.MethodPost, path: statusPath, body: struct {
		Status int `json:"status"`
	}{Status: status}}, &statusResponse); err != nil {
		return platform.Channel{}, err
	}
	if !statusResponse.Success {
		wipeTargetBytes(statusResponse.Data)
		return platform.Channel{}, errors.New("New API target request was rejected")
	}
	wipeTargetBytes(statusResponse.Data)
	statusResponse.Data = nil

	if hasUpdatedChannel {
		updatedChannel.Enabled = input.Enabled
		return updatedChannel, nil
	}
	channel, err := t.getChannel(ctx, numericID)
	if err != nil {
		return platform.Channel{}, err
	}
	channel.Enabled = input.Enabled
	return channel, nil
}

func (t *Target) DeleteChannel(ctx context.Context, id string) error {
	numericID, err := parseTargetChannelID(id)
	if err != nil {
		return err
	}
	var response targetMutationResponse
	if err := t.transport.do(ctx, request{method: http.MethodDelete, path: "/api/channel/" + strconv.Itoa(numericID)}, &response); err != nil {
		return err
	}
	if !response.Success {
		wipeTargetBytes(response.Data)
		return errors.New("New API target request was rejected")
	}
	wipeTargetBytes(response.Data)
	response.Data = nil
	return nil
}

func (t *Target) getChannel(ctx context.Context, id int) (platform.Channel, error) {
	var response targetChannelResponse
	if err := t.transport.get(ctx, "/api/channel/"+strconv.Itoa(id), "", &response); err != nil {
		return platform.Channel{}, err
	}
	if !response.Success || response.Data.ID != id {
		return platform.Channel{}, ErrChannelNotFound
	}
	return t.normalizeChannel(response.Data), nil
}

func (t *Target) channelFromMutation(raw json.RawMessage, expectedID int) (platform.Channel, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return platform.Channel{}, false
	}
	var response channelResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.ID != expectedID || response.Type <= 0 {
		return platform.Channel{}, false
	}
	return t.normalizeChannel(response), true
}

func newAPITypeForInput(input platform.CreateChannelInput) (int, error) {
	provider := strings.TrimSpace(input.Provider)
	if input.Mode == platform.SyncModeProxyEndpoint {
		if _, ok := proxyCapableProviders[provider]; !ok || !validTargetBaseURL(input.BaseURL) {
			return 0, platform.ErrIncompatibleTarget
		}
		return canonicalNewAPIType[platform.ProviderOpenAI], nil
	}
	if input.Mode != platform.SyncModeStaticKey {
		return 0, platform.ErrIncompatibleTarget
	}
	canonical, ok := canonicalNewAPIType[provider]
	if !ok {
		return 0, platform.ErrIncompatibleTarget
	}
	if strings.TrimSpace(input.RawType) == "" {
		return canonical, nil
	}
	rawType, err := strconv.Atoi(strings.TrimSpace(input.RawType))
	if err != nil || rawType <= 0 {
		return 0, platform.ErrIncompatibleTarget
	}
	descriptor := platform.DefaultCatalog().FromNewAPI(rawType)
	if descriptor.DiscoveryOnly || !providerMatchesDescriptor(provider, descriptor.ID) {
		return 0, platform.ErrIncompatibleTarget
	}
	return rawType, nil
}

func providerMatchesDescriptor(provider, descriptor string) bool {
	if provider == descriptor {
		return true
	}
	switch provider {
	case platform.ProviderAIStudio:
		return descriptor == platform.ProviderGemini
	case platform.ProviderVertex:
		return descriptor == platform.ProviderVertexAI
	case platform.ProviderKimi:
		return descriptor == platform.ProviderMoonshot
	default:
		return false
	}
}

func encodeTargetSecret(secret []byte, rawType int) (key, mode string, err error) {
	trimmed := strings.TrimSpace(string(secret))
	if trimmed == "" {
		return "", "", platform.ErrSecretUnavailable
	}
	if strings.HasPrefix(trimmed, "[") {
		keys, parseErr := parseMultiKeys(trimmed)
		if parseErr != nil {
			return "", "", errors.New("channel secret is not a valid multi-key value")
		}
		keys = normalizeTargetModels(keys)
		if len(keys) == 0 {
			return "", "", platform.ErrSecretUnavailable
		}
		if len(keys) == 1 {
			return keys[0], "single", nil
		}
		if rawType == newAPITypeVertexAI {
			return trimmed, "multi_to_single", nil
		}
		return strings.Join(keys, "\n"), "multi_to_single", nil
	}
	keys := normalizeTargetModels(strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n"))
	if len(keys) == 0 {
		return "", "", platform.ErrSecretUnavailable
	}
	if len(keys) == 1 {
		return keys[0], "single", nil
	}
	return strings.Join(keys, "\n"), "multi_to_single", nil
}

func mutationChannelID(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var response struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.ID <= 0 {
		return 0
	}
	return response.ID
}

func parseTargetChannelID(id string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil || parsed <= 0 {
		return 0, ErrChannelNotFound
	}
	return parsed, nil
}

func normalizeTargetModels(models []string) []string {
	result := make([]string, 0, len(models))
	for _, model := range models {
		if model = strings.TrimSpace(model); model != "" {
			result = append(result, model)
		}
	}
	return result
}

func normalizeTargetGroup(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return "default"
	}
	return group
}

func normalizeTargetChannelBaseURL(value string, required bool) (string, error) {
	normalized := strings.TrimRight(strings.TrimSpace(value), "/")
	if normalized == "" {
		if required {
			return "", platform.ErrIncompatibleTarget
		}
		return "", nil
	}
	if !validTargetBaseURL(normalized) {
		return "", fmt.Errorf("%w: channel base URL must be an absolute HTTP(S) URL", platform.ErrIncompatibleTarget)
	}
	return normalized, nil
}

func validTargetBaseURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
