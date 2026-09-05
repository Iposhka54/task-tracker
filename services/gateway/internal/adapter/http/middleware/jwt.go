package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
)

const (
	HealthPath        = "/health"
	UserIDMetadataKey = "user-id"

	authAPIPrefix       = "/api/v1/auth"
	authorizationHeader = "Authorization"
	xForwardedForHeader = "X-Forwarded-For"
	xTokenError         = "X-Token-Error"
	bearerHeader        = "Bearer "

	missRequiredClaimKey = "miss_required_claim"
	tokenExpiredKey      = "token_expired"
	invalidSignatureKey  = "invalid_signature"
	invalidTokenKey      = "invalid_token"
)

type ctxKey int

const (
	userIDKey ctxKey = iota
	metadataKey
)

func UserIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

func UserMetadataFromContext(ctx context.Context) metadata.MD {
	if md, ok := ctx.Value(metadataKey).(metadata.MD); ok {
		return md
	}
	return nil
}

func JWT(secret string, next http.Handler) http.Handler {
	key := []byte(secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipJWT(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		raw, ok := bearerToken(r.Header.Get(authorizationHeader))
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		claims, err := parseAccessToken(key, raw)
		if err != nil {
			writeError(w, err)
			return
		}

		userID := claims.Subject
		if userID == "" {
			w.Header().Set(xTokenError, missRequiredClaimKey)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		md := metadata.Pairs(UserIDMetadataKey, userID)
		ctx = context.WithValue(ctx, metadataKey, md)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func skipJWT(path string) bool {
	return path == HealthPath || strings.HasPrefix(path, authAPIPrefix)
}

func bearerToken(header string) (string, bool) {
	if len(header) < len(bearerHeader) || !strings.EqualFold(header[:len(bearerHeader)], bearerHeader) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearerHeader):])
	return token, token != ""
}

func parseAccessToken(secret []byte, raw string) (*jwt.RegisteredClaims, error) {
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, errors.New("invalid token")
	}

	exp, err := claims.GetExpirationTime()
	if err != nil {
		return nil, err
	}
	if exp == nil {
		return nil, errors.New("token has no expiration time")
	}
	if time.Now().After(exp.Time) {
		return nil, jwt.ErrTokenExpired
	}

	return claims, nil
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		w.Header().Set(xTokenError, tokenExpiredKey)
		http.Error(w, "Token expired", http.StatusUnauthorized)

	case errors.Is(err, jwt.ErrSignatureInvalid):
		w.Header().Set(xTokenError, invalidSignatureKey)
		http.Error(w, "Invalid token signature", http.StatusUnauthorized)

	default:
		w.Header().Set(xTokenError, invalidTokenKey)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
}
