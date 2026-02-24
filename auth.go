package main

import (
	"crypto/subtle"
	"net/http"
)

// requireAuth wraps a handler with Bearer token authentication.
// Accepts the token via Authorization: Bearer <token> header or ?token=<token> query parameter.
func requireAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check Authorization header first.
		if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
			if subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		// Fallback: check query parameter (for browser navigation and EventSource).
		if q := r.URL.Query().Get("token"); q != "" {
			if subtle.ConstantTimeCompare([]byte(q), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	})
}

// requireAuthFunc is the same as requireAuth but accepts an http.HandlerFunc.
func requireAuthFunc(token string, next http.HandlerFunc) http.Handler {
	return requireAuth(token, next)
}
