package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatClient interface {
	Complete(context.Context, []Message) (string, error)
}

type HTTPChatClient struct {
	config Config
	http   *http.Client
}

func newDefaultClient(config Config) (ChatClient, error) {
	return &HTTPChatClient{config: config, http: &http.Client{Timeout: 45 * time.Second}}, nil
}

func (c *HTTPChatClient) Complete(ctx context.Context, messages []Message) (string, error) {
	payload := struct {
		Model       string    `json:"model"`
		Messages    []Message `json:"messages"`
		Temperature float64   `json:"temperature"`
		MaxTokens   int       `json:"max_tokens"`
	}{Model: c.config.Model, Messages: messages, Temperature: 0.2, MaxTokens: 1200}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("could not encode model request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("could not create model request")
	}
	request.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("model request canceled")
		}
		return "", fmt.Errorf("model connection failed: %s", safeTerminal(err.Error()))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var message string
		switch response.StatusCode {
		case http.StatusUnauthorized:
			message = "model authentication failed (HTTP 401); check the configured API key"
		case http.StatusForbidden:
			message = "model access denied (HTTP 403); check the API key and whether it may use TAI_MODEL"
		case http.StatusNotFound:
			message = "model endpoint or model was not found (HTTP 404); check TAI_BASE_URL and TAI_MODEL"
		case http.StatusTooManyRequests:
			message = "model rate limit reached (HTTP 429)"
		default:
			message = fmt.Sprintf("model request failed (HTTP %d)", response.StatusCode)
		}
		if detail := providerErrorDetail(response.Body, c.config.APIKey); detail != "" {
			message += "\nprovider: " + detail
		}
		return "", fmt.Errorf("%s", message)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("could not read model response")
	}
	if len(responseBody) > maxResponseBytes {
		return "", fmt.Errorf("model response exceeded the 2 MiB limit")
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil || len(result.Choices) == 0 {
		return "", fmt.Errorf("model returned an invalid chat-completions payload")
	}
	content := result.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("model returned an empty response")
	}
	return content, nil
}

const (
	maxErrorBodyBytes   = 8 * 1024
	maxErrorDetailBytes = 300
)

// providerErrorDetail extracts only the OpenAI-style {"error":{"message":...}} text,
// so arbitrary response bodies are never echoed.
func providerErrorDetail(body io.Reader, apiKey string) string {
	data, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	detail := strings.Join(strings.Fields(payload.Error.Message), " ")
	if apiKey != "" {
		detail = strings.ReplaceAll(detail, apiKey, "[redacted]")
	}
	if len(detail) > maxErrorDetailBytes {
		detail = truncateUTF8(detail, maxErrorDetailBytes) + "…"
	}
	return safeTerminal(detail)
}
