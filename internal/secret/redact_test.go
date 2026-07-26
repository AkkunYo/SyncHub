package secret

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactCredentialFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		secrets  []string
		preserve []string
	}{
		{
			name:     "bearer with case and whitespace variations",
			input:    "Authorization: bEaReR   test-bearer-value, request_id=req-17",
			secrets:  []string{"test-bearer-value"},
			preserve: []string{"Authorization", "bEaReR", "request_id=req-17"},
		},
		{
			name:  "management and security proof headers",
			input: "X-Management-Key : test-management-value\nx-security-proof=\t\"test-proof-value\"\nstatus: forbidden",
			secrets: []string{
				"test-management-value",
				"test-proof-value",
			},
			preserve: []string{"X-Management-Key", "x-security-proof", "status: forbidden"},
		},
		{
			name:  "json fields",
			input: `{"api_key":"test-json-api","accessToken":"test-json-access","nested":{"refresh_token":"test-json-refresh","password":"test-json-password","client_secret":"test-json-client","token":"test-json-token"},"model":"gpt-test","enabled":true}`,
			secrets: []string{
				"test-json-api",
				"test-json-access",
				"test-json-refresh",
				"test-json-password",
				"test-json-client",
				"test-json-token",
			},
			preserve: []string{`"model":"gpt-test"`, `"enabled":true`, `"nested"`},
		},
		{
			name:     "json fragment in a prefixed log",
			input:    `request payload={"client_secret":"test-fragment-value with spaces","model":"gpt-test"} status=400`,
			secrets:  []string{"test-fragment-value with spaces"},
			preserve: []string{"request payload=", `"model":"gpt-test"`, "status=400"},
		},
		{
			name:  "yaml fields",
			input: "provider: openai\nAPI_KEY : test-yaml-api\naccess-token: 'test-yaml-access'\npassword:\t test-yaml-password # supplied temporarily\nendpoint: https://example.test/v1",
			secrets: []string{
				"test-yaml-api",
				"test-yaml-access",
				"test-yaml-password",
			},
			preserve: []string{"provider: openai", "# supplied temporarily", "endpoint: https://example.test/v1"},
		},
		{
			name:     "url query credentials",
			input:    "GET https://example.test/v1/models?API_Key=test-url-api&model=gpt-test&access_token=test-url-access&security-proof=test-url-proof#result status=401",
			secrets:  []string{"test-url-api", "test-url-access", "test-url-proof"},
			preserve: []string{"https://example.test/v1/models", "model=gpt-test", "#result", "status=401"},
		},
		{
			name:     "relative url and repeated credentials",
			input:    "/proxy?token=test-url-token&page=2&token=test-url-token-two",
			secrets:  []string{"test-url-token", "test-url-token-two"},
			preserve: []string{"/proxy?", "page=2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Redact(tt.input)
			if !strings.Contains(got, Redacted) {
				t.Fatalf("Redact() did not mark any value: %q", got)
			}
			for _, secret := range tt.secrets {
				if strings.Contains(got, secret) {
					t.Errorf("Redact() retained secret %q in %q", secret, got)
				}
			}
			for _, diagnostic := range tt.preserve {
				if !strings.Contains(got, diagnostic) {
					t.Errorf("Redact() removed diagnostic %q from %q", diagnostic, got)
				}
			}
		})
	}
}

func TestRedactJSONRemainsValidAndRedactsNonStringSecrets(t *testing.T) {
	t.Parallel()

	input := `{"key":12345,"credentials":["test-array-one","test-array-two"],"metadata":{"region":"test-region"}}`
	got := Redact(input)

	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("Redact() returned invalid JSON %q: %v", got, err)
	}
	if decoded["key"] != Redacted || decoded["credentials"] != Redacted {
		t.Fatalf("sensitive JSON values = %#v", decoded)
	}
	metadata, ok := decoded["metadata"].(map[string]any)
	if !ok || metadata["region"] != "test-region" {
		t.Fatalf("non-sensitive JSON metadata = %#v", decoded["metadata"])
	}
}

func TestRedactNestedJSONArray(t *testing.T) {
	t.Parallel()

	input := `[{"name":"worker-a","auth":{"token":"test-array-token"}},{"name":"worker-b","message":"Bearer test-array-bearer"}]`
	got := Redact(input)
	for _, raw := range []string{"test-array-token", "test-array-bearer"} {
		if strings.Contains(got, raw) {
			t.Errorf("Redact() retained %q in %q", raw, got)
		}
	}
	if !strings.Contains(got, `"name":"worker-a"`) || !strings.Contains(got, `"name":"worker-b"`) {
		t.Fatalf("Redact() removed array diagnostics: %q", got)
	}
	var decoded []any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("Redact() returned invalid JSON array %q: %v", got, err)
	}
}

func TestRedactLeavesNonSensitiveDiagnosticsUnchanged(t *testing.T) {
	t.Parallel()

	input := `status=401 model=gpt-test monkey=banana message="upstream unavailable" page=2`
	if got := Redact(input); got != input {
		t.Fatalf("Redact() = %q, want unchanged %q", got, input)
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	t.Parallel()

	input := `Authorization: Bearer test-idempotent-value url=/v1?api_key=test-idempotent-query`
	once := Redact(input)
	if twice := Redact(once); twice != once {
		t.Fatalf("Redact() is not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
}
