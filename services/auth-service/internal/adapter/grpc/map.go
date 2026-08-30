package grpcadapter

import (
	"errors"
	"time"

	authpb "github.com/Iposhka54/task-tracker/pkg/api/auth"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func userToProto(u domain.User) *authpb.User {
	return &authpb.User{
		Id:        u.ID.String(),
		Email:     u.Email,
		Username:  u.Username,
		IsActive:  u.IsActive,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: u.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toStatus(err error) error {
	switch {
	case errors.Is(err, domain.ErrEmailTaken), errors.Is(err, domain.ErrUsernameTaken):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrInvalidEmail),
		errors.Is(err, domain.ErrInvalidUsername),
		errors.Is(err, domain.ErrWeakPassword),
		errors.Is(err, domain.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrInvalidCredentials):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, domain.ErrInactive):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}
