package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnvironmentOverridesLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.env")
	if err := os.WriteFile(path, []byte("HTTP_ADDR=127.0.0.1:9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("HTTP_ADDR", "127.0.0.1:9001")
	t.Setenv("GIN_MODE", "test")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9001" {
		t.Fatal("local file overrode process environment")
	}
}

func TestMissingLocalFileIsAllowed(t *testing.T) {
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("GIN_MODE", "test")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestClerkConfiguration(t *testing.T) {
	valid := Clerk{SecretKey: "test-only", APIEndpoint: "https://api.clerk.com", IssuerURL: "https://clerk.example.com", AuthorizedParties: []string{"http://localhost:3000", "https://app.example.com"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Clerk){
		func(c *Clerk) { c.SecretKey = "" },
		func(c *Clerk) { c.IssuerURL = "http://evil.com" },
		func(c *Clerk) { c.APIEndpoint = "https://user:secret@example.com" },
		func(c *Clerk) { c.IssuerURL = "https://clerk.example.com?token=secret" },
		func(c *Clerk) { c.AuthorizedParties = []string{"http://remote.example.com"} },
	} {
		cfg := valid
		change(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestObservabilityConfiguration(t *testing.T) {
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("APP_ENV", "production")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("SENTRY_DSN", "https://public@example.invalid/1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "production" || cfg.LogLevel.String() != "WARN" || cfg.SentryDSN == "" {
		t.Fatal("observability settings not loaded")
	}
	t.Setenv("APP_ENV", "invalid")
	if _, err = Load(); err == nil {
		t.Fatal("invalid environment accepted")
	}
	t.Setenv("APP_ENV", "stage")
	t.Setenv("LOG_LEVEL", "verbose")
	if _, err = Load(); err == nil {
		t.Fatal("invalid level accepted")
	}
}

func TestRatesConfiguration(t *testing.T) {
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("GIN_MODE", "test")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rates.Endpoint != "https://api.frankfurter.dev" || len(cfg.Rates.Providers) != 0 || cfg.Rates.RetentionDays != 30 || cfg.Rates.Timeout.String() != "5s" {
		t.Fatalf("unexpected rate defaults: %+v", cfg.Rates)
	}
	if err = cfg.Rates.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRANKFURTER_ENDPOINT", "http://127.0.0.1:9999")
	t.Setenv("FX_PROVIDERS", "ecb, boe")
	t.Setenv("FX_RETENTION_DAYS", "45")
	t.Setenv("FX_HTTP_TIMEOUT", "2500ms")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rates.Endpoint != "http://127.0.0.1:9999" || len(cfg.Rates.Providers) != 2 || cfg.Rates.RetentionDays != 45 || cfg.Rates.Timeout.String() != "2.5s" {
		t.Fatalf("unexpected overrides: %+v", cfg.Rates)
	}
	if err = cfg.Rates.Validate(); err != nil {
		t.Fatal(err)
	}
	// Unparsable numeric settings never fail the API command; the worker rejects them.
	t.Setenv("FX_RETENTION_DAYS", "many")
	t.Setenv("FX_HTTP_TIMEOUT", "soon")
	cfg, err = Load()
	if err != nil {
		t.Fatal("configuration load failed for a worker-only setting")
	}
	if err = cfg.Rates.Validate(); err == nil {
		t.Fatal("invalid worker settings accepted")
	}
}

func TestRatesValidation(t *testing.T) {
	valid := Rates{Endpoint: "https://api.frankfurter.dev", Providers: []string{"ecb", "BOE-1"}, RetentionDays: 30, Timeout: 5 * time.Second}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Rates){
		func(r *Rates) { r.Endpoint = "ftp://api.frankfurter.dev" },
		func(r *Rates) { r.Endpoint = "http://remote.example.com" },
		func(r *Rates) { r.Endpoint = "https://user:secret@api.frankfurter.dev" },
		func(r *Rates) { r.Endpoint = "https://api.frankfurter.dev/v2" },
		func(r *Rates) { r.Providers = []string{"bad key"} },
		func(r *Rates) { r.RetentionDays = 0 },
		func(r *Rates) { r.RetentionDays = 366 },
		func(r *Rates) { r.Timeout = 0 },
		func(r *Rates) { r.Timeout = 2 * time.Minute },
	} {
		cfg := valid
		change(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("invalid rate settings accepted")
		}
	}
}

func TestDocsEnvironmentDefaults(t *testing.T) {
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "missing.env"))
	for _, tc := range []struct {
		env, override string
		want          bool
		invalid       bool
	}{
		{"stage", "", true, false}, {"production", "", false, false}, {"stage", "false", false, false}, {"production", "true", true, false}, {"stage", "invalid", false, true},
	} {
		t.Setenv("APP_ENV", tc.env)
		t.Setenv("DOCS_ENABLED", tc.override)
		cfg, err := Load()
		if tc.invalid {
			if err == nil {
				t.Fatal("invalid docs flag accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DocsEnabled != tc.want {
			t.Fatal("unexpected docs default")
		}
	}
}
