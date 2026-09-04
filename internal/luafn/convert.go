package luafn

import (
	"math"
	"sort"
	"strconv"

	lua "github.com/yuin/gopher-lua"

	"github.com/yashok111/mocker/internal/jsonx"
)

// The two conversions between decoded JSON and Lua values, and the one place
// the asymmetry between them is decided.
//
// Lua has ONE container type, so a round trip is not the identity: an empty
// table is both `{}` and `[]` and this package has to choose. It chooses the
// OBJECT, because a function that returns an empty container almost always
// built it with string keys and because an object is the shape a schema-bearing
// endpoint declares. A function that needs an empty ARRAY says so by returning
// the string "[]" — the raw-string path exists for exactly this kind of
// escape.

// goToLua turns a decoded JSON value into a Lua value.
func goToLua(l *lua.LState, v any) lua.LValue {
	switch value := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(value)
	case float64:
		return lua.LNumber(value)
	case jsonx.Number:
		if f, err := value.Float64(); err == nil {
			return lua.LNumber(f)
		}
		return lua.LString(value.String())
	case string:
		return lua.LString(value)
	case []any:
		t := l.NewTable()
		for _, item := range value {
			t.Append(goToLua(l, item))
		}
		return t
	case map[string]any:
		t := l.NewTable()
		// Sorted so a body built from a decoded request is stable across
		// runs: Go randomises map traversal, and this package's own output
		// would otherwise differ between two identical calls for a reason
		// that has nothing to do with D4's honest non-determinism.
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.RawSetString(k, goToLua(l, value[k]))
		}
		return t
	default:
		return lua.LNil
	}
}

// luaToGo turns a Lua value into something jsonx can marshal.
//
// A table is an ARRAY when its keys are exactly 1..n and an OBJECT otherwise,
// which is Lua's own convention and the only one a function author will
// expect. A mixed table — array part plus string keys — becomes an object with
// the numeric keys rendered as strings, because dropping half the data
// silently is worse than a shape the author can see in the response.
func luaToGo(v lua.LValue) any {
	switch value := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(value)
	case lua.LNumber:
		f := float64(value)
		if f == math.Trunc(f) && !math.IsInf(f, 0) && math.Abs(f) < 1<<53 {
			return int64(f)
		}
		return f
	case lua.LString:
		return string(value)
	case *lua.LTable:
		return tableToGo(value)
	default:
		// A function, a userdata or a thread has no JSON rendering. Nil is
		// the honest answer and `readReturn` has already refused the shapes
		// where it would be a surprise.
		return nil
	}
}

func tableToGo(t *lua.LTable) any {
	maxIndex, count, stringKeys := 0, 0, false
	t.ForEach(func(k, _ lua.LValue) {
		count++
		if n, ok := k.(lua.LNumber); ok && float64(n) == math.Trunc(float64(n)) && n >= 1 {
			if int(n) > maxIndex {
				maxIndex = int(n)
			}
			return
		}
		stringKeys = true
	})

	if !stringKeys && maxIndex == count && count > 0 {
		arr := make([]any, 0, count)
		for i := 1; i <= count; i++ {
			arr = append(arr, luaToGo(t.RawGetInt(i)))
		}
		return arr
	}

	obj := map[string]any{}
	t.ForEach(func(k, v lua.LValue) {
		switch key := k.(type) {
		case lua.LString:
			obj[string(key)] = luaToGo(v)
		case lua.LNumber:
			obj[strconv.FormatFloat(float64(key), 'f', -1, 64)] = luaToGo(v)
		}
	})
	return obj
}
