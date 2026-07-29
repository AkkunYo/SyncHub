package newapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestModeStatusIsSafeAndTracksAutoProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"role":1,"group":"default"}}`))
	}))
	t.Cleanup(server.Close)
	source, err := NewSource(Config{SourceID: "source-a", BaseURL: server.URL, AccessToken: "test-token", DiscoveryMode: "auto"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got := source.DiscoveryModeStatus(); got != (platform.DiscoveryModeStatus{EffectiveMode: "unresolved", Status: "unresolved"}) {
		t.Fatalf("initial status = %#v", got)
	}
	if _, err := source.Capabilities(context.Background()); err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if got := source.DiscoveryModeStatus(); got != (platform.DiscoveryModeStatus{EffectiveMode: "token", Status: "ready"}) {
		t.Fatalf("resolved status = %#v", got)
	}
}

func TestModeStatusClassifiesProbeFailureWithoutLeakingDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`credential-body-must-not-escape`))
	}))
	t.Cleanup(server.Close)
	source, err := NewSource(Config{SourceID: "source-a", BaseURL: server.URL, AccessToken: "test-token", DiscoveryMode: "auto"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Capabilities(context.Background()); err == nil {
		t.Fatal("Capabilities() error = nil")
	}
	if got := source.DiscoveryModeStatus(); got != (platform.DiscoveryModeStatus{EffectiveMode: "unresolved", Status: "error", ErrorCode: "upstream_unauthenticated"}) {
		t.Fatalf("failure status = %#v", got)
	}
}

func TestExplicitTokenModeIsReadyWithoutProbe(t *testing.T) {
	source, err := NewSource(Config{SourceID: "source-a", BaseURL: "https://example.test", AccessToken: "test-token", DiscoveryMode: "token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := source.DiscoveryModeStatus(); got != (platform.DiscoveryModeStatus{EffectiveMode: "token", Status: "ready"}) {
		t.Fatalf("status = %#v", got)
	}
}
