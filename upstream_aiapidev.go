package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// aiapidevProvider implements both UpstreamProvider and UpstreamExecutor
// for the aiapidev.com async task-based image generation API.
type aiapidevProvider struct{}

func (p *aiapidevProvider) Name() string { return "aiapidev" }

func (p *aiapidevProvider) Match(baseURL string) bool {
	host := extractHostFromBaseURL(baseURL)
	if host == "" {
		return false
	}
	return host == "aiapidev.com" || strings.HasSuffix(host, ".aiapidev.com")
}

func (p *aiapidevProvider) ImageGenerationPath() string {
	return "/v1beta/models"
}

func (p *aiapidevProvider) AllowedImageDomains() string {
	return "r2.dev"
}

// NormalizeModel maps Gemini model names to aiapidev equivalents.
// Unlike grsai, the mapping is source-independent.
func (p *aiapidevProvider) NormalizeModel(rawModel string, source string) string {
	model := strings.TrimSpace(rawModel)
	lower := strings.ToLower(model)
	switch lower {
	case "gemini-3-pro-image-preview":
		return "nanobananapro"
	case "gemini-3.1-flash-image-preview":
		return "nanobanana2"
	default:
		return model
	}
}

// BuildRequestBody is a stub; the real work happens in Execute.
func (p *aiapidevProvider) BuildRequestBody(params ImageGenParams) map[string]any {
	return nil
}

// ParseResponse is a stub; the real work happens in Execute.
func (p *aiapidevProvider) ParseResponse(statusCode int, body []byte) (*UpstreamResult, *UpstreamError) {
	return nil, &UpstreamError{
		HTTPStatus: http.StatusNotImplemented,
		Message:    "aiapidev provider uses Execute(), not ParseResponse()",
	}
}

// Execute handles the full upstream interaction: create task, then poll until
// completion or timeout.
func (p *aiapidevProvider) Execute(ctx ExecuteContext) (*UpstreamResult, *UpstreamError) {
	base := strings.TrimRight(ctx.Auth.UpstreamBase, "/")
	model := ctx.Params.Model
	apiKey := ctx.Auth.RawAPIKey

	// Transform the original Gemini request body for aiapidev
	transformed := aiapidevTransformRequestBody(ctx.OriginalBody)

	bodyBytes, err := json.Marshal(transformed)
	if err != nil {
		return nil, &UpstreamError{
			HTTPStatus: http.StatusInternalServerError,
			Message:    "Failed to encode request body for aiapidev.",
			Note:       err.Error(),
		}
	}

	// POST to create the task
	createURL := base + "/v1beta/models/" + model + ":generateContent"
	req, err := http.NewRequestWithContext(ctx.Ctx, http.MethodPost, createURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &UpstreamError{
			HTTPStatus: http.StatusInternalServerError,
			Message:    "Failed to create upstream request.",
			Note:       err.Error(),
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := ctx.UpstreamClient.Do(req)
	if err != nil {
		return nil, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    "Failed to connect to aiapidev upstream.",
			Note:       err.Error(),
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    "Failed to read aiapidev create response.",
			Note:       err.Error(),
		}
	}

	requestID, upErr := aiapidevParseCreateResponse(resp.StatusCode, respBody)
	if upErr != nil {
		return nil, upErr
	}

	// Poll for result
	deadline := time.Now().Add(600 * time.Second)
	pollURL := base + "/v1beta/tasks/" + requestID
	attempt := 0
	consecutiveErrors := 0

	for {
		delay := aiapidevPollDelay(attempt)
		select {
		case <-time.After(delay):
		case <-ctx.Ctx.Done():
			return nil, &UpstreamError{
				HTTPStatus: 499,
				Message:    "Client disconnected during aiapidev polling.",
				Note:       "requestId=" + requestID,
			}
		}

		if time.Now().After(deadline) {
			return nil, &UpstreamError{
				HTTPStatus: http.StatusGatewayTimeout,
				Message:    "aiapidev task polling timed out after 600s.",
				Note:       "requestId=" + requestID,
			}
		}

		pollReq, err := http.NewRequestWithContext(ctx.Ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, &UpstreamError{
				HTTPStatus: http.StatusInternalServerError,
				Message:    "Failed to create poll request.",
				Note:       err.Error(),
			}
		}
		pollReq.Header.Set("x-goog-api-key", apiKey)

		pollResp, err := ctx.UpstreamClient.Do(pollReq)
		if err != nil {
			// Check if it's a context cancellation (client disconnect)
			if ctx.Ctx.Err() != nil {
				return nil, &UpstreamError{
					HTTPStatus: 499,
					Message:    "Client disconnected during aiapidev polling.",
					Note:       "requestId=" + requestID,
				}
			}
			consecutiveErrors++
			if consecutiveErrors >= 3 {
				return nil, &UpstreamError{
					HTTPStatus: http.StatusBadGateway,
					Message:    "Failed to connect to aiapidev for polling after 3 retries.",
					Note:       err.Error(),
				}
			}
			attempt++
			continue
		}
		consecutiveErrors = 0 // reset on success

		pollBody, err := io.ReadAll(pollResp.Body)
		pollResp.Body.Close()
		if err != nil {
			return nil, &UpstreamError{
				HTTPStatus: http.StatusBadGateway,
				Message:    "Failed to read aiapidev poll response.",
				Note:       err.Error(),
			}
		}

		result, done, pollErr := aiapidevParsePollResponse(pollResp.StatusCode, pollBody)
		if pollErr != nil {
			return nil, pollErr
		}
		if done && result != nil {
			// Set random usage override
			promptTokens := 1000 + rand.Intn(2001)    // [1000, 3000]
			candidateTokens := 1000 + rand.Intn(2001) // [1000, 3000]
			result.UsageOverride = map[string]any{
				"promptTokenCount":     promptTokens,
				"candidatesTokenCount": candidateTokens,
				"totalTokenCount":      promptTokens + candidateTokens,
			}
			return result, nil
		}

		attempt++
	}
}

// aiapidevTransformRequestBody transforms a Gemini-style camelCase request body
// into the aiapidev snake_case format:
//   - contents[].parts[].inlineData → file_data with file_uri and mime_type
//   - generationConfig → generation_config
//   - imageConfig → image_config (with output field removed)
//   - responseModalities → response_modalities
//   - aspectRatio → aspect_ratio
//   - imageSize → image_size
func aiapidevTransformRequestBody(body map[string]any) map[string]any {
	if body == nil {
		return nil
	}

	result := make(map[string]any)

	// Transform contents
	if contents := asSlice(body["contents"]); contents != nil {
		transformed := make([]any, 0, len(contents))
		for _, item := range contents {
			if m := asMap(item); m != nil {
				transformed = append(transformed, aiapidevTransformContent(m))
			}
		}
		result["contents"] = transformed
	}

	// Transform generationConfig → generation_config
	if genConfig := asMap(body["generationConfig"]); genConfig != nil {
		result["generation_config"] = aiapidevTransformGenerationConfig(genConfig)
	}

	// Copy any other top-level fields that are not transformed
	for key, value := range body {
		if key == "contents" || key == "generationConfig" {
			continue
		}
		result[key] = value
	}

	return result
}

// aiapidevTransformContent transforms a single content item:
// keeps role, transforms parts.
func aiapidevTransformContent(content map[string]any) map[string]any {
	out := make(map[string]any)
	if role, ok := content["role"]; ok {
		out["role"] = role
	}

	if parts := asSlice(content["parts"]); parts != nil {
		transformed := make([]any, 0, len(parts))
		for _, part := range parts {
			if m := asMap(part); m != nil {
				transformed = append(transformed, aiapidevTransformPart(m))
			}
		}
		out["parts"] = transformed
	}

	// Copy other fields
	for key, value := range content {
		if key == "role" || key == "parts" {
			continue
		}
		out[key] = value
	}

	return out
}

// aiapidevTransformPart transforms a single part:
// inlineData → file_data, text stays as-is.
func aiapidevTransformPart(part map[string]any) map[string]any {
	out := make(map[string]any)

	if inlineData := asMap(part["inlineData"]); inlineData != nil {
		// Convert inlineData to file_data
		fileData := map[string]any{}
		if data, ok := asString(inlineData["data"]); ok {
			fileData["file_uri"] = data
		}
		if mimeType, ok := asString(inlineData["mimeType"]); ok {
			fileData["mime_type"] = mimeType
		}
		out["file_data"] = fileData
	} else {
		// Copy all fields as-is (text, etc.)
		for key, value := range part {
			out[key] = value
		}
	}

	return out
}

// aiapidevTransformGenerationConfig converts camelCase generation config
// to snake_case and removes the output field from image config.
func aiapidevTransformGenerationConfig(config map[string]any) map[string]any {
	out := make(map[string]any)

	// responseModalities → response_modalities
	if rm, ok := config["responseModalities"]; ok {
		out["response_modalities"] = rm
	}

	// imageConfig → image_config (with transformations)
	if imageConfig := asMap(config["imageConfig"]); imageConfig != nil {
		ic := make(map[string]any)

		// aspectRatio → aspect_ratio
		if ar, ok := imageConfig["aspectRatio"]; ok {
			ic["aspect_ratio"] = ar
		}

		// imageSize → image_size
		if is, ok := imageConfig["imageSize"]; ok {
			ic["image_size"] = is
		}

		// output is intentionally removed (not sent to upstream)

		// Copy any other imageConfig fields that aren't transformed or removed
		for key, value := range imageConfig {
			if key == "aspectRatio" || key == "imageSize" || key == "output" {
				continue
			}
			ic[key] = value
		}

		out["image_config"] = ic
	}

	// Copy any other generationConfig fields that aren't transformed
	for key, value := range config {
		if key == "responseModalities" || key == "imageConfig" {
			continue
		}
		out[key] = value
	}

	return out
}

// aiapidevParseCreateResponse parses the create-task response.
// Extracts requestId on success, returns UpstreamError on failure.
func aiapidevParseCreateResponse(statusCode int, body []byte) (string, *UpstreamError) {
	bodyText := string(body)

	if statusCode < 200 || statusCode >= 300 {
		return "", &UpstreamError{
			HTTPStatus: statusCode,
			Message:    "aiapidev create task returned HTTP " + itoa(statusCode),
			BodyText:   bodyText,
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    "Failed to parse aiapidev create response.",
			BodyText:   bodyText,
			Note:       err.Error(),
		}
	}

	requestID := getStringValue(parsed, "requestId")
	if stringsTrim(requestID) == "" {
		return "", &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    "aiapidev create response missing requestId.",
			BodyText:   bodyText,
			RawJSON:    parsed,
		}
	}

	return requestID, nil
}

// aiapidevParsePollResponse parses a poll response.
// Returns:
//   - (*UpstreamResult, true, nil) when task succeeded
//   - (nil, false, *UpstreamError) when task failed or HTTP error
//   - (nil, false, nil) when task is still in progress (keep polling)
func aiapidevParsePollResponse(statusCode int, body []byte) (*UpstreamResult, bool, *UpstreamError) {
	bodyText := string(body)

	if statusCode < 200 || statusCode >= 300 {
		return nil, false, &UpstreamError{
			HTTPStatus: statusCode,
			Message:    "aiapidev poll returned HTTP " + itoa(statusCode),
			BodyText:   bodyText,
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    "Failed to parse aiapidev poll response.",
			BodyText:   bodyText,
			Note:       err.Error(),
		}
	}

	status := getStringValue(parsed, "status")

	switch status {
	case "failed":
		errorCode := getStringValue(parsed, "errorCode")
		errorMessage := getStringValue(parsed, "errorMessage")
		msg := coalesceString(errorMessage, "aiapidev task failed")
		if errorCode != "" {
			msg = errorCode + ": " + msg
		}
		return nil, false, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    msg,
			BodyText:   bodyText,
			RawJSON:    parsed,
		}

	case "succeeded":
		var imageURLs []string
		if resultObj := asMap(parsed["result"]); resultObj != nil {
			if items := asSlice(resultObj["items"]); items != nil {
				for _, item := range items {
					if m := asMap(item); m != nil {
						if u, ok := asString(m["url"]); ok && stringsTrim(u) != "" {
							imageURLs = append(imageURLs, stringsTrim(u))
						}
					}
				}
			}
		}
		return &UpstreamResult{
			Status:    "succeeded",
			ImageURLs: imageURLs,
			RawData:   parsed,
		}, true, nil

	default:
		// Still in progress (queued, processing, etc.)
		return nil, false, nil
	}
}

// aiapidevPollDelay returns the delay before polling attempt N.
// Attempt 0: 10s, Attempt 1: 30s, Attempt 2+: 10s.
func aiapidevPollDelay(attempt int) time.Duration {
	if attempt == 1 {
		return 30 * time.Second
	}
	return 10 * time.Second
}
