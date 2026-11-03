// Package web serves the embedded taproot dashboard.
package web

import (
	_ "embed"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// index is the single-file dashboard build. scripts/taproot/build-web.sh
// replaces the committed placeholder.
//
//go:embed dist/index.html
var index []byte

// Middleware serves the dashboard for GET and HEAD on "/" and passes every
// other request on. It is a middleware rather than a route because upstream
// registers its own GET "/" before router configurators run, and gin panics
// on a second registration of the same route.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path != "/" || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			c.Next()
			return
		}
		h := c.Writer.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		// The dashboard holds keyless admin actions, so no page may frame it (clickjacking).
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("Content-Length", strconv.Itoa(len(index)))
		c.Status(http.StatusOK)
		if c.Request.Method == http.MethodGet {
			_, _ = c.Writer.Write(index)
		}
		c.Abort()
	}
}
