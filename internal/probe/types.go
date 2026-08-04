package probe

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	TemplateVersion         = "natural-v1"
	defaultMaxResponseBytes = int64(256 << 10)
	maxOutputTokens         = 32
)

type Protocol string

const (
	ProtocolAuto            Protocol = "auto"
	ProtocolChatCompletions Protocol = "chat_completions"
	ProtocolResponses       Protocol = "responses"
	ProtocolCompletions     Protocol = "completions"
)

type Status string

const (
	StatusHealthy             Status = "healthy"
	StatusInconclusive        Status = "inconclusive"
	StatusUnauthorized        Status = "unauthorized"
	StatusModelUnavailable    Status = "model_unavailable"
	StatusProtocolUnavailable Status = "protocol_unavailable"
	StatusRateLimited         Status = "rate_limited"
	StatusTimeout             Status = "timeout"
	StatusCancelled           Status = "cancelled"
	StatusNetworkError        Status = "network_error"
	StatusInvalidResponse     Status = "invalid_response"
	StatusInvalidRequest      Status = "invalid_request"
	StatusUpstreamError       Status = "upstream_error"
	StatusInternalError       Status = "internal_error"
)

type ErrorCode string

const (
	CodeNone                 ErrorCode = ""
	CodeRetainedWordMissing  ErrorCode = "retained_word_missing"
	CodeUnauthorized         ErrorCode = "unauthorized"
	CodeForbidden            ErrorCode = "forbidden"
	CodeModelUnavailable     ErrorCode = "model_unavailable"
	CodeProtocolUnavailable  ErrorCode = "protocol_unavailable"
	CodeRateLimited          ErrorCode = "rate_limited"
	CodeTimeout              ErrorCode = "timeout"
	CodeCancelled            ErrorCode = "cancelled"
	CodeNetwork              ErrorCode = "network_error"
	CodeInvalidJSON          ErrorCode = "invalid_json"
	CodeMissingOutputText    ErrorCode = "missing_output_text"
	CodeUpstreamErrorPayload ErrorCode = "upstream_error_payload"
	CodeResponseTooLarge     ErrorCode = "response_too_large"
	CodeInvalidInput         ErrorCode = "invalid_input"
	CodeRequestRejected      ErrorCode = "request_rejected"
	CodeUpstreamError        ErrorCode = "upstream_error"
	CodeRequestBuildFailed   ErrorCode = "request_build_failed"
	CodeTemplateGeneration   ErrorCode = "template_generation_failed"
)

type Input struct {
	BaseURL  string
	APIKey   string
	Model    string
	Protocol Protocol
}

type Result struct {
	Status          Status
	Protocol        Protocol
	Latency         time.Duration
	CheckedAt       time.Time
	ErrorCode       ErrorCode
	RetryAfter      time.Duration
	TemplateVersion string
}

type PayloadBuilder func(model, prompt string) (any, error)

type TextExtractor func(body []byte) (string, error)

type Capability struct {
	Protocol     Protocol
	Endpoint     string
	BuildPayload PayloadBuilder
	ExtractText  TextExtractor
}

type Service struct {
	client           *http.Client
	random           io.Reader
	now              func() time.Time
	maxResponseBytes int64

	randomMu     sync.Mutex
	capabilityMu sync.RWMutex
	capabilities map[Protocol]Capability
	autoOrder    []Protocol
}

func NewService(client *http.Client) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	service := &Service{
		client:           &clientCopy,
		random:           cryptoReader,
		now:              time.Now,
		maxResponseBytes: defaultMaxResponseBytes,
		capabilities:     make(map[Protocol]Capability),
		autoOrder: []Protocol{
			ProtocolChatCompletions,
			ProtocolResponses,
			ProtocolCompletions,
		},
	}
	for _, capability := range builtInCapabilities() {
		service.capabilities[capability.Protocol] = capability
	}
	return service
}

func (s *Service) RegisterCapability(capability Capability) error {
	if s == nil || capability.Protocol == "" || capability.Protocol == ProtocolAuto ||
		capability.Endpoint == "" || capability.Endpoint[0] != '/' ||
		capability.BuildPayload == nil || capability.ExtractText == nil {
		return errors.New("invalid probe capability")
	}
	s.capabilityMu.Lock()
	defer s.capabilityMu.Unlock()
	if _, exists := s.capabilities[capability.Protocol]; exists {
		return errors.New("probe capability already registered")
	}
	s.capabilities[capability.Protocol] = capability
	return nil
}
