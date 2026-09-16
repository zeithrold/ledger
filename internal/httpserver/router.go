// Package httpserver constructs the Ledger HTTP router.
package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zeithrold/ledger/internal/apidocs"
	"github.com/zeithrold/ledger/internal/apiv1"
	"github.com/zeithrold/ledger/internal/observability"
	"github.com/zeithrold/ledger/internal/problem"
	"github.com/zeithrold/ledger/internal/problemhttp"

	"github.com/gin-gonic/gin"
)

// Pinger is the database readiness dependency.
type Pinger interface{ PingContext(context.Context) error }

// New creates the router with proxy trust disabled.
func New(db Pinger, dependencies ...Dependencies) (*gin.Engine, error) {
	r := gin.New()
	deps := Dependencies{}
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	r.Use(observability.Requests(), problemhttp.Recovery())
	r.Use(deps.Telemetry.Middleware()...)
	r.Use(versionGate())
	r.HandleMethodNotAllowed = true
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.NoRoute(func(c *gin.Context) {
		problemhttp.Write(c, problem.New(problem.NotFound, "The resource was not found."))
	})
	r.NoMethod(func(c *gin.Context) {
		methods := []string{}
		for _, route := range r.Routes() {
			if matchesPath(route.Path, c.Request.URL.Path) {
				methods = append(methods, route.Method)
			}
		}
		sort.Strings(methods)
		c.Header("Allow", strings.Join(methods, ", "))
		problemhttp.Write(c, problem.New(problem.MethodNotAllowed, "The method is not supported for this resource."))
	})
	if err := r.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("configure proxy trust: %w", err)
	}
	a := api{deps: deps, db: db}
	r.Use(a.authenticate(), a.authorize())
	apiv1.RegisterHandlersWithOptions(r, a, apiv1.GinServerOptions{ErrorHandler: bindingError})
	if deps.DocsEnabled {
		apidocs.Register(r)
	}
	return r, nil
}

func matchesPath(pattern, path string) bool {
	a, b := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.HasPrefix(a[i], ":") && b[i] != "" {
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (a api) GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, apiv1.Health{Status: apiv1.HealthStatusOk})
}

func (a api) GetReadiness(c *gin.Context) {
	state := apiv1.ReadinessDatabaseDisabled
	if a.db != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := a.db.PingContext(ctx); err != nil {
			problemhttp.Write(c, problem.New(problem.Unavailable, "The database is unavailable."))
			return
		}
		state = apiv1.ReadinessDatabaseConnected
	}
	c.JSON(http.StatusOK, apiv1.Readiness{Status: apiv1.ReadinessStatusOk, Database: state})
}

func bindingError(c *gin.Context, err error, _ int) {
	p := problem.New(problem.InvalidRequest, "Provide valid path, query and header parameters.")
	// Only static parameter names reach the response; never expose binder error text.
	for _, field := range []problem.FieldError{
		{Location: "path", Name: "book_id", Reason: "A UUID is required."},
		{Location: "path", Name: "user_id", Reason: "A UUID is required."},
		{Location: "query", Name: "after", Reason: "A UUID is required."},
		{Location: "query", Name: "limit", Reason: "An integer between 1 and 100 is required."},
	} {
		if strings.HasPrefix(err.Error(), "Invalid format for parameter "+field.Name+":") {
			p.Fields = []problem.FieldError{field}
			break
		}
	}
	problemhttp.Write(c, p)
}

var _ apiv1.ServerInterface = api{}
