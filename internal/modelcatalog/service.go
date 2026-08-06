package modelcatalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/platform/generic"
	"github.com/AkkunYo/SyncHub/internal/platform/newapi"
	"github.com/AkkunYo/SyncHub/internal/probe"
)

const (
	maxDiscoveryKeys  = 100
	maxModels         = 4096
	maxModelIDBytes   = 512
	maxResponseBytes  = int64(8 << 20)
	globalConcurrency = 4
	hostConcurrency   = 2
)

var errInvalidUpstreamResponse = errors.New("invalid upstream key metadata")

type resourceKey struct {
	upstreamID string
	keyID      string
}

type snapshot struct {
	upstreamID     string
	keyID          string
	models         []string
	verified       bool
	status         SnapshotStatus
	scope          SnapshotScope
	discoveredAt   time.Time
	probes         map[string]ModelProbe
	discoveryState string
}

type operationLimiter struct {
	mu              sync.Mutex
	activeKeys      map[resourceKey]struct{}
	activeUpstreams map[string]struct{}
	activeHosts     map[string]int
	activeTotal     int
}

type Service struct {
	store  ConfigStore
	client *http.Client
	prober Prober
	now    func() time.Time

	mu        sync.RWMutex
	snapshots map[resourceKey]snapshot
	limiter   operationLimiter
}

type discoveredModels struct {
	models []string
	at     time.Time
}

type remoteDiscoveryError struct {
	status     DiscoveryStatus
	code       string
	retryable  bool
	retryAfter time.Duration
}

func (e *remoteDiscoveryError) Error() string { return "model discovery failed" }

func NewService(store ConfigStore, client *http.Client, prober Prober) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if prober == nil {
		prober = probe.NewService(&clientCopy)
	}
	return &Service{
		store: store, client: &clientCopy, prober: prober, now: time.Now,
		snapshots: make(map[resourceKey]snapshot),
		limiter: operationLimiter{
			activeKeys: make(map[resourceKey]struct{}), activeUpstreams: make(map[string]struct{}),
			activeHosts: make(map[string]int),
		},
	}
}

func (s *Service) ListKeys(ctx context.Context, upstream config.UpstreamConfig, adapter platform.UpstreamAdapter) ([]Key, error) {
	if s == nil || ctx == nil || strings.TrimSpace(upstream.ID) == "" {
		return nil, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(upstream.Type)) {
	case "generic":
		return s.listGenericKeys(upstream), nil
	case "newapi":
		if adapter == nil {
			return nil, ErrUnsupported
		}
		return s.listNewAPIKeys(ctx, upstream, adapter)
	default:
		return nil, ErrUnsupported
	}
}

func (s *Service) listGenericKeys(upstream config.UpstreamConfig) []Key {
	configuredKeys := upstream.Keys
	// Store validation normally migrates the legacy api_key field into Keys,
	// but callers may hold an older or in-memory config snapshot. Keep the
	// catalog view aligned with the adapter resolver's default-key behavior.
	if len(configuredKeys) == 0 && strings.TrimSpace(upstream.APIKey) != "" {
		configuredKeys = []config.GenericKeyConfig{{
			ID: config.DefaultGenericKeyID, Name: upstream.Name,
			APIKey: upstream.APIKey, Enabled: true,
		}}
	}
	result := make([]Key, 0, len(configuredKeys))
	for _, configured := range configuredKeys {
		keyID := strings.TrimSpace(configured.ID)
		key := resourceKey{upstreamID: upstream.ID, keyID: keyID}
		s.ensureConfiguredSnapshot(key, configured.Models)
		snapshot, _ := s.snapshot(key)
		result = append(result, keySummary(snapshot, Key{
			ID: keyID, Name: configured.Name, Enabled: configured.Enabled, Source: "manual",
			CredentialPresent: strings.TrimSpace(configured.APIKey) != "", assetID: genericAssetID(upstream.ID, keyID),
		}))
	}
	return result
}

func (s *Service) listNewAPIKeys(ctx context.Context, upstream config.UpstreamConfig, adapter platform.UpstreamAdapter) ([]Key, error) {
	cursor := platform.PageCursor{}
	seen := map[platform.PageCursor]struct{}{cursor: {}}
	result := make([]Key, 0)
	seenIDs := make(map[string]struct{})
	for {
		page, err := adapter.ListAssets(ctx, cursor)
		if err != nil {
			return nil, err
		}
		for _, asset := range page.Assets {
			key, err := newAPIKey(upstream.ID, asset)
			if err != nil {
				return nil, err
			}
			if _, duplicate := seenIDs[key.ID]; duplicate {
				return nil, errInvalidUpstreamResponse
			}
			seenIDs[key.ID] = struct{}{}
			resource := resourceKey{upstreamID: upstream.ID, keyID: key.ID}
			s.ensureRuntimeSnapshot(resource)
			current, _ := s.snapshot(resource)
			result = append(result, keySummary(current, key))
		}
		if !page.HasMore {
			break
		}
		if page.Next == (platform.PageCursor{}) {
			return nil, errInvalidUpstreamResponse
		}
		if _, duplicate := seen[page.Next]; duplicate {
			return nil, errInvalidUpstreamResponse
		}
		seen[page.Next] = struct{}{}
		cursor = page.Next
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func newAPIKey(upstreamID string, asset platform.UpstreamAsset) (Key, error) {
	tokenID := strings.TrimSpace(asset.Metadata["token_id"])
	parsedID, err := strconv.Atoi(tokenID)
	if err != nil || parsedID <= 0 || asset.ID != upstreamID+":token:"+tokenID || asset.SourceID != upstreamID {
		return Key{}, errInvalidUpstreamResponse
	}
	return Key{
		ID: tokenID, Name: strings.TrimSpace(asset.Name), Enabled: asset.Enabled, Source: "newapi",
		SourceGroup: strings.TrimSpace(asset.Metadata["upstream_group"]), CredentialPresent: asset.SecretReadable,
		assetID: asset.ID,
	}, nil
}

func (s *Service) Discover(
	ctx context.Context,
	upstream config.UpstreamConfig,
	adapter platform.UpstreamAdapter,
	keyIDs []string,
) (DiscoveryTask, error) {
	task := DiscoveryTask{TaskID: newTaskID(), KeyIDs: append([]string(nil), keyIDs...), Completed: true}
	if s == nil || ctx == nil || adapter == nil || len(keyIDs) == 0 || len(keyIDs) > maxDiscoveryKeys {
		return DiscoveryTask{}, ErrInvalidRequest
	}
	keys, err := s.ListKeys(ctx, upstream, adapter)
	if err != nil {
		return DiscoveryTask{}, err
	}
	byID := make(map[string]Key, len(keys))
	for _, key := range keys {
		byID[key.ID] = key
	}
	selected := make([]Key, len(keyIDs))
	seen := make(map[string]struct{}, len(keyIDs))
	locked := make([]resourceKey, 0, len(keyIDs))
	for index, rawID := range keyIDs {
		keyID := strings.TrimSpace(rawID)
		if keyID == "" || keyID != rawID {
			s.releaseKeys(locked)
			return DiscoveryTask{}, ErrInvalidRequest
		}
		if _, duplicate := seen[keyID]; duplicate {
			s.releaseKeys(locked)
			return DiscoveryTask{}, ErrInvalidRequest
		}
		seen[keyID] = struct{}{}
		key, exists := byID[keyID]
		if !exists {
			s.releaseKeys(locked)
			return DiscoveryTask{}, ErrKeyNotFound
		}
		resource := resourceKey{upstreamID: upstream.ID, keyID: keyID}
		if !s.limiter.tryKey(resource) {
			s.releaseKeys(locked)
			return DiscoveryTask{}, ErrOperationInProgress
		}
		locked = append(locked, resource)
		selected[index] = key
	}
	defer s.releaseKeys(locked)

	secrets, secretErrors := resolveSelectedSecrets(ctx, upstream.Type, adapter, selected)
	defer wipeSecrets(secrets)
	task.Items = make([]DiscoveryItem, len(selected))
	successes := make(map[string]discoveredModels)
	host, err := normalizedHost(upstream.BaseURL)
	if err != nil {
		return DiscoveryTask{}, ErrInvalidRequest
	}
	for index, key := range selected {
		item := DiscoveryItem{KeyID: key.ID}
		if !key.Enabled || !key.CredentialPresent {
			item.Status, item.ErrorCode = DiscoveryFailed, "secret_unavailable"
			s.recordDiscoveryFailure(resourceKey{upstream.ID, key.ID}, item.Status)
			task.Items[index] = item
			continue
		}
		if secretErr := secretErrors[key.ID]; secretErr != nil {
			item = discoveryItemFromError(key.ID, secretErr)
			s.recordDiscoveryFailure(resourceKey{upstream.ID, key.ID}, item.Status)
			task.Items[index] = item
			continue
		}
		secret, exists := secrets[key.ID]
		if !exists || len(secret.Bytes) == 0 {
			item.Status, item.ErrorCode = DiscoveryFailed, "secret_unavailable"
			s.recordDiscoveryFailure(resourceKey{upstream.ID, key.ID}, item.Status)
			task.Items[index] = item
			continue
		}
		if !s.limiter.tryCapacity(host) {
			item.Status, item.ErrorCode = DiscoveryFailed, "operation_in_progress"
			s.recordDiscoveryFailure(resourceKey{upstream.ID, key.ID}, item.Status)
			task.Items[index] = item
			continue
		}
		models, discoverErr := s.fetchModels(ctx, upstream.BaseURL, secret.Bytes, time.Duration(s.store.Snapshot().App.RequestTimeout))
		s.limiter.releaseCapacity(host)
		if discoverErr != nil {
			item = discoveryItemFromError(key.ID, discoverErr)
			s.recordDiscoveryFailure(resourceKey{upstream.ID, key.ID}, item.Status)
			task.Items[index] = item
			continue
		}
		if len(models) == 0 {
			item.Status = DiscoveryEmpty
			s.recordDiscoveryFailure(resourceKey{upstream.ID, key.ID}, item.Status)
			task.Items[index] = item
			continue
		}
		at := s.currentTime()
		item.Status, item.ModelCount, item.DiscoveredAt = DiscoverySucceeded, len(models), timePointer(at)
		successes[key.ID] = discoveredModels{models: models, at: at}
		task.Items[index] = item
	}

	if strings.EqualFold(upstream.Type, "generic") && len(successes) > 0 {
		if err := s.persistGenericModels(ctx, upstream.ID, successes); err != nil {
			for index := range task.Items {
				if _, succeeded := successes[task.Items[index].KeyID]; !succeeded {
					continue
				}
				task.Items[index] = DiscoveryItem{KeyID: task.Items[index].KeyID, Status: DiscoveryFailed, ErrorCode: "persistence_failed"}
				s.recordDiscoveryFailure(resourceKey{upstream.ID, task.Items[index].KeyID}, DiscoveryFailed)
			}
			successes = map[string]discoveredModels{}
		}
	}
	for keyID, result := range successes {
		s.publish(resourceKey{upstream.ID, keyID}, result, scopeForUpstream(upstream.Type))
	}
	task.Status = aggregateTaskStatus(task.Items)
	return task, nil
}

func (s *Service) Models(upstreamID, keyID string) (KeyModels, bool) {
	current, ok := s.snapshot(resourceKey{strings.TrimSpace(upstreamID), strings.TrimSpace(keyID)})
	if !ok {
		return KeyModels{}, false
	}
	return publicSnapshot(current), true
}

func (s *Service) MutateKey(ctx context.Context, upstreamID, keyID string, mutate func() error) error {
	upstreamID, keyID = strings.TrimSpace(upstreamID), strings.TrimSpace(keyID)
	if s == nil || ctx == nil || upstreamID == "" || keyID == "" || mutate == nil {
		return ErrInvalidRequest
	}
	resource := resourceKey{upstreamID: upstreamID, keyID: keyID}
	if !s.limiter.tryKey(resource) {
		return ErrOperationInProgress
	}
	defer s.limiter.releaseKey(resource)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := mutate(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.snapshots, resource)
	s.mu.Unlock()
	return nil
}

func (s *Service) MutateUpstream(ctx context.Context, upstreamID string, mutate func() error) error {
	upstreamID = strings.TrimSpace(upstreamID)
	if s == nil || ctx == nil || upstreamID == "" || mutate == nil {
		return ErrInvalidRequest
	}
	if !s.limiter.tryUpstream(upstreamID) {
		return ErrOperationInProgress
	}
	defer s.limiter.releaseUpstream(upstreamID)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := mutate(); err != nil {
		return err
	}
	s.mu.Lock()
	for key := range s.snapshots {
		if key.upstreamID == upstreamID {
			delete(s.snapshots, key)
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) Probe(
	ctx context.Context,
	upstream config.UpstreamConfig,
	adapter platform.UpstreamAdapter,
	keyID string,
	model string,
	protocol probe.Protocol,
) (ModelProbe, error) {
	if s == nil || ctx == nil || adapter == nil || !validModelID(model) || !validProtocol(protocol) {
		return ModelProbe{}, ErrInvalidRequest
	}
	keys, err := s.ListKeys(ctx, upstream, adapter)
	if err != nil {
		return ModelProbe{}, err
	}
	var selected Key
	found := false
	for _, key := range keys {
		if key.ID == keyID {
			selected, found = key, true
			break
		}
	}
	if !found {
		return ModelProbe{}, ErrKeyNotFound
	}
	resource := resourceKey{upstream.ID, keyID}
	current, ok := s.snapshot(resource)
	if !ok || !containsModel(current.models, model) {
		return ModelProbe{}, ErrModelUnavailable
	}
	if !selected.Enabled || !selected.CredentialPresent {
		return ModelProbe{}, platform.ErrSecretUnavailable
	}
	if !s.limiter.tryKey(resource) {
		return ModelProbe{}, ErrOperationInProgress
	}
	defer s.limiter.releaseKey(resource)
	host, err := normalizedHost(upstream.BaseURL)
	if err != nil {
		return ModelProbe{}, ErrInvalidRequest
	}
	if !s.limiter.tryCapacity(host) {
		return ModelProbe{}, ErrOperationInProgress
	}
	defer s.limiter.releaseCapacity(host)
	secret, err := adapter.ResolveSecret(ctx, selected.assetID, platform.SecretGrant{})
	if err != nil {
		secret.Wipe()
		return ModelProbe{}, err
	}
	defer secret.Wipe()
	if len(secret.Bytes) == 0 {
		return ModelProbe{}, platform.ErrSecretUnavailable
	}
	input := probe.Input{BaseURL: upstream.BaseURL, APIKey: string(secret.Bytes), Model: model, Protocol: protocol}
	probeResult := s.prober.Probe(ctx, input)
	input.APIKey = ""
	result := sanitizeProbeResult(keyID, model, protocol, probeResult, s.currentTime())
	s.mu.Lock()
	stored := s.snapshots[resource]
	if containsModel(stored.models, model) {
		if stored.probes == nil {
			stored.probes = make(map[string]ModelProbe)
		}
		stored.probes[model] = result
		s.snapshots[resource] = stored
	}
	s.mu.Unlock()
	return result, nil
}

func (s *Service) fetchModels(ctx context.Context, baseURL string, secret []byte, timeout time.Duration) ([]string, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, modelsURL(baseURL), nil)
	if err != nil {
		return nil, &remoteDiscoveryError{status: DiscoveryFailed, code: "request_failed"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(secret))
	response, err := s.client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return nil, &remoteDiscoveryError{status: DiscoveryFailed, code: "timeout"}
		}
		return nil, &remoteDiscoveryError{status: DiscoveryFailed, code: "network_error", retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		return nil, classifyDiscoveryHTTP(response.StatusCode, response.Header.Get("Retry-After"), s.currentTime())
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if requestCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return nil, &remoteDiscoveryError{status: DiscoveryFailed, code: "timeout"}
	}
	if err != nil || len(body) > int(maxResponseBytes) {
		return nil, &remoteDiscoveryError{status: DiscoveryFailed, code: "invalid_response"}
	}
	defer wipeBytes(body)
	var payload struct {
		Data *[]struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Data == nil || len(*payload.Data) > maxModels {
		return nil, &remoteDiscoveryError{status: DiscoveryFailed, code: "invalid_response"}
	}
	seen := make(map[string]struct{}, len(*payload.Data))
	models := make([]string, 0, len(*payload.Data))
	for _, item := range *payload.Data {
		model := strings.TrimSpace(item.ID)
		if !validModelID(model) {
			return nil, &remoteDiscoveryError{status: DiscoveryFailed, code: "invalid_response"}
		}
		if _, duplicate := seen[model]; duplicate {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func resolveSelectedSecrets(
	ctx context.Context,
	upstreamType string,
	adapter platform.UpstreamAdapter,
	keys []Key,
) (map[string]platform.ResolvedSecret, map[string]error) {
	secrets := make(map[string]platform.ResolvedSecret, len(keys))
	failures := make(map[string]error)
	eligible := make([]Key, 0, len(keys))
	for _, key := range keys {
		if !key.Enabled || !key.CredentialPresent {
			failures[key.ID] = platform.ErrSecretUnavailable
			continue
		}
		eligible = append(eligible, key)
	}
	keys = eligible
	if strings.EqualFold(upstreamType, "newapi") {
		batch, ok := adapter.(platform.BatchSecretResolver)
		if !ok || batch.MaxSecretBatchSize() <= 0 {
			for _, key := range keys {
				failures[key.ID] = ErrUnsupported
			}
			return secrets, failures
		}
		limit := batch.MaxSecretBatchSize()
		if limit > maxDiscoveryKeys {
			limit = maxDiscoveryKeys
		}
		for start := 0; start < len(keys); start += limit {
			end := start + limit
			if end > len(keys) {
				end = len(keys)
			}
			assetIDs := make([]string, end-start)
			for index := start; index < end; index++ {
				assetIDs[index-start] = keys[index].assetID
			}
			resolved, err := batch.ResolveSecrets(ctx, assetIDs, platform.SecretGrant{})
			if err != nil {
				wipeResolvedSecretMap(resolved)
				for index := start; index < end; index++ {
					failures[keys[index].ID] = err
				}
				continue
			}
			for index := start; index < end; index++ {
				if secret, exists := resolved[keys[index].assetID]; exists {
					secrets[keys[index].ID] = secret
					delete(resolved, keys[index].assetID)
				} else {
					failures[keys[index].ID] = platform.ErrSecretUnavailable
				}
			}
			wipeResolvedSecretMap(resolved)
		}
		return secrets, failures
	}
	for _, key := range keys {
		secret, err := adapter.ResolveSecret(ctx, key.assetID, platform.SecretGrant{})
		if err != nil {
			secret.Wipe()
			failures[key.ID] = err
			continue
		}
		secrets[key.ID] = secret
	}
	return secrets, failures
}

func (s *Service) persistGenericModels(ctx context.Context, upstreamID string, updates map[string]discoveredModels) error {
	if s.store == nil {
		return errors.New("model catalog config store is unavailable")
	}
	return s.store.Update(ctx, func(cfg *config.Config) error {
		for upstreamIndex := range cfg.Upstreams {
			upstream := &cfg.Upstreams[upstreamIndex]
			if upstream.ID != upstreamID {
				continue
			}
			if upstream.Type != "generic" {
				return ErrUnsupported
			}
			remaining := len(updates)
			for keyIndex := range upstream.Keys {
				models, exists := updates[upstream.Keys[keyIndex].ID]
				if !exists {
					continue
				}
				upstream.Keys[keyIndex].Models = append([]string(nil), models.models...)
				remaining--
			}
			if remaining != 0 {
				return ErrKeyNotFound
			}
			return nil
		}
		return ErrKeyNotFound
	})
}

func (s *Service) ensureConfiguredSnapshot(key resourceKey, models []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.snapshots[key]; exists {
		return
	}
	cloned := append([]string(nil), models...)
	if cloned == nil {
		cloned = []string{}
	}
	status := SnapshotEmpty
	discoveryState := string(DiscoveryEmpty)
	if len(cloned) > 0 {
		status, discoveryState = SnapshotUnverified, "unverified"
	}
	s.snapshots[key] = snapshot{
		upstreamID: key.upstreamID, keyID: key.keyID, models: cloned, status: status,
		scope: SnapshotScopePersisted, probes: make(map[string]ModelProbe), discoveryState: discoveryState,
	}
}

func (s *Service) ensureRuntimeSnapshot(key resourceKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.snapshots[key]; exists {
		return
	}
	s.snapshots[key] = snapshot{
		upstreamID: key.upstreamID, keyID: key.keyID, models: []string{}, status: SnapshotEmpty,
		scope: SnapshotScopeRuntime, probes: make(map[string]ModelProbe), discoveryState: string(DiscoveryEmpty),
	}
}

func (s *Service) publish(key resourceKey, result discoveredModels, scope SnapshotScope) {
	s.mu.Lock()
	previous := s.snapshots[key]
	probes := make(map[string]ModelProbe)
	for _, model := range result.models {
		if value, exists := previous.probes[model]; exists {
			probes[model] = value
		}
	}
	s.snapshots[key] = snapshot{
		upstreamID: key.upstreamID, keyID: key.keyID, models: append([]string(nil), result.models...),
		verified: true, status: SnapshotReady, scope: scope, discoveredAt: result.at,
		probes: probes, discoveryState: string(DiscoverySucceeded),
	}
	s.mu.Unlock()
}

func (s *Service) recordDiscoveryFailure(key resourceKey, state DiscoveryStatus) {
	s.mu.Lock()
	current, exists := s.snapshots[key]
	if !exists {
		current = snapshot{upstreamID: key.upstreamID, keyID: key.keyID, models: []string{}, scope: SnapshotScopeRuntime, probes: make(map[string]ModelProbe)}
	}
	if len(current.models) > 0 {
		current.status = SnapshotStale
	} else {
		current.status = SnapshotEmpty
	}
	current.discoveryState = string(state)
	s.snapshots[key] = current
	s.mu.Unlock()
}

func (s *Service) snapshot(key resourceKey) (snapshot, bool) {
	s.mu.RLock()
	current, exists := s.snapshots[key]
	if exists {
		current.models = append([]string(nil), current.models...)
		current.probes = cloneProbes(current.probes)
	}
	s.mu.RUnlock()
	return current, exists
}

func publicSnapshot(current snapshot) KeyModels {
	models := make([]Model, len(current.models))
	state := "unverified"
	if current.verified {
		state = "discovered"
	}
	for index, modelID := range current.models {
		models[index] = Model{ID: modelID, DiscoveryStatus: state}
		if summary, exists := current.probes[modelID]; exists {
			copy := summary
			models[index].Probe = &copy
		}
	}
	result := KeyModels{
		UpstreamID: current.upstreamID, KeyID: current.keyID, Models: models,
		SnapshotStatus: current.status, SnapshotScope: current.scope, Verified: current.verified,
		Stale: current.status == SnapshotStale,
	}
	if !current.discoveredAt.IsZero() {
		result.DiscoveredAt = timePointer(current.discoveredAt)
	}
	return result
}

func keySummary(current snapshot, key Key) Key {
	key.ModelCount = len(current.models)
	key.DiscoveryStatus = current.discoveryState
	key.SnapshotStatus = current.status
	if !current.discoveredAt.IsZero() {
		key.DiscoveredAt = timePointer(current.discoveredAt)
	}
	return key
}

func (l *operationLimiter) tryKey(key resourceKey) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, active := l.activeUpstreams[key.upstreamID]; active {
		return false
	}
	if _, exists := l.activeKeys[key]; exists {
		return false
	}
	l.activeKeys[key] = struct{}{}
	return true
}

func (l *operationLimiter) tryUpstream(upstreamID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, active := l.activeUpstreams[upstreamID]; active {
		return false
	}
	for key := range l.activeKeys {
		if key.upstreamID == upstreamID {
			return false
		}
	}
	l.activeUpstreams[upstreamID] = struct{}{}
	return true
}

func (l *operationLimiter) releaseUpstream(upstreamID string) {
	l.mu.Lock()
	delete(l.activeUpstreams, upstreamID)
	l.mu.Unlock()
}

func (l *operationLimiter) releaseKey(key resourceKey) {
	l.mu.Lock()
	delete(l.activeKeys, key)
	l.mu.Unlock()
}

func (l *operationLimiter) tryCapacity(host string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeTotal >= globalConcurrency || l.activeHosts[host] >= hostConcurrency {
		return false
	}
	l.activeTotal++
	l.activeHosts[host]++
	return true
}

func (l *operationLimiter) releaseCapacity(host string) {
	l.mu.Lock()
	if l.activeTotal > 0 {
		l.activeTotal--
	}
	if l.activeHosts[host] <= 1 {
		delete(l.activeHosts, host)
	} else {
		l.activeHosts[host]--
	}
	l.mu.Unlock()
}

func (s *Service) releaseKeys(keys []resourceKey) {
	for _, key := range keys {
		s.limiter.releaseKey(key)
	}
}

func discoveryItemFromError(keyID string, err error) DiscoveryItem {
	item := DiscoveryItem{KeyID: keyID, Status: DiscoveryFailed}
	var remote *remoteDiscoveryError
	if errors.As(err, &remote) {
		item.Status, item.ErrorCode, item.Retryable = remote.status, remote.code, remote.retryable
		item.RetryAfterSeconds = durationSeconds(remote.retryAfter)
		return item
	}
	switch {
	case errors.Is(err, newapi.ErrUnauthenticated), errors.Is(err, generic.ErrUnauthenticated):
		item.Status, item.ErrorCode = DiscoveryAuthenticationFailed, "authentication_failed"
	case errors.Is(err, newapi.ErrInsufficientPrivilege), errors.Is(err, generic.ErrForbidden):
		item.Status, item.ErrorCode = DiscoveryAuthenticationFailed, "permission_denied"
	case errors.Is(err, platform.ErrRateLimited):
		item.Status, item.ErrorCode, item.Retryable = DiscoveryRateLimited, "rate_limited", true
		var rateLimit *platform.RateLimitError
		if errors.As(err, &rateLimit) {
			item.RetryAfterSeconds = durationSeconds(rateLimit.RetryAfter)
		}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		item.ErrorCode = "timeout"
	case errors.Is(err, ErrUnsupported):
		item.Status, item.ErrorCode = DiscoveryUnsupported, "model_discovery_unsupported"
	case errors.Is(err, platform.ErrSecretUnavailable), errors.Is(err, platform.ErrAssetDisabled):
		item.ErrorCode = "secret_unavailable"
	default:
		item.ErrorCode = "upstream_failure"
	}
	return item
}

func classifyDiscoveryHTTP(status int, retryAfter string, now time.Time) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &remoteDiscoveryError{status: DiscoveryAuthenticationFailed, code: "authentication_failed"}
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return &remoteDiscoveryError{status: DiscoveryUnsupported, code: "model_discovery_unsupported"}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &remoteDiscoveryError{status: DiscoveryFailed, code: "timeout"}
	case http.StatusTooManyRequests:
		return &remoteDiscoveryError{
			status: DiscoveryRateLimited, code: "rate_limited", retryable: true,
			retryAfter: parseRetryAfter(retryAfter, now),
		}
	default:
		return &remoteDiscoveryError{status: DiscoveryFailed, code: "upstream_failure"}
	}
}

func sanitizeProbeResult(keyID, model string, requested probe.Protocol, input probe.Result, now time.Time) ModelProbe {
	protocol := input.Protocol
	if protocol == "" {
		protocol = requested
	}
	status := input.Status
	switch status {
	case probe.StatusHealthy, probe.StatusInconclusive, probe.StatusUnauthorized, probe.StatusModelUnavailable,
		probe.StatusProtocolUnavailable, probe.StatusRateLimited, probe.StatusTimeout, probe.StatusNetworkError,
		probe.StatusInvalidResponse:
	default:
		status = probe.StatusInvalidResponse
	}
	checkedAt := input.CheckedAt
	if checkedAt.IsZero() {
		checkedAt = now
	}
	latency := input.Latency.Milliseconds()
	if latency < 0 {
		latency = 0
	}
	return ModelProbe{
		KeyID: keyID, Model: model, Protocol: protocol, Status: status, LatencyMS: latency,
		CheckedAt: checkedAt, ErrorCode: string(input.ErrorCode),
		Retryable:         status == probe.StatusRateLimited || status == probe.StatusNetworkError,
		RetryAfterSeconds: durationSeconds(input.RetryAfter), TemplateVersion: input.TemplateVersion,
	}
}

func aggregateTaskStatus(items []DiscoveryItem) TaskStatus {
	successes, failures := 0, 0
	for _, item := range items {
		if item.Status == DiscoverySucceeded || item.Status == DiscoveryEmpty {
			successes++
		} else {
			failures++
		}
	}
	switch {
	case failures == 0:
		return TaskSucceeded
	case successes == 0:
		return TaskFailed
	default:
		return TaskPartiallyFailed
	}
}

func scopeForUpstream(upstreamType string) SnapshotScope {
	if strings.EqualFold(upstreamType, "generic") {
		return SnapshotScopePersisted
	}
	return SnapshotScopeRuntime
}

func genericAssetID(upstreamID, keyID string) string {
	if keyID == config.DefaultGenericKeyID {
		return upstreamID + ":endpoint"
	}
	return upstreamID + ":key:" + keyID
}

func modelsURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, _ := url.Parse(trimmed)
	if parsed != nil && strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1") {
		return trimmed + "/models"
	}
	return trimmed + "/v1/models"
}

func normalizedHost(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", ErrInvalidRequest
	}
	return strings.ToLower(parsed.Host), nil
}

func validModelID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maxModelIDBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validProtocol(value probe.Protocol) bool {
	switch value {
	case probe.ProtocolAuto, probe.ProtocolChatCompletions, probe.ProtocolResponses, probe.ProtocolCompletions:
		return true
	default:
		return false
	}
}

func containsModel(models []string, target string) bool {
	for _, model := range models {
		if model == target {
			return true
		}
	}
	return false
}

func cloneProbes(input map[string]ModelProbe) map[string]ModelProbe {
	result := make(map[string]ModelProbe, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func wipeSecrets(secrets map[string]platform.ResolvedSecret) {
	for key, secret := range secrets {
		secret.Wipe()
		secrets[key] = platform.ResolvedSecret{}
	}
}

func wipeResolvedSecretMap(secrets map[string]platform.ResolvedSecret) {
	for key, secret := range secrets {
		secret.Wipe()
		delete(secrets, key)
	}
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func timePointer(value time.Time) *time.Time {
	copy := value.UTC()
	return &copy
}

func durationSeconds(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	seconds := int64(value / time.Second)
	if value%time.Second != 0 {
		seconds++
	}
	return seconds
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}

func newTaskID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "model_discovery_unavailable"
	}
	return "model_discovery_" + hex.EncodeToString(value)
}

func (s *Service) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

var _ Catalog = (*Service)(nil)
