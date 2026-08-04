package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/platform/cliproxyapi"
	"github.com/AkkunYo/SyncHub/internal/platform/generic"
	"github.com/AkkunYo/SyncHub/internal/platform/newapi"
)

// AdapterResolver constructs adapters using the latest configured request
// timeout and one shared, injectable HTTP client.
type AdapterResolver struct {
	config api.ConfigStore
	client *http.Client

	upstreamMu    sync.Mutex
	upstreamCache map[string]cachedUpstream
}

type cachedUpstream struct {
	identity upstreamIdentity
	adapter  platform.UpstreamAdapter
}

type upstreamIdentity struct {
	sourceType       string
	name             string
	baseURL          string
	userID           int
	requestTimeout   time.Duration
	credentialDigest [sha256.Size]byte
	proxyKeyDigest   [sha256.Size]byte
	managementHeader bool
	discoveryMode    string
}

func NewAdapterResolver(store api.ConfigStore, client *http.Client) *AdapterResolver {
	if client == nil {
		client = &http.Client{}
	}
	return &AdapterResolver{config: store, client: client, upstreamCache: make(map[string]cachedUpstream)}
}

func (r *AdapterResolver) ResolveTarget(
	ctx context.Context,
	cfg config.TargetConfig,
) (platform.TargetAdapter, platform.TargetCapabilities, error) {
	if ctx == nil {
		return nil, platform.TargetCapabilities{}, ErrContextRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, platform.TargetCapabilities{}, err
	}
	timeout, err := r.requestTimeout()
	if err != nil {
		return nil, platform.TargetCapabilities{}, err
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "newapi":
		target, buildErr := newapi.NewTarget(newapi.TargetConfig{
			TargetID: cfg.ID, BaseURL: cfg.BaseURL,
			AccessToken:    firstCredential(cfg.AccessToken, cfg.APIKey),
			UserID:         cfg.UserID,
			RequestTimeout: timeout,
		}, r.client)
		if buildErr != nil {
			return nil, platform.TargetCapabilities{}, errors.New("New API target configuration is invalid")
		}
		return target, target.Capabilities(), nil
	case "cliproxyapi":
		managementKey, useHeader := managementCredential(cfg.ManagementKey, cfg.APIKey)
		target, buildErr := cliproxyapi.NewTarget(cliproxyapi.TargetConfig{
			TargetID: cfg.ID, BaseURL: cfg.BaseURL, ManagementKey: managementKey,
			UseManagementKeyHeader: useHeader, RequestTimeout: timeout,
		}, r.client)
		if buildErr != nil {
			return nil, platform.TargetCapabilities{}, errors.New("CLIProxyAPI target configuration is invalid")
		}
		return target, target.Capabilities(), nil
	case "sub2api":
		return nil, platform.TargetCapabilities{}, errors.Join(ErrUnsupportedPlatform, platform.ErrIncompatibleTarget)
	default:
		return nil, platform.TargetCapabilities{}, errors.Join(ErrUnsupportedPlatform, platform.ErrIncompatibleTarget)
	}
}

func (r *AdapterResolver) ResolveUpstream(ctx context.Context, cfg config.UpstreamConfig) (platform.UpstreamAdapter, error) {
	if ctx == nil {
		return nil, ErrContextRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout, err := r.requestTimeout()
	if err != nil {
		return nil, err
	}
	identity := newUpstreamIdentity(cfg, timeout)
	sourceID := strings.TrimSpace(cfg.ID)

	r.upstreamMu.Lock()
	defer r.upstreamMu.Unlock()
	if cached, exists := r.upstreamCache[sourceID]; exists && cached.identity == identity {
		return cached.adapter, nil
	}

	var source platform.UpstreamAdapter
	switch identity.sourceType {
	case "newapi":
		mode := strings.ToLower(strings.TrimSpace(cfg.DiscoveryMode))
		if strings.TrimSpace(cfg.AccessToken) == "" || strings.TrimSpace(cfg.APIKey) != "" || len(cfg.Keys) != 0 || (mode != "" && mode != config.DiscoveryModeToken) {
			return nil, ErrUnsupportedPlatform
		}
		built, buildErr := newapi.NewSource(newapi.Config{
			SourceID: cfg.ID, BaseURL: cfg.BaseURL,
			AccessToken:    strings.TrimSpace(cfg.AccessToken),
			UserID:         cfg.UserID,
			RequestTimeout: timeout,
			DiscoveryMode:  cfg.DiscoveryMode,
		}, r.client)
		if buildErr != nil {
			return nil, errors.New("New API upstream configuration is invalid")
		}
		source = built
	case "generic":
		keys, keyErr := genericKeysForResolver(cfg)
		if keyErr != nil {
			return nil, errors.New("generic upstream configuration is invalid")
		}
		built, buildErr := generic.NewMultiKeySource(generic.MultiKeyConfig{
			SourceID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, Keys: keys, RequestTimeout: timeout,
		}, r.client)
		if buildErr != nil {
			return nil, errors.New("generic upstream configuration is invalid")
		}
		source = built
	default:
		return nil, ErrUnsupportedPlatform
	}
	if r.upstreamCache == nil {
		r.upstreamCache = make(map[string]cachedUpstream)
	}
	r.upstreamCache[sourceID] = cachedUpstream{identity: identity, adapter: source}
	return source, nil
}

func (r *AdapterResolver) DiscoveryModeStatus(cfg config.UpstreamConfig) platform.DiscoveryModeStatus {
	unresolved := platform.DiscoveryModeStatus{EffectiveMode: "unresolved", Status: "unresolved"}
	if strings.ToLower(strings.TrimSpace(cfg.Type)) != "newapi" {
		return unresolved
	}
	if r == nil || isNilDependency(r.config) {
		return unresolved
	}
	timeout := time.Duration(r.config.Snapshot().App.RequestTimeout)
	if timeout <= 0 {
		return unresolved
	}
	identity := newUpstreamIdentity(cfg, timeout)
	r.upstreamMu.Lock()
	cached, exists := r.upstreamCache[strings.TrimSpace(cfg.ID)]
	r.upstreamMu.Unlock()
	if exists && cached.identity == identity {
		if provider, ok := cached.adapter.(platform.DiscoveryModeStatusProvider); ok && !isNilDependency(provider) {
			return provider.DiscoveryModeStatus()
		}
	}
	if strings.EqualFold(strings.TrimSpace(cfg.DiscoveryMode), config.DiscoveryModeToken) {
		return platform.DiscoveryModeStatus{EffectiveMode: "token", Status: "ready"}
	}
	return unresolved
}

func newUpstreamIdentity(cfg config.UpstreamConfig, timeout time.Duration) upstreamIdentity {
	sourceType := strings.ToLower(strings.TrimSpace(cfg.Type))
	credential := ""
	credentialDigest := [sha256.Size]byte{}
	userID := 0
	useManagementHeader := false
	switch sourceType {
	case "newapi":
		credential = strings.TrimSpace(cfg.AccessToken)
		userID = cfg.UserID
	case "generic":
		credentialDigest = genericKeysIdentityDigest(cfg)
	}
	if sourceType != "generic" {
		credentialDigest = sha256.Sum256([]byte(credential))
	}
	return upstreamIdentity{
		sourceType:       sourceType,
		name:             strings.TrimSpace(cfg.Name),
		baseURL:          strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		userID:           userID,
		requestTimeout:   timeout,
		credentialDigest: credentialDigest,
		proxyKeyDigest:   sha256.Sum256([]byte(strings.TrimSpace(cfg.ProxyAPIKey))),
		managementHeader: useManagementHeader,
		discoveryMode:    cfg.DiscoveryMode,
	}
}

func genericKeysForResolver(cfg config.UpstreamConfig) ([]generic.KeyConfig, error) {
	legacyAPIKey := strings.TrimSpace(cfg.APIKey)
	if len(cfg.Keys) == 0 {
		if legacyAPIKey == "" {
			return nil, errors.New("generic upstream keys are required")
		}
		return []generic.KeyConfig{{
			ID: generic.DefaultKeyID, Name: cfg.Name, APIKey: legacyAPIKey, Enabled: true,
		}}, nil
	}
	if legacyAPIKey != "" {
		return nil, errors.New("generic upstream legacy and multi-key credentials cannot be combined")
	}
	keys := make([]generic.KeyConfig, len(cfg.Keys))
	for i, key := range cfg.Keys {
		keys[i] = generic.KeyConfig{
			ID: key.ID, Name: key.Name, APIKey: key.APIKey, Enabled: key.Enabled,
			Models: append([]string(nil), key.Models...),
		}
	}
	return keys, nil
}

func genericKeysIdentityDigest(cfg config.UpstreamConfig) [sha256.Size]byte {
	hasher := sha256.New()
	writeUint64 := func(value uint64) {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], value)
		_, _ = hasher.Write(encoded[:])
	}
	writeBytes := func(value []byte) {
		writeUint64(uint64(len(value)))
		_, _ = hasher.Write(value)
	}
	writeString := func(value string) {
		writeBytes([]byte(strings.TrimSpace(value)))
	}

	if len(cfg.Keys) == 0 {
		writeString("legacy")
		credential := sha256.Sum256([]byte(strings.TrimSpace(cfg.APIKey)))
		writeBytes(credential[:])
	} else {
		writeString("keys")
		writeUint64(uint64(len(cfg.Keys)))
		for _, key := range cfg.Keys {
			writeString(key.ID)
			writeString(key.Name)
			if key.Enabled {
				writeString("enabled")
			} else {
				writeString("disabled")
			}
			credential := sha256.Sum256([]byte(strings.TrimSpace(key.APIKey)))
			writeBytes(credential[:])
			writeUint64(uint64(len(key.Models)))
			for _, model := range key.Models {
				writeString(model)
			}
		}
	}

	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func (r *AdapterResolver) requestTimeout() (time.Duration, error) {
	if r == nil || isNilDependency(r.config) {
		return 0, ErrDependenciesIncomplete
	}
	timeout := time.Duration(r.config.Snapshot().App.RequestTimeout)
	if timeout <= 0 {
		return 0, errors.New("configured request timeout must be positive")
	}
	return timeout, nil
}

func firstCredential(primary, fallback string) string {
	if primary = strings.TrimSpace(primary); primary != "" {
		return primary
	}
	return strings.TrimSpace(fallback)
}

func managementCredential(managementKey, fallback string) (string, bool) {
	if managementKey = strings.TrimSpace(managementKey); managementKey != "" {
		return managementKey, true
	}
	return strings.TrimSpace(fallback), false
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ api.AdapterResolver = (*AdapterResolver)(nil)
