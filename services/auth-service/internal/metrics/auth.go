package metrics

import (
	"context"
	"errors"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type AuthMetrics struct {
	registerAttempts metric.Int64Counter
	loginAttempts    metric.Int64Counter
	logoutAttempts   metric.Int64Counter
	refreshAttempts  metric.Int64Counter
	usersRegistered  metric.Int64Counter
	sessionsActive   metric.Int64UpDownCounter
}

func New(meter metric.Meter) (*AuthMetrics, error) {
	if meter == nil {
		return nil, nil
	}

	m := &AuthMetrics{}
	var err error

	if m.registerAttempts, err = meter.Int64Counter("auth.register.attempts",
		metric.WithDescription("Registration attempts"),
	); err != nil {
		return nil, err
	}
	if m.loginAttempts, err = meter.Int64Counter("auth.login.attempts",
		metric.WithDescription("Login attempts"),
	); err != nil {
		return nil, err
	}
	if m.logoutAttempts, err = meter.Int64Counter("auth.logout.attempts",
		metric.WithDescription("Logout attempts"),
	); err != nil {
		return nil, err
	}
	if m.refreshAttempts, err = meter.Int64Counter("auth.refresh.attempts",
		metric.WithDescription("Refresh token attempts"),
	); err != nil {
		return nil, err
	}
	if m.usersRegistered, err = meter.Int64Counter("auth.users.registered",
		metric.WithDescription("Successfully registered users"),
	); err != nil {
		return nil, err
	}
	if m.sessionsActive, err = meter.Int64UpDownCounter("auth.sessions.active",
		metric.WithDescription("Active refresh sessions"),
	); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *AuthMetrics) RegisterAttempt(ctx context.Context, err error) {
	if m == nil {
		return
	}
	m.addAttempt(ctx, m.registerAttempts, err)
}

func (m *AuthMetrics) LoginAttempt(ctx context.Context, err error) {
	if m == nil {
		return
	}
	m.addAttempt(ctx, m.loginAttempts, err)
}

func (m *AuthMetrics) LogoutAttempt(ctx context.Context, err error) {
	if m == nil {
		return
	}
	m.addAttempt(ctx, m.logoutAttempts, err)
}

func (m *AuthMetrics) RefreshAttempt(ctx context.Context, err error) {
	if m == nil {
		return
	}
	m.addAttempt(ctx, m.refreshAttempts, err)
}

func (m *AuthMetrics) UserRegistered(ctx context.Context) {
	if m == nil {
		return
	}
	m.usersRegistered.Add(ctx, 1)
}

func (m *AuthMetrics) SessionOpened(ctx context.Context) {
	if m == nil {
		return
	}
	m.sessionsActive.Add(ctx, 1)
}

func (m *AuthMetrics) SessionClosed(ctx context.Context) {
	if m == nil {
		return
	}
	m.sessionsActive.Add(ctx, -1)
}

func (m *AuthMetrics) addAttempt(ctx context.Context, c metric.Int64Counter, err error) {
	if m == nil || c == nil {
		return
	}
	c.Add(ctx, 1, metric.WithAttributes(classify(err)...))
}

func classify(err error) []attribute.KeyValue {
	if err == nil {
		return []attribute.KeyValue{
			attribute.String("result", "success"),
			attribute.String("reason", "ok"),
		}
	}

	reason := "internal"
	switch {
	case errors.Is(err, domain.ErrEmailTaken):
		reason = "email_taken"
	case errors.Is(err, domain.ErrUsernameTaken):
		reason = "username_taken"
	case errors.Is(err, domain.ErrInvalidCredentials):
		reason = "invalid_credentials"
	case errors.Is(err, domain.ErrInactive):
		reason = "inactive"
	case errors.Is(err, domain.ErrNotFound):
		reason = "not_found"
	case errors.Is(err, domain.ErrInvalidInput),
		errors.Is(err, domain.ErrInvalidEmail),
		errors.Is(err, domain.ErrInvalidUsername),
		errors.Is(err, domain.ErrWeakPassword):
		reason = "invalid_input"
	}

	return []attribute.KeyValue{
		attribute.String("result", "failure"),
		attribute.String("reason", reason),
	}
}
