// hooks.go is D10's two stream hooks, and they are here rather than in
// luafn.go because they are a different CONTRACT over the same runner: an
// endpoint function answers a request and returns `status, body, headers`,
// while a tick produces one frame and an onFrame hook answers one inbound
// frame with a verb.
//
// What they share is everything that matters — the sandbox of D3, the fresh
// VM per call, the deadline of D6, the honest non-determinism of D4 and the
// compile-at-write of D8 — and that sharing is structural: all three go
// through [call] below, so a change to the sandbox cannot reach one contract
// and miss another.
package luafn

import (
	"context"
	"errors"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// The argument name each contract binds its single parameter to. They are
// three distinct words on purpose: an author reading `frame` in an onFrame
// hook and `req` in an endpoint function is reading the two different things
// they actually are, and a shared name would invite copying a body between
// two places where it means something else.
const (
	argRequest = "req"
	argOrdinal = "ordinal"
	argFrame   = "frame"
)

// wrapAs is [wrap] generalized over the argument name; wrap itself is the
// request contract's call of it. The no-newline rule and the column shift it
// costs are stated once, on wrap.
func wrapAs(arg, source string) string { return "local " + arg + " = ...; " + source }

// ValidateHook compiles a hook body under its own argument name. It exists
// beside [Validate] rather than being folded into it because a source that
// mentions `frame` must compile as a FRAME hook: the two wrappers differ in
// exactly the identifier they bind, and compiling an onFrame body under the
// request wrapper would accept a source that then reads a nil `frame` at
// runtime — an undefined global in Lua is nil, never an error, which is the
// same silence D9's closing paragraph records as the Lua contract's standing
// hazard.
func ValidateHook(kind, source string) error {
	arg := argFrame
	if kind == HookTick {
		arg = argOrdinal
	}
	l := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer l.Close()
	if _, err := l.LoadString(wrapAs(arg, source)); err != nil {
		return fmt.Errorf("%w: %s", ErrFailed, err.Error())
	}
	return nil
}

// The two hook kinds [ValidateHook] takes, named rather than passed as a
// bare boolean so a call site says which contract it means.
const (
	HookTick    = "tick"
	HookOnFrame = "onFrame"
)

// call is the whole of what the three contracts share: a fresh VM, the
// sandbox, the mock table, this package's deadline on top of the caller's,
// the load, and the call with one argument. It leaves the results ON THE
// STACK for the caller's own reader, because what a return means is exactly
// the part the three do not share.
//
// The caller must close the state, which is why it comes back rather than
// being closed here: reading the results needs the VM alive.
func call(ctx context.Context, arg, source string, push func(*lua.LState) lua.LValue, host Host) (*lua.LState, func(), error) {
	runCtx, cancel := context.WithTimeout(ctx, Timeout)

	l := newState()
	done := func() { l.Close(); cancel() }
	l.SetContext(runCtx)
	installMock(l, runCtx, host)

	fn, err := l.LoadString(wrapAs(arg, source))
	if err != nil {
		done()
		return nil, nil, fmt.Errorf("%w: %s", ErrFailed, err.Error())
	}
	l.Push(fn)
	l.Push(push(l))

	if err := l.PCall(1, lua.MultRet, nil); err != nil {
		defer done()
		switch {
		case ctx.Err() != nil:
			// The CALLER's context first, exactly as Run orders it: a
			// connection that went away is not a broken hook.
			return nil, nil, ErrCanceled
		case runCtx.Err() != nil:
			return nil, nil, ErrTimeout
		default:
			return nil, nil, fmt.Errorf("%w: %s", ErrFailed, firstLine(err.Error()))
		}
	}
	return l, done, nil
}

// RunTick is D10.1: one firing of a Lua tick.
//
// send false with a nil error is the DECLINE — `return nil` — which D10.1
// makes a skipped firing on an open connection, and it is a distinct outcome
// from an error on purpose (acceptance clause 41: a nil return counted as
// both a skip and an error is two things reported for one).
//
// The frame checks a Lua body must pass are NOT here: they belong to the
// caller, beside the byte cap a generated frame is already checked against,
// so that one body cannot break SSE framing while the other cannot.
func RunTick(ctx context.Context, source string, ordinal int, host Host) (body []byte, send bool, err error) {
	l, done, err := call(ctx, argOrdinal, source, func(*lua.LState) lua.LValue {
		return lua.LNumber(ordinal)
	}, host)
	if err != nil {
		return nil, false, err
	}
	defer done()

	if l.GetTop() == 0 {
		// Returning NOTHING and returning nil are the same decline. A tick
		// whose body ends in an `if` with no else is the ordinary way to
		// write "not this time", and refusing it would make the contract
		// harder than what it describes.
		return nil, false, nil
	}
	switch v := l.Get(1).(type) {
	case *lua.LNilType:
		return nil, false, nil
	case lua.LString:
		return []byte(v), true, nil
	case *lua.LTable:
		encoded, mErr := marshalLua(v)
		if mErr != nil {
			return nil, false, fmt.Errorf("%w: the tick body could not be encoded as JSON: %s", ErrFailed, mErr.Error())
		}
		return encoded, true, nil
	default:
		return nil, false, fmt.Errorf("%w: a tick must return a table, a string or nil, got %s", ErrFailed, v.Type())
	}
}

// FrameAction is what an onFrame hook decided (D10.2). Verb is "" for no
// reply, [FrameReply] for one text frame, [FrameClose] for the closing
// handshake — never anything else, because [RunOnFrame] refuses an unknown
// verb rather than treating it as silence.
type FrameAction struct {
	Verb   string
	Data   []byte
	Code   int
	Reason string
}

// The two verbs, verb-first as D10.2 fixes the convention.
const (
	FrameReply = "reply"
	FrameClose = "close"
)

// ErrBadClose is a close code outside 1000 or 4000..4999. It is its own
// sentinel because the caller answers it exactly as it answers any other
// malformed return — the reply is dropped and counted in on_frame_errors —
// and a caller that wanted to tell it apart could, without parsing a string.
var ErrBadClose = errors.New("luafn: a close code must be 1000 or 4000..4999")

// RunOnFrame is D10.2: one inbound frame through the hook.
//
// frameIsObject says whether the caller decoded the frame as a JSON OBJECT.
// The decision is the CALLER's and not this package's, because it is the same
// decision P6d's reactive matcher already makes on the same bytes (a TEXT
// frame carrying a JSON object matches, anything else does not) and two
// answers to it would be two contracts for one wire.
func RunOnFrame(ctx context.Context, source string, frame []byte, frameIsObject bool, host Host) (FrameAction, error) {
	l, done, err := call(ctx, argFrame, source, func(st *lua.LState) lua.LValue {
		if !frameIsObject {
			// D3's body rule, one shape everywhere: what does not decode as
			// an object arrives as the raw string rather than as a table
			// the author would have to guess the shape of.
			return lua.LString(frame)
		}
		return bodyValue(st, frame)
	}, host)
	if err != nil {
		return FrameAction{}, err
	}
	defer done()

	if l.GetTop() == 0 {
		return FrameAction{}, nil
	}
	switch v := l.Get(1).(type) {
	case *lua.LNilType:
		return FrameAction{}, nil
	case lua.LString:
		switch string(v) {
		case FrameReply:
			return frameReply(l)
		case FrameClose:
			return frameClose(l)
		default:
			return FrameAction{}, fmt.Errorf("%w: an onFrame hook must return nil, %q or %q, got %q",
				ErrFailed, FrameReply, FrameClose, string(v))
		}
	default:
		return FrameAction{}, fmt.Errorf("%w: an onFrame hook must return nil, %q or %q, got %s",
			ErrFailed, FrameReply, FrameClose, v.Type())
	}
}

func frameReply(l *lua.LState) (FrameAction, error) {
	switch v := l.Get(2).(type) {
	case lua.LString:
		return FrameAction{Verb: FrameReply, Data: []byte(v)}, nil
	case *lua.LTable:
		encoded, err := marshalLua(v)
		if err != nil {
			return FrameAction{}, fmt.Errorf("%w: the reply could not be encoded as JSON: %s", ErrFailed, err.Error())
		}
		return FrameAction{Verb: FrameReply, Data: encoded}, nil
	default:
		return FrameAction{}, fmt.Errorf("%w: a %q must carry a table or a string, got %s", ErrFailed, FrameReply, v.Type())
	}
}

func frameClose(l *lua.LState) (FrameAction, error) {
	code, ok := l.Get(2).(lua.LNumber)
	if !ok {
		return FrameAction{}, fmt.Errorf("%w: a %q must carry a numeric code, got %s", ErrFailed, FrameClose, l.Get(2).Type())
	}
	c := int(code)
	// The identical range a reactive rule's close is validated against at
	// WRITE time (customep.RuleClose) — checked again here because this one
	// is computed per frame and no write-time check can see it.
	if float64(c) != float64(code) || (c != 1000 && (c < 4000 || c > 4999)) {
		return FrameAction{}, fmt.Errorf("%w: got %v", ErrBadClose, code)
	}
	act := FrameAction{Verb: FrameClose, Code: c}
	if reason, ok := l.Get(3).(lua.LString); ok {
		act.Reason = string(reason)
	}
	return act, nil
}
