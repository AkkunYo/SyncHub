package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestTargetPropagatesTimeoutWhileReadingResponseBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test server does not support flushing")
			return
		}
		flusher.Flush()
		_, _ = io.WriteString(w, `{"success":`)
		flusher.Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{RequestTimeout: 25 * time.Millisecond})

	if _, err := target.ListChannels(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("response body timeout error = %v", err)
	}
}

func TestTargetUsesCreateResponseIDWithoutRelisting(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
			writeTargetList(t, w, 1, 100, 0, nil)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/":
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":91}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{})

	channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI, Secret: []byte("single-channel-key"),
		Name: "OpenAI", Models: []string{"gpt-4o"}, Group: "default", Weight: 100,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.ID != "91" || channel.Provider != platform.ProviderOpenAI || channel.RawType != "1" || !channel.Enabled {
		t.Fatalf("created channel = %#v", channel)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestTargetRejectsAmbiguousCreatedChannelID(t *testing.T) {
	t.Parallel()

	var created atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			items := []map[string]any(nil)
			if created.Load() {
				items = []map[string]any{
					{"id": 101, "type": 1, "name": "same-name", "status": 1},
					{"id": 102, "type": 1, "name": "same-name", "status": 1},
				}
			}
			writeTargetList(t, w, 1, 100, len(items), items)
		case http.MethodPost:
			created.Store(true)
			_, _ = fmt.Fprint(w, `{"success":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{})

	_, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI, Secret: []byte("single-channel-key"), Name: "same-name",
	})
	if !errors.Is(err, ErrChannelIDUnavailable) {
		t.Fatalf("CreateChannel() error = %v", err)
	}
}

func TestTargetUpdateFallsBackToPublicGetResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
			_, _ = fmt.Fprint(w, `{"success":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/55/status":
			_, _ = fmt.Fprint(w, `{"success":true,"data":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/55":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":55,"type":43,"name":"DeepSeek","status":1,"models":"deepseek-chat","group":"default","priority":2,"weight":100}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{})

	channel, err := target.UpdateChannel(context.Background(), "55", platform.UpdateChannelInput{
		Name: "DeepSeek", Models: []string{"deepseek-chat"}, Group: "default", Priority: 2, Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if channel.ID != "55" || channel.Provider != platform.ProviderDeepSeek || !channel.Enabled {
		t.Fatalf("updated channel = %#v", channel)
	}
}

func TestTargetFormatsSupportedSecretShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		secret   string
		rawType  int
		wantKey  string
		wantMode string
		wantErr  error
	}{
		{name: "single", secret: " key-one ", rawType: 1, wantKey: "key-one", wantMode: "single"},
		{name: "newline", secret: "key-one\r\nkey-two", rawType: 1, wantKey: "key-one\nkey-two", wantMode: "multi_to_single"},
		{name: "JSON array", secret: `["key-one", "key-two"]`, rawType: 1, wantKey: "key-one\nkey-two", wantMode: "multi_to_single"},
		{name: "single JSON item", secret: `["key-one"]`, rawType: 1, wantKey: "key-one", wantMode: "single"},
		{name: "Vertex JSON array", secret: `[{"project_id":"one"},{"project_id":"two"}]`, rawType: 41, wantKey: `[{"project_id":"one"},{"project_id":"two"}]`, wantMode: "multi_to_single"},
		{name: "empty", secret: "  ", rawType: 1, wantErr: platform.ErrSecretUnavailable},
		{name: "invalid array", secret: "[invalid", rawType: 1, wantErr: errors.New("invalid")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, mode, err := encodeTargetSecret([]byte(test.secret), test.rawType)
			if test.wantErr != nil {
				if err == nil {
					t.Fatalf("encodeTargetSecret() = %q, %q, nil", key, mode)
				}
				if errors.Is(test.wantErr, platform.ErrSecretUnavailable) && !errors.Is(err, platform.ErrSecretUnavailable) {
					t.Fatalf("encodeTargetSecret() error = %v", err)
				}
				return
			}
			if err != nil || key != test.wantKey || mode != test.wantMode {
				t.Fatalf("encodeTargetSecret() = %q, %q, %v", key, mode, err)
			}
		})
	}
}

func TestTargetAcceptsExplicitAliasRawTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider string
		rawType  string
		want     int
	}{
		{provider: platform.ProviderAIStudio, rawType: "24", want: 24},
		{provider: platform.ProviderKimi, rawType: "25", want: 25},
	}
	for _, test := range tests {
		got, err := newAPITypeForInput(platform.CreateChannelInput{
			Mode: platform.SyncModeStaticKey, Provider: test.provider, RawType: test.rawType,
		})
		if err != nil || got != test.want {
			t.Fatalf("provider=%s raw=%s: type=%d err=%v", test.provider, test.rawType, got, err)
		}
	}
}

func TestTargetRejectsFailedUpdateStatusAndDeleteResponses(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut:
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":8,"type":1,"status":1}}`)
		case r.Method == http.MethodPost:
			_, _ = fmt.Fprint(w, `{"success":false,"message":"status-response-secret"}`)
		case r.Method == http.MethodDelete:
			_, _ = fmt.Fprint(w, `{"success":false,"message":"delete-response-secret"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{AccessToken: "management-response-secret"})

	_, updateErr := target.UpdateChannel(context.Background(), "8", platform.UpdateChannelInput{Group: "default", Enabled: true})
	if updateErr == nil {
		t.Fatal("UpdateChannel() unexpectedly succeeded")
	}
	assertErrorOmits(t, updateErr, "status-response-secret", "management-response-secret")

	deleteErr := target.DeleteChannel(context.Background(), "8")
	if deleteErr == nil {
		t.Fatal("DeleteChannel() unexpectedly succeeded")
	}
	assertErrorOmits(t, deleteErr, "delete-response-secret", "management-response-secret")
}

func TestTargetRejectsInvalidStaticChannelBaseURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid input reached HTTP server")
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{})

	_, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI, BaseURL: "relative/path", Secret: []byte("channel-key"),
	})
	if !errors.Is(err, platform.ErrIncompatibleTarget) || strings.Contains(err.Error(), "channel-key") {
		t.Fatalf("CreateChannel() error = %v", err)
	}
}

func TestTargetCreatesVertexAIStaticChannelPreservingRawType(t *testing.T) {
	t.Parallel()

	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":100}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/":
			creates.Add(1)
			var request createChannelRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			if request.Channel.Type != 41 || request.Channel.Key != `{"type":"service_account","project_id":"project"}` {
				t.Errorf("create payload = %#v", request.Channel)
			}
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":71}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{TargetID: "target", BaseURL: server.URL, AccessToken: "token"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	asset := platform.UpstreamAsset{SourceType: "newapi", Provider: platform.ProviderVertexAI, RawType: "41", Kind: platform.AssetStaticAPIKey, Enabled: true}
	mode, err := platform.SelectSyncMode(asset, target.Capabilities())
	if err != nil || mode != platform.SyncModeStaticKey {
		t.Fatalf("SelectSyncMode() = %q, %v", mode, err)
	}
	channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		AssetID: "source:channel:41", Mode: mode, Provider: asset.Provider, RawType: asset.RawType,
		Name: "Vertex", Secret: []byte(`{"type":"service_account","project_id":"project"}`), Models: []string{"gemini-2.5-pro"}, Group: "default", Weight: 100,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.ID != "71" || channel.Provider != platform.ProviderVertexAI || channel.RawType != "41" || creates.Load() != 1 {
		t.Fatalf("created channel = %#v, creates = %d", channel, creates.Load())
	}
}
