package config

import (
	"os"
	"path/filepath"
	"testing"
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
