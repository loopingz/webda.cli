package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// newMockWebdaServer returns an httptest.Server that handles the Webda API endpoints.
func newMockWebdaServer() *httptest.Server {
	mux := http.NewServeMux()

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
