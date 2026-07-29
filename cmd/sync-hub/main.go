package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/app"
	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/web"
	"github.com/gin-gonic/gin"
)

const (
	defaultConfigPath      = "data/config.yaml"
	defaultListenAddress   = "127.0.0.1:8888"
	defaultShutdownTimeout = 10 * time.Second
	readHeaderTimeout      = 5 * time.Second
	idleTimeout            = 60 * time.Second
)

var version = "dev"
var commit = "unknown"
var buildDate = "unknown"

var (
	errInvalidArguments     = errors.New("invalid arguments")
	errInvalidListenAddress = errors.New("invalid listen address")
	errStartup              = errors.New("startup failed")
	errRuntime              = errors.New("runtime failed")
)

type commandOptions struct {
	configPath     string
	listenOverride string
	showVersion    bool
}

type managedApplication interface {
	Dependencies() api.Dependencies
	Runtime() *api.Runtime
	RunReconcile(context.Context) error
	Close() error
}

type managedHTTPServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

type runtimeDependencies struct {
	newApplication  func(app.Options) (managedApplication, error)
	newRouter       func(api.Dependencies, *api.Runtime) (http.Handler, error)
	wrapHandler     func(http.Handler) http.Handler
	listen          func(string, string) (net.Listener, error)
	newServer       func(string, http.Handler) managedHTTPServer
	shutdownTimeout time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exitCode := execute(ctx, os.Args[1:], os.Stdout, os.Stderr, productionDependencies())
	stop()
	os.Exit(exitCode)
}

func execute(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	deps runtimeDependencies,
) int {
	options, err := parseOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		writeUsage(stdout)
		return 0
	}
	if err != nil {
		writePublicError(stderr, errInvalidArguments)
		return 2
	}
	if options.showVersion {
		_, _ = fmt.Fprintf(stdout, "sync-hub %s (commit %s, built %s)\n", version, commit, buildDate)
		return 0
	}
	if ctx == nil {
		writePublicError(stderr, errInvalidArguments)
		return 2
	}

	if err := runService(ctx, options, deps); err != nil {
		writePublicError(stderr, err)
		return 1
	}
	return 0
}

func parseOptions(args []string) (commandOptions, error) {
	options := commandOptions{configPath: defaultConfigPath}
	flags := flag.NewFlagSet("sync-hub", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.configPath, "config", defaultConfigPath, "configuration file path")
	flags.StringVar(&options.listenOverride, "listen", "", "HTTP listen address override")
	flags.BoolVar(&options.showVersion, "version", false, "print version information")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return commandOptions{}, flag.ErrHelp
		}
		return commandOptions{}, errInvalidArguments
	}
	if flags.NArg() != 0 {
		return commandOptions{}, errInvalidArguments
	}

	options.configPath = strings.TrimSpace(options.configPath)
	options.listenOverride = strings.TrimSpace(options.listenOverride)
	if options.configPath == "" {
		return commandOptions{}, errInvalidArguments
	}
	if options.listenOverride != "" {
		if err := validateListenAddress(options.listenOverride); err != nil {
			return commandOptions{}, err
		}
	}
	return options, nil
}

func writeUsage(writer io.Writer) {
	_, _ = io.WriteString(writer, "usage: sync-hub [-config path] [-listen host:port] [-version]\n")
}

func resolveListenAddress(override string, appConfig config.AppConfig) (string, error) {
	address := strings.TrimSpace(override)
	if address == "" {
		address = net.JoinHostPort(strings.TrimSpace(appConfig.Host), strconv.Itoa(appConfig.Port))
	}
	if err := validateListenAddress(address); err != nil {
		return "", errInvalidListenAddress
	}
	return address, nil
}

func validateListenAddress(address string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || !validListenHost(host) {
		return errInvalidListenAddress
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errInvalidListenAddress
	}
	return nil
}

func validListenHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func productionDependencies() runtimeDependencies {
	return runtimeDependencies{
		newApplication: func(options app.Options) (managedApplication, error) {
			return app.New(options)
		},
		newRouter: func(dependencies api.Dependencies, runtimeState *api.Runtime) (http.Handler, error) {
			gin.SetMode(gin.ReleaseMode)
			return api.NewRouterWithRuntime(dependencies, runtimeState)
		},
		wrapHandler: web.NewHandler,
		listen:      net.Listen,
		newServer: func(address string, handler http.Handler) managedHTTPServer {
			return newHTTPServer(address, handler)
		},
		shutdownTimeout: defaultShutdownTimeout,
	}
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func runService(ctx context.Context, options commandOptions, deps runtimeDependencies) error {
	if options.listenOverride != "" {
		if err := validateListenAddress(options.listenOverride); err != nil {
			return errInvalidListenAddress
		}
	}
	if deps.newApplication == nil || deps.newRouter == nil || deps.wrapHandler == nil ||
		deps.listen == nil || deps.newServer == nil {
		return errStartup
	}

	application, err := deps.newApplication(app.Options{
		ConfigPath: options.configPath,
		Version:    version,
		BuildDate:  buildDate,
	})
	if err != nil || application == nil {
		return errStartup
	}
	closed := false
	defer func() {
		if !closed {
			_ = application.Close()
		}
	}()

	var appConfig config.AppConfig
	if options.listenOverride == "" {
		dependencies := application.Dependencies()
		if dependencies.Config == nil {
			return errStartup
		}
		appConfig = dependencies.Config.Snapshot().App
	}
	address, err := resolveListenAddress(options.listenOverride, appConfig)
	if err != nil {
		return errInvalidListenAddress
	}

	router, err := deps.newRouter(application.Dependencies(), application.Runtime())
	if err != nil || router == nil {
		return errStartup
	}
	handler := deps.wrapHandler(router)
	if handler == nil {
		return errStartup
	}
	listener, err := deps.listen("tcp", address)
	if err != nil || listener == nil {
		return errStartup
	}
	server := deps.newServer(address, handler)
	if server == nil {
		_ = listener.Close()
		return errStartup
	}

	runtimeErr := supervise(ctx, application, server, listener, deps.shutdownTimeout)
	closeErr := application.Close()
	closed = true
	if runtimeErr != nil || closeErr != nil {
		return errRuntime
	}
	return nil
}

func supervise(
	ctx context.Context,
	application managedApplication,
	server managedHTTPServer,
	listener net.Listener,
	shutdownTimeout time.Duration,
) error {
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	runnerCtx, cancelRunner := context.WithCancel(ctx)
	runnerDone := make(chan error, 1)
	serverDone := make(chan error, 1)
	go func() {
		runnerDone <- application.RunReconcile(runnerCtx)
	}()
	go func() {
		serverDone <- server.Serve(listener)
	}()

	var result error
	var runnerErr, serverErr error
	runnerFinished := false
	serverFinished := false
	select {
	case <-ctx.Done():
	case runnerErr = <-runnerDone:
		runnerFinished = true
		if !expectedRunnerStop(runnerErr) {
			result = errRuntime
		}
	case serverErr = <-serverDone:
		serverFinished = true
		result = errRuntime
	}

	// Lifecycle order is deliberate: stop and join background work first, then
	// drain HTTP, and only then let runService close the application resources.
	cancelRunner()
	if !runnerFinished {
		runnerErr = <-runnerDone
		if !expectedRunnerStop(runnerErr) {
			result = errRuntime
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	if err := server.Shutdown(shutdownCtx); err != nil {
		result = errRuntime
		_ = server.Close()
	}
	if !serverFinished {
		select {
		case serverErr = <-serverDone:
		case <-shutdownCtx.Done():
			_ = server.Close()
			serverErr = <-serverDone
			result = errRuntime
		}
	}
	cancelShutdown()
	if serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
		result = errRuntime
	}
	return result
}

func expectedRunnerStop(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func writePublicError(writer io.Writer, err error) {
	message := errRuntime.Error()
	switch {
	case errors.Is(err, errInvalidArguments):
		message = errInvalidArguments.Error()
	case errors.Is(err, errInvalidListenAddress):
		message = errInvalidListenAddress.Error()
	case errors.Is(err, errStartup):
		message = errStartup.Error()
	}
	_, _ = fmt.Fprintln(writer, message)
}
