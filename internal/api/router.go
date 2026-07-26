package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

const maxJSONBodyBytes int64 = 1 << 20

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type runtimeKey struct {
	assetID  string
	targetID string
}

type tupleLock struct {
	mutex sync.Mutex
	refs  int
}

type pendingReconcile struct {
	channelID string
}

type server struct {
	deps    Dependencies
	runtime *Runtime

	locksMu sync.Mutex
	locks   map[runtimeKey]*tupleLock
}

func NewRouter(deps Dependencies) (*gin.Engine, error) {
	return NewRouterWithRuntime(deps, nil)
}

// NewRouterWithRuntime builds a router that shares transient reconciliation
// state with callers such as a background reconciliation scheduler.
func NewRouterWithRuntime(deps Dependencies, runtimeState *Runtime) (*gin.Engine, error) {
	missing := make([]string, 0, 6)
	if isNilDependency(deps.Config) {
		missing = append(missing, "config")
	}
	if isNilDependency(deps.Adapters) {
		missing = append(missing, "adapters")
	}
	if isNilDependency(deps.Discovery) {
		missing = append(missing, "discovery")
	}
	if isNilDependency(deps.Sync) {
		missing = append(missing, "sync")
	}
	if isNilDependency(deps.Mappings) {
		missing = append(missing, "mappings")
	}
	if isNilDependency(deps.Reconcile) {
		missing = append(missing, "reconcile")
	}
	if len(missing) != 0 {
		return nil, errors.New("management API dependencies are incomplete: " + strings.Join(missing, ", "))
	}
	if strings.TrimSpace(deps.Version) == "" {
		deps.Version = "dev"
	}
	if runtimeState == nil {
		runtimeState = NewRuntime()
	}

	s := &server{
		deps:    deps,
		runtime: runtimeState,
		locks:   make(map[runtimeKey]*tupleLock),
	}
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	_ = engine.SetTrustedProxies(nil)
	engine.Use(s.recoveryMiddleware(), s.requestIDMiddleware(), securityHeadersMiddleware())

	v1 := engine.Group("/api/v1")
	{
		v1.GET("/health", s.health)
		v1.GET("/config", s.getConfig)
		v1.PUT("/config/app", s.updateApp)

		v1.POST("/targets", s.createTarget)
		v1.PUT("/targets/:target_id", s.updateTarget)
		v1.DELETE("/targets/:target_id", s.deleteTarget)

		v1.POST("/upstreams", s.createUpstream)
		v1.PUT("/upstreams/:upstream_id", s.updateUpstream)
		v1.DELETE("/upstreams/:upstream_id", s.deleteUpstream)

		v1.GET("/targets/:target_id/channels", s.listChannels)
		v1.PUT("/targets/:target_id/channels/:channel_id", s.updateChannel)
		v1.DELETE("/targets/:target_id/channels/:channel_id", s.deleteChannel)

		v1.POST("/upstreams/:upstream_id/refresh", s.refreshUpstream)
		v1.GET("/upstreams/:upstream_id/assets", s.listAssets)
		v1.GET("/matrix", s.matrix)
		v1.POST("/sync", s.batchSync)

		v1.POST("/targets/:target_id/reconcile", s.reconcileTarget)
		v1.POST("/targets/:target_id/drift/accept", s.acceptDrift)
	}

	engine.NoRoute(func(c *gin.Context) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
	})
	engine.NoMethod(func(c *gin.Context) {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
	})
	return engine, nil
}

func (s *server) requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := ""
		values := c.Request.Header.Values("X-Request-ID")
		if len(values) == 1 && validRequestID.MatchString(values[0]) {
			requestID = values[0]
		}
		if requestID == "" && s.deps.RequestIDGenerator != nil {
			candidate := s.deps.RequestIDGenerator()
			if validRequestID.MatchString(candidate) {
				requestID = candidate
			}
		}
		if requestID == "" {
			requestID = generateRequestID()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Next()
	}
}

func (s *server) recoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() != nil && !c.Writer.Written() {
				writeFailure(c, http.StatusInternalServerError, "internal_error")
				c.Abort()
			}
		}()
		c.Next()
	}
}

func generateRequestID() string {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err == nil {
		return "req_" + hex.EncodeToString(random)
	}
	// rand.Read practically cannot fail on supported Go platforms. Retain a
	// valid, non-sensitive fallback rather than reflecting the error.
	return "req_unavailable"
}

func requestID(c *gin.Context) string {
	value, _ := c.Get("request_id")
	requestID, _ := value.(string)
	if !validRequestID.MatchString(requestID) {
		return generateRequestID()
	}
	return requestID
}

func writeSuccess(c *gin.Context, status int, data any) {
	c.JSON(status, successEnvelope{Success: true, Data: data, RequestID: requestID(c)})
}

func writeFailure(c *gin.Context, status int, code string) {
	message, ok := safeErrorMessages[code]
	if !ok {
		code = "internal_error"
		message = safeErrorMessages[code]
		status = http.StatusInternalServerError
	}
	c.AbortWithStatusJSON(status, errorEnvelope{
		Success:   false,
		Error:     errorResponse{Code: code, Message: message},
		RequestID: requestID(c),
	})
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

func (s *server) lockTuples(keys []runtimeKey) func() {
	locks := make([]*tupleLock, 0, len(keys))
	s.locksMu.Lock()
	for _, key := range keys {
		lock := s.locks[key]
		if lock == nil {
			lock = &tupleLock{}
			s.locks[key] = lock
		}
		lock.refs++
		locks = append(locks, lock)
	}
	s.locksMu.Unlock()
	for _, lock := range locks {
		lock.mutex.Lock()
	}

	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].mutex.Unlock()
		}
		s.locksMu.Lock()
		for i, key := range keys {
			locks[i].refs--
			if locks[i].refs == 0 {
				delete(s.locks, key)
			}
		}
		s.locksMu.Unlock()
	}
}
