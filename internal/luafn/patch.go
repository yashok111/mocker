// patch.go is the response-patch contract: one upstream response body in,
// one patched body out. It is the fourth contract over [call] — beside the
// request function (`req`), the tick (`ordinal`) and the onFrame hook
// (`frame`) — and it exists so a proxied response can be reshaped on its way
// to the client without a second Lua integration: the same sandbox, the same
// fresh VM per call, the same deadline and the same return-encoding rules.
//
// What a patch deliberately does NOT receive is the request: the contract is
// "reshape THIS body", and the operation's own policy already decides where
// the function applies. Two arguments bind — `body` and `status` — so a
// function can decline to touch an error response it did not produce.
package luafn

import (
	"context"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// patchArgs is the argument list bound by [wrapAs] for this contract. Two
// names, not one, because a patch that must skip a 500 the upstream produced
// needs the status beside the body — and no more, because the request is the
// one thing this contract is not about.
const patchArgs = "body, status"

// ValidatePatch compiles a patch body under the patch wrapper. It is a
// sibling of [Validate] and [ValidateHook], not a reuse of either: a source
// that reads `status` compiles only as a PATCH, and compiling it under the
// request wrapper would accept a function that reads a nil global at runtime
// — the same silence the hook validator's comment records.
func ValidatePatch(source string) error {
	l := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer l.Close()
	if _, err := l.LoadString(wrapAs(patchArgs, source)); err != nil {
		return fmt.Errorf("%w: %s", ErrFailed, err.Error())
	}
	return nil
}

// RunPatch executes one patch against one upstream response body.
//
// body arrives under D3's one shape rule — JSON decodes to a Lua value, an
// empty body arrives as nil, anything else as the raw string — and status is
// the upstream response's own code. The return is the tick's decline shape:
// a table becomes JSON, a string is raw bytes, and nil or NOTHING means
// "unchanged", which is the ordinary way to write "not this response". Any
// other shape is a refusal by name, exactly as [readReturn] refuses it.
//
// changed false with a nil error is the UNCHANGED outcome, distinct from an
// error on purpose (the tick's acceptance-clause reasoning): a patch that
// declined and a patch that failed are two things the caller reports
// differently — one serves the body as it arrived, the other serves it with
// a failure mark.
func RunPatch(ctx context.Context, source string, body []byte, status int, host Host) (patched []byte, changed bool, err error) {
	l, done, err := call(ctx, patchArgs, source, func(st *lua.LState) []lua.LValue {
		return []lua.LValue{bodyValue(st, body), lua.LNumber(status)}
	}, host)
	if err != nil {
		return nil, false, err
	}
	defer done()

	if l.GetTop() == 0 {
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
			return nil, false, fmt.Errorf("%w: the patched body could not be encoded as JSON: %s", ErrFailed, mErr.Error())
		}
		return encoded, true, nil
	default:
		return nil, false, fmt.Errorf("%w: a patch must return a table, a string or nil, got %s", ErrFailed, v.Type())
	}
}
