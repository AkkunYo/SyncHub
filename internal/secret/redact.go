package secret

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode"
)

var (
	bearerPattern     = regexp.MustCompile(`(?i)(\bBearer[ \t]+)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;"']+)`)
	assignmentPattern = regexp.MustCompile(`(?im)(^|[^A-Za-z0-9_-])(["']?(?:x[-_]?management[-_]?key|x[-_]?security[-_]?proof|api[-_]?key|access[-_]?token|refresh[-_]?token|id[-_]?token|session[-_]?token|bearer[-_]?token|client[-_]?secret|management[-_]?key|security[-_]?proof|private[-_]?key|password|passwd|credentials?|token|secret|key)["']?[ \t]*[:=][ \t]*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&#]+)`)
)

var sensitiveKeys = map[string]struct{}{
	"accesstoken":        {},
	"apikey":             {},
	"authorization":      {},
	"bearertoken":        {},
	"clientsecret":       {},
	"credential":         {},
	"credentials":        {},
	"idtoken":            {},
	"key":                {},
	"managementkey":      {},
	"passwd":             {},
	"password":           {},
	"privatekey":         {},
	"proxyauthorization": {},
	"refreshtoken":       {},
	"secret":             {},
	"securityproof":      {},
	"sessiontoken":       {},
	"token":              {},
	"xmanagementkey":     {},
	"xsecurityproof":     {},
}

// Redact removes common credentials from a log string while retaining
// non-sensitive context such as field names, endpoints, and status details.
func Redact(input string) string {
	redacted := redactJSON(input)
	redacted = replaceSecretValue(bearerPattern, redacted)
	return replaceSecretValue(assignmentPattern, redacted)
}

func redactJSON(input string) string {
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return input
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return input
	}
	if !redactJSONValue(&value) {
		return input
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return input
	}
	return string(encoded)
}

func redactJSONValue(value *any) bool {
	switch current := (*value).(type) {
	case map[string]any:
		changed := false
		for key, child := range current {
			if isSensitiveKey(key) {
				if child != Redacted {
					current[key] = Redacted
					changed = true
				}
				continue
			}
			if redactJSONValue(&child) {
				current[key] = child
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for index, child := range current {
			if redactJSONValue(&child) {
				current[index] = child
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func isSensitiveKey(key string) bool {
	canonical := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	_, sensitive := sensitiveKeys[canonical]
	return sensitive
}

func replaceSecretValue(pattern *regexp.Regexp, input string) string {
	return pattern.ReplaceAllStringFunc(input, func(match string) string {
		indexes := pattern.FindStringSubmatchIndex(match)
		valueStart, valueEnd := indexes[len(indexes)-2], indexes[len(indexes)-1]
		value := match[valueStart:valueEnd]
		return match[:valueStart] + quoteLike(value, Redacted) + match[valueEnd:]
	})
}

func quoteLike(original, replacement string) string {
	if len(original) < 2 {
		return replacement
	}
	first, last := original[0], original[len(original)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		var output bytes.Buffer
		output.Grow(len(replacement) + 2)
		output.WriteByte(first)
		output.WriteString(replacement)
		output.WriteByte(last)
		return output.String()
	}
	return replacement
}
