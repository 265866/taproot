// Package ingest records every usage record, prices it, and keeps per-account
// quota history from upstream rate-limit headers and usage endpoints.
package ingest

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// Config is the ingest section of taproot.yaml.
type Config struct{}

// Ingest owns usage, quota, and pricing data.
type Ingest struct{}

// New prepares the module. It must register its usage sink before the service starts.
func New(cfg Config, db *store.DB) (*Ingest, error) { return &Ingest{}, nil }

// AttachAuthManager receives the runtime credential manager once the server is built.
func (i *Ingest) AttachAuthManager(m *coreauth.Manager) {}

// Start runs background work until ctx ends.
func (i *Ingest) Start(ctx context.Context) {}

// Routes registers ingest endpoints under the taproot API group.
func (i *Ingest) Routes(r *gin.RouterGroup) {}
