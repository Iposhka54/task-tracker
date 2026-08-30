package port

import (
	"context"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
)

type UserRepo interface {
	Save(ctx context.Context, u domain.User) error
	FindByEmail(ctx context.Context, email string) (domain.User, error)
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	ExistsByUsername(ctx context.Context, username string) (bool, error)
}
