package metric

import (
	"testing"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/domain"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	got := classify(nil)
	if got[0].Value.AsString() != "success" || got[1].Value.AsString() != "ok" {
		t.Fatalf("success: %v", got)
	}

	got = classify(domain.ErrInvalidCredentials)
	if got[0].Value.AsString() != "failure" || got[1].Value.AsString() != "invalid_credentials" {
		t.Fatalf("failure: %v", got)
	}
}
