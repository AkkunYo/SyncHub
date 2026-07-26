package newapi

import (
	"context"
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

func TestTargetValidatesConfigurationAndProviderMapping(t *testing.T) {
	t.Parallel()

	invalid := []TargetConfig{
		{BaseURL: "https://newapi.example.com", AccessToken: "token"},
		{TargetID: "target", BaseURL: "/relative", AccessToken: "token"},
		{TargetID: "target", BaseURL: "ftp://newapi.example.com", AccessToken: "token"},
		{TargetID: "target", BaseURL: "https://newapi.example.com"},
	}
	for _, cfg := range invalid {
		if target, err := NewTarget(cfg, nil); err == nil {
			_ = target
			t.Fatalf("NewTarget(%#v) unexpectedly succeeded", cfg)
		}
	}
	if _, err := NewTarget(TargetConfig{TargetID: "target", BaseURL: "https://newapi.example.com", AccessToken: "token"}, nil); err != nil {
		t.Fatalf("NewTarget(valid) error = %v", err)
	}

	tests := []struct {
		name     string
		input    platform.CreateChannelInput
		wantType int
	}{
		{name: "OpenAI", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI}, wantType: 1},
		{name: "Anthropic", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderAnthropic}, wantType: 14},
		{name: "Gemini", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderGemini}, wantType: 24},
		{name: "Zhipu variant", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderZhipu, RawType: "26"}, wantType: 26},
		{name: "AI Studio alias", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderAIStudio}, wantType: 24},
		{name: "Kimi alias", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderKimi}, wantType: 25},
		{name: "VertexAI", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderVertexAI}, wantType: 41},
		{name: "VertexAI raw type", input: platform.CreateChannelInput{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderVertexAI, RawType: "41"}, wantType: 41},
		{name: "proxy endpoint", input: platform.CreateChannelInput{Mode: platform.SyncModeProxyEndpoint, Provider: platform.ProviderAnthropic, BaseURL: "https://proxy.example.com/v1"}, wantType: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := newAPITypeForInput(test.input)
			if err != nil || got != test.wantType {
				t.Fatalf("newAPITypeForInput() = %d, %v; want %d", got, err, test.wantType)
			}
		})
	}

	rejected := []platform.CreateChannelInput{
		{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderUnknown},
		{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderCustom},
		{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderVertex},
		{Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI, RawType: "999"},
		{Mode: platform.SyncModeNativeAuthFile, Provider: platform.ProviderOpenAI},
		{Mode: platform.SyncModeProxyEndpoint, Provider: platform.ProviderUnknown, BaseURL: "https://proxy.example.com"},
		{Mode: platform.SyncModeProxyEndpoint, Provider: platform.ProviderAnthropic},
	}
	for _, input := range rejected {
		if _, err := newAPITypeForInput(input); !errors.Is(err, platform.ErrIncompatibleTarget) {
			t.Fatalf("newAPITypeForInput(%#v) error = %v", input, err)
		}
	}
}

func TestTargetAdvertisesCanonicalVertexAIStaticMode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("capability inspection unexpectedly made an HTTP request")
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{})

	capabilities := target.Capabilities()
	for provider, wantStatic := range map[string]bool{
		platform.ProviderVertexAI: true,
		platform.ProviderVertex:   false,
	} {
		modes, exists := capabilities.Providers[provider]
		if !exists {
			t.Fatalf("provider %q is missing from capabilities", provider)
		}
		if got := containsSyncMode(modes.Modes, platform.SyncModeStaticKey); got != wantStatic {
			t.Fatalf("provider %q static_key capability = %t, want %t: %#v", provider, got, wantStatic, modes.Modes)
		}
		if !containsSyncMode(modes.Modes, platform.SyncModeProxyEndpoint) {
			t.Fatalf("provider %q does not advertise proxy_endpoint: %#v", provider, modes.Modes)
		}
	}
}

func TestTargetRedactsProtocolFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "non-2xx",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "response-body-sensitive", http.StatusForbidden)
			},
		},
		{
			name: "success false",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"success":false,"message":"response-body-sensitive"}`)
			},
		},
		{
			name: "invalid JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"success":`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(test.handler)
			t.Cleanup(server.Close)
			target := newTestTarget(t, server, TargetConfig{AccessToken: "management-token-sensitive"})
			channels, err := target.ListChannels(context.Background())
			if err == nil || channels != nil {
				t.Fatalf("ListChannels() = %#v, %v", channels, err)
			}
			assertErrorOmits(t, err, "management-token-sensitive", "response-body-sensitive")
		})
	}
}

func TestTargetRedactsRejectedCreateResponse(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.Method {
		case http.MethodGet:
			writeTargetList(t, w, 1, 100, 0, nil)
		case http.MethodPost:
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = fmt.Fprint(w, `{"success":false,"message":"channel-key-sensitive response-body-sensitive"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{AccessToken: "management-token-sensitive"})

	_, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		Mode: platform.SyncModeStaticKey, Provider: platform.ProviderOpenAI, Secret: []byte("channel-key-sensitive"),
	})
	if err == nil {
		t.Fatal("CreateChannel() unexpectedly succeeded")
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
	assertErrorOmits(t, err, "management-token-sensitive", "channel-key-sensitive", "response-body-sensitive")
}

func TestTargetPropagatesTimeoutAndCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{RequestTimeout: 20 * time.Millisecond})

	if _, err := target.ListChannels(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := target.ListChannels(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestTargetRejectsInvalidChannelIDsBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{})

	if _, err := target.UpdateChannel(context.Background(), "not-a-number", platform.UpdateChannelInput{}); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if err := target.DeleteChannel(context.Background(), "../secret"); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid IDs made %d requests", requests.Load())
	}
}

func readAllForTest(r *http.Request) ([]byte, error) {
	return io.ReadAll(r.Body)
}

func TestTargetRequestErrorsNeverContainSecrets(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":false,"message":"upstream-secret"}`)
	}))
	t.Cleanup(server.Close)
	target := newTestTarget(t, server, TargetConfig{AccessToken: "bearer-secret"})
	_, err := target.ListChannels(context.Background())
	if err == nil {
		t.Fatal("ListChannels() unexpectedly succeeded")
	}
	for _, secret := range []string{"bearer-secret", "upstream-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}
