package observability

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type (
	requestKey   struct{}
	actorKey     struct{}
	requestState struct {
		id, method, route string
		panicCaptured     bool
	}
	actorInfo struct{ userID, tenantID string }
)

// Requests emits one access event per request, including rejected versions and panics.
func Requests() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		state := &requestState{id: uuid.NewString(), method: c.Request.Method, route: route}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), requestKey{}, state))
		c.Header("X-Request-ID", state.id)
		defer func() {
			status := c.Writer.Status()
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			} else if status >= 400 {
				level = slog.LevelWarn
			}
			slog.Log(c.Request.Context(), level, "HTTP request completed", "event", "http_request", "status", status, "duration_ms", float64(time.Since(start).Microseconds())/1000, "api_version", c.Writer.Header().Get("X-Ledger-API-Version"))
		}()
		c.Next()
	}
}

// Middleware attaches isolated hubs before sentrygin creates request transactions.
func (r *Runtime) Middleware() []gin.HandlerFunc {
	if r == nil || r.hub == nil {
		return nil
	}
	return []gin.HandlerFunc{
		func(c *gin.Context) {
			hub := r.hub.Clone()
			if state, ok := c.Request.Context().Value(requestKey{}).(*requestState); ok {
				hub.Scope().SetTag("request_id", state.id)
				hub.Scope().SetTag("http.route", state.route)
			}
			c.Request = c.Request.WithContext(sentry.SetHubOnContext(c.Request.Context(), hub))
			c.Next()
		},
		sentrygin.New(sentrygin.Options{Repanic: true, WaitForDelivery: false}),
		func(c *gin.Context) {
			if span := sentrygin.GetSpanFromContext(c); span != nil {
				route := c.FullPath()
				if route == "" {
					route = "unmatched"
				}
				span.Name = c.Request.Method + " " + route
			}
			// sentrygin finishes its span before outer Problem Details recovery. Set
			// the pending status before repanicking so the trace reports a failure.
			defer func() {
				if recovered := recover(); recovered != nil {
					if state, ok := c.Request.Context().Value(requestKey{}).(*requestState); ok {
						state.panicCaptured = true
					}
					if !c.Writer.Written() {
						c.Status(http.StatusInternalServerError)
					}
					panic(recovered)
				}
			}()
			c.Next()
		},
	}
}

// SetUser associates a verified identity only with this request, never global scope.
// Use a Clerk-prefixed subject before bootstrap and the internal UUID once resolved.
func SetUser(c *gin.Context, userID, tenantID string) {
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), actorKey{}, actorInfo{userID, tenantID}))
	if hub := sentrygin.GetHubFromContext(c); hub != nil {
		hub.Scope().SetUser(sentry.User{ID: userID})
		if tenantID != "" {
			hub.Scope().SetTag("tenant_id", tenantID)
		}
	}
}

// ReportProblem preserves occurrence correlation and captures one event for server failures.
// Expected 4xx responses are logs only; sentrygin already captures panics.
func ReportProblem(c *gin.Context, instance, kind string, status int) {
	hub := sentrygin.GetHubFromContext(c)
	if hub == nil {
		return
	}
	hub.Scope().SetTag("problem.instance", instance)
	hub.Scope().SetTag("problem.type", kind)
	if status < 500 {
		return
	}
	if state, ok := c.Request.Context().Value(requestKey{}).(*requestState); ok && state.panicCaptured {
		return
	}
	hub.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		scope.SetFingerprint([]string{"ledger", kind})
		hub.CaptureMessage("HTTP server failure: " + kind)
	})
}
