package port

import (
	"context"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
)

type Auth interface {
	Register(ctx context.Context, cmd RegisterRequest) (Session, error)
	Login(ctx context.Context, cmd LoginRequest) (Session, error)
	Logout(ctx context.Context, cmd LogoutRequest) error
}

type RegisterRequest struct {
	Email    string
	Password string
	Username string
}

type LoginRequest struct {
	Email    string
	Password string
}

type LogoutRequest struct {
	RefreshToken string
}

type Session struct {
	User         domain.User
	AccessToken  string
	RefreshToken string
}
