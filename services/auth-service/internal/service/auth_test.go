package service

import (
	"context"
	"testing"
	"time"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/port"
	"github.com/google/uuid"
)

type fakeRepo struct {
	byEmail    map[string]domain.User
	byUsername map[string]domain.User
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byEmail:    map[string]domain.User{},
		byUsername: map[string]domain.User{},
	}
}

func (r *fakeRepo) Save(_ context.Context, u domain.User) error {
	r.byEmail[u.Email] = u
	r.byUsername[u.Username] = u
	return nil
}

func (r *fakeRepo) FindByEmail(_ context.Context, email string) (domain.User, error) {
	u, ok := r.byEmail[email]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (r *fakeRepo) ExistsByEmail(_ context.Context, email string) (bool, error) {
	_, ok := r.byEmail[email]
	return ok, nil
}

func (r *fakeRepo) ExistsByUsername(_ context.Context, username string) (bool, error) {
	_, ok := r.byUsername[username]
	return ok, nil
}

type fakeHasher struct{}

func (fakeHasher) Hash(password string) (string, error) {
	return "hash:" + password, nil
}

func (fakeHasher) Compare(hash, password string) error {
	if hash != "hash:"+password {
		return domain.ErrInvalidCredentials
	}
	return nil
}

type fakeIssuer struct{}

func (fakeIssuer) IssueAccess(userID uuid.UUID) (string, error) {
	return "access:" + userID.String(), nil
}

type fakeRefreshRepo struct {
	last string
}

func (s *fakeRefreshRepo) Save(_ context.Context, _ uuid.UUID, token string, _ time.Duration) error {
	s.last = token
	return nil
}

type fakeCache struct {
	data map[string]string
}

func newFakeCache() *fakeCache {
	return &fakeCache{data: map[string]string{}}
}

func (c *fakeCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.data[key] = value
	return nil
}

func (c *fakeCache) Get(_ context.Context, key string) (string, error) {
	v, ok := c.data[key]
	if !ok {
		return "", port.ErrCacheMiss
	}
	return v, nil
}

func (c *fakeCache) Del(_ context.Context, key string) error {
	delete(c.data, key)
	return nil
}

func TestAuth_Register_ReturnsTokens(t *testing.T) {
	t.Parallel()

	db := &fakeRefreshRepo{}
	cache := newFakeCache()
	auth := NewAuth(newFakeRepo(), fakeHasher{}, fakeIssuer{}, db, cache, time.Hour)

	got, err := auth.Register(context.Background(), port.RegisterRequest{
		Email:    "ada@example.com",
		Password: "secret123",
		Username: "ada",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got.AccessToken == "" || got.RefreshToken == "" {
		t.Fatalf("tokens: access=%q refreshStore=%q", got.AccessToken, got.RefreshToken)
	}
	if got.AccessToken != "access:"+got.User.ID.String() {
		t.Fatalf("access token: %s", got.AccessToken)
	}
	if got.RefreshToken != db.last {
		t.Fatalf("refresh not stored in db")
	}
	if cache.data[refreshKey(got.RefreshToken)] != got.User.ID.String() {
		t.Fatalf("refresh not stored in cache")
	}
}

func TestAuth_Login(t *testing.T) {
	t.Parallel()

	users := newFakeRepo()
	db := &fakeRefreshRepo{}
	cache := newFakeCache()
	auth := NewAuth(users, fakeHasher{}, fakeIssuer{}, db, cache, time.Hour)
	ctx := context.Background()

	reg, err := auth.Register(ctx, port.RegisterRequest{
		Email: "ada@example.com", Password: "secret123", Username: "ada",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := auth.Login(ctx, port.LoginRequest{Email: "Ada@Example.com", Password: "secret123"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got.User.ID != reg.User.ID {
		t.Fatalf("user id: %s", got.User.ID)
	}
	if got.AccessToken == "" || got.RefreshToken == "" {
		t.Fatal("empty tokens")
	}

	_, err = auth.Login(ctx, port.LoginRequest{Email: "ada@example.com", Password: "wrongpass"})
	if err != domain.ErrInvalidCredentials {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}
