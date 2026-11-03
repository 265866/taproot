// Package backup snapshots config, credentials, and the taproot database on a
// schedule and restores them on request.
package backup

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
)

// Config is the backups section of taproot.yaml.
type Config struct{}

// Paths names the files a backup covers.
type Paths struct {
	ConfigFile  string
	TaprootFile string
	AuthDir     string
	DataDir     string
}

// Backups owns snapshots.
type Backups struct{}

// New prepares the module.
func New(cfg Config, db *store.DB, paths Paths) (*Backups, error) { return &Backups{}, nil }

// Start runs the schedule until ctx ends.
func (b *Backups) Start(ctx context.Context) {}

// Routes registers backup endpoints under the taproot API group.
func (b *Backups) Routes(r *gin.RouterGroup) {}
