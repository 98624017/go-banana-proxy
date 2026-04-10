package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultPort            = "8787"
	defaultBananaBaseURL   = "https://api.grsai.com"
	defaultUpstreamTimeout = 10 * time.Minute
)

type Config struct {
	BananaBaseURL   string
	PublicBaseURL    string
	Port            string
	UpstreamTimeout time.Duration
}

// loadConfig reads and normalizes all environment variables at startup,
// keeping configuration parsing out of the request handling path.
func loadConfig() Config {
	cfg := Config{
		BananaBaseURL:   strings.TrimSpace(os.Getenv("BANANA_BASE_URL")),
		PublicBaseURL:    strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")),
		Port:            strings.TrimSpace(os.Getenv("PORT")),
		UpstreamTimeout: defaultUpstreamTimeout,
	}

	if cfg.BananaBaseURL == "" {
		cfg.BananaBaseURL = defaultBananaBaseURL
	}

	if cfg.Port == "" {
		cfg.Port = defaultPort
	}

	if !isNumeric(cfg.Port) {
		log.Printf("Invalid PORT=%q, fallback to %s", cfg.Port, defaultPort)
		cfg.Port = defaultPort
	}

	return cfg
}

func isNumeric(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func main() {
	cfg := loadConfig()
	server := NewServer(cfg)

	log.Printf("banana-proxy (go) listening on :%s", cfg.Port)
	log.Printf("BANANA_BASE_URL=%s", cfg.BananaBaseURL)
	if cfg.PublicBaseURL != "" {
		log.Printf("PUBLIC_BASE_URL=%s", cfg.PublicBaseURL)
	}

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
