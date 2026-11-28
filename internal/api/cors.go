package api

import (
	"net/http"
	"strings"
)

// DefaultCORSOrigins is the allowlist used when CORS_ORIGINS is not set.
var DefaultCORSOrigins = []string{
	"web",
	"http://localhost:5173",
	"https://localhost",
	"capacitor://localhost",
}

const (
	corsAllowMethods  = "GET, HEAD, POST, PATCH, DELETE, OPTIONS"
	corsAllowHeaders  = "Authorization, Content-Type, If-Match, If-None-Match, X-Request-ID"
	corsExposeHeaders = "ETag, Retry-After, Location, X-Request-ID"
	corsMaxAge        = "600"
)

// CORS wraps a handler with a strict origin allowlist. OPTIONS requests are
// answered before the wrapped handler runs, so preflights never reach auth.
// The wildcard is deliberately ignored: this is an allowlist, not a policy.
func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin != "" && origin != "*" {
			allowed[origin] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addVary(w, "Origin")
			origin := r.Header.Get("Origin")
			_, permitted := allowed[origin]
			if permitted {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Expose-Headers", corsExposeHeaders)
			}
			if r.Method == http.MethodOptions {
				if permitted {
					addVary(w, "Access-Control-Request-Method")
					addVary(w, "Access-Control-Request-Headers")
					w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
					w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
					w.Header().Set("Access-Control-Max-Age", corsMaxAge)
				}
				w.Header().Set("Allow", corsAllowMethods)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
