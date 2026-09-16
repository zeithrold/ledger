package jobs

import (
	"testing"
	"time"

	"github.com/zeithrold/ledger/internal/config"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	if err := Run(t.Context(), config.Config{}, nil); err == nil {
		t.Fatal("accepted a worker without a database URL")
	}
	invalid := config.Config{DatabaseURL: "postgres://ledger@127.0.0.1:1/ledger", Rates: config.Rates{Endpoint: "ftp://example.com", RetentionDays: 30, Timeout: time.Second}}
	if err := Run(t.Context(), invalid, nil); err == nil {
		t.Fatal("accepted invalid rate settings")
	}
	// A malformed DSN fails during pool configuration and leaks no credential.
	malformed := config.Config{DatabaseURL: "not-a-dsn", Rates: config.Rates{Endpoint: "https://api.frankfurter.dev", RetentionDays: 30, Timeout: time.Second}}
	if err := Run(t.Context(), malformed, nil); err == nil {
		t.Fatal("accepted an invalid database URL")
	}
}
