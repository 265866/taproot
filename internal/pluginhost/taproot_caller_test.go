package pluginhost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/sdk/api/handlers"
	"github.com/265866/taproot/sdk/pluginabi"
	"github.com/265866/taproot/sdk/pluginapi"
)

type callerKey struct{}

// TestCallerPluginReachesTheModelExecutor drives host.model.execute the way a
// loaded plugin calls it and checks the context the model executor receives,
// which usage records inherit, names the calling plugin.
func TestCallerPluginReachesTheModelExecutor(t *testing.T) {
	host := New()
	var gotID string
	var gotOK bool
	var gotCaller any
	host.SetModelExecutor(&fakeHostModelExecutor{
		executeModel: func(ctx context.Context, _ handlers.ModelExecutionRequest) (handlers.ModelExecutionResponse, *interfaces.ErrorMessage) {
			gotID, gotOK = CallerPlugin(ctx)
			gotCaller = ctx.Value(callerKey{})
			return handlers.ModelExecutionResponse{StatusCode: 200}, nil
		},
	})
	execute := func(ctx context.Context, callbackID string) {
		t.Helper()
		raw, err := json.Marshal(rpcHostModelExecutionRequest{
			HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{EntryProtocol: "claude", ExitProtocol: "claude", Model: "m"},
			HostCallbackID:            callbackID,
		})
		if err != nil {
			t.Fatal(err)
		}
		gotID, gotOK, gotCaller = "", false, nil
		if _, err := host.callFromPlugin(ctx, pluginabi.MethodHostModelExecute, raw); err != nil {
			t.Fatalf("callFromPlugin: %v", err)
		}
	}

	// A plugin's own timer: cliproxyHostCall builds the context from the
	// loaded plugin's entry, with no callback ID.
	instance := &hostCallbackInstance{}
	execute(withHostCallbackIdentity(context.Background(), "claude-keep-rolling", instance), "")
	if gotID != "claude-keep-rolling" || !gotOK {
		t.Errorf("timer call: CallerPlugin = %q %t, want claude-keep-rolling true", gotID, gotOK)
	}

	// A call inside a host-to-plugin call resolves the registered context,
	// which keeps the caller's values and names the plugin.
	outer := context.WithValue(context.Background(), callerKey{}, "http request")
	id, release := host.openCallbackContextForPluginInstance(outer, "web-search-router", instance)
	defer release()
	execute(withHostCallbackIdentity(context.Background(), "web-search-router", instance), id)
	if gotID != "web-search-router" || !gotOK || gotCaller != "http request" {
		t.Errorf("nested call: CallerPlugin = %q %t, caller value %v", gotID, gotOK, gotCaller)
	}

	// The instance alone still marks a plugin callback whose ID is unknown.
	if id, ok := CallerPlugin(withHostCallbackIdentity(context.Background(), "", instance)); id != "" || !ok {
		t.Errorf("instance only: CallerPlugin = %q %t, want \"\" true", id, ok)
	}
	if id, ok := CallerPlugin(context.Background()); id != "" || ok {
		t.Errorf("plain context: CallerPlugin = %q %t, want \"\" false", id, ok)
	}
}

func TestPluginNameReadsActiveMetadata(t *testing.T) {
	host := newHostWithRecords(
		capabilityRecord{id: "claude-keep-rolling", meta: pluginapi.Metadata{Name: " Claude Keep Rolling "}},
		capabilityRecord{id: "unnamed"},
	)
	for id, want := range map[string]string{"claude-keep-rolling": "Claude Keep Rolling", "unnamed": "", "gone": ""} {
		if got := host.PluginName(id); got != want {
			t.Errorf("PluginName(%q) = %q, want %q", id, got, want)
		}
	}
	if got := (*Host)(nil).PluginName("claude-keep-rolling"); got != "" {
		t.Errorf("nil host PluginName = %q", got)
	}
}
