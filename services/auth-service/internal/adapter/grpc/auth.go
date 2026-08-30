package grpcadapter

import (
	"context"

	authpb "github.com/Iposhka54/task-tracker/pkg/api/auth"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/port"
)

type AuthServer struct {
	authpb.UnimplementedAuthServiceServer
	auth port.Auth
}

func New(auth port.Auth) *AuthServer {
	return &AuthServer{auth: auth}
}

func (s *AuthServer) Register(ctx context.Context, req *authpb.RegisterRequest) (*authpb.RegisterResponse, error) {
	result, err := s.auth.Register(ctx, port.RegisterRequest{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
		Username: req.GetUsername(),
	})
	if err != nil {
		return nil, toStatus(err)
	}

	return &authpb.RegisterResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		User:         userToProto(result.User),
	}, nil
}

func (s *AuthServer) Login(ctx context.Context, req *authpb.LoginRequest) (*authpb.LoginResponse, error) {
	result, err := s.auth.Login(ctx, port.LoginRequest{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		return nil, toStatus(err)
	}

	return &authpb.LoginResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		User:         userToProto(result.User),
	}, nil
}
