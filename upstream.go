package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// UpstreamProvider defines the rewrite rules for a specific upstream service.
// Different real upstreams (e.g., api.grsai.com) each implement this interface.
type UpstreamProvider interface {
	// Name returns a human-readable identifier for logging and debugging.
	Name() string

	// Match checks whether the given base-url belongs to this provider.
	Match(baseURL string) bool

	// ImageGenerationPath returns the upstream image generation API path.
	ImageGenerationPath() string

	// BuildRequestBody converts standardized parameters into the upstream request body.
	BuildRequestBody(params ImageGenParams) map[string]any

	// NormalizeModel maps an external model name to the upstream model name.
	// source distinguishes the caller: "gemini" or "openai".
	NormalizeModel(rawModel string, source string) string

	// ParseResponse parses the upstream HTTP response into a standardized result.
	// Returns (*UpstreamResult, nil) on successful parse (even if upstream status is "failed").
	// Returns (nil, *UpstreamError) for HTTP errors, parse failures, or business errors.
	ParseResponse(statusCode int, body []byte) (*UpstreamResult, *UpstreamError)

	// AllowedImageDomains returns a comma-separated list of allowed image hostnames
	// for this upstream (e.g., "grsai.com,aitohumanize.com").
	AllowedImageDomains() string
}

// ImageGenParams is the standardized parameter set extracted from Gemini/OpenAI requests.
type ImageGenParams struct {
	Model       string
	Prompt      string
	URLs        []string
	AspectRatio string
	ImageSize   string
}

// UpstreamResult represents a successfully parsed upstream response.
type UpstreamResult struct {
	Status        string         // "succeeded", "failed", etc.
	ImageURLs     []string       // image URLs on success
	FailureReason string         // "input_moderation", "output_moderation", "error", etc.
	ErrorDetail   string         // error text from upstream
	StartTime     *int64
	EndTime       *int64
	RawData       map[string]any // original upstream data for upstream_meta transparency
	UsageOverride map[string]any // optional: override usageMetadata in Gemini response
}

// UpstreamError represents an error encountered during upstream response parsing.
type UpstreamError struct {
	HTTPStatus int    // suggested HTTP status for the proxy response
	Code       *int   // upstream business error code (from "code" field)
	Message    string // human-readable error message
	BodyText   string // raw response body for debugging
	RawJSON    any    // parsed JSON if available
	Note       string // additional context
}

func (e *UpstreamError) Error() string { return e.Message }

// providerRegistry resolves an UpstreamProvider by matching the base-url.
type providerRegistry struct {
	providers []UpstreamProvider
	fallback  UpstreamProvider
}

// newProviderRegistry creates a registry with the given providers.
// The first provider is also used as the fallback when no match is found.
func newProviderRegistry(providers ...UpstreamProvider) *providerRegistry {
	var fallback UpstreamProvider
	if len(providers) > 0 {
		fallback = providers[0]
	}
	return &providerRegistry{providers: providers, fallback: fallback}
}

// Resolve finds the matching provider for the given base-url.
// Returns the fallback provider if no match is found.
func (r *providerRegistry) Resolve(baseURL string) UpstreamProvider {
	for _, p := range r.providers {
		if p.Match(baseURL) {
			return p
		}
	}
	return r.fallback
}

// UpstreamExecutor is an optional interface for providers that control the
// full upstream request lifecycle (e.g., async task-based providers).
// Providers that implement this interface bypass the default
// BuildRequestBody → POST → ParseResponse flow.
type UpstreamExecutor interface {
	UpstreamProvider

	// Execute handles the full upstream interaction including task creation
	// and polling if needed. Returns the final standardized result.
	Execute(ctx ExecuteContext) (*UpstreamResult, *UpstreamError)
}

// ExecuteContext provides everything an UpstreamExecutor needs.
type ExecuteContext struct {
	Ctx            context.Context
	UpstreamClient *http.Client
	FetchClient    *http.Client
	Auth           AuthResult
	OriginalBody   map[string]any // raw Gemini request body from client
	Params         ImageGenParams
	GeminiOutput   string // "url" or "base64"
}

// extractHostFromBaseURL extracts the hostname from a base-url string.
// Shared utility for provider Match implementations.
func extractHostFromBaseURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}
