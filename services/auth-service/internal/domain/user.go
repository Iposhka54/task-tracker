package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID           uuid.UUID
	Email        string
	Username     string
	PasswordHash string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewUser(email, username, passwordHash string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.TrimSpace(username)

	if email == "" || !strings.Contains(email, "@") {
		return User{}, ErrInvalidEmail
	}
	if len(username) < 3 || len(username) > 50 {
		return User{}, ErrInvalidUsername
	}
	if passwordHash == "" {
		return User{}, ErrInvalidInput
	}

	now := time.Now().UTC()
	return User{
		ID:           uuid.New(),
		Email:        email,
		Username:     username,
		PasswordHash: passwordHash,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrInvalidUsername    = errors.New("invalid username")
	ErrWeakPassword       = errors.New("password too short")
	ErrEmailTaken         = errors.New("email already taken")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrNotFound           = errors.New("not found")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInactive           = errors.New("user is inactive")
)
