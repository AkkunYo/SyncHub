package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Service) Probe(ctx context.Context, input Input) Result {
	started := s.currentTime()
	baseResult := Result{
		Protocol:        input.Protocol,
		TemplateVersion: TemplateVersion,
	}
	if s == nil || ctx == nil {
		baseResult.Status = StatusInvalidRequest
		baseResult.ErrorCode = CodeInvalidInput
		return s.finish(baseResult, started)
	}
	if contextResult, done := classifyContext(ctx); done {
		return s.finish(withMetadata(contextResult, input.Protocol), started)
	}
	baseURL, valid := validateInput(input, s.hasCapability(input.Protocol))
	if !valid {
		baseResult.Status = StatusInvalidRequest
		baseResult.ErrorCode = CodeInvalidInput
		return s.finish(baseResult, started)
	}

	s.randomMu.Lock()
	task, err := generateTask(s.random)
	s.randomMu.Unlock()
	if err != nil {
		baseResult.Status = StatusInternalError
		baseResult.ErrorCode = CodeTemplateGeneration
		return s.finish(baseResult, started)
	}
	baseResult.TemplateVersion = task.TemplateVersion

	protocols := []Protocol{input.Protocol}
	if input.Protocol == ProtocolAuto {
		protocols = append([]Protocol(nil), s.autoOrder...)
	}
	var modelUnavailable *Result
	var last Result
	for _, protocol := range protocols {
		capability, exists := s.capability(protocol)
		if !exists {
			baseResult.Status = StatusInvalidRequest
			baseResult.ErrorCode = CodeInvalidInput
			return s.finish(baseResult, started)
		}
		attempt := s.probeCapability(ctx, baseURL, input, task, capability)
		last = attempt
		if input.Protocol != ProtocolAuto ||
			(attempt.Status != StatusModelUnavailable && attempt.Status != StatusProtocolUnavailable) {
			return s.finish(attempt, started)
		}
		if attempt.Status == StatusModelUnavailable && modelUnavailable == nil {
			copy := attempt
			modelUnavailable = &copy
		}
	}
	if modelUnavailable != nil {
		return s.finish(*modelUnavailable, started)
	}
	return s.finish(last, started)
}

func (s *Service) probeCapability(
	ctx context.Context,
	baseURL *url.URL,
	input Input,
	task generatedTask,
	capability Capability,
) Result {
	result := Result{Protocol: capability.Protocol, TemplateVersion: task.TemplateVersion}
	payload, err := capability.BuildPayload(input.Model, task.Prompt)
	if err != nil {
		result.Status = StatusInternalError
		result.ErrorCode = CodeRequestBuildFailed
		return result
	}
	body, err := json.Marshal(payload)
	if err != nil {
		result.Status = StatusInternalError
		result.ErrorCode = CodeRequestBuildFailed
		return result
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(baseURL, capability.Endpoint), bytes.NewReader(body))
	if err != nil {
		result.Status = StatusInvalidRequest
		result.ErrorCode = CodeInvalidInput
		return result
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+input.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "SyncHub-Probe/"+TemplateVersion)

	response, err := s.client.Do(request)
	if err != nil {
		return withMetadata(classifyTransport(ctx, err), capability.Protocol)
	}
	defer response.Body.Close()
	if classified, done := s.classifyHTTP(response); done {
		classified.Protocol = capability.Protocol
		classified.TemplateVersion = task.TemplateVersion
		return classified
	}

	responseBody, tooLarge, err := readBounded(response.Body, s.maxResponseBytes)
	if err != nil {
		return withMetadata(classifyTransport(ctx, err), capability.Protocol)
	}
	if tooLarge {
		result.Status = StatusInvalidResponse
		result.ErrorCode = CodeResponseTooLarge
		return result
	}
	if !json.Valid(responseBody) {
		result.Status = StatusInvalidResponse
		result.ErrorCode = CodeInvalidJSON
		return result
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err == nil && hasJSONValue(envelope.Error) {
		result.Status = StatusInvalidResponse
		result.ErrorCode = CodeUpstreamErrorPayload
		return result
	}
	text, err := capability.ExtractText(responseBody)
	if err != nil {
		result.Status = StatusInvalidResponse
		if errors.Is(err, errMissingOutputText) {
			result.ErrorCode = CodeMissingOutputText
		} else {
			result.ErrorCode = CodeInvalidJSON
		}
		return result
	}
	if strings.Contains(text, task.RetainedWord) {
		result.Status = StatusHealthy
		return result
	}
	result.Status = StatusInconclusive
	result.ErrorCode = CodeRetainedWordMissing
	return result
}

func validateInput(input Input, protocolKnown bool) (*url.URL, bool) {
	if strings.TrimSpace(input.BaseURL) == "" || !validOpaqueInput(input.APIKey) ||
		!validOpaqueInput(input.Model) || !protocolKnown {
		return nil, false
	}
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, false
	}
	return parsed, true
}

func validOpaqueInput(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return false
		}
	}
	return true
}

func endpointURL(baseURL *url.URL, endpoint string) string {
	resolved := *baseURL
	basePath := strings.TrimRight(resolved.Path, "/")
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(endpoint, "/v1/") {
		endpoint = strings.TrimPrefix(endpoint, "/v1")
	}
	resolved.Path = basePath + endpoint
	resolved.RawPath = ""
	return resolved.String()
}

func (s *Service) classifyHTTP(response *http.Response) (Result, bool) {
	result := Result{}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		result.Status, result.ErrorCode = StatusUnauthorized, CodeUnauthorized
	case http.StatusForbidden:
		result.Status, result.ErrorCode = StatusUnauthorized, CodeForbidden
	case http.StatusNotFound:
		result.Status, result.ErrorCode = StatusModelUnavailable, CodeModelUnavailable
	case http.StatusMethodNotAllowed:
		result.Status, result.ErrorCode = StatusProtocolUnavailable, CodeProtocolUnavailable
	case http.StatusTooManyRequests:
		result.Status, result.ErrorCode = StatusRateLimited, CodeRateLimited
		result.RetryAfter = parseRetryAfter(response.Header.Get("Retry-After"), s.currentTime())
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		result.Status, result.ErrorCode = StatusTimeout, CodeTimeout
	default:
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return Result{}, false
		}
		if response.StatusCode >= 500 {
			result.Status, result.ErrorCode = StatusUpstreamError, CodeUpstreamError
		} else {
			result.Status, result.ErrorCode = StatusInvalidResponse, CodeRequestRejected
		}
	}
	return result, true
}

func classifyContext(ctx context.Context) (Result, bool) {
	switch ctx.Err() {
	case nil:
		return Result{}, false
	case context.DeadlineExceeded:
		return Result{Status: StatusTimeout, ErrorCode: CodeTimeout}, true
	default:
		return Result{Status: StatusCancelled, ErrorCode: CodeCancelled}, true
	}
}

func classifyTransport(ctx context.Context, err error) Result {
	if ctx != nil {
		if result, done := classifyContext(ctx); done {
			return result
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Result{Status: StatusTimeout, ErrorCode: CodeTimeout}
	}
	if errors.Is(err, context.Canceled) {
		return Result{Status: StatusCancelled, ErrorCode: CodeCancelled}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return Result{Status: StatusTimeout, ErrorCode: CodeTimeout}
	}
	return Result{Status: StatusNetworkError, ErrorCode: CodeNetwork}
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	if limit <= 0 {
		limit = defaultMaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return nil, true, nil
	}
	return body, false, nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		const maxDuration = time.Duration(1<<63 - 1)
		if seconds > 0 && seconds <= int64(maxDuration/time.Second) {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null"))
}

func (s *Service) capability(protocol Protocol) (Capability, bool) {
	s.capabilityMu.RLock()
	defer s.capabilityMu.RUnlock()
	capability, exists := s.capabilities[protocol]
	return capability, exists
}

func (s *Service) hasCapability(protocol Protocol) bool {
	if s == nil {
		return false
	}
	if protocol == ProtocolAuto {
		return true
	}
	_, exists := s.capability(protocol)
	return exists
}

func (s *Service) finish(result Result, started time.Time) Result {
	finished := s.currentTime()
	result.Latency = finished.Sub(started)
	if result.Latency < 0 {
		result.Latency = 0
	}
	result.CheckedAt = finished.UTC()
	if result.TemplateVersion == "" {
		result.TemplateVersion = TemplateVersion
	}
	return result
}

func (s *Service) currentTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

func withMetadata(result Result, protocol Protocol) Result {
	result.Protocol = protocol
	result.TemplateVersion = TemplateVersion
	return result
}
