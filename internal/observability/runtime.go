package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	sentryslog "github.com/getsentry/sentry-go/slog"
	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/config"
)

// Runtime owns one command's logger and optional Sentry client.
type Runtime struct {
	Logger *slog.Logger
	local  *slog.Logger
	hub    *sentry.Hub
}

// New enables Sentry only for an explicit DSN. A transport can be injected for offline tests.
func New(ctx context.Context, cfg config.Config, service string, output io.Writer, transport sentry.Transport) (*Runtime, error) {
	if cfg.Environment != "stage" && cfg.Environment != "production" {
		return nil, errors.New("APP_ENV must be stage or production")
	}
	secrets := []string{cfg.SentryDSN, cfg.DatabaseURL, cfg.Clerk.SecretKey, cfg.LLM.APIKey, cfg.S3.AccessKeyID, cfg.S3.SecretAccessKey, cfg.S3.SessionToken}
	destinations := []slog.Handler{slog.NewJSONHandler(output, &slog.HandlerOptions{Level: cfg.LogLevel})}
	result := &Runtime{local: slog.New(destinations[0]).With("service", service, "environment", cfg.Environment)}
	if cfg.SentryDSN != "" {
		options := clientOptions(cfg, service)
		options.Transport = transport
		client, err := sentry.NewClient(options)
		if err != nil {
			return nil, errors.New("initialize Sentry failed; check SENTRY_DSN")
		}
		result.hub = sentry.NewHub(client, sentry.NewScope())
		// In sentry-go 0.49+, constructing the official slog integration enables logs.
		// EnableLogs and its replacement DisableLogs no longer exist in ClientOptions.
		destinations = append(destinations, sentryslog.Option{}.NewSentryHandler(sentry.SetHubOnContext(ctx, result.hub)))
	}
	result.Logger = slog.New(&handler{destinations: destinations, level: cfg.LogLevel, secrets: secrets}).With("service", service, "environment", cfg.Environment)
	return result, nil
}

func clientOptions(cfg config.Config, service string) sentry.ClientOptions {
	rate := 1.0
	if cfg.Environment == "production" {
		rate = 0.5
	}
	denyAll := func() *sentry.KeyValueCollectionBehavior {
		return &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff}
	}
	return sentry.ClientOptions{
		Dsn: cfg.SentryDSN, Environment: cfg.Environment, ServerName: service,
		EnableTracing: true, AttachStacktrace: true, TracesSampleRate: rate, SampleRate: 1.0,
		// Apply the local environment policy even when an upstream trace was sampled differently.
		TracesSampler:          func(sentry.SamplingContext) float64 { return rate },
		TraceIgnoreStatusCodes: [][]int{},
		HTTPClient:             &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		DataCollection:         &sentry.DataCollection{UserInfo: sentry.Set(false), HTTPBodies: []sentry.BodyType{}, Cookies: denyAll(), QueryParams: denyAll(), HTTPHeaders: &sentry.HeaderCollectionConfig{Request: denyAll(), Response: denyAll()}},
		BeforeSend:             sanitizeEvent, BeforeSendTransaction: sanitizeEvent,
	}
}

func sanitizeEvent(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
	event.Request = nil
	event.User = sentry.User{ID: event.User.ID}
	if hint != nil && hint.RecoveredException != nil {
		event.Message = "Unhandled panic"
		for i := range event.Exception {
			event.Exception[i].Value = "Unhandled panic"
		}
	}
	return event
}

// Install routes application, standard-library and Gin framework logs to slog.
func (r *Runtime) Install() {
	slog.SetDefault(r.Logger)
	gin.DefaultWriter = Writer(r.Logger.With("component", "gin"), slog.LevelDebug)
	gin.DefaultErrorWriter = Writer(r.Logger.With("component", "gin"), slog.LevelError)
}

// Close flushes logs, errors and transactions before closing transport workers.
func (r *Runtime) Close() {
	if r.hub == nil {
		return
	}
	if !r.hub.Flush(5 * time.Second) {
		r.local.Warn("Sentry flush timed out", "event", "sentry_flush_timeout")
	}
	r.hub.Client().Close()
}

// Run gives both binaries identical configuration, logging and flush lifecycles.
func Run(service string, fn func(context.Context, config.Config, *Runtime) error) (exitCode int) {
	ctx := context.Background()
	fallback := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", service)
	slog.SetDefault(fallback)
	cfg, err := config.Load()
	if err != nil {
		fallback.ErrorContext(ctx, "configuration failed", "error", err)
		return 1
	}
	runtime, err := New(ctx, cfg, service, os.Stdout, nil)
	if err != nil {
		fallback.ErrorContext(ctx, "observability initialization failed", "error", err)
		return 1
	}
	runtime.Install()
	defer runtime.Close()
	defer func() {
		if recovered := recover(); recovered != nil {
			if runtime.hub != nil {
				runtime.hub.RecoverWithContext(ctx, recovered)
			}
			runtime.Logger.ErrorContext(ctx, "process panicked", "event", "process_panic")
			exitCode = 1
		}
	}()
	runtime.Logger.InfoContext(ctx, "process starting", "event", "process_start", "sentry_enabled", runtime.hub != nil)
	if err = fn(ctx, cfg, runtime); err != nil {
		runtime.Logger.ErrorContext(ctx, "process failed", "event", "process_failure", "error", err)
		if runtime.hub != nil {
			runtime.hub.WithScope(func(scope *sentry.Scope) {
				scope.SetLevel(sentry.LevelError)
				runtime.hub.CaptureMessage("Process failed; inspect correlated structured logs")
			})
		}
		return 1
	}
	runtime.Logger.InfoContext(ctx, "process stopped", "event", "process_stop")
	return 0
}
