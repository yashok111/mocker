package luafn

import (
	"math/rand"
	"sort"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// The sandbox is built ALLOWLIST-ONLY, never by stripping a fully opened
// state: `lua.NewState` with SkipOpenLibs preloads NOTHING, five libraries are
// opened by name, and the names those five register that this product will not
// serve are then removed one by one. A18 D3.
//
// Why not a strip list over a default state: `OpenBase` alone registers
// twenty-eight names, and four of them (`loadstring`, `module`, `newproxy`,
// `require`) are not in the Lua 5.1 manual's own base-library table — a
// strip list written from the manual misses them. Read out of
// gopher-lua v1.1.2's own `baselib.go` on 2026-09-04, which is also where the
// five divergences from the gate document below were found.

// openLibs is the whole of what is opened. `io`, `package`, `debug`,
// `coroutine` and `channel` are NEVER opened (D3 step 5): a coroutine can
// launder an infinite loop past a single SetContext, and the other four reach
// the host outright.
var openLibs = []struct {
	name string
	open lua.LGFunction
}{
	{lua.BaseLibName, lua.OpenBase},
	{lua.StringLibName, lua.OpenString},
	{lua.TabLibName, lua.OpenTable},
	{lua.MathLibName, lua.OpenMath},
	{lua.OsLibName, lua.OpenOs},
}

// removedGlobals are the names `OpenBase` registers and this sandbox takes
// back. Five of them are the gate's own list; the two `*fenv` names are a
// DIVERGENCE the gate could not see, because gopher-lua's source is not in
// this tree and the gate reviewed a design rather than a library:
//
//   - `getfenv`/`setfenv` are Lua 5.1's environment manipulators. With `load`
//     and `loadstring` gone they reach nothing new — an environment swap needs
//     a function to compile into it — but the whole point of this construction
//     is that there is ONE environment, and a name whose only purpose is to
//     change that is removed rather than argued about.
//   - `collectgarbage` calls process-wide `runtime.GC()` in gopher-lua, so one
//     endpoint's function would impose GC latency on every other request.
//
// `rawget`, `rawset` and `rawequal` deliberately STAY (D3 step 3a): they
// bypass metatables and reach nothing outside the state, and a global this
// list removed is gone from the table rather than hidden behind a metatable,
// so `rawget(_G, "load")` returns the same nil an ordinary index does.
// `rawlen` is NOT in this list because gopher-lua does not have it — it is a
// Lua 5.2 name, and the gate document names it in error.
var removedGlobals = []string{
	"collectgarbage",
	"dofile",
	"getfenv",
	"load",
	"loadfile",
	"loadstring",
	"module",
	"newproxy",
	"print",
	"require",
	"setfenv",
}

// keptOS is the whole of what survives in `os`. Everything else registered by
// `OpenOs` is removed, and `setenv` is the DIVERGENCE: gopher-lua registers a
// WRITER to the process environment that the gate document's removal list does
// not name. `difftime` is arithmetic over two numbers and reaches nothing, so
// it stays beside `time` and `clock` rather than being removed for symmetry.
var keptOS = map[string]bool{
	"clock":    true,
	"date":     true,
	"difftime": true,
	"time":     true,
}

// removedString is `string.dump`, and it is here rather than in a comment
// because the gate document says it is "unimplemented" and the acceptance
// clause asserts it evaluates to nil. Both are half true: gopher-lua REGISTERS
// `dump` and its body raises "GopherLua does not support the string.dump", so
// a clause asserting nil goes red against an implementation that changed
// nothing. Removing it makes the clause true and takes an error-raising stub
// out of a surface that has no use for it.
var removedString = []string{"dump"}

// newState builds one sandboxed VM. Every call gets a fresh one: D3's
// statelessness is by construction rather than by cleanup, and D10 refuses VM
// pooling on that guarantee's account.
func newState() *lua.LState {
	l := lua.NewState(lua.Options{SkipOpenLibs: true})
	for _, lib := range openLibs {
		l.Push(l.NewFunction(lib.open))
		l.Push(lua.LString(lib.name))
		l.Call(1, 0)
	}

	for _, name := range removedGlobals {
		l.SetGlobal(name, lua.LNil)
	}
	if os, ok := l.GetGlobal("os").(*lua.LTable); ok {
		var drop []string
		os.ForEach(func(k, _ lua.LValue) {
			if name, ok := k.(lua.LString); ok && !keptOS[string(name)] {
				drop = append(drop, string(name))
			}
		})
		for _, name := range drop {
			os.RawSetString(name, lua.LNil)
		}
	}
	if str, ok := l.GetGlobal("string").(*lua.LTable); ok {
		for _, name := range removedString {
			str.RawSetString(name, lua.LNil)
		}
	}

	installRNG(l)
	pinDateToUTC(l)
	return l
}

// installRNG replaces `math.random` with a host closure over a per-VM
// generator and removes `math.randomseed`.
//
// This is an OVERRIDE and not a seeding, because seeding is unimplementable
// here: gopher-lua's `mathRandom` calls Go's PACKAGE-GLOBAL `math/rand` and
// `mathRandomseed` calls the global `rand.Seed` (mathlib.go:186-205, read
// 2026-09-04). A "seed per VM" would therefore reach across every concurrent
// request in the process — and, worse, a Lua call would advance a generator
// the rest of the binary shares. A18 D3.
// It is an override and not a seeding for a SECOND reason this project only
// found by running it: on Go 1.27 `rand.Seed` is a no-op — a probe on
// 2026-09-04 seeded the global twice with the same value and drew two
// different numbers — so gopher-lua's `mathRandomseed` does not even do the
// shared-state thing it looks like it does. It changes nothing. A sandbox that
// "seeded per VM" through that path would have shipped a knob that turns
// nothing.
//
// newRandSource is a package var so the package's own test can make a VM's
// generator deterministic and observe that two VMs do not share one. The same
// shape internal/stream keeps for maxStreamLifetime, and for the same reason:
// the alternative is a test that compares two unpredictable sequences and
// proves nothing.
var newRandSource = func() *rand.Rand {
	// Seeded from the clock rather than from the workspace seed: D4 puts a
	// function-bearing endpoint OUT of the determinism guarantee, honestly and
	// on the owner's word.
	return rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // G404: a mock's body values, never a credential — the workspace seed is this product's determinism knob and D4 puts functions outside it
}

func installRNG(l *lua.LState) {
	src := newRandSource()
	random := func(state *lua.LState) int {
		switch state.GetTop() {
		case 0:
			state.Push(lua.LNumber(src.Float64()))
		case 1:
			n := state.CheckInt(1)
			if n < 1 {
				state.RaiseError("bad argument #1 to 'random' (interval is empty)")
				return 0
			}
			state.Push(lua.LNumber(src.Intn(n) + 1))
		default:
			lo, hi := state.CheckInt(1), state.CheckInt(2)
			if hi < lo {
				state.RaiseError("bad argument #2 to 'random' (interval is empty)")
				return 0
			}
			state.Push(lua.LNumber(src.Intn(hi-lo+1) + lo))
		}
		return 1
	}
	if math, ok := l.GetGlobal("math").(*lua.LTable); ok {
		math.RawSetString("random", l.NewFunction(random))
		math.RawSetString("randomseed", lua.LNil)
	}
}

// pinDateToUTC makes `os.date` format in UTC whatever the process timezone
// is: the same function on two machines must not answer differently because
// one of them runs with TZ set. A18 D3.
//
// It WRAPS gopher-lua's own `osDate` rather than reimplementing it — the
// library already honours Lua's leading `!` as "UTC", so the whole of the pin
// is to guarantee that prefix. Reimplementing would mean a second strftime in
// this tree, and the one place the two could disagree is exactly the output
// this pin exists to make identical.
//
// The no-argument call is why the wrapper supplies a format instead of only
// prefixing one: gopher-lua reads `isUTC` inside its `GetTop() >= 1` branch,
// so `os.date()` with no argument formats in LOCAL time and the `!` never
// reaches it (oslib.go, read 2026-09-04). Passing `!%c` — Lua's own default
// format — puts every call through the UTC branch.
func pinDateToUTC(l *lua.LState) {
	os, ok := l.GetGlobal("os").(*lua.LTable)
	if !ok {
		return
	}
	inner, ok := os.RawGetString("date").(*lua.LFunction)
	if !ok {
		return
	}
	utc := func(state *lua.LState) int {
		format := state.OptString(1, "%c")
		if !strings.HasPrefix(format, "!") {
			format = "!" + format
		}
		args := []lua.LValue{lua.LString(format)}
		if state.GetTop() >= 2 {
			args = append(args, lua.LNumber(state.CheckNumber(2)))
		}
		state.Push(inner)
		for _, a := range args {
			state.Push(a)
		}
		state.Call(len(args), 1)
		return 1
	}
	os.RawSetString("date", l.NewFunction(utc))
}

// globalNames is what a test pins against a frozen allowlist: a gopher-lua
// upgrade that registers a new global fails the build rather than a customer.
// Sorted so the failure message is readable.
func globalNames(l *lua.LState) []string {
	var names []string
	if g, ok := l.Get(lua.GlobalsIndex).(*lua.LTable); ok {
		g.ForEach(func(k, _ lua.LValue) {
			if name, ok := k.(lua.LString); ok {
				names = append(names, string(name))
			}
		})
	}
	sort.Strings(names)
	return names
}
