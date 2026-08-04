package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var retainedWordPattern = regexp.MustCompile(`保留词“([^”]+)”`)

func TestGenerateTaskUsesVersionedSafeRandomContent(t *testing.T) {
	t.Parallel()

	first, err := generateTask(bytes.NewReader(bytes.Repeat([]byte{0}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateTask(bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if first.TemplateVersion != TemplateVersion || second.TemplateVersion != TemplateVersion {
		t.Fatalf("template versions = %q, %q", first.TemplateVersion, second.TemplateVersion)
	}
	if first.RetainedWord == second.RetainedWord {
		t.Fatalf("retained words are not random: %q", first.RetainedWord)
	}
	for _, task := range []generatedTask{first, second} {
		assertSafeTask(t, task)
	}
}

func TestProbeSupportsBuiltInProtocols(t *testing.T) {
	t.Parallel()

	tests := []struct {
		protocol Protocol
		path     string
		prompt   func(map[string]any) string
		response func(string) string
	}{
		{
			protocol: ProtocolChatCompletions,
			path:     "/v1/chat/completions",
			prompt: func(body map[string]any) string {
				messages := body["messages"].([]any)
				return messages[0].(map[string]any)["content"].(string)
			},
			response: func(retainedWord string) string {
				return fmt.Sprintf(`{"choices":[{"message":{"content":"处理完成，%s"}}]}`, retainedWord)
			},
		},
		{
			protocol: ProtocolResponses,
			path:     "/v1/responses",
			prompt: func(body map[string]any) string {
				return body["input"].(string)
			},
			response: func(retainedWord string) string {
				return fmt.Sprintf(`{"output":[{"content":[{"type":"output_text","text":"处理完成，%s"}]}]}`, retainedWord)
			},
		},
		{
			protocol: ProtocolCompletions,
			path:     "/v1/completions",
			prompt: func(body map[string]any) string {
				return body["prompt"].(string)
			},
			response: func(retainedWord string) string {
				return fmt.Sprintf(`{"choices":[{"text":"处理完成，%s"}]}`, retainedWord)
			},
		},
	}

	for index, test := range tests {
		test := test
		t.Run(string(test.protocol), func(t *testing.T) {
			t.Parallel()
			const apiKey = "probe-secret-key"
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Errorf("path = %q, want %q", request.URL.Path, test.path)
				}
				if authorization := request.Header.Get("Authorization"); authorization != "Bearer "+apiKey {
					t.Errorf("Authorization = %q", authorization)
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				prompt := test.prompt(body)
				retainedWord := retainedWordFromPrompt(t, prompt)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response(retainedWord))
			}))
			t.Cleanup(server.Close)

			service := NewService(server.Client())
			service.random = bytes.NewReader(bytes.Repeat([]byte{byte(index + 2)}, 64))
			result := service.Probe(context.Background(), Input{
				BaseURL:  server.URL + "/v1",
				APIKey:   apiKey,
				Model:    "safe-text-model",
				Protocol: test.protocol,
			})

			if result.Status != StatusHealthy || result.ErrorCode != CodeNone {
				t.Fatalf("result = %#v", result)
			}
			if result.Protocol != test.protocol || result.TemplateVersion != TemplateVersion {
				t.Fatalf("result metadata = %#v", result)
			}
			if result.CheckedAt.IsZero() || result.Latency < 0 {
				t.Fatalf("timing metadata = %#v", result)
			}
		})
	}
}

func TestProbeAutoFallsBackAcrossUnsupportedEndpoints(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.URL.Path == "/v1/chat/completions" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		retainedWord := retainedWordFromPrompt(t, body["input"].(string))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"output_text":"%s"}`, retainedWord)
	}))
	t.Cleanup(server.Close)

	service := NewService(server.Client())
	service.random = bytes.NewReader(bytes.Repeat([]byte{4}, 64))
	result := service.Probe(context.Background(), Input{
		BaseURL:  server.URL,
		APIKey:   "secret",
		Model:    "responses-only-model",
		Protocol: ProtocolAuto,
	})

	if result.Status != StatusHealthy || result.Protocol != ProtocolResponses {
		t.Fatalf("result = %#v", result)
	}
	wantPaths := []string{"/v1/chat/completions", "/v1/responses"}
	if fmt.Sprint(paths) != fmt.Sprint(wantPaths) {
		t.Fatalf("paths = %v, want %v", paths, wantPaths)
	}
}

func TestProbeClassifiesHTTPFailuresWithoutReturningBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		status     Status
		code       ErrorCode
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, status: StatusUnauthorized, code: CodeUnauthorized},
		{name: "forbidden", statusCode: http.StatusForbidden, status: StatusUnauthorized, code: CodeForbidden},
		{name: "model unavailable", statusCode: http.StatusNotFound, status: StatusModelUnavailable, code: CodeModelUnavailable},
		{name: "method unavailable", statusCode: http.StatusMethodNotAllowed, status: StatusProtocolUnavailable, code: CodeProtocolUnavailable},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			const sensitiveBody = "upstream-secret-response"
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.statusCode)
				_, _ = io.WriteString(writer, sensitiveBody)
			}))
			t.Cleanup(server.Close)

			result := NewService(server.Client()).Probe(context.Background(), Input{
				BaseURL:  server.URL,
				APIKey:   "sensitive-api-key",
				Model:    "private-model",
				Protocol: ProtocolChatCompletions,
			})
			if result.Status != test.status || result.ErrorCode != test.code {
				t.Fatalf("result = %#v", result)
			}
			assertResultRedacted(t, result, sensitiveBody, "sensitive-api-key", "private-model", server.URL)
		})
	}
}

func TestProbeParsesRetryAfter(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "17")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	result := NewService(server.Client()).Probe(context.Background(), Input{
		BaseURL:  server.URL,
		APIKey:   "secret",
		Model:    "model",
		Protocol: ProtocolChatCompletions,
	})
	if result.Status != StatusRateLimited || result.ErrorCode != CodeRateLimited || result.RetryAfter != 17*time.Second {
		t.Fatalf("result = %#v", result)
	}
}

func TestProbeDistinguishesInconclusiveAndInvalidResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		status Status
		code   ErrorCode
	}{
		{name: "retained word missing", body: `{"choices":[{"message":{"content":"完成了自然语言任务"}}]}`, status: StatusInconclusive, code: CodeRetainedWordMissing},
		{name: "invalid json", body: `{`, status: StatusInvalidResponse, code: CodeInvalidJSON},
		{name: "empty output", body: `{"choices":[{"message":{"content":"  "}}]}`, status: StatusInvalidResponse, code: CodeMissingOutputText},
		{name: "error envelope", body: `{"error":{"message":"sensitive upstream detail"}}`, status: StatusInvalidResponse, code: CodeUpstreamErrorPayload},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.body)
			}))
			t.Cleanup(server.Close)

			result := NewService(server.Client()).Probe(context.Background(), Input{
				BaseURL:  server.URL,
				APIKey:   "secret",
				Model:    "model",
				Protocol: ProtocolChatCompletions,
			})
			if result.Status != test.status || result.ErrorCode != test.code {
				t.Fatalf("result = %#v", result)
			}
			assertResultRedacted(t, result, test.body, "sensitive upstream detail")
		})
	}
}

func TestProbeBoundsResponseBodies(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"`+strings.Repeat("x", 256)+`"}}]}`)
	}))
	t.Cleanup(server.Close)

	service := NewService(server.Client())
	service.maxResponseBytes = 64
	result := service.Probe(context.Background(), Input{
		BaseURL:  server.URL,
		APIKey:   "secret",
		Model:    "model",
		Protocol: ProtocolChatCompletions,
	})
	if result.Status != StatusInvalidResponse || result.ErrorCode != CodeResponseTooLarge {
		t.Fatalf("result = %#v", result)
	}
}

func TestProbePropagatesContextAndClassifiesTimeout(t *testing.T) {
	t.Parallel()

	type contextKey string
	const key contextKey = "probe"
	contextObserved := false
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		contextObserved = request.Context().Value(key) == "expected"
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), key, "expected"), 20*time.Millisecond)
	defer cancel()

	result := NewService(client).Probe(ctx, Input{
		BaseURL:  "https://probe.invalid",
		APIKey:   "secret",
		Model:    "model",
		Protocol: ProtocolChatCompletions,
	})
	if !contextObserved {
		t.Fatal("request context was not propagated")
	}
	if result.Status != StatusTimeout || result.ErrorCode != CodeTimeout {
		t.Fatalf("result = %#v", result)
	}
}

func TestProbeRedactsNetworkErrorsAndRequestMaterial(t *testing.T) {
	t.Parallel()

	const apiKey = "network-secret-key"
	var requestBody string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		requestBody = string(body)
		return nil, errors.New("dial failed with " + apiKey + " and " + requestBody)
	})}
	service := NewService(client)
	service.random = bytes.NewReader(bytes.Repeat([]byte{7}, 64))
	result := service.Probe(context.Background(), Input{
		BaseURL:  "https://probe.invalid",
		APIKey:   apiKey,
		Model:    "sensitive-model",
		Protocol: ProtocolChatCompletions,
	})
	if result.Status != StatusNetworkError || result.ErrorCode != CodeNetwork {
		t.Fatalf("result = %#v", result)
	}
	assertResultRedacted(t, result, apiKey, requestBody, "sensitive-model", "probe.invalid")
}

func TestServiceAcceptsAdditionalProtocolCapabilities(t *testing.T) {
	t.Parallel()

	const customProtocol Protocol = "custom_generate"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/generate" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		retainedWord := retainedWordFromPrompt(t, body["task"].(string))
		_, _ = fmt.Fprintf(writer, `{"text":"%s"}`, retainedWord)
	}))
	t.Cleanup(server.Close)

	service := NewService(server.Client())
	err := service.RegisterCapability(Capability{
		Protocol: customProtocol,
		Endpoint: "/v2/generate",
		BuildPayload: func(model, prompt string) (any, error) {
			return map[string]any{"model": model, "task": prompt}, nil
		},
		ExtractText: func(body []byte) (string, error) {
			var response struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				return "", err
			}
			return response.Text, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.random = bytes.NewReader(bytes.Repeat([]byte{9}, 64))
	result := service.Probe(context.Background(), Input{
		BaseURL:  server.URL,
		APIKey:   "secret",
		Model:    "model",
		Protocol: customProtocol,
	})
	if result.Status != StatusHealthy || result.Protocol != customProtocol {
		t.Fatalf("result = %#v", result)
	}
}

func TestProbeRejectsInvalidInputsWithoutNetworkAccess(t *testing.T) {
	t.Parallel()

	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}
	tests := []Input{
		{BaseURL: "", APIKey: "key", Model: "model", Protocol: ProtocolChatCompletions},
		{BaseURL: "https://example.com", APIKey: "", Model: "model", Protocol: ProtocolChatCompletions},
		{BaseURL: "https://example.com", APIKey: "key", Model: "", Protocol: ProtocolChatCompletions},
		{BaseURL: "file:///tmp/probe", APIKey: "key", Model: "model", Protocol: ProtocolChatCompletions},
		{BaseURL: "https://user@example.com", APIKey: "key", Model: "model", Protocol: ProtocolChatCompletions},
		{BaseURL: "https://example.com?key=secret", APIKey: "key", Model: "model", Protocol: ProtocolChatCompletions},
		{BaseURL: "https://example.com", APIKey: "key", Model: "model", Protocol: "unknown"},
	}
	service := NewService(client)
	for _, input := range tests {
		result := service.Probe(context.Background(), input)
		if result.Status != StatusInvalidRequest || result.ErrorCode != CodeInvalidInput {
			t.Fatalf("input %#v result = %#v", input, result)
		}
	}
	if called {
		t.Fatal("invalid input reached the network")
	}
}

func assertSafeTask(t *testing.T, task generatedTask) {
	t.Helper()
	if task.Prompt == "" || task.RetainedWord == "" || !strings.Contains(task.Prompt, task.RetainedWord) {
		t.Fatalf("invalid task = %#v", task)
	}
	if len([]rune(task.Prompt)) > 120 || strings.Count(task.Prompt, "。") != 1 {
		t.Fatalf("task is not one short sentence: %q", task.Prompt)
	}
	lower := strings.ToLower(strings.ReplaceAll(task.Prompt, " ", ""))
	for _, forbidden := range []string{"hi", "hello", "你是谁", "回答ok"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("task contains forbidden phrase %q: %q", forbidden, task.Prompt)
		}
	}
}

func retainedWordFromPrompt(t *testing.T, prompt string) string {
	t.Helper()
	match := retainedWordPattern.FindStringSubmatch(prompt)
	if len(match) != 2 {
		t.Fatalf("prompt lacks retained word: %q", prompt)
	}
	return match[1]
}

func assertResultRedacted(t *testing.T, result Result, forbidden ...string) {
	t.Helper()
	rendered := fmt.Sprintf("%+v", result)
	for _, value := range forbidden {
		if value != "" && strings.Contains(rendered, value) {
			t.Fatalf("result leaked %q: %s", value, rendered)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
