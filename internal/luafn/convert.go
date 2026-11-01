package luafn

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

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

// goToLua turns a decoded JSON value into a Lua value, a JSON null into Lua
// nil — the rule for every input but a patch body (see [toLua]).
func goToLua(l *lua.LState, v any) lua.LValue { return toLua(l, v, lua.LNil) }

// toLua is goToLua with the Lua value a JSON null becomes passed in. Two
// callers, two answers, on purpose:
//
//   - nil (lua.LNil) for a request body, an onFrame frame, mock.state, an
//     entity row: a nil is what `req.body.x == nil` — the test every existing
//     workspace function writes — has always seen, and an object key whose
//     value is nil is simply absent, which is all those readers ask about.
//   - the VM's `mock.null` for a PATCH body ([RunPatch]): a patch re-encodes
//     the very body it was given, so a null that became nil was a null the
//     client never saw again — an object key silently dropped, `[1,null,3]`
//     re-encoded as `{"1":1,"3":3}`, `[null,null]` as `{}` (found on a real
//     budget-view response, 2026-11-01). The sentinel is a value, so the key
//     stays, the array keeps its positions, and luaToGo renders it back as
//     null.
//
// Scoping it by caller rather than switching every input to the sentinel is
// the owner's decision: a sentinel is TRUTHY in Lua, so `if req.body.x then`
// over a null field would flip for every function already written.
func toLua(l *lua.LState, v any, null lua.LValue) lua.LValue {
	switch value := v.(type) {
	case nil:
		return null
	case bool:
		return lua.LBool(value)
	case float64:
		return lua.LNumber(value)
	case int:
		// Decoded JSON never produces these two — every number is a float64
		// or a jsonx.Number — but a host built in Go may (a count, a row id
		// typed int64), and dropping such a value as "no JSON rendering"
		// would silently lose a key. A19.
		return lua.LNumber(value)
	case int64:
		return lua.LNumber(value)
	case jsonx.Number:
		return numberToLua(l, value)
	case string:
		return lua.LString(value)
	case []any:
		return arrayToLua(l, value, null)
	case map[string]any:
		return objectToLua(l, value, null)
	default:
		return lua.LNil
	}
}

// arrayToLua is toLua for a JSON array.
//
// RawSetInt and not Append: gopher-lua's Append is a documented no-op for
// LNil (table.go, `if value == LNil { return }`), so `[1, null, 3]` appended
// would arrive as `{1, 3}` — the null gone and the third element moved to
// index 2, which a function that pairs arrays positionally computes on
// without an error. A nil at a numeric index is a HOLE, and Lua's `#` on a
// table with holes is any border; the request-body doc says so. Review
// finding 7.
//
// Every decoded array carries the mock.array mark: it WAS an array, so a
// table that is empty when it goes back out — `[]` untouched, an all-null
// array where null is nil, or one the function emptied — serves `[]` and not
// the `{}` an unmarked empty table encodes as. The mark is read for the empty
// case only, so a table the function fills or keys by name is decided by
// luaToGo's ordinary rules. One small metatable per decoded array is the
// price.
func arrayToLua(l *lua.LState, value []any, null lua.LValue) *lua.LTable {
	t := l.NewTable()
	for i, item := range value {
		t.RawSetInt(i+1, toLua(l, item, null))
	}
	markArray(l, t)
	return t
}

// objectToLua is toLua for a JSON object. Keys are set sorted so a body
// built from a decoded request is stable across runs: Go randomises map
// traversal, and this package's own output would otherwise differ between
// two identical calls for a reason that has nothing to do with D4's honest
// non-determinism.
func objectToLua(l *lua.LState, value map[string]any, null lua.LValue) *lua.LTable {
	t := l.NewTable()
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.RawSetString(k, toLua(l, value[k], null))
	}
	return t
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
// Lua cannot write an empty ARRAY literal — `{}` is an object — and that one
// gap is the format's loudest trap for a schema-validating consumer. The
// escape hatch is `mock.array(t)`, which marks the table through a metatable
// field; this converter reads the mark for the EMPTY case only, and every
// other shape keeps the rules above. Restricting the mark to the empty case
// is what keeps it honest: a marked table can never lose keys to the mark,
// because the mark's only power is turning "no keys at all" into `[]`.
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
		// A big integer goes back out as its own digits. Everything else
		// here is null: nil itself, `mock.null` (whose whole meaning that
		// is), and a function, any other userdata or a thread, which have
		// no JSON rendering — nil is the honest answer and `readReturn` has
		// already refused the shapes where it would be a surprise.
		if n, ok := bigIntOf(v); ok {
			return n, nil
		}
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

	// The mock.array mark, read here and nowhere else: an EMPTY marked table
	// is the array the author said they meant, where Lua's own `{}` gives the
	// converter no way to tell. Anything non-empty is decided by the rules
	// below with the mark unread — the mark adds a shape, it never overrides
	// one.
	if count == 0 && isArrayMarked(t) {
		return []any{}, nil
	}

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

// arrayMetaKey is the metatable field mock.array sets. It lives here rather
// than in mock.go because the CONVERTER is the mark's only reader and the
// helper its only writer; one constant, two files, no third spelling.
const arrayMetaKey = "__jsarray"

// isArrayMarked answers whether t carries the mock.array mark. gopher-lua
// keeps a table's metatable on the struct itself, so the read is one type
// assertion and one field lookup — no LState, and therefore callable from
// the converter's plain-value walk.
func isArrayMarked(t *lua.LTable) bool {
	meta, ok := t.Metatable.(*lua.LTable)
	if !ok {
		return false
	}
	return meta.RawGetString(arrayMetaKey) == lua.LBool(true)
}

// jsonNull is the Value of the userdata `mock.null` is. A type and not a
// pointer: only this package can construct one, so a userdata carrying it is
// the sentinel whatever VM it came from, and the check needs no LState.
type jsonNull struct{}

// newJSONNull builds one VM's `mock.null`. One per VM, never a shared
// package value: gopher-lua's setmetatable accepts a userdata, so a shared
// sentinel would be state one function's VM could write under another's.
func newJSONNull(l *lua.LState) *lua.LUserData {
	ud := l.NewUserData()
	ud.Value = jsonNull{}
	return ud
}

// isJSONNull answers whether v is a `mock.null`.
func isJSONNull(v lua.LValue) bool {
	ud, ok := v.(*lua.LUserData)
	if !ok {
		return false
	}
	_, ok = ud.Value.(jsonNull)
	return ok
}

// numberToLua is the one place a decoded JSON number becomes a Lua value.
// Lua's only number is a float64, so an INTEGER a float64 cannot hold
// exactly — past ±2^53, an id from a 64-bit column — would come back out
// rounded (12345678901234567890 served as 12345678901234567000) even from a
// function or a patch that never touched it. Such a number becomes an opaque
// big integer instead (see newBigInt); every other number, fractions and
// exponents included, is the float it always was.
//
// A literal outside float64's RANGE (`1e400`) takes the same wrapper: it
// has no Lua number at all, and the wrapper is the one shape that re-emits
// it as the number it was rather than as a string or an infinity.
func numberToLua(l *lua.LState, n jsonx.Number) lua.LValue {
	f, err := n.Float64()
	if err != nil || beyondFloat(n.String()) {
		return newBigInt(l, n)
	}
	return lua.LNumber(f)
}

// maxExactInt is 2^53, the largest magnitude below which every integer has
// an exact float64.
const maxExactInt = 1 << 53

// beyondFloat answers whether text is an integer literal a float64 cannot
// hold exactly. Only an integer qualifies: a literal with a fraction or an
// exponent was never exact in JSON's own reading either.
func beyondFloat(text string) bool {
	if strings.ContainsAny(text, ".eE") {
		return false
	}
	i, err := strconv.ParseInt(text, 10, 64)
	return err != nil || i > maxExactInt || i < -maxExactInt
}

// bigIntType names the per-VM metatable every big integer shares; it lives
// in the VM's registry, which no sandboxed function can reach.
const bigIntType = "mocker.bigint"

// newBigInt wraps n as a userdata whose metatable gives an author exactly
// what the patch and request-body contracts promise and no more: tostring
// and `..` yield the original digits, `==` compares two big integers by
// those digits, and luaToGo re-emits them verbatim. Arithmetic and ordering
// are refused by Lua itself (a userdata is not a number); `tonumber(
// tostring(v))` is the author's explicit, rounding, way out.
func newBigInt(l *lua.LState, n jsonx.Number) *lua.LUserData {
	ud := l.NewUserData()
	ud.Value = n
	l.SetMetatable(ud, bigIntMeta(l))
	return ud
}

func bigIntMeta(l *lua.LState) lua.LValue {
	if meta := l.GetTypeMetatable(bigIntType); meta != lua.LNil {
		return meta
	}
	meta := l.NewTypeMetatable(bigIntType)
	meta.RawSetString("__tostring", l.NewFunction(func(l *lua.LState) int {
		l.Push(lua.LString(concatText(l, l.Get(1))))
		return 1
	}))
	meta.RawSetString("__concat", l.NewFunction(func(l *lua.LState) int {
		l.Push(lua.LString(concatText(l, l.Get(1)) + concatText(l, l.Get(2))))
		return 1
	}))
	meta.RawSetString("__eq", l.NewFunction(func(l *lua.LState) int {
		a, _ := bigIntOf(l.Get(1))
		b, _ := bigIntOf(l.Get(2))
		l.Push(lua.LBool(a == b))
		return 1
	}))
	return meta
}

// concatText is one operand of a `..` that has a big integer on one side:
// the digits for a big integer, Lua's own text for a string or a number, and
// Lua's own refusal for anything else.
func concatText(l *lua.LState, v lua.LValue) string {
	if n, ok := bigIntOf(v); ok {
		return n.String()
	}
	if lua.LVCanConvToString(v) {
		return lua.LVAsString(v)
	}
	l.RaiseError("attempt to concatenate a %s value", v.Type().String())
	return ""
}

// bigIntOf answers whether v is a big integer, and its digits.
func bigIntOf(v lua.LValue) (jsonx.Number, bool) {
	ud, ok := v.(*lua.LUserData)
	if !ok {
		return "", false
	}
	n, ok := ud.Value.(jsonx.Number)
	return n, ok
}

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
