package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/app"
	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/gin-gonic/gin"
)

// User journeys covered here:
//   - An operator can discover version/build metadata without starting services.
//   - An operator gets the configured default address or an explicit CLI override.
//   - A running process serves both the management API and embedded console.
//   - Cancellation drains reconciliation before HTTP and config resources close.
//   - Invalid input and startup failures are non-zero without exposing credentials.

func TestParseOptionsDefinesStableCommandContract(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got, err := parseOptions(nil)
		if err != nil {
			t.Fatalf("parse defaults: %v", err)
		}
		if got.configPath != defaultConfigPath {
			t.Fatalf("config path = %q, want %q", got.configPath, defaultConfigPath)
		}
		if got.listenOverride != "" {
			t.Fatalf("listen override = %q, want empty", got.listenOverride)
		}
		if got.showVersion {
			t.Fatal("showVersion = true, want false")
		}
	})

	t.Run("overrides", func(t *testing.T) {
		got, err := parseOptions([]string{
			"-config", "/tmp/sync-hub.yaml",
			"-listen", "127.0.0.1:9000",
			"-version",
		})
		if err != nil {
			t.Fatalf("parse overrides: %v", err)
		}
		if got.configPath != "/tmp/sync-hub.yaml" {
			t.Fatalf("config path = %q", got.configPath)
		}
		if got.listenOverride != "127.0.0.1:9000" {
			t.Fatalf("listen override = %q", got.listenOverride)
		}
		if !got.showVersion {
			t.Fatal("showVersion = false, want true")
		}
	})

	for _, args := range [][]string{
		{"-config", ""},
		{"-listen", "missing-port"},
		{"-unknown", "credential-that-must-not-be-printed"},
		{"unexpected-positional-argument"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%q) succeeded, want error", args)
		}
	}
}

func TestResolveListenAddressUsesConfigAndOverride(t *testing.T) {
	defaultAppConfig := config.Default().App
	got, err := resolveListenAddress("", defaultAppConfig)
	if err != nil {
		t.Fatalf("resolve default address: %v", err)
	}
	if defaultListenAddress != "127.0.0.1:8888" {
		t.Fatalf("default listen constant = %q, want 127.0.0.1:8888", defaultListenAddress)
	}
	if got != defaultListenAddress {
		t.Fatalf("default address = %q, want %q", got, defaultListenAddress)
	}

	configured := defaultAppConfig
	configured.Host = "localhost"
	configured.Port = 7777
	got, err = resolveListenAddress("", configured)
	if err != nil || got != "localhost:7777" {
		t.Fatalf("configured address = %q, %v; want localhost:7777", got, err)
	}

	got, err = resolveListenAddress("127.0.0.1:9999", configured)
	if err != nil || got != "127.0.0.1:9999" {
		t.Fatalf("override address = %q, %v; want 127.0.0.1:9999", got, err)
	}

	credential := "listen-secret-should-not-leak"
	if _, err = resolveListenAddress(credential, configured); err == nil {
		t.Fatal("invalid override succeeded")
	} else if strings.Contains(err.Error(), credential) {
		t.Fatalf("invalid address error leaked input: %v", err)
	}
}

func TestExecuteVersionDoesNotStartApplication(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version, commit, buildDate
	version, commit, buildDate = "v1.2.3", "abc123", "2026-07-26T10:00:00+08:00"
	t.Cleanup(func() {
		version, commit, buildDate = oldVersion, oldCommit, oldBuildDate
	})

	deps := productionDependencies()
	deps.newApplication = func(app.Options) (managedApplication, error) {
		t.Fatal("application started for -version")
		return nil, nil
	}
	var stdout, stderr bytes.Buffer
	if code := execute(context.Background(), []string{"-version"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, value := range []string{"v1.2.3", "abc123", "2026-07-26T10:00:00+08:00"} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("version output %q does not contain %q", stdout.String(), value)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestExecuteHelpDoesNotStartApplication(t *testing.T) {
	deps := productionDependencies()
	deps.newApplication = func(app.Options) (managedApplication, error) {
		t.Fatal("application started for -help")
		return nil, nil
	}
	var stdout, stderr bytes.Buffer
	if code := execute(context.Background(), []string{"-help"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, flagName := range []string{"-config", "-listen", "-version"} {
		if !strings.Contains(stdout.String(), flagName) {
			t.Fatalf("help output %q does not contain %q", stdout.String(), flagName)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestNewHTTPServerSetsResourceTimeouts(t *testing.T) {
	server := newHTTPServer("127.0.0.1:8888", http.NotFoundHandler())
	if server.Addr != "127.0.0.1:8888" {
		t.Fatalf("Addr = %q", server.Addr)
	}
	if server.Handler == nil {
		t.Fatal("Handler is nil")
	}
	if server.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %s, want positive", server.ReadHeaderTimeout)
	}
	if server.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %s, want positive", server.IdleTimeout)
	}
}

func TestProductionRouterRunsGinInReleaseMode(t *testing.T) {
	previousMode := gin.Mode()
	previousWriter := gin.DefaultWriter
	gin.SetMode(gin.DebugMode)
	var debugOutput bytes.Buffer
	gin.DefaultWriter = &debugOutput
	t.Cleanup(func() {
		gin.SetMode(previousMode)
		gin.DefaultWriter = previousWriter
	})

	deps := productionDependencies()
	application, err := deps.newApplication(app.Options{
		ConfigPath: t.TempDir() + "/config.yaml",
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	router, err := deps.newRouter(application.Dependencies(), application.Runtime())
	if err != nil {
		t.Fatalf("create production router: %v", err)
	}
	if router == nil {
		t.Fatal("production router is nil")
	}
	if got := gin.Mode(); got != gin.ReleaseMode {
		t.Fatalf("Gin mode = %q, want %q", got, gin.ReleaseMode)
	}
	if got := debugOutput.String(); got != "" {
		t.Fatalf("Gin emitted debug startup output: %q", got)
	}
}

func TestExecuteServesEmbeddedConsoleAndManagementAPI(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deps := productionDependencies()
	var requestedAddress string
	deps.listen = func(_, address string) (net.Listener, error) {
		requestedAddress = address
		return listener, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	configPath := t.TempDir() + "/config.yaml"
	go func() {
		done <- execute(ctx, []string{"-config", configPath}, &stdout, &stderr, deps)
	}()

	baseURL := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: time.Second}
	waitForHTTP(t, client, baseURL+"/api/v1/health")

	assertHTTPResponse(t, client, baseURL+"/api/v1/health", http.StatusOK, `"success":true`)
	assertHTTPResponse(t, client, baseURL+"/", http.StatusOK, "<!doctype html>")

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process did not stop after cancellation")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if requestedAddress != defaultListenAddress {
		t.Fatalf("requested listen address = %q, want %q", requestedAddress, defaultListenAddress)
	}
}

func TestExecuteStopsRunnerBeforeHTTPAndApplication(t *testing.T) {
	events := &eventLog{}
	runnerStarted := make(chan struct{})
	fakeApp := &stubApplication{
		runReconcile: func(ctx context.Context) error {
			close(runnerStarted)
			<-ctx.Done()
			events.add("runner stopped")
			return ctx.Err()
		},
		close: func() error {
			events.add("application closed")
			return nil
		},
	}
	fakeHTTP := newStubHTTPServer(func() {
		events.add("http shutdown")
	})
	deps := stubDependencies(fakeApp, fakeHTTP)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		done <- execute(ctx, []string{"-listen", defaultListenAddress}, io.Discard, &stderr, deps)
	}()

	select {
	case <-runnerStarted:
	case <-time.After(time.Second):
		t.Fatal("reconcile runner did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("execute did not stop")
	}

	want := []string{"runner stopped", "http shutdown", "application closed"}
	if got := events.snapshot(); !equalStrings(got, want) {
		t.Fatalf("shutdown order = %q, want %q", got, want)
	}
	if fakeApp.runCalls.Load() != 1 {
		t.Fatalf("RunReconcile calls = %d, want 1", fakeApp.runCalls.Load())
	}
	if fakeApp.closeCalls.Load() != 1 {
		t.Fatalf("Close calls = %d, want 1", fakeApp.closeCalls.Load())
	}
}

func TestExecuteReturnsNonzeroWithoutLeakingSecrets(t *testing.T) {
	credential := "credential-value-must-remain-private"
	tests := []struct {
		name string
		args []string
		deps func() runtimeDependencies
	}{
		{
			name: "invalid arguments",
			args: []string{"-unknown", credential},
			deps: productionDependencies,
		},
		{
			name: "invalid listen address",
			args: []string{"-listen", credential},
			deps: productionDependencies,
		},
		{
			name: "application startup",
			deps: func() runtimeDependencies {
				deps := productionDependencies()
				deps.newApplication = func(app.Options) (managedApplication, error) {
					return nil, errors.New("upstream access_token=" + credential)
				}
				return deps
			},
		},
		{
			name: "listen startup",
			args: []string{"-listen", defaultListenAddress},
			deps: func() runtimeDependencies {
				fakeApp := &stubApplication{}
				deps := stubDependencies(fakeApp, newStubHTTPServer(nil))
				deps.listen = func(_, _ string) (net.Listener, error) {
					return nil, errors.New("bind failed using api_key=" + credential)
				}
				return deps
			},
		},
		{
			name: "runner failure",
			args: []string{"-listen", defaultListenAddress},
			deps: func() runtimeDependencies {
				fakeApp := &stubApplication{runReconcile: func(context.Context) error {
					return errors.New("remote response includes " + credential)
				}}
				return stubDependencies(fakeApp, newStubHTTPServer(nil))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := execute(context.Background(), test.args, io.Discard, &stderr, test.deps())
			if code == 0 {
				t.Fatalf("exit code = 0, stderr = %q", stderr.String())
			}
			if strings.Contains(stderr.String(), credential) {
				t.Fatalf("stderr leaked credential: %q", stderr.String())
			}
			if strings.TrimSpace(stderr.String()) == "" {
				t.Fatal("stderr is empty, want stable public error")
			}
		})
	}
}

func TestExecuteClosesApplicationWhenRouterConstructionFails(t *testing.T) {
	credential := "router-secret"
	fakeApp := &stubApplication{}
	deps := stubDependencies(fakeApp, newStubHTTPServer(nil))
	deps.newRouter = func(api.Dependencies, *api.Runtime) (http.Handler, error) {
		return nil, errors.New("router failed with " + credential)
	}
	var stderr bytes.Buffer
	code := execute(context.Background(), []string{"-listen", defaultListenAddress}, io.Discard, &stderr, deps)
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if fakeApp.closeCalls.Load() != 1 {
		t.Fatalf("Close calls = %d, want 1", fakeApp.closeCalls.Load())
	}
	if strings.Contains(stderr.String(), credential) {
		t.Fatalf("stderr leaked router error: %q", stderr.String())
	}
}

func TestExecuteDrainsAfterServeFailureWithoutLeakingError(t *testing.T) {
	credential := "serve-error-secret"
	runnerStarted := make(chan struct{})
	fakeApp := &stubApplication{runReconcile: func(ctx context.Context) error {
		close(runnerStarted)
		<-ctx.Done()
		return ctx.Err()
	}}
	server := &immediateHTTPServer{serveErr: errors.New("serve failed with " + credential)}
	deps := stubDependencies(fakeApp, server)
	var stderr bytes.Buffer
	code := execute(context.Background(), []string{"-listen", defaultListenAddress}, io.Discard, &stderr, deps)
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	select {
	case <-runnerStarted:
	default:
		t.Fatal("reconcile runner did not start")
	}
	if server.shutdownCalls.Load() != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", server.shutdownCalls.Load())
	}
	if fakeApp.closeCalls.Load() != 1 {
		t.Fatalf("Close calls = %d, want 1", fakeApp.closeCalls.Load())
	}
	if strings.Contains(stderr.String(), credential) {
		t.Fatalf("stderr leaked Serve error: %q", stderr.String())
	}
}

func TestExecuteReportsShutdownAndCloseFailuresWithoutLeakingErrors(t *testing.T) {
	credential := "shutdown-close-secret"
	tests := []struct {
		name       string
		closeError error
		server     *stubHTTPServer
	}{
		{
			name:   "shutdown",
			server: newStubHTTPServerWithError(errors.New("shutdown failed with " + credential)),
		},
		{
			name:       "close",
			closeError: errors.New("close failed with " + credential),
			server:     newStubHTTPServer(nil),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			fakeApp := &stubApplication{
				runReconcile: func(ctx context.Context) error {
					cancel()
					<-ctx.Done()
					return ctx.Err()
				},
				close: func() error { return test.closeError },
			}
			var stderr bytes.Buffer
			code := execute(ctx, []string{"-listen", defaultListenAddress}, io.Discard, &stderr,
				stubDependencies(fakeApp, test.server))
			if code == 0 {
				t.Fatal("exit code = 0, want non-zero")
			}
			if strings.Contains(stderr.String(), credential) {
				t.Fatalf("stderr leaked lifecycle error: %q", stderr.String())
			}
			if fakeApp.closeCalls.Load() != 1 {
				t.Fatalf("Close calls = %d, want 1", fakeApp.closeCalls.Load())
			}
			if test.name == "shutdown" && test.server.closeCalls.Load() != 1 {
				t.Fatalf("HTTP Close calls = %d, want 1", test.server.closeCalls.Load())
			}
		})
	}
}

func waitForHTTP(t *testing.T, client *http.Client, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("HTTP service %s did not become ready", url)
}

func assertHTTPResponse(t *testing.T, client *http.Client, url string, wantStatus int, wantBody string) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d: %s", url, response.StatusCode, wantStatus, body)
	}
	if !strings.Contains(strings.ToLower(string(body)), strings.ToLower(wantBody)) {
		t.Fatalf("GET %s body does not contain %q: %s", url, wantBody, body)
	}
}

type stubApplication struct {
	deps         api.Dependencies
	runtime      *api.Runtime
	runReconcile func(context.Context) error
	close        func() error
	runCalls     atomic.Int32
	closeCalls   atomic.Int32
}

func (a *stubApplication) Dependencies() api.Dependencies { return a.deps }

func (a *stubApplication) Runtime() *api.Runtime { return a.runtime }

func (a *stubApplication) RunReconcile(ctx context.Context) error {
	a.runCalls.Add(1)
	if a.runReconcile == nil {
		return nil
	}
	return a.runReconcile(ctx)
}

func (a *stubApplication) Close() error {
	a.closeCalls.Add(1)
	if a.close == nil {
		return nil
	}
	return a.close()
}

type stubHTTPServer struct {
	shutdown     chan struct{}
	shutdownOnce sync.Once
	onShutdown   func()
	shutdownErr  error
	closeCalls   atomic.Int32
}

func newStubHTTPServer(onShutdown func()) *stubHTTPServer {
	return &stubHTTPServer{shutdown: make(chan struct{}), onShutdown: onShutdown}
}

func newStubHTTPServerWithError(err error) *stubHTTPServer {
	return &stubHTTPServer{shutdown: make(chan struct{}), shutdownErr: err}
}

func (s *stubHTTPServer) Serve(listener net.Listener) error {
	defer listener.Close()
	<-s.shutdown
	return http.ErrServerClosed
}

func (s *stubHTTPServer) Shutdown(context.Context) error {
	if s.onShutdown != nil {
		s.onShutdown()
	}
	s.shutdownOnce.Do(func() { close(s.shutdown) })
	return s.shutdownErr
}

func (s *stubHTTPServer) Close() error {
	s.closeCalls.Add(1)
	s.shutdownOnce.Do(func() { close(s.shutdown) })
	return nil
}

type immediateHTTPServer struct {
	serveErr      error
	shutdownCalls atomic.Int32
}

func (s *immediateHTTPServer) Serve(net.Listener) error { return s.serveErr }

func (s *immediateHTTPServer) Shutdown(context.Context) error {
	s.shutdownCalls.Add(1)
	return nil
}

func (s *immediateHTTPServer) Close() error { return nil }

type passiveListener struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func newPassiveListener() *passiveListener {
	return &passiveListener{closed: make(chan struct{})}
}

func (l *passiveListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *passiveListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *passiveListener) Addr() net.Addr {
	return stubAddr(defaultListenAddress)
}

type stubAddr string

func (a stubAddr) Network() string { return "tcp" }

func (a stubAddr) String() string { return string(a) }

func stubDependencies(application managedApplication, server managedHTTPServer) runtimeDependencies {
	return runtimeDependencies{
		newApplication: func(app.Options) (managedApplication, error) {
			return application, nil
		},
		newRouter: func(api.Dependencies, *api.Runtime) (http.Handler, error) {
			return http.NotFoundHandler(), nil
		},
		wrapHandler: func(handler http.Handler) http.Handler { return handler },
		listen: func(_, _ string) (net.Listener, error) {
			return newPassiveListener(), nil
		},
		newServer:       func(string, http.Handler) managedHTTPServer { return server },
		shutdownTimeout: time.Second,
	}
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
