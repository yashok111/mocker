// Package luafn is the ONE importing package of github.com/yuin/gopher-lua,
// the tree's third library, admitted by A18 D1 on a written §30.9 measurement.
// `boundary_test.go` fails the build on a second importer, exactly as
// internal/wsmock does for coder/websocket and internal/yamlx for yaml.
//
// What it owns: one sandboxed VM per invocation (D3, stateless by
// construction), the request table a function receives, the return convention
// it answers with, the execution deadline, and the classification of every way
// a call can end. What it deliberately does NOT own: the helpers that reach
// the product. `mock.jwt` and `mock.entities` arrive through the Host
// interface, constructed by the caller — the same construct-and-receive split
// internal/mockplane already keeps for the `ref` resolver, and the reason is
// the same: a leaf that imported the store would be a leaf no longer.
package luafn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Timeout is the wall clock one invocation gets. A fixed value and not an
// environment variable (D6): a knob is a carve-out away if a real load asks
// for one, and until then every deployment answers the same way.
//
// It bounds Lua BYTECODE and not a single native call: gopher-lua checks the
// context between VM instructions, so `string.rep("x", 1e9)` allocates before
// any check runs. That residual is accepted and recorded in CARVE-OUTS.md
// rather than hidden behind this constant.
// It is a package VAR and not a const so this package's own test can shorten
// it — the same shape internal/stream keeps for maxStreamLifetime, and for the
// same reason: a two-second wait in a unit test is two seconds nobody gets
// back. Nothing outside a test writes it.
var Timeout = 2 * time.Second

// The three ways a call ends badly, and they are three because the mock plane
// answers them differently: a deadline is a 503, everything else the function
// did wrong is a 500, and a client that walked away is answered by nobody.
var (
	ErrTimeout  = errors.New("luafn: the function exceeded its time budget")
	ErrFailed   = errors.New("luafn: the function failed")
	ErrCanceled = errors.New("luafn: the request was canceled")
)

// maxNoteBytes caps what a caller may put in a traffic note. Lua error text
// can embed request data — a token echoed into an error message is the obvious
// case — and a note is not a disclosure channel (D6).
const maxNoteBytes = 200

// Request is what a function sees as its single argument. Every field is
// filled by the caller from the request it is serving; nothing here reads a
// header or a URL on its own.
type Request struct {
	Method     string
	Path       string
	PathParams map[string]string
	Query      map[string][]string
	// Headers arrive with whatever case the client sent. The keys a function
	// sees are lowercased here, and a repeated header's values are joined with
	// ", " rather than becoming an array — RFC 9110 already defines that join
	// as equivalent, and one shape per field is worth more than symmetry with
	// Query, which has no such rule and therefore does use an array (D3).
	Headers map[string][]string
	Body    []byte
}

// Response is what a function returned, already validated into shapes the
// caller can put on the wire. The caller still applies the shared safety tail
// — the browser-executable media-type refusal, the header checks, the size cap
// — because those rules belong to both planes and not to this package.
type Response struct {
	Status int
	Body   []byte
	// MediaType is application/json when the function returned a TABLE and
	// empty when it returned a string, which the caller reads as "the function
	// chose no type" rather than as a default.
	MediaType string
	Headers   map[string]string
}

// Host is the seam for the two helpers that reach the product. It is an
// interface rather than an import because this package is a leaf: a nil Host
// is legal and makes both helpers answer their own error, which is what a
// preview with no live workspace behind it needs.
//
// Both methods take a context, and it is the INVOCATION's — the same value
// [Run] hands [lua.LState.SetContext], threaded through installMock rather
// than captured when the Host was built (D6). Without it the two-second budget
// would bound Lua bytecode and nothing else: mock.entities reads through the
// store and can queue behind a checkpoint restore or a reset-data holding the
// single writer connection, and a helper that outlived the budget would answer
// long after the plane had given up on the request.
type Host interface {
	// JWT signs with the workspace's own settings.auth. It answers
	// ("", error) when the workspace carries alg "none" or no key: an unsigned
	// token pretending to be signed is worse than an error (D3).
	JWT(ctx context.Context, claims map[string]any) (string, error)
	// Entities reads a confirmed family's rows. A nil scope means the serving
	// request's own; a non-nil one is an explicit ancestor tuple, whose values
	// arrive RAW — the host encodes them, because resources.EncodeScope is the
	// one owner of that join and a second one here is an encoding a UNIQUE
	// index could disagree with (D3).
	Entities(ctx context.Context, family string, scope []string) ([]map[string]any, error)
}

// Validate compiles the source and nothing else. Both writers run it before
// storing a function, so a syntax error is a 400 carrying the parser's own
// words rather than a 500 on the first request — this plane always answers,
// and a deferred parse is a 500 nobody asked for (D8).
func Validate(source string) error {
	l := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer l.Close()
	if _, err := l.LoadString(wrap(source)); err != nil {
		return fmt.Errorf("%w: %s", ErrFailed, err.Error())
	}
	return nil
}

// wrap is how `req` reaches a function, and the whole of the authoring
// contract: the stored source IS the body, so `return 200, {ok = true}` is a
// complete function and an agent writing one through MCP writes no ceremony.
//
// The prefix carries no NEWLINE on purpose. A one-line preamble would shift
// every line number in the parser's own error message by one, and D8 promises
// the writer's `400 bad_function` carries "the parser's own words" — words
// that point at the wrong line are worse than no words.
//
// What it DOES shift is the COLUMN on line 1, by the length of the prefix:
// gopher-lua reports `line:1(column:30)` for an error the author wrote at
// column 13. Measured 2026-09-04, accepted rather than corrected — undoing it
// would mean parsing the library's message format, and this tree depends on
// gopher-lua's exact wording in no place at all (internal/wsmock keeps its one
// such dependency in a single function for the same reason). A line and a
// `near '<token>'` are what an author navigates by; the column on the first
// line is the one coordinate that is off, and it is off by a constant.
func wrap(source string) string { return "local req = ...; " + source }

// Run executes one function against one request in a VM created for it and
// closed when it returns.
//
// The deadline is this package's own const applied on top of whatever the
// caller's context already carries, so a caller cannot lengthen it and a
// caller whose own context expires first still wins. The distinction between
// the two matters at the exit: the caller's context ending is a CLIENT that
// went away and is answered by nobody, while this package's own deadline is
// the 503 the operator asked for.
func Run(ctx context.Context, source string, req Request, host Host) (Response, error) {
	// D10 gave this package two more contracts, and [call] (hooks.go) is
	// what they share with this one: the fresh VM, the sandbox, the mock
	// table, the deadline on top of the caller's, and the caller-context
	// -before-deadline classification at the exit. Run keeps only what is
	// its own — the argument it builds and the return it reads.
	l, done, err := call(ctx, argRequest, source, func(st *lua.LState) lua.LValue {
		return requestTable(st, req)
	}, host)
	if err != nil {
		return Response{}, err
	}
	defer done()
	return readReturn(l)
}

// Note trims an error's message to what a traffic note may carry.
func Note(err error) string {
	if err == nil {
		return ""
	}
	msg := firstLine(err.Error())
	if len(msg) > maxNoteBytes {
		return msg[:maxNoteBytes]
	}
	return msg
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// requestTable builds the `req` argument. It is a plain table with no
// metatable: a function that assigns into it changes nothing anybody reads,
// because the VM it lives in is closed at the end of this call.
func requestTable(l *lua.LState, req Request) *lua.LTable {
	t := l.NewTable()
	t.RawSetString("method", lua.LString(req.Method))
	t.RawSetString("path", lua.LString(req.Path))

	params := l.NewTable()
	for k, v := range req.PathParams {
		params.RawSetString(k, lua.LString(v))
	}
	t.RawSetString("pathParams", params)

	query := l.NewTable()
	for k, vs := range req.Query {
		switch len(vs) {
		case 0:
			query.RawSetString(k, lua.LString(""))
		case 1:
			query.RawSetString(k, lua.LString(vs[0]))
		default:
			arr := l.NewTable()
			for _, v := range vs {
				arr.Append(lua.LString(v))
			}
			query.RawSetString(k, arr)
		}
	}
	t.RawSetString("query", query)

	headers := l.NewTable()
	for k, vs := range req.Headers {
		headers.RawSetString(strings.ToLower(k), lua.LString(strings.Join(vs, ", ")))
	}
	t.RawSetString("headers", headers)

	t.RawSetString("body", bodyValue(l, req.Body))
	return t
}

// bodyValue decodes a JSON body into a table and hands anything else over as
// the raw string. One shape rule, used here and again by the WebSocket hook of
// D10, so a function author learns it once.
func bodyValue(l *lua.LState, body []byte) lua.LValue {
	if len(body) == 0 {
		return lua.LNil
	}
	var decoded any
	if err := jsonx.Unmarshal(body, &decoded); err != nil {
		return lua.LString(body)
	}
	return goToLua(l, decoded)
}

// readReturn reads `status, body, headers` off the stack and refuses every
// other shape by name. D3 is explicit that a non-number status or a boolean
// body is a failure and NOT a silent coercion: a mock that quietly turns
// `true` into `"true"` teaches its author the wrong contract.
func readReturn(l *lua.LState) (Response, error) {
	n := l.GetTop()
	if n == 0 {
		return Response{}, fmt.Errorf("%w: the function returned nothing; it must return status, body, headers", ErrFailed)
	}
	defer l.SetTop(0)

	statusVal := l.Get(1)
	status, ok := statusVal.(lua.LNumber)
	if !ok {
		return Response{}, fmt.Errorf("%w: the status must be a number, got %s", ErrFailed, statusVal.Type())
	}
	code := int(status)
	if float64(code) != float64(status) || code < 100 || code > 599 {
		return Response{}, fmt.Errorf("%w: the status must be a whole number in 100..599, got %v", ErrFailed, status)
	}
	resp := Response{Status: code}

	if n >= 2 {
		switch body := l.Get(2).(type) {
		case *lua.LNilType:
			// A nil body is an empty body, and it is the one shape that is
			// neither a table nor a string and still legal.
		case lua.LString:
			resp.Body = []byte(body)
		case *lua.LTable:
			encoded, err := marshalLua(body)
			if err != nil {
				return Response{}, fmt.Errorf("%w: the body could not be encoded as JSON: %s", ErrFailed, err.Error())
			}
			resp.Body = encoded
			resp.MediaType = "application/json"
		default:
			return Response{}, fmt.Errorf("%w: the body must be a table, a string or nil, got %s", ErrFailed, body.Type())
		}
	}

	if n >= 3 {
		switch headers := l.Get(3).(type) {
		case *lua.LNilType:
		case *lua.LTable:
			out := map[string]string{}
			var bad error
			headers.ForEach(func(k, v lua.LValue) {
				name, okName := k.(lua.LString)
				value, okValue := v.(lua.LString)
				if !okName || !okValue {
					bad = fmt.Errorf("%w: every header name and value must be a string, got %s = %s", ErrFailed, k.Type(), v.Type())
					return
				}
				out[string(name)] = string(value)
			})
			if bad != nil {
				return Response{}, bad
			}
			resp.Headers = out
		default:
			return Response{}, fmt.Errorf("%w: the headers must be a table or nil, got %s", ErrFailed, headers.Type())
		}
	}
	return resp, nil
}
