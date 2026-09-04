package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWT_SkipsAuthAndHealth(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := JWT("secret", next)

	for _, path := range []string{"/health", "/api/v1/auth/login", "/api/v1/auth/refresh"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: got %d", path, rec.Code)
		}
	}
}

func TestJWT_RequiresBearerForAPI(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := JWT("secret", next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestJWT_AcceptsValidToken(t *testing.T) {
	t.Parallel()

	const secret = "secret"
	raw := mustSign(t, secret, "user-1", time.Hour)

	var gotUser string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	h := JWT(secret, next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d", rec.Code)
	}
	if gotUser != "user-1" {
		t.Fatalf("user id: %q", gotUser)
	}
}

func TestJWT_RejectsExpiredAndWrongSecret(t *testing.T) {
	t.Parallel()

	h := JWT("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	expired := mustSign(t, "secret", "user-1", -time.Hour)
	foreign := mustSign(t, "other", "user-1", time.Hour)

	for _, token := range []string{expired, foreign, "not-a-jwt"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: got %d", token, rec.Code)
		}
	}
}

func mustSign(t *testing.T, secret, subject string, ttl time.Duration) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   subject,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	})
	raw, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
