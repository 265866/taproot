package pluginhost

import (
	"context"
	"strings"
)

// CallerPlugin reports whether ctx belongs to a call a plugin made back into
// the host, such as host.model.execute, and returns that plugin's ID when the
// host recorded one. Usage records of such executions carry this context, so
// taproot can attribute requests that no HTTP caller made.
func CallerPlugin(ctx context.Context) (pluginID string, ok bool) {
	pluginID = hostCallbackPluginIDFromContext(ctx)
	return pluginID, pluginID != "" || hostCallbackInstanceFromContext(ctx) != nil
}

// PluginName returns the display name an active plugin reports in its
// metadata, or "" when no active plugin has that ID. Unlike
// RegisteredPlugins, it never calls into a plugin.
func (h *Host) PluginName(pluginID string) string {
	if h == nil {
		return ""
	}
	for _, record := range h.activeRecords() {
		if record.id == pluginID {
			return strings.TrimSpace(record.meta.Name)
		}
	}
	return ""
}
