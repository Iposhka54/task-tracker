package middleware

import (
	"net/http"
	"strings"

	"github.com/Iposhka54/task-tracker/services/gateway/internal/limiter"
)

const authAPIPrefix = "/api/v1/auth"

func Limiter(auth, api *limiter.CleanableRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ip = forwarded
		}

		crl := api
		switch {
		case r.URL.Path == "/health":
			next.ServeHTTP(w, r)
			return
		case strings.HasPrefix(r.URL.Path, authAPIPrefix):
			crl = auth
		}
		if !crl.GetLimiter(ip).Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
