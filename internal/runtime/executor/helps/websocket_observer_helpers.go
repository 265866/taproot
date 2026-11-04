package helps

import (
	"context"
	"strings"

	cliproxyauth "github.com/265866/taproot/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/265866/taproot/sdk/cliproxy/executor"
	"github.com/MoeForge/moego/ojson"
)

// EmitWebSocketResponseEvent delivers an upstream WebSocket response frame to any configured observer.
func EmitWebSocketResponseEvent(ctx context.Context, opts cliproxyexecutor.Options, auth *cliproxyauth.Auth, provider, model string, payload []byte) {
	if opts.WebSocketResponseObserver == nil || len(payload) == 0 {
		return
	}
	var authID, authLabel, authType string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, _ = auth.AccountInfo()
	}
	eventType := strings.Clone(ojson.GetBytesNoCopy(payload, "type").String())
	var requestID string
	var traceID string
	if opts.Metadata != nil {
		if reqID, ok := opts.Metadata["request_id"].(string); ok {
			requestID = reqID
		}
		if trID, ok := opts.Metadata["trace_id"].(string); ok {
			traceID = trID
		}
	}
	requestedModel := PayloadRequestedModel(opts, model)
	opts.WebSocketResponseObserver(ctx, cliproxyexecutor.WebSocketResponseEvent{
		RequestID:      requestID,
		TraceID:        traceID,
		SourceFormat:   opts.SourceFormat.String(),
		Model:          model,
		RequestedModel: requestedModel,
		Provider:       provider,
		AuthID:         authID,
		AuthLabel:      authLabel,
		AuthType:       authType,
		EventType:      eventType,
		Payload:        payload,
		Metadata:       opts.Metadata,
	})
}
