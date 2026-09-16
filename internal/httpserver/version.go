package httpserver

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/apicontract"
	"github.com/zeithrold/ledger/internal/problem"
)

// VersionHeader pins the minor contract independently of the path's major version.
const VersionHeader = apicontract.VersionHeader

func versionGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path != "/api" && !strings.HasPrefix(path, "/api/") {
			c.Next()
			return
		}
		addVary(c.Writer.Header(), VersionHeader)
		c.Header("Cache-Control", "no-store")
		if c.Request.Method == http.MethodOptions && c.GetHeader("Origin") != "" && c.GetHeader("Access-Control-Request-Method") != "" {
			c.Next()
			return
		}
		parts := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
		versions, ok := apicontract.Supported(parts[0])
		if !ok {
			problem.Write(c, problem.New(problem.MajorUnsupported, "The API major version is not supported."))
			return
		}
		values := c.Request.Header.Values(VersionHeader)
		if len(values) == 0 {
			problem.Write(c, problem.New(problem.VersionRequired, "Provide the X-Ledger-API-Version request header."))
			return
		}
		if len(values) != 1 {
			problem.Write(c, problem.New(problem.VersionInvalid, "Provide exactly one YYYY-MM-DD version."))
			return
		}
		requested := values[0]
		date, err := time.Parse(time.DateOnly, requested)
		if err != nil || date.Format(time.DateOnly) != requested {
			problem.Write(c, problem.New(problem.VersionInvalid, "Provide exactly one YYYY-MM-DD version."))
			return
		}
		if !slices.Contains(versions, requested) {
			problem.Write(c, problem.New(problem.VersionUnsupported, "The requested API version is not supported."), versions...)
			return
		}
		// Echo the negotiated date, which may be the tolerated preceding
		// revision rather than the current one, so a client can see exactly
		// which revision the response was served under.
		c.Header(VersionHeader, requested)
		c.Next()
	}
}

func addVary(h http.Header, value string) {
	for _, line := range h.Values("Vary") {
		for _, item := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) || strings.TrimSpace(item) == "*" {
				return
			}
		}
	}
	h.Add("Vary", value)
}
