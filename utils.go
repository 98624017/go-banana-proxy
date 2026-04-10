package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func truncateText(text string, maxLen int) string {
	if maxLen <= 0 {
		return text
	}
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "... (truncated, len=" + itoa(len(text)) + ")"
}

// isAllowedUpstreamHostname checks if the hostname is in the allowed list.
// allowedDomainsList is a comma-separated string of domains (e.g., "grsai.com,aitohumanize.com").
func isAllowedUpstreamHostname(hostname string, allowedDomainsList string) bool {
	h := strings.ToLower(stringsTrim(hostname))
	if h == "" {
		return false
	}

	// Default domains if not configured
	defaultDomains := []string{"grsai.com"}

	var allowed []string
	if stringsTrim(allowedDomainsList) != "" {
		for _, d := range strings.Split(allowedDomainsList, ",") {
			trimmed := strings.ToLower(stringsTrim(d))
			if trimmed != "" {
				allowed = append(allowed, trimmed)
			}
		}
	}
	if len(allowed) == 0 {
		allowed = defaultDomains
	}

	for _, domain := range allowed {
		if h == domain || strings.HasSuffix(h, "."+domain) {
			return true
		}
	}
	return false
}

func googleStatusFromHttpStatus(httpStatus int) string {
	switch httpStatus {
	case 400:
		return "INVALID_ARGUMENT"
	case 401:
		return "UNAUTHENTICATED"
	case 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 500:
		return "INTERNAL"
	case 502, 503, 504:
		return "UNAVAILABLE"
	default:
		return "UNKNOWN"
	}
}

func normalizeImageMimeType(contentType string) string {
	parts := strings.Split(contentType, ";")
	value := stringsTrim(parts[0])
	if value == "" {
		return "image/png"
	}
	return value
}

func guessImageMimeTypeFromUrl(rawUrl string) string {
	parsed, err := url.Parse(rawUrl)
	if err != nil {
		return "image/png"
	}
	p := strings.ToLower(parsed.Path)
	if strings.HasSuffix(p, ".png") {
		return "image/png"
	}
	if strings.HasSuffix(p, ".jpg") || strings.HasSuffix(p, ".jpeg") {
		return "image/jpeg"
	}
	if strings.HasSuffix(p, ".webp") {
		return "image/webp"
	}
	if strings.HasSuffix(p, ".gif") {
		return "image/gif"
	}
	return "image/png"
}

func toBeijingTime(ts *int64) any {
	if ts == nil {
		return nil
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil
	}
	return time.Unix(*ts, 0).In(loc).Format("2006/01/02 15:04:05")
}

func requestOrigin(r *http.Request) string {
	scheme := r.URL.Scheme
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
		if forwarded := stringsTrim(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			if len(parts) > 0 && stringsTrim(parts[0]) != "" {
				scheme = stringsTrim(parts[0])
			}
		}
	}

	host := r.Host
	if host == "" {
		host = r.URL.Host
	}

	return scheme + "://" + host
}

func readResponseText(resp *http.Response) (string, error) {
	if resp == nil || resp.Body == nil {
		return "", errors.New("Empty response body")
	}
	defer resp.Body.Close()
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return string(bytes), err
	}
	return string(bytes), nil
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func nullableString(value string) any {
	if stringsTrim(value) == "" {
		return nil
	}
	return value
}

func toNullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func jsonNewEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

func jsonUnmarshal(data []byte, value any) error {
	return json.Unmarshal(data, value)
}

func ioReadAll(src io.Reader) ([]byte, error) {
	return io.ReadAll(src)
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func roundTo3(value float64) float64 {
	return math.Round(value*1000) / 1000
}

func asString(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	s, ok := value.(string)
	if ok {
		return s, true
	}
	return "", false
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(value any) []any {
	if value == nil {
		return nil
	}
	if s, ok := value.([]any); ok {
		return s
	}
	return nil
}

func asInt(value any) (*int, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case int:
		return &v, true
	case int64:
		vv := int(v)
		return &vv, true
	case float64:
		vv := int(v)
		return &vv, true
	case json.Number:
		iv, err := v.Int64()
		if err != nil {
			return nil, false
		}
		vv := int(iv)
		return &vv, true
	default:
		return nil, false
	}
}

func asInt64(value any) (*int64, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case int64:
		return &v, true
	case int:
		vv := int64(v)
		return &vv, true
	case float64:
		vv := int64(v)
		return &vv, true
	case json.Number:
		iv, err := v.Int64()
		if err != nil {
			return nil, false
		}
		return &iv, true
	default:
		return nil, false
	}
}

func asFloat(value any) (*float64, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case float64:
		return &v, true
	case json.Number:
		fv, err := v.Float64()
		if err != nil {
			return nil, false
		}
		return &fv, true
	case int:
		vv := float64(v)
		return &vv, true
	case int64:
		vv := float64(v)
		return &vv, true
	default:
		return nil, false
	}
}

func getStringArray(value any) []string {
	items := asSlice(value)
	if items == nil {
		return nil
	}
	var out []string
	for _, item := range items {
		if s, ok := asString(item); ok {
			trimmed := stringsTrim(s)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

func readJSONBody(r *http.Request) (map[string]any, error) {
	if r == nil || r.Body == nil {
		return nil, errors.New("empty body")
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}
