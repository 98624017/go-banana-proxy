package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

func jsonMarshal(payload any) ([]byte, error) {
	return json.Marshal(payload)
}

func firstNonEmptyString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := getStringValue(payload, key); stringsTrim(value) != "" {
			return stringsTrim(value)
		}
	}
	return ""
}

func getStringValue(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload[key]; ok {
		if s, ok := asString(value); ok {
			return s
		}
	}
	return ""
}

func coalesceString(value string, fallback string) string {
	if stringsTrim(value) != "" {
		return value
	}
	return fallback
}

func coalesceInt(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
}

func buildProxyURL(base string, rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	cleanBase := strings.TrimRight(stringsTrim(base), "/")
	if cleanBase == "" {
		return rawURL
	}
	return cleanBase + "/proxy/image?url=" + url.QueryEscape(rawURL)
}

func extractInt64(payload map[string]any, key string) *int64 {
	if payload == nil {
		return nil
	}
	value, ok := payload[key]
	if !ok {
		return nil
	}
	if parsed, ok := asInt64(value); ok {
		return parsed
	}
	return nil
}

func toNullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func timeNowUnix() int64 {
	return time.Now().Unix()
}
