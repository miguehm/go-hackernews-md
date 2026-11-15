package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

type unauthorizedResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// BearerAPIKeyAuth returns middleware that requires Authorization: Bearer <key>.
// Keys are compared in constant time (via SHA-256 digests) to avoid timing and length leaks.
// If keys is empty, all requests are rejected as unauthorized.
func BearerAPIKeyAuth(keys []string) func(http.Handler) http.Handler {
	allowed := make([][32]byte, 0, len(keys))
	for _, k := range keys {
		allowed = append(allowed, sha256.Sum256([]byte(k)))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok || !tokenMatchesAny(token, allowed) {
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

func tokenMatchesAny(token string, allowed [][32]byte) bool {
	if len(allowed) == 0 {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	match := 0
	for _, a := range allowed {
		if subtle.ConstantTimeCompare(sum[:], a[:]) == 1 {
			match = 1
		}
	}
	return match == 1
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(unauthorizedResponse{
		Error:   "unauthorized",
		Message: "missing or invalid API key",
	})
}
