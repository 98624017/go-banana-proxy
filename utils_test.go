package main

import (
	"net/http/httptest"
	"testing"
)

func TestPublicBaseURLOverride(t *testing.T) {
	cfg := Config{PublicBaseURL: "https://proxy.example.com/"}
	server := NewServer(cfg)
	req := httptest.NewRequest("GET", "http://origin.test/v1/images/generations", nil)
	base := server.proxyBase(req)
	if base != "https://proxy.example.com" {
		t.Fatalf("base mismatch: got %s", base)
	}

	cfg = Config{}
	server = NewServer(cfg)
	req = httptest.NewRequest("GET", "http://origin.test/v1/images/generations", nil)
	base = server.proxyBase(req)
	if base != "http://origin.test" {
		t.Fatalf("base mismatch: got %s", base)
	}
}

func TestServeHTTPProxyQueryRoute(t *testing.T) {
	server := NewServer(Config{})
	req := httptest.NewRequest("GET", "http://origin.test/proxy/image", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, req)

	if recorder.Code != 404 {
		t.Fatalf("status mismatch: got %d want %d", recorder.Code, 404)
	}
}

func TestUpstreamHostnameAllowlist(t *testing.T) {
	cases := []struct {
		host    string
		domains string
		want    bool
	}{
		{"grsai.com", "grsai.com,aitohumanize.com", true},
		{"api.grsai.com", "grsai.com,aitohumanize.com", true},
		{"evilgrsai.com", "grsai.com", false},
		{"grsai.com.evil", "grsai.com", false},
		{"sub.evilgrsai.com", "grsai.com", false},
		{"aitohumanize.com", "grsai.com,aitohumanize.com", true},
		{"cdn.aitohumanize.com", "grsai.com,aitohumanize.com", true},
		{"", "grsai.com", false},
	}
	for _, tc := range cases {
		if got := isAllowedUpstreamHostname(tc.host, tc.domains); got != tc.want {
			t.Errorf("isAllowedUpstreamHostname(%q, %q) = %v, want %v", tc.host, tc.domains, got, tc.want)
		}
	}
}

func TestBuildProxyURL(t *testing.T) {
	cases := []struct {
		base   string
		rawURL string
		want   string
	}{
		{"https://proxy.example.com", "https://api.grsai.com/img/123.png", "https://proxy.example.com/proxy/image?url=https%3A%2F%2Fapi.grsai.com%2Fimg%2F123.png"},
		{"https://proxy.example.com/", "https://api.grsai.com/img/123.png", "https://proxy.example.com/proxy/image?url=https%3A%2F%2Fapi.grsai.com%2Fimg%2F123.png"},
		{"", "https://api.grsai.com/img/123.png", "https://api.grsai.com/img/123.png"},
		{"https://proxy.example.com", "", ""},
	}
	for _, tc := range cases {
		got := buildProxyURL(tc.base, tc.rawURL)
		if got != tc.want {
			t.Errorf("buildProxyURL(%q, %q) = %q, want %q", tc.base, tc.rawURL, got, tc.want)
		}
	}
}

func TestHealthEndpoint(t *testing.T) {
	server := NewServer(Config{})
	req := httptest.NewRequest("GET", "http://localhost/health", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if recorder.Body.String() != "ok" {
		t.Fatalf("body = %q, want ok", recorder.Body.String())
	}
}

func TestDeletedRoutesReturn404(t *testing.T) {
	server := NewServer(Config{})
	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/v1/images/generations/async"},
		{"GET", "/v1/images/generations/result?id=123"},
		{"GET", "/v1/images/tasks/123"},
		{"GET", "/v1/images/proxy/abc123"},
		{"GET", "/proxy/image?url=https://example.com"},
		{"POST", "/v1/videos"},
		{"GET", "/v1/videos/123"},
	}
	for _, route := range routes {
		req := httptest.NewRequest(route.method, "http://localhost"+route.path, nil)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		if recorder.Code != 404 {
			t.Errorf("%s %s: status = %d, want 404", route.method, route.path, recorder.Code)
		}
	}
}
