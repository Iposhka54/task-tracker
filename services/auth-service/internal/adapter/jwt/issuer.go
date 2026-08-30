package jwtadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func New(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) IssueAccess(ctx context.Context, userID uuid.UUID) (string, error) {
	_, span := otel.Tracer("auth-service/jwt").Start(ctx, "jwt.IssueAccess",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("user.id", userID.String())),
	)
	defer span.End()

	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.secret)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "sign access token")
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}
