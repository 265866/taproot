// Package reqlog stores request logs natively: compressed, indexed, and pruned
// to a configured budget.
package reqlog

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
)

// Config is the request_logs section of taproot.yaml.
type Config struct{}

// Logs owns request log storage.
type Logs struct{}

// New prepares the module.
func New(cfg Config, db *store.DB, dataDir string) (*Logs, error) { return &Logs{}, nil }

// LoggerFactory replaces the upstream file request logger. Nil keeps upstream's.
func (l *Logs) LoggerFactory() func(*config.Config, string) logging.RequestLogger { return nil }

// Start runs background work until ctx ends.
func (l *Logs) Start(ctx context.Context) {}

// Routes registers request log endpoints under the taproot API group.
func (l *Logs) Routes(r *gin.RouterGroup) {}
