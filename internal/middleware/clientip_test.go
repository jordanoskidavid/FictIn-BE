package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{"direct connection keeps the socket address", false, "203.0.113.7:51000", nil, "203.0.113.7:51000"},
		{"spoofed headers ignored by default", false, "203.0.113.7:51000",
			map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "1.2.3.4", "True-Client-IP": "1.2.3.4"}, "203.0.113.7:51000"},
		{"behind a proxy: rightmost forwarded entry", true, "10.0.0.2:40000",
			map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.9"}, "198.51.100.9"},
		{"behind a proxy: client-injected leftmost entry is not trusted", true, "10.0.0.2:40000",
			map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.9"}, "198.51.100.9"},
		{"behind a proxy: X-Real-IP alone is not trusted", true, "10.0.0.2:40000",
			map[string]string{"X-Real-IP": "6.6.6.6"}, "10.0.0.2:40000"},
		{"behind a proxy: IPv6 entry", true, "10.0.0.2:40000",
			map[string]string{"X-Forwarded-For": "2001:db8::1"}, "2001:db8::1"},
		{"behind a proxy: garbage entry falls back to the socket", true, "10.0.0.2:40000",
			map[string]string{"X-Forwarded-For": "not-an-ip"}, "10.0.0.2:40000"},
		{"behind a proxy: no header", true, "10.0.0.2:40000", nil, "10.0.0.2:40000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := ClientIP(tt.trustProxy)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			assert.Equal(t, tt.want, got)
		})
	}
}
