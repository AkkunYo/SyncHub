package app

import (
	"context"
	"crypto/sha256"
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
	"github.com/AkkunYo/SyncHub/internal/platform/newapi"
	"github.com/AkkunYo/SyncHub/internal/platform/sub2api"
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
		built, buildErr := newapi.NewSource(newapi.Config{
			SourceID: cfg.ID, BaseURL: cfg.BaseURL,
			AccessToken:    firstCredential(cfg.AccessToken, cfg.APIKey),
			UserID:         cfg.UserID,
			RequestTimeout: timeout,
			DiscoveryMode:  cfg.DiscoveryMode,
		}, r.client)
		if buildErr != nil {
			return nil, errors.New("New API upstream configuration is invalid")
		}
		source = built
	case "cliproxyapi":
		managementKey, useHeader := managementCredential(cfg.ManagementKey, cfg.APIKey)
		built, buildErr := cliproxyapi.NewSource(cliproxyapi.Config{
			SourceID: cfg.ID, BaseURL: cfg.BaseURL, ManagementKey: managementKey, ProxyAPIKey: cfg.ProxyAPIKey,
			UseManagementKeyHeader: useHeader, RequestTimeout: timeout,
		}, r.client)
		if buildErr != nil {
			return nil, errors.New("CLIProxyAPI upstream configuration is invalid")
		}
		source = built
	case "sub2api":
		built, buildErr := sub2api.NewSource(sub2api.Config{
			SourceID: cfg.ID, BaseURL: cfg.BaseURL, APIKey: strings.TrimSpace(cfg.APIKey),
			RequestTimeout: timeout,
		}, r.client)
		if buildErr != nil {
			return nil, errors.New("Sub2Api upstream configuration is invalid")
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

func newUpstreamIdentity(cfg config.UpstreamConfig, timeout time.Duration) upstreamIdentity {
	sourceType := strings.ToLower(strings.TrimSpace(cfg.Type))
	credential := ""
	userID := 0
	useManagementHeader := false
	switch sourceType {
	case "newapi":
		credential = firstCredential(cfg.AccessToken, cfg.APIKey)
		userID = cfg.UserID
	case "cliproxyapi":
		credential, useManagementHeader = managementCredential(cfg.ManagementKey, cfg.APIKey)
	case "sub2api":
		credential = strings.TrimSpace(cfg.APIKey)
	}
	return upstreamIdentity{
		sourceType:       sourceType,
		baseURL:          strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		userID:           userID,
		requestTimeout:   timeout,
		credentialDigest: sha256.Sum256([]byte(credential)),
		proxyKeyDigest:   sha256.Sum256([]byte(strings.TrimSpace(cfg.ProxyAPIKey))),
		managementHeader: useManagementHeader,
		discoveryMode:    cfg.DiscoveryMode,
	}
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
