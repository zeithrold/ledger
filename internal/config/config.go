// Package config loads process and local development settings.
package config

import (
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"

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
}

// Clerk holds session verification settings and a reserved client publishable key.
type Clerk struct {
	SecretKey         string
	PublishableKey    string
	APIEndpoint       string
	IssuerURL         string
	AuthorizedParties []string
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
