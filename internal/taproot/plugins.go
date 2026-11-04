package taproot

import (
	"context"
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
)

// pluginCallers tells ingest which plugin made a usage record in-process.
// The plugin host arrives with the router configurator; until then, and for
// a plugin that has been unloaded, devices are named by plugin ID.
type pluginCallers struct {
	host atomic.Pointer[pluginhost.Host]
}

func (p *pluginCallers) CallerPlugin(ctx context.Context) (string, bool) {
	return pluginhost.CallerPlugin(ctx)
}

func (p *pluginCallers) PluginName(pluginID string) string {
	return p.host.Load().PluginName(pluginID)
}

func (p *pluginCallers) attach(h *handlers.BaseAPIHandler) {
	if host, ok := h.PluginHost.(*pluginhost.Host); ok {
		p.host.Store(host)
	}
}
