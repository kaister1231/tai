package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHTTPChatClientUsesOpenAICompatibleEndpoint(t *testing.T) {
	var mu sync.Mutex
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawRequest = true
		mu.Unlock()
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload["model"] != "test-model" {
			t.Errorf("model = %v", payload["model"])
		}
		writeChatResponse(w, "{\"type\":\"answer\",\"text\":\"完成。\"}")
	}))
	defer server.Close()

	client := &HTTPChatClient{
		config: Config{BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "test-key"},
		http:   server.Client(),
	}
	content, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "你好"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	mu.Lock()
	requestSeen := sawRequest
	mu.Unlock()
	if !requestSeen || content != "{\"type\":\"answer\",\"text\":\"完成。\"}" {
		t.Fatalf("unexpected response: %q (request seen: %v)", content, requestSeen)
	}
}

func TestHTTPChatClientSanitizesAuthenticationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "provider details", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := &HTTPChatClient{
		config: Config{BaseURL: server.URL, Model: "test-model", APIKey: "do-not-print"},
		http:   server.Client(),
	}
	_, err := client.Complete(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("expected a useful authentication error, got %v", err)
	}
	if strings.Contains(err.Error(), "do-not-print") || strings.Contains(err.Error(), "provider details") {
		t.Fatalf("error leaked sensitive response content: %v", err)
	}
}

func TestHTTPChatClientShowsBoundedProviderErrorMessage(t *testing.T) {
	long := strings.Repeat("x", 1000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"message": "该令牌无权访问模型 test-model (key do-not-print)\x1b[31m\n" + long,
		}})
	}))
	defer server.Close()
	client := &HTTPChatClient{
		config: Config{BaseURL: server.URL, Model: "test-model", APIKey: "do-not-print"},
		http:   server.Client(),
	}
	_, err := client.Complete(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "access denied (HTTP 403)") || !strings.Contains(msg, "该令牌无权访问模型 test-model") {
		t.Fatalf("expected provider reason, got %q", msg)
	}
	if strings.Contains(msg, "do-not-print") || strings.Contains(msg, "\x1b") || strings.Contains(msg, long) {
		t.Fatalf("error detail was not sanitized or bounded: %q", msg)
	}
}

func TestLoadConfigRequiresRemoteAPIKey(t *testing.T) {
	t.Setenv("TAI_BASE_URL", "https://example.invalid/v1")
	t.Setenv("TAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := loadConfig(30*time.Second, 1024); err == nil || !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
}

func TestLoadConfigAllowsLoopbackWithoutKey(t *testing.T) {
	t.Setenv("TAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("TAI_MODEL", "")
	t.Setenv("TAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	config, err := loadConfig(30*time.Second, 1024)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if config.Model != defaultModel || config.APIKey != "" {
		t.Fatalf("unexpected defaults: %#v", config)
	}
}
