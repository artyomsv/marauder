package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		fallback string
		want     string
	}{
		{"x-forwarded-for first hop", map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.1"}, "10.0.0.1:5000", "203.0.113.7"},
		{"x-real-ip", map[string]string{"X-Real-IP": "203.0.113.8"}, "10.0.0.1:5000", "203.0.113.8"},
		{"true-client-ip wins", map[string]string{"True-Client-IP": "203.0.113.9", "X-Real-IP": "198.51.100.1"}, "10.0.0.1:5000", "203.0.113.9"},
		{"no headers keeps remoteaddr", nil, "10.0.0.1:5000", "10.0.0.1:5000"},
		{"garbage header ignored", map[string]string{"X-Real-IP": "not-an-ip"}, "10.0.0.1:5000", "10.0.0.1:5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.fallback
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tt.want {
				t.Errorf("RemoteAddr = %q, want %q", got, tt.want)
			}
		})
	}
}
