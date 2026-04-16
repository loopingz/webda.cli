package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
)

// newMockWebdaServer returns an httptest.Server that handles the Webda API endpoints.
func newMockWebdaServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		token, _ := req["token"].(string)
		if token == "" || token == "bad-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		seq := 1
		if s, ok := req["sequence"].(json.Number); ok {
			v, _ := s.Int64()
			seq = int(v) + 1
		} else if s, ok := req["sequence"].(float64); ok {
			seq = int(s) + 1
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"sequence":     seq,
			"expires_in":   3600,
		})
	})

	mux.HandleFunc("/auth/me", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"uuid":  "user-123",
			"email": "test@example.com",
		})
	})

	mux.HandleFunc("/operations", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"operations": map[string]any{
				"TestService.doWork": map[string]any{
					"id": "TestService.doWork",
					"input": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
						},
						"required": []string{"name"},
					},
				},
			},
		})
	})

	return httptest.NewServer(mux)
}
