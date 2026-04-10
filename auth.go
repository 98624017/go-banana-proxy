package main

import (
	"net/http"
	"strings"
)

type AuthResult struct {
	UpstreamBase string
	UpstreamAuth string
	ErrorMessage string
	RawAPIKey    string // raw key without "Bearer " prefix
}

// parseUpstreamAuth 兼容三种上游鉴权输入：
// 1. Authorization: Bearer <api-key>
// 2. Authorization: Bearer <base-url>|<api-key>
// 3. X-Upstream-Base-Url + Authorization / x-goog-api-key
func parseUpstreamAuth(r *http.Request, cfg Config, allowGoogKey bool) AuthResult {
	var authHeader string
	if r != nil {
		authHeader = r.Header.Get("Authorization")
		if authHeader == "" && allowGoogKey {
			authHeader = r.Header.Get("x-goog-api-key")
		}
	}

	if stringsTrim(authHeader) == "" {
		return AuthResult{ErrorMessage: "Missing upstream Authorization header from NewAPI."}
	}

	rawToken := stringsTrim(authHeader)
	if strings.HasPrefix(strings.ToLower(rawToken), "bearer ") {
		rawToken = stringsTrim(rawToken[len("bearer "):])
	}

	upstreamBase := ""
	if r != nil {
		upstreamBase = stringsTrim(r.Header.Get("X-Upstream-Base-Url"))
	}
	apiKey := rawToken

	if upstreamBase == "" && strings.Contains(rawToken, "|") {
		parts := strings.SplitN(rawToken, "|", 2)
		if len(parts) == 2 {
			base := stringsTrim(parts[0])
			key := stringsTrim(parts[1])
			if base != "" && key != "" {
				upstreamBase = base
				apiKey = key
			}
		}
	}

	if upstreamBase == "" {
		upstreamBase = cfg.BananaBaseURL
	}

	return AuthResult{UpstreamBase: upstreamBase, UpstreamAuth: "Bearer " + apiKey, RawAPIKey: apiKey}
}
