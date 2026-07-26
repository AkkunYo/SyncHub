package cliproxyapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestTargetWaitsForAsynchronousCPAAuthRegistration(t *testing.T) {
	t.Parallel()

	var createdAt atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodGet:
			created := createdAt.Load()
			if created > 0 && time.Since(time.Unix(0, created)) >= 150*time.Millisecond {
				_, _ = fmt.Fprint(w, `{"files":[{"id":"delayed-real-id","auth_index":"delayed-index","name":"delayed","provider":"gemini","status":"ready","runtime_only":true}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"files":[]}`)
			}
		case r.URL.Path == "/v0/management/gemini-api-key" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `{"gemini-api-key":[]}`)
		case r.URL.Path == "/v0/management/gemini-api-key" && r.Method == http.MethodPut:
			createdAt.Store(time.Now().UnixNano())
			_, _ = fmt.Fprint(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	target, err := NewTarget(TargetConfig{
		TargetID:       "target",
		BaseURL:        server.URL,
		ManagementKey:  "key",
		RequestTimeout: time.Second,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	channel, err := target.CreateChannel(context.Background(), platform.CreateChannelInput{
		AssetID:  "asset",
		Mode:     platform.SyncModeStaticKey,
		Provider: platform.ProviderGemini,
		Secret:   []byte("secret"),
		Group:    "default",
		Weight:   100,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.ID != "delayed-real-id" {
		t.Fatalf("created channel = %#v", channel)
	}
}
