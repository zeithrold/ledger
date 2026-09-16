// Package config loads process and local development settings.
package config

import (
	"errors"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds application settings without initiating external connections.
type Config struct {
	DocsEnabled bool
	Environment string
	LogLevel    slog.Level
	SentryDSN   string
	HTTPAddr    string
	GinMode     string
	DatabaseURL string
	Clerk       Clerk
	LLM         LLM
	S3          S3
	Rates       Rates
}

// Clerk holds session verification settings and a reserved client publishable key.
type Clerk struct {
	SecretKey         string
	PublishableKey    string
	APIEndpoint       string
	IssuerURL         string
	AuthorizedParties []string
}

// Rates holds market-reference-rate settings for the background worker.
// An unparsable numeric setting is kept at zero so the independent API command
// is unaffected; the worker's Validate reports it.
type Rates struct {
	Endpoint      string
	Providers     []string
	RetentionDays int
	Timeout       time.Duration
}

// LLM holds reserved DeepSeek connection settings.
type LLM struct {
	Endpoint string
	APIKey   string
	Model    string
}

// S3 holds reserved screenshot storage settings.
type S3 struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Load accepts a missing local file and never overrides process environment.
func Load() (Config, error) {
	path := value("CONFIG_FILE", ".env.local")
	if err := godotenv.Load(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("load local configuration failed")
	}
	level := slog.LevelInfo
	switch value("LOG_LEVEL", "info") {
	case "debug":
		level = slog.LevelDebug
	case "info":
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return Config{}, errors.New("LOG_LEVEL must be debug, info, warn or error")
	}
	c := Config{
		Environment: value("APP_ENV", "stage"),
		LogLevel:    level,
		SentryDSN:   strings.TrimSpace(os.Getenv("SENTRY_DSN")),
		HTTPAddr:    value("HTTP_ADDR", "127.0.0.1:8080"),
		GinMode:     value("GIN_MODE", "release"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Clerk:       Clerk{SecretKey: os.Getenv("CLERK_SECRET_KEY"), PublishableKey: os.Getenv("CLERK_PUBLISHABLE_KEY"), APIEndpoint: value("CLERK_API_ENDPOINT", "https://api.clerk.com"), IssuerURL: os.Getenv("CLERK_ISSUER_URL"), AuthorizedParties: splitParties(os.Getenv("CLERK_AUTHORIZED_PARTIES"))},
		LLM:         LLM{value("LLM_ENDPOINT", "https://api.deepseek.com"), os.Getenv("LLM_API_KEY"), os.Getenv("LLM_MODEL")},
		S3:          S3{os.Getenv("S3_ENDPOINT"), os.Getenv("S3_REGION"), os.Getenv("S3_BUCKET"), os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY"), os.Getenv("S3_SESSION_TOKEN")},
		Rates:       ratesFromEnvironment(),
	}
	if c.Environment != "stage" && c.Environment != "production" {
		return Config{}, errors.New("APP_ENV must be stage or production")
	}
	c.DocsEnabled = c.Environment == "stage"
	if raw := os.Getenv("DOCS_ENABLED"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, errors.New("DOCS_ENABLED must be a boolean")
		}
		c.DocsEnabled = enabled
	}
	if err := validateGinMode(c.GinMode); err != nil {
		return Config{}, err
	}
	return c, nil
}

func validateGinMode(mode string) error {
	switch mode {
	case "debug", "release", "test":
		return nil
	default:
		return errors.New("GIN_MODE must be debug, release or test")
	}
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Validate checks authentication configuration without exposing its values.
// It is invoked by API startup, not by the independent migration command.
func (c Clerk) Validate() error {
	if strings.TrimSpace(c.SecretKey) == "" {
		return errors.New("CLERK_SECRET_KEY is required for the API")
	}
	for _, raw := range []string{c.APIEndpoint, c.IssuerURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("Clerk endpoint and issuer must be HTTPS origins")
		}
	}
	for _, raw := range c.AuthorizedParties {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" ||
			(u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1"))) {
			return errors.New("CLERK_AUTHORIZED_PARTIES must contain origins (HTTP allowed only on loopback)")
		}
	}
	return nil
}

func splitParties(raw string) []string {
	var result []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

func ratesFromEnvironment() Rates {
	retention := 30
	if raw := strings.TrimSpace(os.Getenv("FX_RETENTION_DAYS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			retention = parsed
		} else {
			retention = 0
		}
	}
	timeout := 5 * time.Second
	if raw := strings.TrimSpace(os.Getenv("FX_HTTP_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			timeout = parsed
		} else {
			timeout = 0
		}
	}
	return Rates{
		Endpoint:      value("FRANKFURTER_ENDPOINT", "https://api.frankfurter.dev"),
		Providers:     splitParties(os.Getenv("FX_PROVIDERS")),
		RetentionDays: retention,
		Timeout:       timeout,
	}
}

var providerKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Validate checks the worker's market-rate settings without exposing their values.
// Worker startup invokes it; the API and migration commands do not depend on it.
func (r Rates) Validate() error {
	for _, provider := range r.Providers {
		if !providerKey.MatchString(provider) {
			return errors.New("FX_PROVIDERS must contain provider keys")
		}
	}
	if r.RetentionDays < 1 || r.RetentionDays > 365 {
		return errors.New("FX_RETENTION_DAYS must be an integer between 1 and 365")
	}
	if r.Timeout <= 0 || r.Timeout > time.Minute {
		return errors.New("FX_HTTP_TIMEOUT must be a positive duration of at most 1m")
	}
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1"))) {
		return errors.New("FRANKFURTER_ENDPOINT must be an HTTPS origin (HTTP allowed only on loopback)")
	}
	return nil
}
