package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/Iposhka54/task-tracker/services/gateway/internal/limiter"
)

func Limiter(auth, api *limiter.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == HealthPath {
			next.ServeHTTP(w, r)
			return
		}

		crl := api
		if strings.HasPrefix(r.URL.Path, authAPIPrefix) {
			crl = auth
		}

		ok, err := crl.Allow(r.Context(), clientIP(r))
		if err != nil {
			slog.ErrorContext(r.Context(), "rate limit", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		if !ok {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get(xForwardedForHeader); forwarded != "" {
		if i := strings.IndexByte(forwarded, ','); i >= 0 {
			forwarded = forwarded[:i]
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
