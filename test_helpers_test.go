package main

import (
	"encoding/json"
	"net/http"
)

func writeChatResponse(w http.ResponseWriter, content string) {
	payload := map[string]any{
		"choices": []any{
			map[string]any{"message": map[string]any{"content": content}},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
