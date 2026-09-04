package luafn

import (
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
func installMock(l *lua.LState, host Host) {
	mock := l.NewTable()
	mock.RawSetString("now", l.NewFunction(mockNow))
	mock.RawSetString("jwt", l.NewFunction(mockJWT(host)))
	mock.RawSetString("entities", l.NewFunction(mockEntities(host)))
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

func mockJWT(host Host) lua.LGFunction {
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
		token, err := host.JWT(claims)
		if err != nil {
			l.Push(lua.LNil)
			l.Push(lua.LString(Note(err)))
			return 2
		}
		l.Push(lua.LString(token))
		return 1
	}
}

func mockEntities(host Host) lua.LGFunction {
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
		var scope []string
		if t, ok := l.Get(2).(*lua.LTable); ok {
			t.ForEach(func(_, v lua.LValue) {
				scope = append(scope, v.String())
			})
		}

		rows, err := host.Entities(family, scope)
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
