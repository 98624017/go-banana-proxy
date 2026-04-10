package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAiapidevMatch(t *testing.T) {
	p := &aiapidevProvider{}
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"https://www.aiapidev.com", true},
		{"https://aiapidev.com", true},
		{"https://sub.aiapidev.com", true},
		{"https://other.com", false},
		{"https://evilaiapidev.com", false},
		{"https://aiapidev.com.evil.com", false},
		{"", false},
	}
	for _, tc := range cases {
		got := p.Match(tc.baseURL)
		if got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestAiapidevNormalizeModel(t *testing.T) {
	p := &aiapidevProvider{}
	cases := []struct {
		raw    string
		source string
		want   string
	}{
		{"gemini-3-pro-image-preview", "gemini", "nanobananapro"},
		{"gemini-3.1-flash-image-preview", "gemini", "nanobanana2"},
		{"some-custom-model", "gemini", "some-custom-model"},
		{"", "gemini", ""},
		{"gemini-3-pro-image-preview", "openai", "nanobananapro"},
		{"gemini-3.1-flash-image-preview", "openai", "nanobanana2"},
		{"my-model", "openai", "my-model"},
		{"  gemini-3-pro-image-preview  ", "gemini", "nanobananapro"},
	}
	for _, tc := range cases {
		got := p.NormalizeModel(tc.raw, tc.source)
		if got != tc.want {
			t.Errorf("NormalizeModel(%q, %q) = %q, want %q", tc.raw, tc.source, got, tc.want)
		}
	}
}

func TestAiapidevMetadata(t *testing.T) {
	p := &aiapidevProvider{}
	if p.Name() != "aiapidev" {
		t.Errorf("Name() = %q, want %q", p.Name(), "aiapidev")
	}
	if p.AllowedImageDomains() != "r2.dev" {
		t.Errorf("AllowedImageDomains() = %q, want %q", p.AllowedImageDomains(), "r2.dev")
	}
}

func TestAiapidevTransformRequestBody(t *testing.T) {
	input := map[string]any{
		"contents": []any{
			map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{
						"text": "draw a cat",
					},
					map[string]any{
						"inlineData": map[string]any{
							"mimeType": "image/png",
							"data":     "https://example.com/image.png",
						},
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseModalities": []any{"TEXT", "IMAGE"},
			"imageConfig": map[string]any{
				"aspectRatio": "16:9",
				"imageSize":   "1K",
				"output":      "url",
			},
		},
	}

	result := aiapidevTransformRequestBody(input)

	// Check contents transformation
	contents := asSlice(result["contents"])
	if len(contents) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(contents))
	}
	content := asMap(contents[0])
	if content["role"] != "user" {
		t.Errorf("role = %v, want user", content["role"])
	}

	parts := asSlice(content["parts"])
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	// First part: text should be kept as-is
	textPart := asMap(parts[0])
	if textPart["text"] != "draw a cat" {
		t.Errorf("text part = %v, want 'draw a cat'", textPart["text"])
	}

	// Second part: inlineData should be transformed to file_data
	fileDataPart := asMap(parts[1])
	if fileDataPart["inlineData"] != nil {
		t.Error("inlineData should be removed after transformation")
	}
	fileData := asMap(fileDataPart["file_data"])
	if fileData == nil {
		t.Fatal("file_data should be present")
	}
	if fileData["file_uri"] != "https://example.com/image.png" {
		t.Errorf("file_uri = %v, want https://example.com/image.png", fileData["file_uri"])
	}
	if fileData["mime_type"] != "image/png" {
		t.Errorf("mime_type = %v, want image/png", fileData["mime_type"])
	}

	// Check generationConfig → generation_config
	if result["generationConfig"] != nil {
		t.Error("generationConfig should be removed (replaced by generation_config)")
	}
	genConfig := asMap(result["generation_config"])
	if genConfig == nil {
		t.Fatal("generation_config should be present")
	}

	// Check responseModalities → response_modalities
	if genConfig["responseModalities"] != nil {
		t.Error("responseModalities should be removed (replaced by response_modalities)")
	}
	respModalities := asSlice(genConfig["response_modalities"])
	if len(respModalities) != 2 {
		t.Errorf("response_modalities length = %d, want 2", len(respModalities))
	}

	// Check imageConfig → image_config
	if genConfig["imageConfig"] != nil {
		t.Error("imageConfig should be removed (replaced by image_config)")
	}
	imageConfig := asMap(genConfig["image_config"])
	if imageConfig == nil {
		t.Fatal("image_config should be present")
	}

	// Check aspectRatio → aspect_ratio
	if imageConfig["aspectRatio"] != nil {
		t.Error("aspectRatio should be removed (replaced by aspect_ratio)")
	}
	if imageConfig["aspect_ratio"] != "16:9" {
		t.Errorf("aspect_ratio = %v, want 16:9", imageConfig["aspect_ratio"])
	}

	// Check imageSize → image_size
	if imageConfig["imageSize"] != nil {
		t.Error("imageSize should be removed (replaced by image_size)")
	}
	if imageConfig["image_size"] != "1K" {
		t.Errorf("image_size = %v, want 1K", imageConfig["image_size"])
	}

	// Check output is removed
	if imageConfig["output"] != nil {
		t.Error("output should be removed from image_config")
	}
}

func TestAiapidevParseCreateResponse(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		raw := map[string]any{"requestId": "task-abc-123"}
		body, _ := json.Marshal(raw)
		requestID, upErr := aiapidevParseCreateResponse(200, body)
		if upErr != nil {
			t.Fatalf("unexpected error: %v", upErr)
		}
		if requestID != "task-abc-123" {
			t.Errorf("requestId = %q, want %q", requestID, "task-abc-123")
		}
	})

	t.Run("http_error", func(t *testing.T) {
		body := []byte(`{"error":"unauthorized"}`)
		requestID, upErr := aiapidevParseCreateResponse(401, body)
		if requestID != "" {
			t.Errorf("expected empty requestId, got %q", requestID)
		}
		if upErr == nil {
			t.Fatal("expected error")
		}
		if upErr.HTTPStatus != 401 {
			t.Errorf("HTTPStatus = %d, want 401", upErr.HTTPStatus)
		}
	})

	t.Run("missing_requestId", func(t *testing.T) {
		raw := map[string]any{"status": "ok"}
		body, _ := json.Marshal(raw)
		requestID, upErr := aiapidevParseCreateResponse(200, body)
		if requestID != "" {
			t.Errorf("expected empty requestId, got %q", requestID)
		}
		if upErr == nil {
			t.Fatal("expected error")
		}
		if upErr.HTTPStatus != 502 {
			t.Errorf("HTTPStatus = %d, want 502", upErr.HTTPStatus)
		}
	})
}

func TestAiapidevParsePollResponse(t *testing.T) {
	t.Run("queued_not_done", func(t *testing.T) {
		raw := map[string]any{"status": "queued"}
		body, _ := json.Marshal(raw)
		result, done, upErr := aiapidevParsePollResponse(200, body)
		if result != nil {
			t.Error("expected nil result for queued status")
		}
		if done {
			t.Error("expected done=false for queued status")
		}
		if upErr != nil {
			t.Errorf("unexpected error: %v", upErr)
		}
	})

	t.Run("succeeded", func(t *testing.T) {
		raw := map[string]any{
			"status": "succeeded",
			"result": map[string]any{
				"items": []any{
					map[string]any{"url": "https://img.r2.dev/abc.png"},
					map[string]any{"url": "https://img.r2.dev/def.png"},
				},
			},
		}
		body, _ := json.Marshal(raw)
		result, done, upErr := aiapidevParsePollResponse(200, body)
		if upErr != nil {
			t.Fatalf("unexpected error: %v", upErr)
		}
		if !done {
			t.Error("expected done=true for succeeded status")
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if len(result.ImageURLs) != 2 {
			t.Fatalf("expected 2 image URLs, got %d", len(result.ImageURLs))
		}
		if result.ImageURLs[0] != "https://img.r2.dev/abc.png" {
			t.Errorf("ImageURLs[0] = %q", result.ImageURLs[0])
		}
		if result.ImageURLs[1] != "https://img.r2.dev/def.png" {
			t.Errorf("ImageURLs[1] = %q", result.ImageURLs[1])
		}
		if result.Status != "succeeded" {
			t.Errorf("Status = %q, want succeeded", result.Status)
		}
	})

	t.Run("failed", func(t *testing.T) {
		raw := map[string]any{
			"status":       "failed",
			"errorCode":    "content_blocked",
			"errorMessage": "Content was blocked by moderation",
		}
		body, _ := json.Marshal(raw)
		result, done, upErr := aiapidevParsePollResponse(200, body)
		if result != nil {
			t.Error("expected nil result for failed status")
		}
		if done {
			t.Error("expected done=false for failed status")
		}
		if upErr == nil {
			t.Fatal("expected error for failed status")
		}
		if upErr.HTTPStatus != 502 {
			t.Errorf("HTTPStatus = %d, want 502", upErr.HTTPStatus)
		}
		// Error message should contain the error details
		if upErr.Message == "" {
			t.Error("expected non-empty error message")
		}
	})

	t.Run("http_error", func(t *testing.T) {
		body := []byte(`{"error":"server error"}`)
		result, done, upErr := aiapidevParsePollResponse(500, body)
		if result != nil {
			t.Error("expected nil result for HTTP error")
		}
		if done {
			t.Error("expected done=false for HTTP error")
		}
		if upErr == nil {
			t.Fatal("expected error for HTTP error")
		}
		if upErr.HTTPStatus != 500 {
			t.Errorf("HTTPStatus = %d, want 500", upErr.HTTPStatus)
		}
	})
}

func TestAiapidevPollDelay(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 10 * time.Second},
		{1, 30 * time.Second},
		{2, 10 * time.Second},
		{3, 10 * time.Second},
		{10, 10 * time.Second},
	}
	for _, tc := range cases {
		got := aiapidevPollDelay(tc.attempt)
		if got != tc.want {
			t.Errorf("aiapidevPollDelay(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// TestAiapidevImplementsExecutor verifies that aiapidevProvider implements
// both UpstreamProvider and UpstreamExecutor interfaces at compile time.
func TestAiapidevImplementsExecutor(t *testing.T) {
	var _ UpstreamProvider = (*aiapidevProvider)(nil)
	var _ UpstreamExecutor = (*aiapidevProvider)(nil)
}
