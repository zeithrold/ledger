// Package problemhttp adapts application errors to the HTTP contract.
package problemhttp

import (
	"log/slog"
	"net/http"

	"github.com/zeithrold/ledger/internal/problem"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/zeithrold/ledger/internal/observability"
)

type definition struct {
	status int
	title  string
}

var definitions = map[problem.Kind]definition{
	problem.Conflict:        {409, "Accounting conflict"},
	problem.VersionRequired: {400, "API version required"}, problem.VersionInvalid: {400, "Invalid API version"},
	problem.VersionUnsupported: {400, "Unsupported API version"}, problem.InvalidRequest: {400, "Invalid request"},
	problem.AuthenticationRequired: {401, "Authentication required"}, problem.InvalidToken: {401, "Invalid token"},
	problem.UserDisabled: {403, "User disabled"}, problem.AccessDenied: {403, "Access denied"},
	problem.MajorUnsupported: {404, "Unsupported API major version"}, problem.NotFound: {404, "Not found"},
	problem.MethodNotAllowed: {405, "Method not allowed"}, problem.BootstrapRequired: {409, "Bootstrap required"},
	problem.Unavailable: {503, "Service unavailable"}, problem.Internal: {500, "Internal server error"},
}

// Write is the sole application error responder. Extensions cannot override core fields.
func Write(c *gin.Context, p *problem.Error, versions ...string) {
	if c.Writer.Written() {
		c.Abort()
		return
	}
	d, ok := definitions[p.Kind]
	if !ok {
		p = problem.New(problem.Internal, "The request could not be completed.")
		d = definitions[problem.Internal]
	}
	instance := "urn:uuid:" + uuid.NewString()
	body := gin.H{
		"type": "https://ledger.ztd.me/errors/" + string(p.Kind), "title": d.title,
		"status": d.status, "detail": p.Detail, "instance": instance,
	}
	if len(p.Fields) > 0 {
		body["errors"] = p.Fields
	}
	if versions != nil {
		body["supported_versions"] = versions
	}
	if d.status == http.StatusUnauthorized {
		c.Header("WWW-Authenticate", "Bearer")
	}
	level := slog.LevelWarn
	if d.status >= 500 {
		level = slog.LevelError
	}
	attrs := []any{"event", "http_problem", "instance", instance, "type", string(p.Kind), "status", d.status}
	if p.Cause != nil {
		attrs = append(attrs, "error", p.Cause)
	}
	slog.Log(c.Request.Context(), level, "request failed", attrs...)
	observability.ReportProblem(c, instance, string(p.Kind), d.status)
	c.Header("Content-Type", "application/problem+json")
	c.AbortWithStatusJSON(d.status, body)
}

// Recovery prevents panic values and default Gin text from reaching clients or logs.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				Write(c, problem.New(problem.Internal, "The request could not be completed."))
			}
		}()
		c.Next()
	}
}
