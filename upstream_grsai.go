package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// grsaiParseBody parses a grsai response body (JSON or SSE "data:" lines).
func grsaiParseBody(text string) (any, error) {
	body := stringsTrim(text)
	if body == "" {
		return nil, errors.New("Empty Banana response body")
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		var value any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			return nil, err
		}
		return value, nil
	}
	lines := strings.Split(body, "\n")
	var dataLines []string
	for _, line := range lines {
		trimmed := stringsTrim(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "data:") {
			payload := stringsTrim(trimmed[len("data:"):])
			if strings.EqualFold(payload, "[done]") {
				continue
			}
			dataLines = append(dataLines, trimmed)
		}
	}
	if len(dataLines) == 0 {
		return nil, errors.New("No data: lines found in Banana response: " + truncateText(body, 200))
	}
	last := dataLines[len(dataLines)-1]
	jsonStr := stringsTrim(last[len("data:"):])
	var value any
	if err := json.Unmarshal([]byte(jsonStr), &value); err != nil {
		return nil, err
	}
	return value, nil
}

// grsaiExtractCode extracts the "code" field from a grsai response payload.
func grsaiExtractCode(payload map[string]any) *int {
	if payload == nil {
		return nil
	}
	if value, ok := payload["code"]; ok {
		if parsed, ok := asInt(value); ok {
			return parsed
		}
	}
	return nil
}

// grsaiExtractMessage extracts the error message from a grsai response payload.
func grsaiExtractMessage(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload["msg"]; ok {
		if s, ok := asString(value); ok {
			return s
		}
	}
	if value, ok := payload["message"]; ok {
		if s, ok := asString(value); ok {
			return s
		}
	}
	return ""
}

var grsaiAuthErrorPattern = regexp.MustCompile(`(?i)unauthorized|forbidden|invalid\s*(api\s*key|key|token)|api\s*key|apikey|token|authorization`)

func grsaiIsLikelyAuthError(message string) bool {
	if stringsTrim(message) == "" {
		return false
	}
	return grsaiAuthErrorPattern.MatchString(message)
}

// grsaiGuessHTTPStatus maps upstream code/message to an HTTP status code.
func grsaiGuessHTTPStatus(code *int, msg string) int {
	if code != nil {
		switch *code {
		case 400, 401, 403, 404, 409, 422, 429:
			return *code
		}
	}
	if grsaiIsLikelyAuthError(msg) {
		return http.StatusUnauthorized
	}
	return http.StatusBadGateway
}

// grsaiExtractResults extracts the "results" array from a grsai data payload.
func grsaiExtractResults(payload map[string]any) []map[string]any {
	if payload == nil {
		return nil
	}
	value, ok := payload["results"]
	if !ok {
		return nil
	}
	items := asSlice(value)
	if items == nil {
		return nil
	}
	results := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			results = append(results, m)
		}
	}
	return results
}

type grsaiProvider struct{}

func (p *grsaiProvider) Name() string { return "grsai" }

func (p *grsaiProvider) Match(baseURL string) bool {
	host := extractHostFromBaseURL(baseURL)
	if host == "" {
		return false
	}
	return host == "grsai.com" || strings.HasSuffix(host, ".grsai.com")
}

func (p *grsaiProvider) ImageGenerationPath() string {
	return "/v1/draw/nano-banana"
}

func (p *grsaiProvider) AllowedImageDomains() string {
	return "grsai.com,aitohumanize.com"
}

func (p *grsaiProvider) NormalizeModel(rawModel string, source string) string {
	model := strings.TrimSpace(rawModel)
	if source == "gemini" {
		lower := strings.ToLower(model)
		switch lower {
		case "gemini-3-pro-image-preview":
			return "nano-banana-pro"
		case "gemini-2.5-flash-image":
			return "nano-banana-fast"
		case "gemini-3.1-flash-image-preview":
			return "nano-banana-2"
		default:
			if model == "" {
				return "nano-banana-fast"
			}
			return model
		}
	}
	// openai source (and any other source)
	if model == "" {
		return "nano-banana-fast"
	}
	return model
}

func (p *grsaiProvider) BuildRequestBody(params ImageGenParams) map[string]any {
	return map[string]any{
		"model":        params.Model,
		"prompt":       params.Prompt,
		"urls":         params.URLs,
		"aspectRatio":  params.AspectRatio,
		"imageSize":    params.ImageSize,
		"shutProgress": true,
	}
}

// ParseResponse parses a grsai HTTP response.
// grsai envelope: {"code": 0, "msg": "success", "data": { ... }}
// Also handles SSE-style "data:" lines.
func (p *grsaiProvider) ParseResponse(statusCode int, body []byte) (*UpstreamResult, *UpstreamError) {
	bodyText := string(body)
	parsed, parseErr := grsaiParseBody(bodyText)

	// HTTP error
	if statusCode < 200 || statusCode >= 300 {
		var bodyMap map[string]any
		if parsed != nil {
			bodyMap, _ = parsed.(map[string]any)
		}
		code := grsaiExtractCode(bodyMap)
		msg := grsaiExtractMessage(bodyMap)
		httpStatus := http.StatusBadGateway
		if code != nil {
			httpStatus = grsaiGuessHTTPStatus(code, msg)
		} else if statusCode >= 400 && statusCode < 500 {
			httpStatus = statusCode
		}
		return nil, &UpstreamError{
			HTTPStatus: httpStatus,
			Code:       code,
			Message:    coalesceString(msg, "Banana upstream returned HTTP "+itoa(statusCode)),
			BodyText:   bodyText,
			RawJSON:    parsed,
		}
	}

	if parseErr != nil {
		return nil, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    "Failed to parse Banana response.",
			BodyText:   bodyText,
			Note:       parseErr.Error(),
		}
	}

	bodyMap, ok := parsed.(map[string]any)
	if !ok {
		return nil, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    "Banana upstream returned an unexpected response format.",
			BodyText:   bodyText,
			RawJSON:    parsed,
		}
	}

	code := grsaiExtractCode(bodyMap)
	msg := grsaiExtractMessage(bodyMap)

	if code != nil && *code != 0 {
		httpStatus := grsaiGuessHTTPStatus(code, msg)
		return nil, &UpstreamError{
			HTTPStatus: httpStatus,
			Code:       code,
			Message:    coalesceString(msg, "Banana upstream error (code="+itoa(*code)+")"),
			BodyText:   bodyText,
			RawJSON:    bodyMap,
		}
	}

	// Unwrap data envelope when code field is present (inline extractBananaData logic)
	bananaData := bodyMap
	if code != nil {
		if data, ok := bodyMap["data"]; ok {
			if m, ok := data.(map[string]any); ok {
				bananaData = m
			}
		}
	}

	statusValue := strings.TrimSpace(getStringValue(bananaData, "status"))
	if statusValue == "" {
		return nil, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Code:       code,
			Message:    coalesceString(msg, "Banana upstream returned an unexpected response without status."),
			BodyText:   bodyText,
			RawJSON:    bodyMap,
		}
	}

	failureReason := getStringValue(bananaData, "failure_reason")
	errorDetail := getStringValue(bananaData, "error")
	startTime := extractInt64(bananaData, "start_time")
	endTime := extractInt64(bananaData, "end_time")

	var imageURLs []string
	for _, r := range grsaiExtractResults(bananaData) {
		if u := strings.TrimSpace(getStringValue(r, "url")); u != "" {
			imageURLs = append(imageURLs, u)
		}
	}

	return &UpstreamResult{
		Status:        statusValue,
		ImageURLs:     imageURLs,
		FailureReason: failureReason,
		ErrorDetail:   errorDetail,
		StartTime:     startTime,
		EndTime:       endTime,
		RawData:       bananaData,
	}, nil
}
