package main

import (
	"net/http"
	"strings"
	"time"
)

type Server struct {
	cfg            Config
	upstreamClient *http.Client
	fetchClient    *http.Client // for image fetching in Gemini base64 mode, 30s timeout
	registry       *providerRegistry
}

func NewServer(cfg Config) *Server {
	grsai := &grsaiProvider{}
	aiapidev := &aiapidevProvider{}
	registry := newProviderRegistry(grsai, aiapidev)
	return &Server{
		cfg:            cfg,
		upstreamClient: &http.Client{Timeout: cfg.UpstreamTimeout},
		fetchClient:    &http.Client{Timeout: 30 * time.Second},
		registry:       registry,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := normalizePath(r.URL.Path)

	if r.Method == http.MethodPost {
		if strings.HasPrefix(path, "/v1beta/models/") && strings.HasSuffix(path, ":generateContent") {
			s.handleGeminiGenerateContent(w, r)
			return
		}
		if path == "/v1/images/generations" {
			s.handleSyncGeneration(w, r)
			return
		}
	}

	if path == "/health" && r.Method == http.MethodGet {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("Not found"))
}

func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	if path != "/" {
		return strings.TrimRight(path, "/")
	}
	return path
}

// proxyBase determines the public-facing proxy prefix, preferring explicit
// configuration and falling back to the current request's origin.
func (s *Server) proxyBase(r *http.Request) string {
	if s.cfg.PublicBaseURL != "" {
		return strings.TrimRight(s.cfg.PublicBaseURL, "/")
	}
	return requestOrigin(r)
}
