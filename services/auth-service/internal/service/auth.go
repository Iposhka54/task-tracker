package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/metric"
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
	metrics     *metric.AuthMetrics
}

func NewAuth(
	users port.UserRepo,
	hasher port.Hasher,
	tokens port.TokenIssuer,
	refreshRepo port.RefreshRepo,
	cache port.Cache,
	refreshTTL time.Duration,
	metrics *metric.AuthMetrics,
) *Auth {
	return &Auth{
		users:       users,
		hasher:      hasher,
		tokens:      tokens,
		refreshRepo: refreshRepo,
		cache:       cache,
		refreshTTL:  refreshTTL,
		metrics:     metrics,
	}
}

var _ port.Auth = (*Auth)(nil)

func (a *Auth) Register(ctx context.Context, cmd port.RegisterRequest) (port.Session, error) {
	sess, err := a.register(ctx, cmd)
	a.metrics.RegisterAttempt(ctx, err)
	return sess, err
}

func (a *Auth) register(ctx context.Context, cmd port.RegisterRequest) (port.Session, error) {
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
		slog.WarnContext(ctx, "register rejected", "email", email, "error", err)
		return port.Session{}, err
	}
	if err = a.ensureUsernameFree(ctx, username); err != nil {
		slog.WarnContext(ctx, "register rejected", "username", username, "error", err)
		return port.Session{}, err
	}

	hash, err := a.hasher.Hash(cmd.Password)
	if err != nil {
		slog.ErrorContext(ctx, "hash password", "error", err)
		return port.Session{}, err
	}

	user, err := domain.NewUser(email, username, hash)
	if err != nil {
		return port.Session{}, err
	}
	if err = a.users.Save(ctx, user); err != nil {
		slog.ErrorContext(ctx, "save user", "email", email, "error", err)
		return port.Session{}, err
	}

	sess, err := a.issueSession(ctx, user)
	if err != nil {
		return port.Session{}, err
	}
	a.metrics.UserRegistered(ctx)
	a.metrics.SessionOpened(ctx)
	slog.InfoContext(ctx, "user registered", "user_id", user.ID.String())
	return sess, nil
}

func (a *Auth) Login(ctx context.Context, cmd port.LoginRequest) (port.Session, error) {
	sess, err := a.login(ctx, cmd)
	a.metrics.LoginAttempt(ctx, err)
	return sess, err
}

func (a *Auth) login(ctx context.Context, cmd port.LoginRequest) (port.Session, error) {
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
			slog.WarnContext(ctx, "login failed", "email", email, "error", domain.ErrInvalidCredentials)
			return port.Session{}, domain.ErrInvalidCredentials
		}
		slog.ErrorContext(ctx, "find user by email", "error", err)
		return port.Session{}, err
	}
	if !user.IsActive {
		slog.WarnContext(ctx, "login rejected: inactive user", "user_id", user.ID.String())
		return port.Session{}, domain.ErrInactive
	}
	if err = a.hasher.Compare(user.PasswordHash, cmd.Password); err != nil {
		slog.WarnContext(ctx, "login failed", "user_id", user.ID.String(), "error", domain.ErrInvalidCredentials)
		return port.Session{}, domain.ErrInvalidCredentials
	}

	sess, err := a.issueSession(ctx, user)
	if err != nil {
		return port.Session{}, err
	}
	a.metrics.SessionOpened(ctx)
	slog.InfoContext(ctx, "user logged in", "user_id", user.ID.String())
	return sess, nil
}

func (a *Auth) Logout(ctx context.Context, cmd port.LogoutRequest) error {
	err := a.logout(ctx, cmd)
	a.metrics.LogoutAttempt(ctx, err)
	return err
}

func (a *Auth) logout(ctx context.Context, cmd port.LogoutRequest) error {
	if cmd.RefreshToken == "" {
		return domain.ErrInvalidInput
	}

	userId, err := a.refreshRepo.Consume(ctx, cmd.RefreshToken)
	if err != nil {
		slog.ErrorContext(ctx, "delete refresh token", "error", err)
		return err
	}
	a.metrics.SessionClosed(ctx)

	if err = a.cache.Del(ctx, refreshKey(cmd.RefreshToken)); err != nil {
		if errors.Is(err, port.ErrCacheMiss) {
			slog.InfoContext(ctx, "user logged out")
			return nil
		}
		slog.ErrorContext(ctx, "delete refresh cache", "error", err)
		return err
	}

	slog.InfoContext(ctx, "user logged out", "user_id", userId.String())
	return nil
}

func (a *Auth) RefreshToken(ctx context.Context, cmd port.RefreshTokenRequest) (port.Session, error) {
	sess, err := a.refreshToken(ctx, cmd)
	a.metrics.RefreshAttempt(ctx, err)
	return sess, err
}

func (a *Auth) refreshToken(ctx context.Context, cmd port.RefreshTokenRequest) (port.Session, error) {
	if cmd.RefreshToken == "" {
		return port.Session{}, domain.ErrInvalidInput
	}

	userID, err := a.refreshRepo.Consume(ctx, cmd.RefreshToken)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_ = a.cache.Del(ctx, refreshKey(cmd.RefreshToken))
			slog.WarnContext(ctx, "refresh rejected", "error", domain.ErrInvalidCredentials)
			return port.Session{}, domain.ErrInvalidCredentials
		}
		slog.ErrorContext(ctx, "consume refresh token", "error", err)
		return port.Session{}, err
	}

	_ = a.cache.Del(ctx, refreshKey(cmd.RefreshToken))

	user, err := a.users.FindByID(ctx, userID)
	if err != nil {
		a.metrics.SessionClosed(ctx)
		if errors.Is(err, domain.ErrNotFound) {
			slog.WarnContext(ctx, "refresh rejected: user missing", "user_id", userID.String())
			return port.Session{}, domain.ErrInvalidCredentials
		}
		slog.ErrorContext(ctx, "find user by id", "user_id", userID.String(), "error", err)
		return port.Session{}, err
	}
	if !user.IsActive {
		a.metrics.SessionClosed(ctx)
		slog.WarnContext(ctx, "refresh rejected: inactive user", "user_id", user.ID.String())
		return port.Session{}, domain.ErrInactive
	}

	sess, err := a.issueSession(ctx, user)
	if err != nil {
		a.metrics.SessionClosed(ctx)
		return port.Session{}, err
	}
	slog.InfoContext(ctx, "tokens rotated", "user_id", user.ID.String())
	return sess, nil
}

func (a *Auth) issueSession(ctx context.Context, user domain.User) (port.Session, error) {
	accessToken, err := a.tokens.IssueAccess(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "issue access token", "user_id", user.ID.String(), "error", err)
		return port.Session{}, err
	}

	refreshToken := uuid.NewString()
	if err = a.refreshRepo.Save(ctx, user.ID, refreshToken, a.refreshTTL); err != nil {
		slog.ErrorContext(ctx, "save refresh token", "user_id", user.ID.String(), "error", err)
		return port.Session{}, err
	}
	if err = a.cache.Set(ctx, refreshKey(refreshToken), user.ID.String(), a.refreshTTL); err != nil {
		slog.ErrorContext(ctx, "cache refresh token", "user_id", user.ID.String(), "error", err)
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
