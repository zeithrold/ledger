// Package problem defines the stable Ledger Problem Details contract.
package problem

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/zeithrold/ledger/internal/observability"
)

// Kind is a stable problem type, independent of API versions.
type Kind string

// Stable problem kinds shared by all API versions.
const (
	VersionRequired        Kind = "api-version-required"
	VersionInvalid         Kind = "api-version-invalid"
	VersionUnsupported     Kind = "api-version-unsupported"
	InvalidRequest         Kind = "invalid-request"
	AuthenticationRequired Kind = "authentication-required"
	InvalidToken           Kind = "invalid-token"
	UserDisabled           Kind = "user-disabled"
	AccessDenied           Kind = "access-denied"
	MajorUnsupported       Kind = "api-major-version-unsupported"
	NotFound               Kind = "not-found"
	MethodNotAllowed       Kind = "method-not-allowed"
	BootstrapRequired      Kind = "bootstrap-required"
	Unavailable            Kind = "service-unavailable"
	Internal               Kind = "internal-error"
)

type definition struct {
	status int
	title  string
}

var definitions = map[Kind]definition{
	VersionRequired: {400, "API version required"}, VersionInvalid: {400, "Invalid API version"},
	VersionUnsupported: {400, "Unsupported API version"}, InvalidRequest: {400, "Invalid request"},
	AuthenticationRequired: {401, "Authentication required"}, InvalidToken: {401, "Invalid token"},
	UserDisabled: {403, "User disabled"}, AccessDenied: {403, "Access denied"},
	MajorUnsupported: {404, "Unsupported API major version"}, NotFound: {404, "Not found"},
	MethodNotAllowed: {405, "Method not allowed"}, BootstrapRequired: {409, "Bootstrap required"},
	Unavailable: {503, "Service unavailable"}, Internal: {500, "Internal server error"},
}

// FieldError describes a rejected parameter without echoing its value.
type FieldError struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Reason   string `json:"reason"`
}

// Error carries only safe, client-facing information.
type Error struct {
	Kind   Kind
	Detail string
	Fields []FieldError
	Cause  error
}

func (e *Error) Error() string { return string(e.Kind) }

// New constructs an application error with a stable type.
func New(kind Kind, detail string) *Error { return &Error{Kind: kind, Detail: detail} }

// Write is the sole application error responder. Extensions cannot override core fields.
func Write(c *gin.Context, p *Error, versions ...string) {
	if c.Writer.Written() {
		c.Abort()
		return
	}
	d, ok := definitions[p.Kind]
	if !ok {
		p = New(Internal, "The request could not be completed.")
		d = definitions[Internal]
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
				Write(c, New(Internal, "The request could not be completed."))
			}
		}()
		c.Next()
	}
}
