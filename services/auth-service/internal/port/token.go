package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type TokenIssuer interface {
	IssueAccess(userID uuid.UUID) (string, error)
}

type RefreshRepo interface {
	Save(ctx context.Context, userID uuid.UUID, token string, ttl time.Duration) error
}
