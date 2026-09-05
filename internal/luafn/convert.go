package luafn

import (
	"errors"
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
		// RawSetInt and not Append: gopher-lua's Append is a documented
		// no-op for LNil (table.go, `if value == LNil { return }`), so
		// `[1, null, 3]` appended would arrive as `{1, 3}` — the null gone
		// and the third element moved to index 2, which a function that
		// pairs arrays positionally computes on without an error. A nil at
		// a numeric index is a HOLE, and Lua's `#` on a table with holes
		// is any border; the request-body doc says so. Review finding 7.
		for i, item := range value {
			t.RawSetInt(i+1, goToLua(l, item))
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

// maxTableDepth bounds how deep a Lua table may nest before the converter
// refuses it. The converter is Go recursion over a structure the FUNCTION
// built, so the depth is the one resource the sandbox's other bounds do not
// reach: the instruction-level context check cannot interrupt a Go frame, and
// a Go stack that hits its 1 GB ceiling is `fatal error: stack overflow` —
// unrecoverable, outside every recover, the whole process gone with both
// planes and every open stream. Sixty-four is deeper than any response a
// mock has a reason to build and shallower than anything that could matter
// to the stack. Review finding 1.
const maxTableDepth = 64

// The two refusals the converter can make, wrapped by every caller into
// ErrFailed with the caller's own contract named ("the body …", "the tick
// body …").
var (
	errTableCycle = errors.New("a table refers to itself")
	errTableDepth = errors.New("a table nests deeper than " + strconv.Itoa(maxTableDepth) + " levels")
)

// luaToGo turns a Lua value into something jsonx can marshal.
//
// A table is an ARRAY when its keys are exactly 1..n and an OBJECT otherwise,
// which is Lua's own convention and the only one a function author will
// expect. A mixed table — array part plus string keys — becomes an object with
// the numeric keys rendered as strings, because dropping half the data
// silently is worse than a shape the author can see in the response.
//
// It refuses a table that contains itself and a table nested past
// maxTableDepth; see that constant for why both are refusals and not
// truncations. The guard is the set of tables on the CURRENT PATH and not a
// visited set, because the same table referenced twice from two siblings is a
// legal shape (`local u = {…}; return 200, {a = u, b = u}`) and encodes as two
// copies, exactly as JSON has to render it.
func luaToGo(v lua.LValue) (any, error) {
	c := converter{active: map[*lua.LTable]bool{}}
	return c.value(v, 0)
}

type converter struct {
	active map[*lua.LTable]bool
}

func (c *converter) value(v lua.LValue, depth int) (any, error) {
	switch value := v.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(value), nil
	case lua.LNumber:
		f := float64(value)
		if f == math.Trunc(f) && !math.IsInf(f, 0) && math.Abs(f) < 1<<53 {
			return int64(f), nil
		}
		return f, nil
	case lua.LString:
		return string(value), nil
	case *lua.LTable:
		return c.table(value, depth)
	default:
		// A function, a userdata or a thread has no JSON rendering. Nil is
		// the honest answer and `readReturn` has already refused the shapes
		// where it would be a surprise.
		return nil, nil
	}
}

func (c *converter) table(t *lua.LTable, depth int) (any, error) {
	if depth >= maxTableDepth {
		return nil, errTableDepth
	}
	if c.active[t] {
		return nil, errTableCycle
	}
	c.active[t] = true
	defer delete(c.active, t)

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
			item, err := c.value(t.RawGetInt(i), depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, item)
		}
		return arr, nil
	}

	obj := map[string]any{}
	var failed error
	t.ForEach(func(k, v lua.LValue) {
		if failed != nil {
			return
		}
		var name string
		switch key := k.(type) {
		case lua.LString:
			name = string(key)
		case lua.LNumber:
			name = strconv.FormatFloat(float64(key), 'f', -1, 64)
		default:
			return
		}
		item, err := c.value(v, depth+1)
		if err != nil {
			failed = err
			return
		}
		obj[name] = item
	})
	if failed != nil {
		return nil, failed
	}
	return obj, nil
}

// tableToGo is luaToGo for a value already known to be a table; mock.jwt's
// claims argument is its one caller.
func tableToGo(t *lua.LTable) (any, error) { return luaToGo(t) }

// marshalLua encodes a Lua value as JSON through this package's own
// converter. It is one function rather than three call sites writing
// jsonx.Marshal(luaToGo(v)) because the three contracts (a response body, a
// tick frame, an onFrame reply) must agree byte for byte about what a Lua
// table becomes — including the sorted object keys luaToGo already imposes,
// which is what keeps two encodings of one table identical.
func marshalLua(v lua.LValue) ([]byte, error) {
	converted, err := luaToGo(v)
	if err != nil {
		return nil, err
	}
	return jsonx.Marshal(converted)
}
