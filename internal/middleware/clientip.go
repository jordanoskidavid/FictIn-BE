package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP sets r.RemoteAddr to the client's address, used for logging and
// for rate-limiting unauthenticated requests.
//
// By default (trustProxy=false) the socket address is kept and forwarding
// headers are ignored, because any client can send them. Set trustProxy only
// when the server runs behind exactly one reverse proxy that appends the
// connecting address to X-Forwarded-For (e.g. nginx proxy_add_x_forwarded_for).
// The rightmost entry is then the one the proxy wrote; entries to its left
// come from the client and are not trusted.
//
// This replaces chi's RealIP, which trusted X-Forwarded-For/X-Real-IP/
// True-Client-IP from anyone (IP spoofing, GHSA-3fxj-6jh8-hvhx).
func ClientIP(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !trustProxy {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip := rightmostForwarded(r.Header.Values("X-Forwarded-For")); ip != "" {
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

func rightmostForwarded(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := strings.Split(values[len(values)-1], ",")
	candidate := strings.TrimSpace(parts[len(parts)-1])
	if net.ParseIP(candidate) == nil {
		return ""
	}
	return candidate
}
