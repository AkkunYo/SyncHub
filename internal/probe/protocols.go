package probe

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

var errMissingOutputText = errors.New("missing output text")

func builtInCapabilities() []Capability {
	return []Capability{
		{
			Protocol: ProtocolChatCompletions,
			Endpoint: "/v1/chat/completions",
			BuildPayload: func(model, prompt string) (any, error) {
				return map[string]any{
					"model":      model,
					"messages":   []map[string]string{{"role": "user", "content": prompt}},
					"max_tokens": maxOutputTokens,
					"stream":     false,
				}, nil
			},
			ExtractText: extractChatText,
		},
		{
			Protocol: ProtocolResponses,
			Endpoint: "/v1/responses",
			BuildPayload: func(model, prompt string) (any, error) {
				return map[string]any{
					"model":             model,
					"input":             prompt,
					"max_output_tokens": maxOutputTokens,
					"stream":            false,
				}, nil
			},
			ExtractText: extractResponsesText,
		},
		{
			Protocol: ProtocolCompletions,
			Endpoint: "/v1/completions",
			BuildPayload: func(model, prompt string) (any, error) {
				return map[string]any{
					"model":      model,
					"prompt":     prompt,
					"max_tokens": maxOutputTokens,
					"stream":     false,
				}, nil
			},
			ExtractText: extractCompletionText,
		},
	}
}

func extractChatText(body []byte) (string, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 || len(response.Choices[0].Message.Content) == 0 {
		return "", errMissingOutputText
	}
	content := response.Choices[0].Message.Content
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return requireText(text)
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return "", errMissingOutputText
	}
	var combined bytes.Buffer
	for _, part := range parts {
		combined.WriteString(part.Text)
	}
	return requireText(combined.String())
}

func extractResponsesText(body []byte) (string, error) {
	var response struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.OutputText) != "" {
		return response.OutputText, nil
	}
	var combined bytes.Buffer
	for _, output := range response.Output {
		for _, content := range output.Content {
			combined.WriteString(content.Text)
		}
	}
	return requireText(combined.String())
}

func extractCompletionText(body []byte) (string, error) {
	var response struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 {
		return "", errMissingOutputText
	}
	return requireText(response.Choices[0].Text)
}

func requireText(text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errMissingOutputText
	}
	return text, nil
}
