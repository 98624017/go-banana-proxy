package main

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type geminiParams struct {
	prompt      string
	urls        []string
	aspectRatio string
	imageSize   string
	imageOutput string
}

func (s *Server) handleGeminiGenerateContent(w http.ResponseWriter, r *http.Request) {
	path := normalizePath(r.URL.Path)
	rawModel := strings.TrimSuffix(strings.TrimPrefix(path, "/v1beta/models/"), ":generateContent")

	auth := parseUpstreamAuth(r, s.cfg, true)
	if auth.ErrorMessage != "" {
		geminiError(w, http.StatusUnauthorized, auth.ErrorMessage, nil)
		return
	}

	provider := s.registry.Resolve(auth.UpstreamBase)
	model := provider.NormalizeModel(rawModel, "gemini")

	body, err := readJSONBody(r)
	if err != nil {
		geminiError(w, http.StatusBadRequest, "Invalid JSON body", nil)
		return
	}

	params := extractGeminiGenerateContentParams(body)
	aspectRatio := stringsTrim(r.URL.Query().Get("aspectRatio"))
	if aspectRatio == "" {
		aspectRatio = stringsTrim(r.URL.Query().Get("aspect_ratio"))
	}
	if aspectRatio == "" {
		aspectRatio = params.aspectRatio
	}

	imageSize := stringsTrim(r.URL.Query().Get("imageSize"))
	if imageSize == "" {
		imageSize = stringsTrim(r.URL.Query().Get("image_size"))
	}
	if imageSize == "" {
		imageSize = params.imageSize
	}

	output := stringsTrim(r.URL.Query().Get("output"))
	if output == "" {
		output = params.imageOutput
	}

	options := imageSyncOptions{
		model:             model,
		prompt:            params.prompt,
		urls:              params.urls,
		aspectRatio:       aspectRatio,
		imageSize:         imageSize,
		responseStyle:     "gemini",
		geminiImageOutput: output,
		originalBody:      body,
	}

	s.handleImageGenerationSync(w, r, options)
}

func (s *Server) handleSyncGeneration(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r)
	if err != nil {
		openAIError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body", nil)
		return
	}

	auth := parseUpstreamAuth(r, s.cfg, true)
	if auth.ErrorMessage != "" {
		openAIError(w, http.StatusUnauthorized, "upstream_auth_missing", auth.ErrorMessage, nil)
		return
	}

	provider := s.registry.Resolve(auth.UpstreamBase)

	model := firstNonEmptyString(body, "model", "model_name")
	model = provider.NormalizeModel(model, "openai")

	prompt := getStringValue(body, "prompt")
	urls := getStringArray(body["urls"])
	if len(urls) == 0 {
		urls = getStringArray(body["images"])
	}

	aspectRatio := firstNonEmptyString(body, "aspect_ratio", "aspectRatio")
	if aspectRatio == "" {
		aspectRatio = "auto"
	}

	imageSize := firstNonEmptyString(body, "image_size", "imageSize")
	if imageSize == "" {
		imageSize = "1K"
	}

	options := imageSyncOptions{
		model:       model,
		prompt:      prompt,
		urls:        urls,
		aspectRatio: aspectRatio,
		imageSize:   imageSize,
	}

	s.handleImageGenerationSync(w, r, options)
}

type imageSyncOptions struct {
	model             string
	prompt            string
	urls              []string
	aspectRatio       string
	imageSize         string
	responseStyle     string
	geminiImageOutput string
	originalBody      map[string]any // for UpstreamExecutor providers
}

type httpError struct {
	status  int
	message string
}

func (e httpError) Error() string {
	return e.message
}

// handleImageGenerationSync is the core sync image generation adapter:
// a single upstream call producing either OpenAI or Gemini style responses.
func (s *Server) handleImageGenerationSync(w http.ResponseWriter, r *http.Request, opts imageSyncOptions) {
	isGemini := opts.responseStyle == "gemini"
	makeError := func(status int, code string, message string, detail any) {
		if isGemini {
			geminiError(w, status, message, detail)
			return
		}
		openAIError(w, status, code, message, detail)
	}

	if stringsTrim(opts.prompt) == "" {
		makeError(http.StatusBadRequest, "invalid_request_error", "Field \"prompt\" is required and must be a string.", nil)
		return
	}

	auth := parseUpstreamAuth(r, s.cfg, true)
	if auth.ErrorMessage != "" {
		makeError(http.StatusUnauthorized, "upstream_auth_missing", auth.ErrorMessage, nil)
		return
	}

	provider := s.registry.Resolve(auth.UpstreamBase)

	// Check for UpstreamExecutor — providers that control the full request lifecycle.
	if executor, ok := provider.(UpstreamExecutor); ok {
		if !isGemini {
			openAIError(w, http.StatusBadRequest, "unsupported_upstream",
				"This upstream only supports the Gemini generateContent endpoint.", nil)
			return
		}
		s.handleExecutorImageGeneration(w, r, opts, auth, provider, executor)
		return
	}

	makeUpstreamError := func(status int, upErr *UpstreamError) {
		if !isGemini {
			openAIUpstreamError(w, status, UpstreamErrorInput{
				UpstreamHttpStatus: &upErr.HTTPStatus,
				UpstreamCode:       upErr.Code,
				UpstreamMessage:    upErr.Message,
				UpstreamJSON:       upErr.RawJSON,
				UpstreamBodyText:   upErr.BodyText,
				Note:               upErr.Note,
			})
			return
		}
		// gemini format
		message := "Upstream error"
		if stringsTrim(upErr.Message) != "" {
			message = stringsTrim(upErr.Message)
		}
		details := map[string]any{
			"upstream_http_status": upErr.HTTPStatus,
			"upstream_code":        toNullableInt(upErr.Code),
			"upstream_message":     nullableString(upErr.Message),
			"note":                 nullableString(upErr.Note),
		}
		if upErr.BodyText != "" {
			details["upstream_body"] = truncateText(upErr.BodyText, 2000)
		}
		if upErr.RawJSON != nil {
			details["upstream_json"] = upErr.RawJSON
		}
		geminiError(w, status, message, details)
	}

	params := ImageGenParams{
		Model:       opts.model,
		Prompt:      opts.prompt,
		URLs:        opts.urls,
		AspectRatio: opts.aspectRatio,
		ImageSize:   opts.imageSize,
	}
	reqBody := provider.BuildRequestBody(params)

	bodyBytes, err := jsonMarshal(reqBody)
	if err != nil {
		makeError(http.StatusInternalServerError, "internal_error", "Failed to encode request.", nil)
		return
	}

	req, err := http.NewRequest(http.MethodPost, auth.UpstreamBase+provider.ImageGenerationPath(), bytes.NewReader(bodyBytes))
	if err != nil {
		makeError(http.StatusInternalServerError, "internal_error", "Failed to create upstream request.", nil)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth.UpstreamAuth)

	tStart := time.Now()
	resp, err := s.upstreamClient.Do(req)
	if err != nil {
		if isGemini {
			geminiError(w, http.StatusBadGateway, "Failed to connect to upstream.", map[string]any{"note": err.Error()})
			return
		}
		openAIError(w, http.StatusBadGateway, "upstream_connection_error", "Failed to connect to upstream.", err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		makeUpstreamError(http.StatusBadGateway, &UpstreamError{
			HTTPStatus: resp.StatusCode,
			Message:    "Failed to read upstream response body.",
			Note:       err.Error(),
		})
		return
	}

	result, upErr := provider.ParseResponse(resp.StatusCode, respBody)
	if upErr != nil {
		makeUpstreamError(upErr.HTTPStatus, upErr)
		return
	}

	// Handle non-success status from upstream
	if result.Status != "succeeded" {
		if result.FailureReason == "input_moderation" || result.FailureReason == "output_moderation" {
			makeError(http.StatusBadRequest, "content_policy_violation", "Upstream moderation triggered: "+result.FailureReason, map[string]any{
				"upstream_http_status":    resp.StatusCode,
				"upstream_failure_reason": result.FailureReason,
				"upstream_error":          result.ErrorDetail,
				"upstream":                result.RawData,
			})
			return
		}

		message := result.ErrorDetail
		if stringsTrim(message) == "" {
			message = "Upstream did not complete successfully. status=" + result.Status + ", failure_reason=" + coalesceString(result.FailureReason, "none")
		}
		makeUpstreamError(http.StatusBadGateway, &UpstreamError{
			HTTPStatus: http.StatusBadGateway,
			Message:    message,
			RawJSON:    result.RawData,
			Note:       "failure_reason=" + coalesceString(result.FailureReason, "none"),
		})
		return
	}

	if len(result.ImageURLs) == 0 {
		makeUpstreamError(http.StatusBadGateway, &UpstreamError{
			HTTPStatus: resp.StatusCode,
			Message:    "Upstream succeeded but no image url was returned.",
			RawJSON:    result.RawData,
		})
		return
	}

	tEnd := time.Now()
	durationMs := tEnd.Sub(tStart).Milliseconds()
	durationSeconds := roundTo3(float64(durationMs) / 1000)

	startTimeBeijing := toBeijingTime(result.StartTime)
	endTimeBeijing := toBeijingTime(result.EndTime)

	if isGemini {
		outputMode := normalizeGeminiImageOutputMode(opts.geminiImageOutput)
		firstURL := result.ImageURLs[0]

		allowedDomains := provider.AllowedImageDomains()

		var inlineDataPart map[string]any
		if outputMode == "url" {
			proxyURL, err := s.buildProxyUrlFromUpstreamImageUrl(firstURL, r, allowedDomains)
			if err != nil {
				status := http.StatusBadGateway
				if err.status != 0 {
					status = err.status
				}
				makeError(status, "upstream_image_proxy_url_failed", coalesceString(err.message, "Failed to build proxy url from upstream image url."), map[string]any{
					"upstream_image_url": truncateText(firstURL, 2000),
				})
				return
			}

			mimeType := guessImageMimeTypeFromUrl(firstURL)
			inlineDataPart = map[string]any{
				"inlineData": map[string]any{
					"mimeType": mimeType,
					"data":     proxyURL,
				},
			}
		} else {
			inlineData, err := s.fetchImageUrlAsInlineData(firstURL, allowedDomains)
			if err != nil {
				status := http.StatusBadGateway
				if err.status != 0 {
					status = err.status
				}
				makeError(status, "upstream_image_fetch_failed", coalesceString(err.message, "Failed to fetch image from upstream url."), map[string]any{
					"upstream_image_url": truncateText(firstURL, 2000),
				})
				return
			}
			inlineDataPart = inlineData
		}

		imageCount := 1
		payload := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"role":  "model",
						"parts": []any{inlineDataPart},
					},
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     imageCount,
				"candidatesTokenCount": imageCount,
				"totalTokenCount":      imageCount * 2,
			},
			"bananaProxyMeta": map[string]any{
				"durationSeconds":  durationSeconds,
				"startTimeBeijing": startTimeBeijing,
				"endTimeBeijing":   endTimeBeijing,
				"durationMs":       durationMs,
			},
		}

		writeJSON(w, http.StatusOK, payload)
		return
	}

	// OpenAI format response
	proxyBase := s.proxyBase(r)
	data := make([]map[string]any, 0, len(result.ImageURLs))
	for _, rawURL := range result.ImageURLs {
		proxied := buildProxyURL(proxyBase, rawURL)
		data = append(data, map[string]any{"url": proxied})
	}

	created := timeNowUnix()
	if result.EndTime != nil {
		created = *result.EndTime
	}

	upstreamMeta := map[string]any{
		"status":             result.RawData["status"],
		"progress":           result.RawData["progress"],
		"failure_reason":     result.RawData["failure_reason"],
		"error":              result.RawData["error"],
		"callback_url":       result.RawData["callback_url"],
		"start_time":         toNullableInt64(result.StartTime),
		"end_time":           toNullableInt64(result.EndTime),
		"start_time_beijing": startTimeBeijing,
		"end_time_beijing":   endTimeBeijing,
		"duration_ms":        deriveDurationMs(result.StartTime, result.EndTime),
		"duration_seconds":   deriveDurationSeconds(result.StartTime, result.EndTime),
	}

	imageCount := len(data)
	if imageCount == 0 {
		imageCount = 1
	}

	payload := map[string]any{
		"id":      result.RawData["id"],
		"created": created,
		"data":    data,
		"usage": map[string]any{
			"prompt_tokens":     imageCount,
			"completion_tokens": imageCount,
			"total_tokens":      imageCount,
		},
		"upstream_meta": upstreamMeta,
	}

	writeJSON(w, http.StatusOK, payload)
}

// extractGeminiGenerateContentParams extracts only the minimal field set
// that this proxy actually forwards to the upstream.
func extractGeminiGenerateContentParams(body map[string]any) geminiParams {
	contents := asSlice(body["contents"])
	var content map[string]any
	for _, item := range contents {
		if m, ok := item.(map[string]any); ok {
			if getStringValue(m, "role") == "user" {
				content = m
				break
			}
		}
	}
	if content == nil && len(contents) > 0 {
		if m, ok := contents[0].(map[string]any); ok {
			content = m
		}
	}

	parts := asSlice(content["parts"])
	var texts []string
	var urls []string
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := asString(m["text"]); ok {
			if trimmed := stringsTrim(text); trimmed != "" {
				texts = append(texts, trimmed)
			}
		}
		if inlineData, ok := m["inlineData"].(map[string]any); ok {
			if data, ok := asString(inlineData["data"]); ok {
				if trimmed := stringsTrim(data); trimmed != "" {
					urls = append(urls, trimmed)
				}
			}
		}
	}

	prompt := strings.Join(texts, "\n")
	generationConfig, _ := body["generationConfig"].(map[string]any)
	imageConfig, _ := generationConfig["imageConfig"].(map[string]any)

	aspectRatio := getStringValue(imageConfig, "aspectRatio")
	if aspectRatio == "" {
		aspectRatio = "auto"
	}

	imageSize := getStringValue(imageConfig, "imageSize")
	if imageSize == "" {
		imageSize = "1K"
	}

	imageOutput := getStringValue(imageConfig, "output")

	return geminiParams{
		prompt:      prompt,
		urls:        urls,
		aspectRatio: aspectRatio,
		imageSize:   imageSize,
		imageOutput: imageOutput,
	}
}

func normalizeGeminiImageOutputMode(rawOutput string) string {
	value := strings.ToLower(stringsTrim(rawOutput))
	switch value {
	case "url":
		return "url"
	case "base64":
		return "base64"
	default:
		return "base64"
	}
}

func (s *Server) buildProxyUrlFromUpstreamImageUrl(rawURL string, r *http.Request, allowedDomains string) (string, *httpError) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", &httpError{status: http.StatusBadGateway, message: "Invalid image url returned by upstream."}
	}

	if !isAllowedUpstreamHostname(target.Hostname(), allowedDomains) {
		return "", &httpError{status: http.StatusForbidden, message: "Forbidden host in image url returned by upstream."}
	}

	proxyBase := s.proxyBase(r)
	return buildProxyURL(proxyBase, target.String()), nil
}

func (s *Server) fetchImageUrlAsInlineData(rawURL string, allowedDomains string) (map[string]any, *httpError) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, &httpError{status: http.StatusBadGateway, message: "Invalid image url returned by upstream."}
	}

	if !isAllowedUpstreamHostname(target.Hostname(), allowedDomains) {
		return nil, &httpError{status: http.StatusForbidden, message: "Forbidden host in image url returned by upstream."}
	}

	resp, err := s.fetchClient.Get(target.String())
	if err != nil {
		return nil, &httpError{status: http.StatusBadGateway, message: "Failed to fetch image from upstream url."}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpError{status: http.StatusBadGateway, message: "Image fetch failed with HTTP " + itoa(resp.StatusCode) + "."}
	}

	bytes, err := ioReadAll(resp.Body)
	if err != nil {
		return nil, &httpError{status: http.StatusBadGateway, message: "Failed to read image response."}
	}

	mimeType := normalizeImageMimeType(resp.Header.Get("Content-Type"))
	b64 := base64Encode(bytes)

	return map[string]any{
		"inlineData": map[string]any{
			"mimeType": mimeType,
			"data":     b64,
		},
	}, nil
}

// handleExecutorImageGeneration handles the full lifecycle for UpstreamExecutor providers.
func (s *Server) handleExecutorImageGeneration(
	w http.ResponseWriter, r *http.Request,
	opts imageSyncOptions, auth AuthResult,
	provider UpstreamProvider, executor UpstreamExecutor,
) {
	params := ImageGenParams{
		Model:       opts.model,
		Prompt:      opts.prompt,
		URLs:        opts.urls,
		AspectRatio: opts.aspectRatio,
		ImageSize:   opts.imageSize,
	}

	ctx := ExecuteContext{
		Ctx:            r.Context(),
		UpstreamClient: s.upstreamClient,
		FetchClient:    s.fetchClient,
		Auth:           auth,
		OriginalBody:   opts.originalBody,
		Params:         params,
		GeminiOutput:   normalizeGeminiImageOutputMode(opts.geminiImageOutput),
	}

	result, upErr := executor.Execute(ctx)
	if upErr != nil {
		message := "Upstream error"
		if stringsTrim(upErr.Message) != "" {
			message = stringsTrim(upErr.Message)
		}
		details := map[string]any{
			"upstream_http_status": upErr.HTTPStatus,
			"upstream_code":        toNullableInt(upErr.Code),
			"upstream_message":     nullableString(upErr.Message),
			"note":                 nullableString(upErr.Note),
		}
		if upErr.BodyText != "" {
			details["upstream_body"] = truncateText(upErr.BodyText, 2000)
		}
		if upErr.RawJSON != nil {
			details["upstream_json"] = upErr.RawJSON
		}
		geminiError(w, upErr.HTTPStatus, message, details)
		return
	}

	if result.Status != "succeeded" {
		msg := result.ErrorDetail
		if stringsTrim(msg) == "" {
			msg = "Upstream did not complete successfully. status=" + result.Status
		}
		geminiError(w, http.StatusBadGateway, msg, map[string]any{
			"upstream_failure_reason": result.FailureReason,
			"upstream":               result.RawData,
		})
		return
	}

	if len(result.ImageURLs) == 0 {
		geminiError(w, http.StatusBadGateway, "Upstream succeeded but no image url was returned.", nil)
		return
	}

	// Build image part (proxy URL or base64)
	firstURL := result.ImageURLs[0]
	allowedDomains := provider.AllowedImageDomains()
	outputMode := normalizeGeminiImageOutputMode(opts.geminiImageOutput)

	var inlineDataPart map[string]any
	if outputMode == "url" {
		proxyURL, pErr := s.buildProxyUrlFromUpstreamImageUrl(firstURL, r, allowedDomains)
		if pErr != nil {
			status := http.StatusBadGateway
			if pErr.status != 0 {
				status = pErr.status
			}
			geminiError(w, status, coalesceString(pErr.message, "Failed to build proxy url."), map[string]any{
				"upstream_image_url": truncateText(firstURL, 2000),
			})
			return
		}
		mimeType := guessImageMimeTypeFromUrl(firstURL)
		inlineDataPart = map[string]any{
			"inlineData": map[string]any{
				"mimeType": mimeType,
				"data":     proxyURL,
			},
		}
	} else {
		inlineData, fErr := s.fetchImageUrlAsInlineData(firstURL, allowedDomains)
		if fErr != nil {
			status := http.StatusBadGateway
			if fErr.status != 0 {
				status = fErr.status
			}
			geminiError(w, status, coalesceString(fErr.message, "Failed to fetch image."), map[string]any{
				"upstream_image_url": truncateText(firstURL, 2000),
			})
			return
		}
		inlineDataPart = inlineData
	}

	// UsageMetadata: use override if set, otherwise default
	usageMetadata := map[string]any{
		"promptTokenCount":     1,
		"candidatesTokenCount": 1,
		"totalTokenCount":      2,
	}
	if result.UsageOverride != nil {
		usageMetadata = result.UsageOverride
	}

	payload := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{inlineDataPart},
				},
				"finishReason":  "STOP",
				"safetyRatings": []any{},
			},
		},
		"usageMetadata": usageMetadata,
	}

	writeJSON(w, http.StatusOK, payload)
}

func deriveDurationMs(startTime *int64, endTime *int64) any {
	if startTime == nil || endTime == nil || *endTime < *startTime {
		return nil
	}
	return (*endTime - *startTime) * 1000
}

func deriveDurationSeconds(startTime *int64, endTime *int64) any {
	if startTime == nil || endTime == nil || *endTime < *startTime {
		return nil
	}
	return roundTo3(float64(*endTime - *startTime))
}
