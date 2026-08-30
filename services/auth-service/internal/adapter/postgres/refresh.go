package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/port"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RefreshRepo struct {
	pool *pgxpool.Pool
}

func NewRefreshRepo(pool *pgxpool.Pool) *RefreshRepo {
	return &RefreshRepo{pool: pool}
}

var _ port.RefreshRepo = (*RefreshRepo)(nil)

func (r *RefreshRepo) Save(ctx context.Context, userID uuid.UUID, token string, ttl time.Duration) error {
	sum := sha256.Sum256([]byte(token))
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)
	`, hex.EncodeToString(sum[:]), userID, time.Now().UTC().Add(ttl))
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}
