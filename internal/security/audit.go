package security

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

const redacted = "[redacted]"

var credentialText = regexp.MustCompile(`(?i)\b(authorization|cookie|password|secret|token|stoken|api[_-]?key|receive_code|download_url|callback(?:_var)?)\b\s*[:=]\s*([^\s,;"}]+)`)

func secretKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{"authorization", "cookie", "password", "secret", "token", "stoken", "api_key", "apikey", "receive_code", "download_url", "signed_url", "callback"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func redactURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return value
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if secretKey(key) || key == "pwd" {
			query.Set(key, redacted)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// RedactValue recursively removes credentials from an audit value.
func RedactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if secretKey(key) {
				result[key] = redacted
			} else {
				result[key] = RedactValue(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = RedactValue(item)
		}
		return result
	case string:
		return redactURL(typed)
	default:
		return value
	}
}

// Summary returns bounded JSON suitable for a durable audit record.
func Summary(value any, maxRunes int) string {
	encoded, err := json.Marshal(RedactValue(value))
	if err != nil {
		return `{"error":"audit serialization failed"}`
	}
	runes := []rune(string(encoded))
	if maxRunes > 0 && len(runes) > maxRunes {
		preview, _ := json.Marshal(map[string]any{"truncated": true, "preview": string(runes[:maxRunes])})
		return string(preview)
	}
	return string(runes)
}

// TextSummary parses JSON when possible and otherwise redacts credential assignments in text.
func TextSummary(value string, maxRunes int) string {
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) == nil {
		return Summary(parsed, maxRunes)
	}
	redactedText := credentialText.ReplaceAllString(value, "$1=[redacted]")
	return Summary(map[string]any{"text": redactedText}, maxRunes)
}
