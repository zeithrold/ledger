// Package apidocs serves the Scalar reference with a pinned jsDelivr script.
package apidocs

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/apicontract"
)

//go:embed assets/*
var assets embed.FS

// Register enables documentation outside the authenticated API namespace.
func Register(r *gin.Engine) {
	r.GET("/docs", func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'")
		c.Header("Cache-Control", "no-store")
		page := template.Must(template.ParseFS(assets, "assets/index.html"))
		var body bytes.Buffer
		if err := page.Execute(&body, apicontract.URL()); err != nil {
			panic("render documentation")
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", body.Bytes())
	})
	for _, name := range []string{"init.js"} {
		r.GET("/docs/assets/"+name, func(c *gin.Context) {
			data, err := assets.ReadFile("assets/" + name)
			if err != nil {
				panic("missing documentation asset")
			}
			c.Header("X-Content-Type-Options", "nosniff")
			c.Data(http.StatusOK, "text/javascript; charset=utf-8", data)
		})
	}
	r.GET(apicontract.URL(), func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "application/json", apicontract.Document())
	})
}
