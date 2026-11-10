package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/logging"
	"github.com/265866/taproot/internal/taproot/reqlog"
	"github.com/265866/taproot/internal/taproot/store"
)

// writeRequestLogs records the burst requests, and the others marked logged,
// through the request logger the server installs, with fake request and
// response bodies.
func writeRequestLogs(ctx context.Context, db *store.DB, dataDir string, requests []request) (int, error) {
	logs, err := reqlog.New(reqlog.Config{}, db, dataDir)
	if err != nil {
		return 0, fmt.Errorf("request logs: %w", err)
	}
	cfg := &config.Config{}
	cfg.RequestLog = true
	logger := logs.LoggerFactory()(cfg, "")
	logs.Start(ctx)
	defer logs.Close()
	n := 0
	for _, r := range requests {
		if !r.burst && !r.logged {
			continue
		}
		e := renderExchange(r)
		var apiErrs []*interfaces.ErrorMessage
		if r.Failed {
			apiErrs = []*interfaces.ErrorMessage{{StatusCode: r.Fail.StatusCode, Error: fmt.Errorf("%s", e.response)}}
		}
		status := http.StatusOK
		if r.Failed {
			status = r.Fail.StatusCode
		}
		end := r.RequestedAt.Add(r.Latency)
		if err := logger.LogRequest(e.path, http.MethodPost, e.reqHeaders, e.body, status, e.respHeaders, e.response,
			nil, e.apiRequest, e.apiResponse, nil, apiErrs, r.RequestID, r.RequestedAt, end); err != nil {
			return n, fmt.Errorf("log request %s: %w", r.RequestID, err)
		}
		n++
	}
	return n, nil
}

type exchange struct {
	path                    string
	reqHeaders, respHeaders map[string][]string
	body, response          []byte
	apiRequest, apiResponse []byte
}

func renderExchange(r request) exchange {
	traceID := r.RequestedAt.UTC().Format("20060102150405") + "-" + r.AuthIndex + "-" + r.RequestID
	e := exchange{respHeaders: map[string][]string{logging.CPATraceIDHeader: {traceID}}}
	upstreamURL := ""
	// Clients send the bare UUID; the proxy records it as <provider>:<uuid>.
	_, clientSession, _ := strings.Cut(r.SessionID, ":")
	if r.Provider == "claude" {
		e.path, upstreamURL = "/v1/messages?beta=true", "https://api.anthropic.com/v1/messages?beta=true"
		e.reqHeaders = map[string][]string{
			"Content-Type":             {"application/json"},
			"User-Agent":               {"claude-cli/2.1.316 (external, cli)"},
			"Anthropic-Version":        {"2023-06-01"},
			"X-Claude-Code-Session-Id": {clientSession},
			"Authorization":            {"Bearer taproot"},
		}
		userID := mustJSON(map[string]string{"session_id": clientSession})
		e.body = mustJSON(map[string]any{
			"model": r.Model, "max_tokens": 32000, "stream": true,
			"metadata": map[string]string{"user_id": string(userID)},
			"system":   []map[string]string{{"type": "text", "text": "You are a coding assistant working in the example-app repository."}},
			"messages": []map[string]any{{"role": "user", "content": []map[string]string{{"type": "text", "text": r.prompt}}}},
		})
		e.response = claudeStream(r)
	} else {
		e.path, upstreamURL = "/v1/responses", "https://chatgpt.com/backend-api/codex/responses"
		e.reqHeaders = map[string][]string{
			"Content-Type":  {"application/json"},
			"User-Agent":    {"codex_cli_rs/0.159.3"},
			"Session_id":    {clientSession},
			"Authorization": {"Bearer taproot"},
		}
		e.body = mustJSON(map[string]any{
			"model": r.Model, "stream": true, "store": false,
			"instructions":     "You are a coding agent working in the example-app repository.",
			"reasoning":        map[string]string{"effort": "medium"},
			"input":            []map[string]any{{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": r.prompt}}}},
			"prompt_cache_key": clientSession,
		})
		e.response = codexStream(r)
	}
	status := http.StatusOK
	if r.Failed {
		status = r.Fail.StatusCode
		e.response = []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
		e.respHeaders["Content-Type"] = []string{"application/json"}
	} else {
		e.respHeaders["Content-Type"] = []string{"text/event-stream"}
	}
	ts := r.RequestedAt.UTC().Format(time.RFC3339Nano)
	e.apiRequest = []byte(fmt.Sprintf("=== API REQUEST 1 ===\nTimestamp: %s\nUpstream URL: %s\n\nHeaders:\nAuthorization: Bearer [redacted]\nContent-Type: application/json\n\nBody:\n%s\n", ts, upstreamURL, e.body))
	var hdr strings.Builder
	names := make([]string, 0, len(r.ResponseHeaders))
	for k := range r.ResponseHeaders {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(&hdr, "%s: %s\n", k, r.ResponseHeaders.Get(k))
	}
	e.apiResponse = []byte(fmt.Sprintf("=== API RESPONSE 1 ===\nTimestamp: %s\nStatus: %d\n\nHeaders:\n%s\nBody:\n%s\n",
		r.RequestedAt.Add(r.TTFT).UTC().Format(time.RFC3339Nano), status, hdr.String(), e.response))
	return e
}

func claudeStream(r request) []byte {
	d := r.Detail
	cache5m, cache1h := int64(0), int64(0)
	if d.CacheCreation5mTokens != nil {
		cache5m = *d.CacheCreation5mTokens
	}
	if d.CacheCreation1hTokens != nil {
		cache1h = *d.CacheCreation1hTokens
	}
	start := map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_demo" + r.TraceID, "type": "message", "role": "assistant", "model": r.Model, "content": []any{},
		"usage": map[string]any{
			"input_tokens": d.InputTokens, "output_tokens": 1,
			"cache_read_input_tokens": d.CacheReadTokens, "cache_creation_input_tokens": d.CacheCreationTokens,
			"cache_creation": map[string]int64{"ephemeral_5m_input_tokens": cache5m, "ephemeral_1h_input_tokens": cache1h},
		},
	}}
	events := []any{
		start,
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": r.reply}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "end_turn"}, "usage": map[string]int64{"output_tokens": d.OutputTokens}},
		map[string]any{"type": "message_stop"},
	}
	var b strings.Builder
	for _, ev := range events {
		raw := mustJSON(ev)
		var typ struct{ Type string }
		_ = json.Unmarshal(raw, &typ)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", typ.Type, raw)
	}
	return []byte(b.String())
}

func codexStream(r request) []byte {
	d := r.Detail
	resp := map[string]any{
		"id": "resp_demo" + r.TraceID, "object": "response", "model": r.Model, "status": "completed",
		"output": []any{map[string]any{
			"type": "message", "id": "msg_demo" + r.TraceID, "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": r.reply, "annotations": []any{}}},
		}},
		"usage": map[string]any{
			"input_tokens": d.InputTokens, "input_tokens_details": map[string]int64{"cached_tokens": d.CachedTokens},
			"output_tokens": d.OutputTokens, "output_tokens_details": map[string]int64{"reasoning_tokens": d.ReasoningTokens},
			"total_tokens": d.TotalTokens,
		},
	}
	created := mustJSON(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_demo" + r.TraceID, "status": "in_progress", "output": []any{}}})
	completed := mustJSON(map[string]any{"type": "response.completed", "response": resp})
	return []byte(fmt.Sprintf("event: response.created\ndata: %s\n\nevent: response.completed\ndata: %s\n\n", created, completed))
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
