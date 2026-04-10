package main

import (
	"encoding/json"
	"testing"
)

func TestGrsaiNormalizeModel(t *testing.T) {
	p := &grsaiProvider{}
	cases := []struct {
		raw    string
		source string
		want   string
	}{
		{"gemini-3-pro-image-preview", "gemini", "nano-banana-pro"},
		{"gemini-2.5-flash-image", "gemini", "nano-banana-fast"},
		{"gemini-3.1-flash-image-preview", "gemini", "nano-banana-2"},
		{"some-custom-model", "gemini", "some-custom-model"},
		{"", "openai", "nano-banana-fast"},
		{"my-model", "openai", "my-model"},
		{"  gemini-3-pro-image-preview  ", "gemini", "nano-banana-pro"},
	}
	for _, tc := range cases {
		got := p.NormalizeModel(tc.raw, tc.source)
		if got != tc.want {
			t.Errorf("NormalizeModel(%q, %q) = %q, want %q", tc.raw, tc.source, got, tc.want)
		}
	}
}

func TestGrsaiMatch(t *testing.T) {
	p := &grsaiProvider{}
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"https://api.grsai.com", true},
		{"https://grsai.com", true},
		{"https://sub.api.grsai.com", true},
		{"https://api.other.com", false},
		{"https://evilgrsai.com", false},
		{"https://grsai.com.evil.com", false},
		{"", false},
	}
	for _, tc := range cases {
		got := p.Match(tc.baseURL)
		if got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestGrsaiMetadata(t *testing.T) {
	p := &grsaiProvider{}
	if p.Name() != "grsai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "grsai")
	}
	if p.ImageGenerationPath() != "/v1/draw/nano-banana" {
		t.Errorf("ImageGenerationPath() = %q", p.ImageGenerationPath())
	}
	if p.AllowedImageDomains() != "grsai.com,aitohumanize.com" {
		t.Errorf("AllowedImageDomains() = %q", p.AllowedImageDomains())
	}
}

func TestGrsaiBuildRequestBody(t *testing.T) {
	p := &grsaiProvider{}
	params := ImageGenParams{
		Model: "nano-banana-fast", Prompt: "a cat",
		URLs: []string{"https://example.com/ref.png"}, AspectRatio: "16:9", ImageSize: "1K",
	}
	body := p.BuildRequestBody(params)
	if body["model"] != "nano-banana-fast" {
		t.Errorf("model = %v", body["model"])
	}
	if body["prompt"] != "a cat" {
		t.Errorf("prompt = %v", body["prompt"])
	}
	if body["aspectRatio"] != "16:9" {
		t.Errorf("aspectRatio = %v", body["aspectRatio"])
	}
	if body["imageSize"] != "1K" {
		t.Errorf("imageSize = %v", body["imageSize"])
	}
	if body["shutProgress"] != true {
		t.Errorf("shutProgress = %v", body["shutProgress"])
	}
}

func TestGrsaiParseResponseSuccess(t *testing.T) {
	p := &grsaiProvider{}
	raw := map[string]any{
		"code": 0, "msg": "success",
		"data": map[string]any{
			"status":         "succeeded",
			"results":        []any{map[string]any{"url": "https://api.grsai.com/img/123.png"}},
			"start_time":     1700000000,
			"end_time":       1700000010,
			"failure_reason": "", "error": "",
		},
	}
	body, _ := json.Marshal(raw)
	result, upErr := p.ParseResponse(200, body)
	if upErr != nil {
		t.Fatalf("unexpected error: %v", upErr)
	}
	if result.Status != "succeeded" {
		t.Errorf("status = %q", result.Status)
	}
	if len(result.ImageURLs) != 1 || result.ImageURLs[0] != "https://api.grsai.com/img/123.png" {
		t.Errorf("ImageURLs = %v", result.ImageURLs)
	}
	if result.StartTime == nil || *result.StartTime != 1700000000 {
		t.Errorf("StartTime = %v", result.StartTime)
	}
	if result.EndTime == nil || *result.EndTime != 1700000010 {
		t.Errorf("EndTime = %v", result.EndTime)
	}
}

func TestGrsaiParseResponseHTTPError(t *testing.T) {
	p := &grsaiProvider{}
	raw := map[string]any{"code": 401, "msg": "unauthorized"}
	body, _ := json.Marshal(raw)
	result, upErr := p.ParseResponse(401, body)
	if result != nil {
		t.Fatalf("expected nil result")
	}
	if upErr == nil {
		t.Fatal("expected error")
	}
	if upErr.HTTPStatus != 401 {
		t.Errorf("HTTPStatus = %d, want 401", upErr.HTTPStatus)
	}
}

func TestGrsaiParseResponseBusinessError(t *testing.T) {
	p := &grsaiProvider{}
	raw := map[string]any{"code": -1, "msg": "insufficient balance"}
	body, _ := json.Marshal(raw)
	result, upErr := p.ParseResponse(200, body)
	if result != nil {
		t.Fatalf("expected nil result")
	}
	if upErr == nil {
		t.Fatal("expected error")
	}
	if upErr.Code == nil || *upErr.Code != -1 {
		t.Errorf("Code = %v, want -1", upErr.Code)
	}
}

func TestGrsaiParseResponseModeration(t *testing.T) {
	p := &grsaiProvider{}
	raw := map[string]any{
		"code": 0, "msg": "success",
		"data": map[string]any{
			"status": "failed", "failure_reason": "input_moderation",
			"error": "content blocked", "results": []any{},
		},
	}
	body, _ := json.Marshal(raw)
	result, upErr := p.ParseResponse(200, body)
	if upErr != nil {
		t.Fatalf("unexpected error: %v", upErr)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Status != "failed" {
		t.Errorf("status = %q", result.Status)
	}
	if result.FailureReason != "input_moderation" {
		t.Errorf("FailureReason = %q", result.FailureReason)
	}
}

func TestGrsaiParseResponseNoResults(t *testing.T) {
	p := &grsaiProvider{}
	raw := map[string]any{
		"code": 0, "msg": "success",
		"data": map[string]any{"status": "succeeded", "results": []any{}},
	}
	body, _ := json.Marshal(raw)
	result, upErr := p.ParseResponse(200, body)
	if upErr != nil {
		t.Fatalf("unexpected error: %v", upErr)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if len(result.ImageURLs) != 0 {
		t.Errorf("ImageURLs should be empty, got %v", result.ImageURLs)
	}
}

func TestGrsaiParseResponseSSE(t *testing.T) {
	p := &grsaiProvider{}
	sseBody := "data: {\"code\":0,\"msg\":\"success\",\"data\":{\"status\":\"succeeded\",\"results\":[{\"url\":\"https://api.grsai.com/img/456.png\"}]}}\n\ndata: [DONE]\n"
	result, upErr := p.ParseResponse(200, []byte(sseBody))
	if upErr != nil {
		t.Fatalf("unexpected error: %v", upErr)
	}
	if result.Status != "succeeded" {
		t.Errorf("status = %q", result.Status)
	}
	if len(result.ImageURLs) != 1 || result.ImageURLs[0] != "https://api.grsai.com/img/456.png" {
		t.Errorf("ImageURLs = %v", result.ImageURLs)
	}
}
