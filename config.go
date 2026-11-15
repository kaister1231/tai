package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL       = "https://api.openai.com/v1"
	defaultModel         = "gpt-4o-mini"
	defaultCommandLimit  = 4
	maxPromptBytes       = 8 * 1024
	maxPipedInputBytes   = 64 * 1024
	maxPipedContextBytes = 6 * 1024
	maxMessageBytes      = 16 * 1024
	maxHistoryMessages   = 12
	maxResponseBytes     = 2 * 1024 * 1024
	maxOutputLimit       = 1024 * 1024
)

type Config struct {
	BaseURL     string
	Model       string
	APIKey      string
	CommandTime time.Duration
	OutputLimit int
}

func loadConfig(commandTimeout time.Duration, outputLimit int) (Config, error) {
	baseURL := strings.TrimSpace(os.Getenv("TAI_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Config{}, fmt.Errorf("TAI_BASE_URL must be an http(s) URL without credentials, query, or fragment")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return Config{}, fmt.Errorf("TAI_BASE_URL is empty")
	}
	model := strings.TrimSpace(os.Getenv("TAI_MODEL"))
	if model == "" {
		model = defaultModel
	}
	apiKey := strings.TrimSpace(os.Getenv("TAI_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if apiKey == "" && !isLoopbackHost(parsed.Hostname()) {
		return Config{}, fmt.Errorf("missing API key: set TAI_API_KEY or OPENAI_API_KEY (local Ollama URLs do not require one)")
	}
	if commandTimeout <= 0 || commandTimeout > 10*time.Minute {
		return Config{}, fmt.Errorf("--timeout must be greater than 0 and no more than 10m")
	}
	if outputLimit <= 0 || outputLimit > maxOutputLimit {
		return Config{}, fmt.Errorf("--output-limit must be between 1 and %d bytes per stream", maxOutputLimit)
	}
	return Config{BaseURL: baseURL, Model: model, APIKey: apiKey, CommandTime: commandTimeout, OutputLimit: outputLimit}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
