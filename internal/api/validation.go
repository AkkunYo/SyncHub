package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

var (
	errInvalidInput = errors.New("invalid API input")
	validIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@+\-]{0,255}$`)
	validHostLabel  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

func decodeStrictJSON[T any](c *gin.Context) (T, error) {
	var zero T
	if c.Request.ContentLength > maxJSONBodyBytes {
		return zero, errInvalidInput
	}
	if encoding := strings.TrimSpace(c.GetHeader("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return zero, errInvalidInput
	}
	contentTypes := c.Request.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		return zero, errInvalidInput
	}
	mediaType, parameters, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/json" {
		return zero, errInvalidInput
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "charset") || !strings.EqualFold(value, "utf-8") {
			return zero, errInvalidInput
		}
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var value *T
	if err := decoder.Decode(&value); err != nil || value == nil {
		return zero, errInvalidInput
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return zero, errInvalidInput
	}
	return *value, nil
}

func requireEmptyBody(c *gin.Context) error {
	if c.Request.ContentLength > maxJSONBodyBytes {
		return errInvalidInput
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxJSONBodyBytes+1))
	if err != nil || int64(len(data)) > maxJSONBodyBytes || len(strings.TrimSpace(string(data))) != 0 {
		return errInvalidInput
	}
	return nil
}

func queryValues(c *gin.Context, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return nil, errInvalidInput
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allowedSet[key]; !ok || len(entries) != 1 {
			return nil, errInvalidInput
		}
	}
	return values, nil
}

func validateNoQuery(c *gin.Context) error {
	values, err := queryValues(c)
	if err != nil || len(values) != 0 {
		return errInvalidInput
	}
	return nil
}

func validateIdentifier(value string) error {
	if !validIdentifier.MatchString(value) {
		return errInvalidInput
	}
	return nil
}

func validateText(value string, maximum int, allowEmpty bool) error {
	if !utf8.ValidString(value) || len(value) > maximum || (!allowEmpty && strings.TrimSpace(value) == "") {
		return errInvalidInput
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return errInvalidInput
		}
	}
	return nil
}

func normalizeRequiredText(value string, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	if err := validateText(value, maximum, false); err != nil {
		return "", err
	}
	return value, nil
}

func normalizeBaseURL(value string, allowEmpty bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && allowEmpty {
		return "", nil
	}
	if err := validateText(value, 2048, false); err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", errInvalidInput
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errInvalidInput
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateHost(host string) error {
	if host == "" || len(host) > 253 || strings.TrimSpace(host) != host {
		return errInvalidInput
	}
	if net.ParseIP(host) != nil || strings.EqualFold(host, "localhost") {
		return nil
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !validHostLabel.MatchString(label) {
			return errInvalidInput
		}
	}
	return nil
}

func parsePositiveDuration(value string) (time.Duration, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, errInvalidInput
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, errInvalidInput
	}
	return duration, nil
}

func normalizeModels(models []string) ([]string, error) {
	if len(models) == 0 || len(models) > 256 {
		return nil, errInvalidInput
	}
	result := make([]string, len(models))
	seen := make(map[string]struct{}, len(models))
	for i, model := range models {
		model = strings.TrimSpace(model)
		if err := validateText(model, 256, false); err != nil {
			return nil, err
		}
		if _, exists := seen[model]; exists {
			return nil, errInvalidInput
		}
		seen[model] = struct{}{}
		result[i] = model
	}
	return result, nil
}

func validatePriorityAndWeight(priority, weight int) error {
	if priority < -1_000_000 || priority > 1_000_000 || weight < 0 || weight > 1_000_000 {
		return errInvalidInput
	}
	return nil
}

func validateCredential(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 64<<10 || !utf8.ValidString(value) {
		return errInvalidInput
	}
	return nil
}
