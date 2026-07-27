package newapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestTransportClassifiesAuthenticationAndPermissionFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		want    error
		notWant error
	}{
		{name: "unauthenticated", status: http.StatusUnauthorized, want: ErrUnauthenticated, notWant: ErrInsufficientPrivilege},
		{name: "forbidden", status: http.StatusForbidden, want: ErrInsufficientPrivilege, notWant: ErrUnauthenticated},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			t.Cleanup(server.Close)

			transport := testTransport(server)
			var destination map[string]any
			err := transport.get(context.Background(), "/api/user/self", "", &destination)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, test.want)
			}
			if errors.Is(err, test.notWant) {
				t.Fatalf("error = %v, must not match %v", err, test.notWant)
			}
		})
	}
}

func TestTransportPreservesRetryAfterOnRateLimit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "delta seconds", header: "17", want: 17 * time.Second},
		{name: "http date", header: now.Add(31 * time.Second).Format(http.TimeFormat), want: 31 * time.Second},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", test.header)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			t.Cleanup(server.Close)

			transport := testTransport(server)
			var destination map[string]any
			err := transport.get(context.Background(), "/api/token/batch/keys", "", &destination)
			if !errors.Is(err, platform.ErrRateLimited) {
				t.Fatalf("error = %v, want ErrRateLimited", err)
			}
			var rateLimitErr *platform.RateLimitError
			if !errors.As(err, &rateLimitErr) {
				t.Fatalf("error = %T %v, want *platform.RateLimitError", err, err)
			}
			if got := rateLimitErr.RetryAfter; got < test.want-time.Second || got > test.want+time.Second {
				t.Fatalf("RetryAfter = %s, want about %s", got, test.want)
			}
		})
	}
}

func testTransport(server *httptest.Server) transport {
	return transport{
		baseURL:        server.URL,
		accessToken:    "test-token",
		requestTimeout: time.Second,
		client:         server.Client(),
	}
}
