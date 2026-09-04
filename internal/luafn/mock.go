package luafn

import (
	"context"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// mockTableKeys is the whole of what `mock` holds, pinned here and asserted by
// a test against this literal — the same discipline the frozen `_G` allowlist
// keeps, and for the same reason: a grep for a `mock.write`-shaped name would
// pass over a writer called anything else, while a key set cannot.
//
// There is no writer, deliberately: the anonymous mock plane's own POST and
// DELETE verbs remain the only things that create or delete an entity row
// (D3).
var mockTableKeys = []string{"entities", "jwt", "now"}

// installMock puts the three helpers on the state. A nil Host is legal — a
// preview has no live workspace behind it — and then the two helpers that need
// one answer their own error rather than being absent, so a function written
// against a real workspace still runs in a preview and reports why.
//
// ctx is the INVOCATION's, and it is passed rather than read back off the
// state (D6: "threaded, not assumed"). Run derives it, hands it to SetContext
// and hands the same value here, so the two cannot disagree — and a reader
// following the deadline from Run to the store read sees it in the signature
// instead of having to know that lua.LState carries one.
func installMock(l *lua.LState, ctx context.Context, host Host) {
	mock := l.NewTable()
	mock.RawSetString("now", l.NewFunction(mockNow))
	mock.RawSetString("jwt", l.NewFunction(mockJWT(ctx, host)))
	mock.RawSetString("entities", l.NewFunction(mockEntities(ctx, host)))
	l.SetGlobal("mock", mock)
}

// mockNow is the REAL clock, and D4 puts it out of the determinism guarantee
// on the owner's own word: a token that expires in an hour has to expire in an
// hour of somebody's actual day.
func mockNow(l *lua.LState) int {
	offset := int64(l.OptNumber(1, 0))
	l.Push(lua.LNumber(time.Now().Unix() + offset))
	return 1
}

func mockJWT(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			l.Push(lua.LNil)
			l.Push(lua.LString("no_host"))
			return 2
		}
		claims := map[string]any{}
		if t, ok := l.Get(1).(*lua.LTable); ok {
			if obj, ok := tableToGo(t).(map[string]any); ok {
				claims = obj
			}
		}
		token, err := host.JWT(ctx, claims)
		if err != nil {
			l.Push(lua.LNil)
			l.Push(lua.LString(Note(err)))
			return 2
		}
		l.Push(lua.LString(token))
		return 1
	}
}

func mockEntities(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			l.Push(lua.LNil)
			l.Push(lua.LString("no_host"))
			return 2
		}
		family := l.CheckString(1)

		// The scope arrives as RAW values and the HOST encodes them. A second
		// encoder here would be one a UNIQUE index could disagree with, which
		// is the rule resources.EncodeScope already owns for every other
		// caller.
		//
		// Read by INDEX, not through ForEach: a scope is an ORDERED ancestor
		// tuple — {"7", "5"} and {"5", "7"} are two different families' worth
		// of rows — and ForEach walks gopher-lua's hash part in map order once
		// a table has one, so a table built any way other than a plain literal
		// could hand the host its values shuffled. Len/RawGetInt is the array
		// part, positionally, which is the only reading that carries the
		// meaning.
		var scope []string
		if t, ok := l.Get(2).(*lua.LTable); ok {
			n := t.Len()
			scope = make([]string, 0, n)
			for i := 1; i <= n; i++ {
				scope = append(scope, t.RawGetInt(i).String())
			}
		}

		rows, err := host.Entities(ctx, family, scope)
		if err != nil {
			l.Push(lua.LNil)
			l.Push(lua.LString(Note(err)))
			return 2
		}
		out := l.NewTable()
		for _, row := range rows {
			out.Append(goToLua(l, row))
		}
		l.Push(out)
		return 1
	}
}
