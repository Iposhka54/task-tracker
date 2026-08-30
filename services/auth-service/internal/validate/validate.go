package validate

import (
	"strings"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
)

func Email(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || !strings.Contains(s, "@") {
		return "", domain.ErrInvalidEmail
	}
	return s, nil
}

func Username(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 50 {
		return "", domain.ErrInvalidUsername
	}
	return s, nil
}

func Password(s string) error {
	if len(s) < 8 {
		return domain.ErrWeakPassword
	}
	return nil
}
