package cliproxyapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

var (
	ErrChannelNotFound      = errors.New("CLIProxyAPI target channel not found")
	ErrChannelIDUnavailable = errors.New("CLIProxyAPI created a channel but did not expose its id")
)

type TargetConfig struct {
	TargetID               string
	BaseURL                string
	ManagementKey          string
	UseManagementKeyHeader bool
	RequestTimeout         time.Duration
}

type Target struct {
	config  TargetConfig
	client  *http.Client
	catalog *platform.ProviderCatalog
}

type targetAuthFilesResponse struct {
	Files []targetAuthEntry `json:"files"`
}

type targetAuthEntry struct {
	ID            string `json:"id"`
	AuthIndex     string `json:"auth_index"`
	AuthIndexDash string `json:"auth-index"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Provider      string `json:"provider"`
	Status        string `json:"status"`
	Priority      int    `json:"priority"`
	Disabled      bool   `json:"disabled"`
	Unavailable   bool   `json:"unavailable"`
	RuntimeOnly   bool   `json:"runtime_only"`
}

func NewTarget(cfg TargetConfig, client *http.Client) (*Target, error) {
	cfg.TargetID = strings.TrimSpace(cfg.TargetID)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.ManagementKey = strings.TrimSpace(cfg.ManagementKey)
	if cfg.TargetID == "" {
		return nil, errors.New("target id is required")
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
	return &Target{config: cfg, client: client, catalog: platform.DefaultCatalog()}, nil
}

func (t *Target) Capabilities() platform.TargetCapabilities {
	nativeAndStatic := platform.ProviderCapability{Modes: []platform.SyncMode{platform.SyncModeStaticKey, platform.SyncModeNativeAuthFile}}
	nativeOnly := platform.ProviderCapability{Modes: []platform.SyncMode{platform.SyncModeNativeAuthFile}}
	return platform.TargetCapabilities{
		Platform:         "cliproxyapi",
		NativeAuthSchema: "cpa-auth-v1",
		Providers: map[string]platform.ProviderCapability{
			platform.ProviderOpenAI:      {Modes: []platform.SyncMode{platform.SyncModeStaticKey, platform.SyncModeProxyEndpoint}},
			platform.ProviderAnthropic:   nativeAndStatic,
			platform.ProviderGemini:      nativeAndStatic,
			platform.ProviderCodex:       nativeAndStatic,
			platform.ProviderXAI:         nativeAndStatic,
			platform.ProviderVertex:      nativeAndStatic,
			platform.ProviderVertexAI:    nativeAndStatic,
			platform.ProviderAIStudio:    nativeAndStatic,
			platform.ProviderAntigravity: nativeOnly,
			platform.ProviderKimi:        nativeOnly,
			platform.ProviderKiro:        nativeOnly,
		},
	}
}

func (t *Target) ListChannels(ctx context.Context) ([]platform.Channel, error) {
	entries, err := t.listAuthEntries(ctx)
	if err != nil {
		return nil, err
	}
	channels := make([]platform.Channel, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name
		if name == "" {
			name = entry.ID
		}
		models, err := t.listTargetModels(ctx, name)
		if err != nil {
			return nil, err
		}
		descriptor := t.catalog.FromCLIProxyAPI(entry.rawProvider())
		channels = append(channels, platform.Channel{
			ID:       entry.ID,
			Name:     name,
			Provider: descriptor.ID,
			RawType:  entry.rawProvider(),
			Models:   models,
			Group:    "default",
			Priority: entry.Priority,
			Weight:   100,
			Enabled:  entry.enabled(),
		})
	}
	return channels, nil
}

func (t *Target) CreateChannel(ctx context.Context, input platform.CreateChannelInput) (platform.Channel, error) {
	if err := validateTargetSettings(input.Group, input.Weight); err != nil {
		return platform.Channel{}, err
	}
	if len(input.Secret) == 0 {
		return platform.Channel{}, platform.ErrSecretUnavailable
	}
	route, err := targetRouteForInput(input)
	if err != nil {
		return platform.Channel{}, err
	}
	if input.Mode == platform.SyncModeNativeAuthFile {
		if err := validateNativeAuthFile(input.Provider, input.Secret); err != nil {
			return platform.Channel{}, err
		}
	}
	before, err := t.listAuthEntries(ctx)
	if err != nil {
		return platform.Channel{}, err
	}
	beforeIDs := make(map[string]struct{}, len(before))
	for _, entry := range before {
		beforeIDs[entry.ID] = struct{}{}
	}

	switch input.Mode {
	case platform.SyncModeNativeAuthFile:
		if err := t.uploadAuthFile(ctx, input); err != nil {
			return platform.Channel{}, err
		}
	case platform.SyncModeStaticKey, platform.SyncModeProxyEndpoint:
		if err := t.appendStaticConfig(ctx, input, route); err != nil {
			return platform.Channel{}, err
		}
	default:
		return platform.Channel{}, platform.ErrIncompatibleTarget
	}

	created, err := t.findCreatedEntry(ctx, beforeIDs, route.rawProviders)
	if err != nil {
		return platform.Channel{}, err
	}
	priority := input.Priority
	weight := input.Weight
	if weight == 0 {
		weight = 100
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = created.Name
	}
	return platform.Channel{
		ID:       created.ID,
		Name:     name,
		Provider: input.Provider,
		RawType:  created.rawProvider(),
		BaseURL:  strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"),
		Models:   append([]string(nil), input.Models...),
		Group:    "default",
		Priority: priority,
		Weight:   weight,
		Enabled:  true,
	}, nil
}

func (t *Target) UpdateChannel(ctx context.Context, id string, input platform.UpdateChannelInput) (platform.Channel, error) {
	if err := validateTargetSettings(input.Group, input.Weight); err != nil {
		return platform.Channel{}, err
	}
	entry, err := t.findEntry(ctx, id)
	if err != nil {
		return platform.Channel{}, err
	}
	descriptor := t.catalog.FromCLIProxyAPI(entry.rawProvider())
	if entry.RuntimeOnly {
		route, ok := targetRouteForRawProvider(entry.rawProvider())
		if !ok {
			return platform.Channel{}, platform.ErrIncompatibleTarget
		}
		if err := t.rewriteStaticConfig(ctx, entry, route, func(config map[string]any) {
			config["priority"] = input.Priority
			if baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"); baseURL != "" {
				config["base-url"] = baseURL
			}
			if len(input.Models) > 0 {
				config["models"] = configModels(input.Models)
			}
			if route.rootField == "openai-compatibility" && strings.TrimSpace(input.Name) != "" {
				config["name"] = strings.TrimSpace(input.Name)
			}
		}); err != nil {
			return platform.Channel{}, err
		}
	} else if err := t.patchNativePriority(ctx, entry, input.Priority); err != nil {
		return platform.Channel{}, err
	}
	if err := t.patchStatus(ctx, entry.ID, !input.Enabled); err != nil {
		return platform.Channel{}, err
	}
	weight := input.Weight
	if weight == 0 {
		weight = 100
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = entry.Name
	}
	return platform.Channel{
		ID:       entry.ID,
		Name:     name,
		Provider: descriptor.ID,
		RawType:  entry.rawProvider(),
		BaseURL:  strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"),
		Models:   append([]string(nil), input.Models...),
		Group:    "default",
		Priority: input.Priority,
		Weight:   weight,
		Enabled:  input.Enabled,
	}, nil
}

func (t *Target) DeleteChannel(ctx context.Context, id string) error {
	entry, err := t.findEntry(ctx, id)
	if err != nil {
		return err
	}
	if !entry.RuntimeOnly {
		name := entry.Name
		if name == "" {
			name = entry.ID
		}
		requestURL := t.config.BaseURL + "/v0/management/auth-files?" + url.Values{"name": []string{name}}.Encode()
		return t.doStatus(ctx, http.MethodDelete, requestURL, nil, "")
	}
	route, ok := targetRouteForRawProvider(entry.rawProvider())
	if !ok {
		return platform.ErrIncompatibleTarget
	}
	return t.rewriteStaticConfig(ctx, entry, route, nil)
}

type targetRoute struct {
	endpoint     string
	rootField    string
	rawProviders map[string]struct{}
}

func targetRouteForInput(input platform.CreateChannelInput) (targetRoute, error) {
	if input.Mode == platform.SyncModeNativeAuthFile {
		if providerSupportsNative(input.Provider) {
			return targetRoute{rawProviders: rawProviderSet(input.Provider)}, nil
		}
		return targetRoute{}, platform.ErrIncompatibleTarget
	}
	if input.Mode == platform.SyncModeProxyEndpoint {
		if strings.TrimSpace(input.BaseURL) == "" {
			return targetRoute{}, platform.ErrIncompatibleTarget
		}
		return targetRoute{
			endpoint:     "openai-compatibility",
			rootField:    "openai-compatibility",
			rawProviders: map[string]struct{}{"openai": {}, "openai-compatibility": {}},
		}, nil
	}
	if input.Mode != platform.SyncModeStaticKey {
		return targetRoute{}, platform.ErrIncompatibleTarget
	}
	switch input.Provider {
	case platform.ProviderGemini, platform.ProviderAIStudio:
		return route("gemini-api-key", "gemini", "aistudio"), nil
	case platform.ProviderAnthropic:
		return route("claude-api-key", "claude", "anthropic"), nil
	case platform.ProviderCodex:
		return route("codex-api-key", "codex"), nil
	case platform.ProviderXAI:
		return route("xai-api-key", "xai"), nil
	case platform.ProviderVertex, platform.ProviderVertexAI:
		return route("vertex-api-key", "vertex"), nil
	case platform.ProviderOpenAI:
		return route("openai-compatibility", "openai", "openai-compatibility"), nil
	default:
		return targetRoute{}, platform.ErrIncompatibleTarget
	}
}

func targetRouteForRawProvider(provider string) (targetRoute, bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini", "aistudio":
		return route("gemini-api-key", "gemini", "aistudio"), true
	case "claude", "anthropic":
		return route("claude-api-key", "claude", "anthropic"), true
	case "codex":
		return route("codex-api-key", "codex"), true
	case "xai":
		return route("xai-api-key", "xai"), true
	case "vertex":
		return route("vertex-api-key", "vertex"), true
	case "openai", "openai-compatibility":
		return route("openai-compatibility", "openai", "openai-compatibility"), true
	default:
		return targetRoute{}, false
	}
}

func route(endpoint string, providers ...string) targetRoute {
	set := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		set[provider] = struct{}{}
	}
	return targetRoute{endpoint: endpoint, rootField: endpoint, rawProviders: set}
}

func providerSupportsNative(provider string) bool {
	switch provider {
	case platform.ProviderAnthropic, platform.ProviderGemini, platform.ProviderCodex,
		platform.ProviderXAI, platform.ProviderVertex, platform.ProviderVertexAI,
		platform.ProviderAIStudio, platform.ProviderAntigravity, platform.ProviderKimi,
		platform.ProviderKiro:
		return true
	default:
		return false
	}
}

func validateNativeAuthFile(provider string, secret []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(secret, &fields); err != nil || fields == nil {
		return incompatibleNativeAuthFile()
	}
	defer wipeRawFields(fields)

	var authType string
	if err := json.Unmarshal(fields["type"], &authType); err != nil {
		return incompatibleNativeAuthFile()
	}
	authType = strings.ToLower(strings.TrimSpace(authType))
	if !nativeAuthTypeMatchesProvider(provider, authType) {
		return incompatibleNativeAuthFile()
	}
	if authType == "vertex" {
		if !hasServiceAccountSchema(fields) {
			return incompatibleNativeAuthFile()
		}
		return nil
	}
	if !hasOAuthCredential(fields) {
		return incompatibleNativeAuthFile()
	}
	return nil
}

func nativeAuthTypeMatchesProvider(provider, authType string) bool {
	switch provider {
	case platform.ProviderAnthropic:
		return authType == "claude" || authType == "anthropic"
	case platform.ProviderGemini:
		return authType == "gemini" || authType == "gemini-cli"
	case platform.ProviderCodex:
		return authType == "codex"
	case platform.ProviderXAI:
		return authType == "xai"
	case platform.ProviderVertex, platform.ProviderVertexAI:
		return authType == "vertex"
	case platform.ProviderAIStudio:
		return authType == "aistudio" || authType == "gemini-cli"
	case platform.ProviderAntigravity:
		return authType == "antigravity"
	case platform.ProviderKimi:
		return authType == "kimi"
	case platform.ProviderKiro:
		return authType == "kiro"
	default:
		return false
	}
}

func hasOAuthCredential(fields map[string]json.RawMessage) bool {
	for _, field := range []string{"access_token", "refresh_token", "api_key"} {
		if hasNonBlankJSONString(fields[field]) {
			return true
		}
	}
	var token map[string]json.RawMessage
	if err := json.Unmarshal(fields["token"], &token); err != nil || token == nil {
		return false
	}
	defer wipeRawFields(token)
	return hasNonBlankJSONString(token["access_token"]) || hasNonBlankJSONString(token["refresh_token"])
}

func hasServiceAccountSchema(fields map[string]json.RawMessage) bool {
	if !hasNonBlankJSONString(fields["project_id"]) {
		return false
	}
	var serviceAccount map[string]json.RawMessage
	if err := json.Unmarshal(fields["service_account"], &serviceAccount); err != nil || serviceAccount == nil {
		return false
	}
	defer wipeRawFields(serviceAccount)
	return hasNonBlankJSONString(serviceAccount["client_email"]) && hasNonBlankJSONString(serviceAccount["private_key"])
}

func hasNonBlankJSONString(raw json.RawMessage) bool {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return strings.TrimSpace(value) != ""
}

func wipeRawFields(fields map[string]json.RawMessage) {
	for key, raw := range fields {
		wipe(raw)
		delete(fields, key)
	}
}

func incompatibleNativeAuthFile() error {
	return fmt.Errorf("%w: native auth file schema is incompatible", platform.ErrIncompatibleTarget)
}

func rawProviderSet(provider string) map[string]struct{} {
	set := map[string]struct{}{}
	switch provider {
	case platform.ProviderAnthropic:
		set["claude"] = struct{}{}
		set["anthropic"] = struct{}{}
	case platform.ProviderGemini:
		set["gemini"] = struct{}{}
		set["gemini-cli"] = struct{}{}
	case platform.ProviderCodex:
		set["codex"] = struct{}{}
	case platform.ProviderXAI:
		set["xai"] = struct{}{}
	case platform.ProviderVertex, platform.ProviderVertexAI:
		set["vertex"] = struct{}{}
	case platform.ProviderAIStudio:
		set["aistudio"] = struct{}{}
	case platform.ProviderAntigravity:
		set["antigravity"] = struct{}{}
	case platform.ProviderKimi:
		set["kimi"] = struct{}{}
	case platform.ProviderKiro:
		set["kiro"] = struct{}{}
	}
	return set
}

func validateTargetSettings(group string, weight int) error {
	group = strings.TrimSpace(group)
	if group != "" && !strings.EqualFold(group, "default") {
		return fmt.Errorf("%w: CLIProxyAPI does not support channel groups", platform.ErrIncompatibleTarget)
	}
	if weight != 0 && weight != 100 {
		return fmt.Errorf("%w: CLIProxyAPI does not support channel weights", platform.ErrIncompatibleTarget)
	}
	return nil
}

func (t *Target) appendStaticConfig(ctx context.Context, input platform.CreateChannelInput, route targetRoute) error {
	entries, err := t.readStaticConfig(ctx, route)
	if err != nil {
		return err
	}
	defer clearConfigSecrets(entries)
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	entry := map[string]any{
		"priority": input.Priority,
		"base-url": baseURL,
		"models":   configModels(input.Models),
	}
	if route.rootField == "openai-compatibility" {
		name := stableConfigName(input)
		entry["name"] = name
		entry["disabled"] = false
		entry["api-key-entries"] = []any{map[string]any{"api-key": string(input.Secret)}}
	} else {
		entry["api-key"] = string(input.Secret)
	}
	entries = append(entries, entry)
	return t.writeStaticConfig(ctx, route, entries)
}

func (t *Target) uploadAuthFile(ctx context.Context, input platform.CreateChannelInput) error {
	name := stableAuthFileName(input.AssetID)
	requestURL := t.config.BaseURL + "/v0/management/auth-files?" + url.Values{"name": []string{name}}.Encode()
	return t.doStatus(ctx, http.MethodPost, requestURL, input.Secret, "application/json")
}

func stableAuthFileName(assetID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(assetID)))
	return "synchub-" + hex.EncodeToString(digest[:8]) + ".json"
}

func stableConfigName(input platform.CreateChannelInput) string {
	name := strings.TrimSpace(input.Name)
	if name != "" {
		name = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, name)
		name = strings.Trim(name, "-")
	}
	if name == "" {
		digest := sha256.Sum256([]byte(input.AssetID))
		name = "synchub-" + hex.EncodeToString(digest[:6])
	}
	return name
}

func configModels(models []string) []any {
	result := make([]any, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		result = append(result, map[string]any{"name": model, "alias": model})
	}
	return result
}

func (t *Target) findCreatedEntry(ctx context.Context, before map[string]struct{}, providers map[string]struct{}) (targetAuthEntry, error) {
	waitLimit := t.config.RequestTimeout
	if waitLimit > 2*time.Second {
		waitLimit = 2 * time.Second
	}
	deadline := time.Now().Add(waitLimit)
	for {
		entries, err := t.listAuthEntries(ctx)
		if err != nil {
			return targetAuthEntry{}, err
		}
		matches := make([]targetAuthEntry, 0, 1)
		for _, entry := range entries {
			if _, existed := before[entry.ID]; existed {
				continue
			}
			if !routeAcceptsProvider(providers, entry.rawProvider()) {
				continue
			}
			matches = append(matches, entry)
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return targetAuthEntry{}, ErrChannelIDUnavailable
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return targetAuthEntry{}, ErrChannelIDUnavailable
		}
		pause := 50 * time.Millisecond
		if remaining < pause {
			pause = remaining
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return targetAuthEntry{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func routeAcceptsProvider(providers map[string]struct{}, rawProvider string) bool {
	rawProvider = strings.ToLower(strings.TrimSpace(rawProvider))
	if _, expected := providers[rawProvider]; expected {
		return true
	}
	_, isOpenAICompatibilityRoute := providers["openai-compatibility"]
	return isOpenAICompatibilityRoute && platform.IsCLIProxyOpenAICompatibleProvider(rawProvider)
}

func (t *Target) findEntry(ctx context.Context, id string) (targetAuthEntry, error) {
	id = strings.TrimSpace(id)
	entries, err := t.listAuthEntries(ctx)
	if err != nil {
		return targetAuthEntry{}, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return targetAuthEntry{}, ErrChannelNotFound
}

func (t *Target) listAuthEntries(ctx context.Context) ([]targetAuthEntry, error) {
	var response targetAuthFilesResponse
	if err := t.doJSON(ctx, http.MethodGet, t.config.BaseURL+"/v0/management/auth-files", nil, &response); err != nil {
		return nil, err
	}
	for i := range response.Files {
		response.Files[i].ID = strings.TrimSpace(response.Files[i].ID)
		response.Files[i].Name = strings.TrimSpace(response.Files[i].Name)
		if response.Files[i].AuthIndex == "" {
			response.Files[i].AuthIndex = strings.TrimSpace(response.Files[i].AuthIndexDash)
		}
	}
	return response.Files, nil
}

func (t *Target) listTargetModels(ctx context.Context, name string) ([]string, error) {
	requestURL := t.config.BaseURL + "/v0/management/auth-files/models?" + url.Values{"name": []string{name}}.Encode()
	var response authModelsResponse
	if err := t.doJSON(ctx, http.MethodGet, requestURL, nil, &response); err != nil {
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

func (entry targetAuthEntry) rawProvider() string {
	provider := strings.ToLower(strings.TrimSpace(entry.Provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(entry.Type))
	}
	return provider
}

func (entry targetAuthEntry) enabled() bool {
	status := strings.ToLower(strings.TrimSpace(entry.Status))
	return !entry.Disabled && !entry.Unavailable && status != "disabled" && status != "error"
}

func (t *Target) readStaticConfig(ctx context.Context, route targetRoute) ([]map[string]any, error) {
	var response map[string]json.RawMessage
	requestURL := t.config.BaseURL + "/v0/management/" + route.endpoint
	if err := t.doJSON(ctx, http.MethodGet, requestURL, nil, &response); err != nil {
		return nil, err
	}
	raw, exists := response[route.rootField]
	if !exists {
		return nil, errors.New("CLIProxyAPI returned an invalid static configuration response")
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, errors.New("CLIProxyAPI returned an invalid static configuration response")
	}
	return entries, nil
}

func (t *Target) writeStaticConfig(ctx context.Context, route targetRoute, entries []map[string]any) error {
	body, err := json.Marshal(entries)
	if err != nil {
		return errors.New("failed to encode CLIProxyAPI static configuration")
	}
	defer wipe(body)
	requestURL := t.config.BaseURL + "/v0/management/" + route.endpoint
	return t.doStatus(ctx, http.MethodPut, requestURL, body, "application/json")
}

func (t *Target) rewriteStaticConfig(ctx context.Context, entry targetAuthEntry, route targetRoute, mutate func(map[string]any)) error {
	if strings.TrimSpace(entry.AuthIndex) == "" {
		return ErrChannelNotFound
	}
	entries, err := t.readStaticConfig(ctx, route)
	if err != nil {
		return err
	}
	defer clearConfigSecrets(entries)
	index := -1
	for i := range entries {
		if containsAuthIndex(entries[i], entry.AuthIndex) {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrChannelNotFound
	}
	if mutate == nil {
		entries = append(entries[:index], entries[index+1:]...)
	} else {
		mutate(entries[index])
	}
	return t.writeStaticConfig(ctx, route, entries)
}

func containsAuthIndex(value any, wanted string) bool {
	switch current := value.(type) {
	case map[string]any:
		if authIndex, _ := current["auth-index"].(string); strings.TrimSpace(authIndex) == wanted {
			return true
		}
		if authIndex, _ := current["auth_index"].(string); strings.TrimSpace(authIndex) == wanted {
			return true
		}
		for _, child := range current {
			if containsAuthIndex(child, wanted) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsAuthIndex(child, wanted) {
				return true
			}
		}
	}
	return false
}

func (t *Target) patchStatus(ctx context.Context, id string, disabled bool) error {
	body, err := json.Marshal(map[string]any{"name": id, "disabled": disabled})
	if err != nil {
		return errors.New("failed to encode CLIProxyAPI status update")
	}
	defer wipe(body)
	return t.doStatus(ctx, http.MethodPatch, t.config.BaseURL+"/v0/management/auth-files/status", body, "application/json")
}

func (t *Target) patchNativePriority(ctx context.Context, entry targetAuthEntry, priority int) error {
	body, err := json.Marshal(map[string]any{"name": entry.ID, "priority": priority})
	if err != nil {
		return errors.New("failed to encode CLIProxyAPI auth update")
	}
	defer wipe(body)
	return t.doStatus(ctx, http.MethodPatch, t.config.BaseURL+"/v0/management/auth-files/fields", body, "application/json")
}

func clearConfigSecrets(entries []map[string]any) {
	for _, entry := range entries {
		clearConfigValue(entry)
	}
}

func clearConfigValue(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "api-key" {
				current[key] = ""
				continue
			}
			clearConfigValue(child)
		}
	case []any:
		for _, child := range current {
			clearConfigValue(child)
		}
	}
}

func (t *Target) doJSON(ctx context.Context, method, requestURL string, body []byte, destination any) error {
	response, err := t.do(ctx, method, requestURL, body, "application/json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("CLIProxyAPI returned an invalid target response")
	}
	return nil
}

func (t *Target) doStatus(ctx context.Context, method, requestURL string, body []byte, contentType string) error {
	response, err := t.do(ctx, method, requestURL, body, contentType)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return nil
}

func (t *Target) do(ctx context.Context, method, requestURL string, body []byte, contentType string) (*http.Response, error) {
	requestCtx, cancel := context.WithTimeout(ctx, t.config.RequestTimeout)
	request, err := http.NewRequestWithContext(requestCtx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, errors.New("failed to create CLIProxyAPI target request")
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if t.config.UseManagementKeyHeader {
		request.Header.Set("X-Management-Key", t.config.ManagementKey)
	} else {
		request.Header.Set("Authorization", "Bearer "+t.config.ManagementKey)
	}
	response, err := t.client.Do(request)
	if err != nil {
		cancel()
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, errors.New("CLIProxyAPI target request failed")
	}
	response.Body = &cancelOnCloseReadCloser{ReadCloser: response.Body, cancel: cancel}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		_ = response.Body.Close()
		return nil, fmt.Errorf("CLIProxyAPI target request returned status %d", response.StatusCode)
	}
	return response, nil
}
