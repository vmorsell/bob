package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAuth(t *testing.T) {
	const token = "secret-token-123"

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := requireAuth(token, inner)

	tests := []struct {
		name       string
		authHeader string
		queryToken string
		wantStatus int
	}{
		{"valid Bearer header", "Bearer secret-token-123", "", http.StatusOK},
		{"invalid Bearer header", "Bearer wrong-token", "", http.StatusUnauthorized},
		{"no header valid query", "", "secret-token-123", http.StatusOK},
		{"no header invalid query", "", "wrong-token", http.StatusUnauthorized},
		{"no auth at all", "", "", http.StatusUnauthorized},
		{"malformed header falls through", "Token secret-token-123", "", http.StatusUnauthorized},
		{"both present header valid", "Bearer secret-token-123", "wrong-token", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/test"
			if tt.queryToken != "" {
				url += "?token=" + tt.queryToken
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
