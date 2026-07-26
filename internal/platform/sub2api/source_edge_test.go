package sub2api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestSourceRejectsUnsafeListResponsesWithoutLeakingThem(t *testing.T) {
	marker := strings.Join([]string{"upstream", "body", "marker"}, "-")
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non 2xx", status: http.StatusBadGateway, body: `{"error":"` + marker + `"}`},
		{name: "business failure", status: http.StatusOK, body: `{"code":73,"message":"` + marker + `","data":null}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{"code":0,"message":"` + marker + `"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			source := mustNewTestSource(t, server.URL, testAdminKey(), time.Second)
			_, err := source.ListAssets(context.Background(), platform.PageCursor{})
			if err == nil {
				t.Fatal("ListAssets() error = nil, want rejection")
			}
			for _, forbidden := range []string{testAdminKey(), marker, tt.body} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error leaked forbidden response material %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestSourceRejectsUnsafeModelResponsesWithoutPublishingRecords(t *testing.T) {
	marker := strings.Join([]string{"model", "body", "marker"}, "-")
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non 2xx", status: http.StatusServiceUnavailable, body: marker},
		{name: "business failure", status: http.StatusOK, body: `{"code":9,"message":"` + marker + `"}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{"code":0,"data":[` + marker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var exportCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/admin/accounts":
					writeEnvelope(t, w, accountPage([]any{map[string]any{
						"id": 61, "name": "Model Failure", "platform": "openai", "type": "apikey",
						"credentials_status": map[string]bool{"has_api_key": true}, "status": "active", "schedulable": true,
					}}, 1, 1, 100))
				case "/api/v1/admin/accounts/61/models":
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
				case "/api/v1/admin/accounts/data":
					exportCalls.Add(1)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			source := mustNewTestSource(t, server.URL, testAdminKey(), time.Second)
			_, err := source.ListAssets(context.Background(), platform.PageCursor{})
			if err == nil {
				t.Fatal("ListAssets() error = nil, want model failure")
			}
			if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), testAdminKey()) {
				t.Fatalf("model error leaked sensitive material: %v", err)
			}
			if _, resolveErr := source.ResolveSecret(context.Background(), "source-main:key:61", platform.SecretGrant{}); !errors.Is(resolveErr, platform.ErrSecretUnavailable) {
				t.Fatalf("ResolveSecret() after failed listing error = %v", resolveErr)
			}
			if exportCalls.Load() != 0 {
				t.Fatalf("failed page published a secret record")
			}
		})
	}
}

func TestSourcePropagatesTimeoutAndCallerCancellation(t *testing.T) {
	t.Run("response body timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		}))
		defer server.Close()

		source := mustNewTestSource(t, server.URL, testAdminKey(), 25*time.Millisecond)
		_, err := source.ListAssets(context.Background(), platform.PageCursor{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ListAssets() error = %v, want deadline exceeded", err)
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			calls.Add(1)
		}))
		defer server.Close()

		source := mustNewTestSource(t, server.URL, testAdminKey(), time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := source.ListAssets(ctx, platform.PageCursor{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListAssets() error = %v, want context canceled", err)
		}
		if calls.Load() > 1 {
			t.Fatalf("canceled call made %d requests", calls.Load())
		}
	})
}

func TestSourceRejectsUnsafeSecretExportResponses(t *testing.T) {
	marker := strings.Join([]string{"export", "body", "marker"}, "-")
	secretValue := strings.Join([]string{"exported", "key", "value"}, "-")
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non 2xx", status: http.StatusForbidden, body: `{"error":"` + marker + `"}`},
		{name: "business failure", status: http.StatusOK, body: `{"code":44,"message":"` + marker + `"}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{"code":0,"data":{"accounts":[` + marker},
		{name: "empty export", status: http.StatusOK, body: `{"code":0,"message":"success","data":{"accounts":[]}}`},
		{name: "mismatched account", status: http.StatusOK, body: `{"code":0,"message":"success","data":{"accounts":[{"platform":"openai","type":"oauth","credentials":{"api_key":"` + secretValue + `"}}]}}`},
		{name: "blank secret", status: http.StatusOK, body: `{"code":0,"message":"success","data":{"accounts":[{"platform":"openai","type":"apikey","credentials":{"api_key":"   "}}]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/admin/accounts":
					writeEnvelope(t, w, accountPage([]any{map[string]any{
						"id": 71, "name": "Export Target", "platform": "openai", "type": "apikey",
						"credentials_status": map[string]bool{"has_api_key": true}, "status": "active", "schedulable": true,
					}}, 1, 1, 100))
				case "/api/v1/admin/accounts/71/models":
					writeEnvelope(t, w, []any{})
				case "/api/v1/admin/accounts/data":
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			source := mustNewTestSource(t, server.URL, testAdminKey(), time.Second)
			if _, err := source.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
				t.Fatalf("ListAssets() error = %v", err)
			}
			_, err := source.ResolveSecret(context.Background(), "source-main:key:71", platform.SecretGrant{})
			if err == nil {
				t.Fatal("ResolveSecret() error = nil, want unavailable export")
			}
			for _, forbidden := range []string{testAdminKey(), marker, secretValue, tt.body} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("secret error leaked forbidden material %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestSourceRejectsAccountWithoutStableID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/accounts" {
			http.NotFound(w, r)
			return
		}
		writeEnvelope(t, w, accountPage([]any{map[string]any{
			"id": 0, "name": "No ID", "platform": "openai", "type": "apikey", "status": "active",
		}}, 1, 1, 100))
	}))
	defer server.Close()

	source := mustNewTestSource(t, server.URL, testAdminKey(), time.Second)
	_, err := source.ListAssets(context.Background(), platform.PageCursor{})
	if err == nil || strings.Contains(err.Error(), "No ID") {
		t.Fatalf("ListAssets() error = %v, want sanitized invalid account error", err)
	}
}
