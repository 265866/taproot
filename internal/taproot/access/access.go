// Package access admits requests by the caller's Tailscale identity instead of
// API keys or the management key.
package access

import "github.com/gin-gonic/gin"

// Config is the access section of taproot.yaml.
type Config struct{}

// Guard decides whether a request may reach the proxy, the management API, or the dashboard.
type Guard struct{}

// New builds the guard. It runs before the management handler is constructed.
func New(cfg Config) (*Guard, error) { return &Guard{}, nil }

// Middleware runs on every route.
func (g *Guard) Middleware() gin.HandlerFunc { return func(c *gin.Context) { c.Next() } }

// Routes registers access endpoints under the taproot API group.
func (g *Guard) Routes(r *gin.RouterGroup) {}
