package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/port"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/validate"
	"github.com/google/uuid"
)

type Auth struct {
	users       port.UserRepo
	hasher      port.Hasher
	tokens      port.TokenIssuer
	refreshRepo port.RefreshRepo
	cache       port.Cache
	refreshTTL  time.Duration
}

func NewAuth(
	users port.UserRepo,
	hasher port.Hasher,
	tokens port.TokenIssuer,
	refreshRepo port.RefreshRepo,
	cache port.Cache,
	refreshTTL time.Duration,
) *Auth {
	return &Auth{
		users:       users,
		hasher:      hasher,
		tokens:      tokens,
		refreshRepo: refreshRepo,
		cache:       cache,
		refreshTTL:  refreshTTL,
	}
}

var _ port.Auth = (*Auth)(nil)

func (a *Auth) Register(ctx context.Context, cmd port.RegisterRequest) (port.Session, error) {
	email, err := validate.Email(cmd.Email)
	if err != nil {
		return port.Session{}, err
	}
	username, err := validate.Username(cmd.Username)
	if err != nil {
		return port.Session{}, err
	}
	if err = validate.Password(cmd.Password); err != nil {
		return port.Session{}, err
	}

	if err = a.ensureEmailFree(ctx, email); err != nil {
		return port.Session{}, err
	}
	if err = a.ensureUsernameFree(ctx, username); err != nil {
		return port.Session{}, err
	}

	hash, err := a.hasher.Hash(cmd.Password)
	if err != nil {
		return port.Session{}, err
	}

	user, err := domain.NewUser(email, username, hash)
	if err != nil {
		return port.Session{}, err
	}
	if err = a.users.Save(ctx, user); err != nil {
		return port.Session{}, err
	}

	return a.issueSession(ctx, user)
}

func (a *Auth) Login(ctx context.Context, cmd port.LoginRequest) (port.Session, error) {
	email, err := validate.Email(cmd.Email)
	if err != nil {
		return port.Session{}, err
	}
	if cmd.Password == "" {
		return port.Session{}, domain.ErrInvalidCredentials
	}

	user, err := a.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return port.Session{}, domain.ErrInvalidCredentials
		}
		return port.Session{}, err
	}
	if !user.IsActive {
		return port.Session{}, domain.ErrInactive
	}
	if err = a.hasher.Compare(user.PasswordHash, cmd.Password); err != nil {
		return port.Session{}, domain.ErrInvalidCredentials
	}

	return a.issueSession(ctx, user)
}

func (a *Auth) Logout(ctx context.Context, cmd port.LogoutRequest) error {
	if cmd.RefreshToken == "" {
		return domain.ErrInvalidInput
	}

	if err := a.refreshRepo.Delete(ctx, cmd.RefreshToken); err != nil {
		return err
	}

	if err := a.cache.Del(ctx, refreshKey(cmd.RefreshToken)); err != nil {
		if errors.Is(err, port.ErrCacheMiss) {
			return nil
		}
		return err
	}

	return nil
}

func (a *Auth) RefreshToken(ctx context.Context, cmd port.RefreshTokenRequest) (port.Session, error) {
	userID, err := a.refreshRepo.Consume(ctx, cmd.RefreshToken)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_ = a.cache.Del(ctx, refreshKey(cmd.RefreshToken))
			return port.Session{}, domain.ErrInvalidCredentials
		}
		return port.Session{}, err
	}

	_ = a.cache.Del(ctx, refreshKey(cmd.RefreshToken))

	user, err := a.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return port.Session{}, domain.ErrInvalidCredentials
		}
		return port.Session{}, err
	}
	if !user.IsActive {
		return port.Session{}, domain.ErrInactive
	}

	return a.issueSession(ctx, user)
}

func (a *Auth) issueSession(ctx context.Context, user domain.User) (port.Session, error) {
	accessToken, err := a.tokens.IssueAccess(user.ID)
	if err != nil {
		return port.Session{}, err
	}

	refreshToken := uuid.NewString()
	if err = a.refreshRepo.Save(ctx, user.ID, refreshToken, a.refreshTTL); err != nil {
		return port.Session{}, err
	}
	if err = a.cache.Set(ctx, refreshKey(refreshToken), user.ID.String(), a.refreshTTL); err != nil {
		return port.Session{}, err
	}

	return port.Session{
		User:         user,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (a *Auth) ensureEmailFree(ctx context.Context, email string) error {
	taken, err := a.users.ExistsByEmail(ctx, email)
	if err != nil {
		return err
	}
	if taken {
		return domain.ErrEmailTaken
	}
	return nil
}

func (a *Auth) ensureUsernameFree(ctx context.Context, username string) error {
	taken, err := a.users.ExistsByUsername(ctx, username)
	if err != nil {
		return err
	}
	if taken {
		return domain.ErrUsernameTaken
	}
	return nil
}

func refreshKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "auth:refresh:" + hex.EncodeToString(sum[:])
}
