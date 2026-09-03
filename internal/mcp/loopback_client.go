package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

// loopback is the one path every tool has into the admin plane (§A6): pull
// the inbound /mcp request Handler placed on the context, pair it with a
// tool's own method/path/body, and hand both to Caller.CallAsMCP so every
// tool goes through exactly the handlers, validation and error envelopes
// the admin UI does — never around them into a repository directly.
//
// Tools A and Tools B (internal/mcp/tools_ops.go, internal/mcp/
// tools_traffic.go) use this type verbatim, via addOperationTools and
// addTrafficTools (tools.go) — it is the "lb" parameter both receive.
type loopback struct {
	calls Caller
}

// newLoopback wraps calls — the Caller every /mcp request ultimately
// reaches through Handler().
func newLoopback(calls Caller) *loopback {
	return &loopback{calls: calls}
}

// do issues one admin-API request as the inbound /mcp request Handler
// attached to ctx (see inboundRequest/withInboundRequest, mcp.go), and
// returns exactly what CallAsMCP returned: status and body raw. err is
// non-nil ONLY when the call could not be MADE at all — identity
// resolution, request construction, or an unlisted route (§B2 rule 7) — or
// when ctx carries no inbound request, which should never happen outside a
// bug in this package: Handler always attaches one before any tool handler
// runs, so a tool seeing that error found a defect here, not a client
// mistake.
//
// Use do directly whenever a tool must inspect status itself before
// deciding success or failure: a 404 meaning "no override exists yet"
// (§C-a), a GET that must be resent whole on PUT (§C-b), or a 409 that is a
// refusal to project back to the model, not a failure of the tool call
// itself (§C-d). Use call for the ordinary case where any non-2xx status is
// simply the tool's own error.
func (lb *loopback) do(ctx context.Context, method, path string, body []byte) (status int, respBody []byte, err error) {
	req, ok := inboundRequest(ctx)
	if !ok {
		return 0, nil, errors.New("mcp: loopback: no inbound request on context — Handler must attach one before any tool handler runs")
	}
	return lb.calls.CallAsMCP(ctx, req, method, path, body)
}

// call is do's convenience form for the common case: a 2xx body is decoded
// into out (a pointer, exactly like jsonx.Unmarshal's own contract — nil
// skips decoding, for a call whose only interesting output is success
// itself); anything else — a failure to make the call, or a non-2xx status
// — becomes the tool error §I requires. That is deliberately a plain Go
// error, never a hand-built CallToolResult: ToolHandlerFor (the SDK's own
// mcp/tool.go) packs a returned error into CallToolResult.Content with
// IsError set automatically, which is what lets the model read it as
// something to react to rather than the call transport itself failing.
func (lb *loopback) call(ctx context.Context, method, path string, body []byte, out any) error {
	status, respBody, err := lb.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return toolErr(status, respBody)
	}
	if out == nil {
		return nil
	}
	if err := jsonx.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("mcp: decode admin response (status %d): %w", status, err)
	}
	return nil
}

// toolErr turns a non-2xx admin response into the tool error §I requires.
// A 4xx is the caller's own mistake — an unknown opKey, a field the admin
// plane rejected — so its message carries the admin plane's OWN
// httpx.ErrorBody.Error.Message through: exactly the words an operator
// would read for the same failure in the UI, letting the model
// self-correct from them rather than from a generic wrapper. A 5xx is not a
// mistake the model's own arguments could have caused, so nothing past its
// status is surfaced — respBody is never echoed for one.
func toolErr(status int, respBody []byte) error {
	var env httpx.ErrorBody
	if jsonx.Unmarshal(respBody, &env) != nil || env.Error.Message == "" {
		return fmt.Errorf("admin API returned %d", status)
	}
	// A 5xx whose code is the generic internal one says nothing the model
	// could act on; every OTHER 5xx envelope does — 503 write_busy ("try
	// again"), the stream cap, live state not wired — and the model needs
	// those words to tell a retry from a crash.
	if status >= 500 && env.Error.Code == httpx.CodeInternal {
		return fmt.Errorf("admin API returned %d", status)
	}
	return fmt.Errorf("admin API returned %d: %s", status, env.Error.Message)
}
