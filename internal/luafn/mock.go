package luafn

import (
	"context"
	"strconv"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// mockTableKeys is the whole of what `mock` holds, pinned here and asserted by
// a test against this literal — the same discipline the frozen `_G` allowlist
// keeps, and for the same reason: a grep for a `mock.write`-shaped name would
// pass over a writer called anything else, while a key set cannot.
//
// A19 adds one key, `generate`, and a writer that lives under `entities`
// rather than as a key of its own: `mock.entities` is a callable table —
// `mock.entities(family)` still reads, and `.create/.update/.delete` write —
// and a second test pins those sub-keys ([entitiesTableKeys]). The owner
// asked for both on 2026-09-05 («давай сделаем 1 и 2», a Russian string
// quoted as data), reversing A18 D3's "no writer": the mock plane's own
// POST/DELETE verbs are no longer the only things that create or delete an
// entity row, and the two doors share one store and one set of caps.
var mockTableKeys = []string{"entities", "generate", "jwt", "now"}

// entitiesTableKeys pins what `mock.entities` carries beside its `__call`.
var entitiesTableKeys = []string{"create", "delete", "update"}

// installMock puts the helpers on the state. A nil Host is legal — a preview
// has no live workspace behind it — and then every helper that needs one
// answers its own error rather than being absent, so a function written
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
	mock.RawSetString("generate", l.NewFunction(mockGenerate(ctx, host)))

	// `mock.entities` is a table whose __call is the reader, so the A18
	// spelling `mock.entities("/x")` is unchanged and the three writers hang
	// off it as fields. The metatable is set on the table itself and not
	// exposed: a function cannot reach it (getmetatable/setmetatable are in
	// base and could, but a metatable on a host-owned table is not a
	// sandbox boundary — the host closures behind it are).
	entities := l.NewTable()
	entities.RawSetString("create", l.NewFunction(mockEntityCreate(ctx, host)))
	entities.RawSetString("update", l.NewFunction(mockEntityUpdate(ctx, host)))
	entities.RawSetString("delete", l.NewFunction(mockEntityDelete(ctx, host)))
	meta := l.NewTable()
	meta.RawSetString("__call", l.NewFunction(mockEntitiesCall(ctx, host)))
	l.SetMetatable(entities, meta)
	mock.RawSetString("entities", entities)

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

// fail is the one shape every helper's refusal takes: nil plus a reason
// string, the reason already trimmed to what a note may carry.
func fail(l *lua.LState, err error) int {
	l.Push(lua.LNil)
	l.Push(lua.LString(Note(err)))
	return 2
}

func failNoHost(l *lua.LState) int {
	l.Push(lua.LNil)
	l.Push(lua.LString("no_host"))
	return 2
}

// tableArg converts the table at stack index i into a JSON object, or
// answers (nil, false) when it is not a table or does not convert; the caller
// decides whether that is a default (jwt's empty claims) or a refusal.
func tableArg(l *lua.LState, i int) (obj map[string]any, ok bool, err error) {
	t, isTable := l.Get(i).(*lua.LTable)
	if !isTable {
		return nil, false, nil
	}
	converted, err := tableToGo(t)
	if err != nil {
		return nil, true, err
	}
	obj, isObj := converted.(map[string]any)
	if !isObj {
		return nil, false, nil
	}
	return obj, true, nil
}

func mockJWT(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			return failNoHost(l)
		}
		claims := map[string]any{}
		if obj, ok, err := tableArg(l, 1); err != nil {
			// A cyclic or too-deep claims table is answered like any
			// other host refusal — nil plus a reason — and not by
			// signing whatever prefix survived.
			return fail(l, err)
		} else if ok {
			claims = obj
		}
		token, err := host.JWT(ctx, claims)
		if err != nil {
			return fail(l, err)
		}
		l.Push(lua.LString(token))
		return 1
	}
}

// mockGenerate is A19's first helper: a body generated from a schema, the way
// a generated variant's is, handed back as a table for the function to edit.
// Two argument forms, the owner's choice: a string is a JSON pointer into the
// BOUND spec (`"#/components/schemas/User"`, becoming `{"$ref": ptr}` so the
// generator resolves it through the same resolver an inline custom-endpoint
// schema's refs go through), a table is an inline JSON Schema. Anything else
// is `bad_schema`; a pointer that does not start with `#/` is too, because
// there is no document a bare word could name.
//
// The value is deterministic per (workspace seed, request, schema) exactly as
// a generated response is — two calls with the same schema in one function
// return the same table, and a function that wants two different users asks
// for an array of two. This is the generator's own contract, not this
// package's addition, and it is written in the guide.
func mockGenerate(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			return failNoHost(l)
		}
		var schema map[string]any
		switch arg := l.Get(1).(type) {
		case lua.LString:
			if len(arg) < 3 || arg[:2] != "#/" {
				return fail(l, errBadSchema)
			}
			schema = map[string]any{"$ref": string(arg)}
		case *lua.LTable:
			obj, ok, err := tableArg(l, 1)
			if err != nil {
				return fail(l, err)
			}
			if !ok {
				return fail(l, errBadSchema)
			}
			schema = obj
		default:
			return fail(l, errBadSchema)
		}
		value, err := host.Generate(ctx, schema)
		if err != nil {
			return fail(l, err)
		}
		l.Push(goToLua(l, value))
		return 1
	}
}

// scopeArg reads the optional scope tuple at stack index i.
//
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
func scopeArg(l *lua.LState, i int) []string {
	t, ok := l.Get(i).(*lua.LTable)
	if !ok {
		return nil
	}
	n := t.Len()
	scope := make([]string, 0, n)
	for j := 1; j <= n; j++ {
		scope = append(scope, t.RawGetInt(j).String())
	}
	return scope
}

// keyArg reads an entity key at stack index i: a string as is, a whole
// number as its decimal text (an author writing `mock.entities.update("/x",
// 7, …)` means the row whose id is 7, and making them quote it would be a
// refusal for no reason). Anything else is `bad_key`; whether the text is a
// legal key for the family's id type is the host's question.
func keyArg(l *lua.LState, i int) (string, bool) {
	switch v := l.Get(i).(type) {
	case lua.LString:
		return string(v), v != ""
	case lua.LNumber:
		f := float64(v)
		if f != float64(int64(f)) {
			return "", false
		}
		return strconv.FormatInt(int64(f), 10), true
	default:
		return "", false
	}
}

// mockEntitiesCall is `mock.entities(family[, scope])` — the A18 reader,
// reached through the table's __call. Argument 1 is the table itself.
func mockEntitiesCall(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			return failNoHost(l)
		}
		family := l.CheckString(2)
		rows, err := host.Entities(ctx, family, scopeArg(l, 3))
		if err != nil {
			return fail(l, err)
		}
		out := l.NewTable()
		for i, row := range rows {
			out.RawSetInt(i+1, goToLua(l, row))
		}
		l.Push(out)
		return 1
	}
}

// mockEntityCreate is `mock.entities.create(family, data[, scope])` → the
// stored row, id assigned by the family's own strategy — the identical write
// the mock plane's anonymous POST performs, through the same store and under
// the same caps, so a function cannot create what a POST could not.
func mockEntityCreate(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			return failNoHost(l)
		}
		family := l.CheckString(1)
		data, ok, err := tableArg(l, 2)
		if err != nil {
			return fail(l, err)
		}
		if !ok {
			return fail(l, errBadData)
		}
		row, err := host.EntityCreate(ctx, family, scopeArg(l, 3), data)
		if err != nil {
			return fail(l, err)
		}
		l.Push(goToLua(l, row))
		return 1
	}
}

// mockEntityUpdate is `mock.entities.update(family, key, patch[, scope])` →
// the row after a SHALLOW merge of patch over it, or `nil, "not_found"`. A
// key absent from patch is untouched and a key cannot be removed — a Lua nil
// in a table constructor is an absent key, not a value — which the guide
// says; an author who needs a field gone writes the whole row through
// create after delete.
func mockEntityUpdate(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			return failNoHost(l)
		}
		family := l.CheckString(1)
		key, ok := keyArg(l, 2)
		if !ok {
			return fail(l, errBadKey)
		}
		patch, isTable, err := tableArg(l, 3)
		if err != nil {
			return fail(l, err)
		}
		if !isTable {
			return fail(l, errBadData)
		}
		row, err := host.EntityUpdate(ctx, family, scopeArg(l, 4), key, patch)
		if err != nil {
			return fail(l, err)
		}
		l.Push(goToLua(l, row))
		return 1
	}
}

// mockEntityDelete is `mock.entities.delete(family, key[, scope])` → true
// when a row went, false when there was none; an error only for a family or
// scope the host cannot name.
func mockEntityDelete(ctx context.Context, host Host) lua.LGFunction {
	return func(l *lua.LState) int {
		if host == nil {
			return failNoHost(l)
		}
		family := l.CheckString(1)
		key, ok := keyArg(l, 2)
		if !ok {
			return fail(l, errBadKey)
		}
		deleted, err := host.EntityDelete(ctx, family, scopeArg(l, 3), key)
		if err != nil {
			return fail(l, err)
		}
		l.Push(lua.LBool(deleted))
		return 1
	}
}
