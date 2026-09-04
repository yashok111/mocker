package luafn

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// frozenGlobals is the WHOLE surviving `_G` key set, pinned against a literal
// rather than checked for absences. A18 D3 step 6, and the discipline is the
// point: an absence list cannot see a name nobody thought of, so a gopher-lua
// upgrade that registers a new global has to fail this build rather than a
// customer.
//
// `rawequal`, `rawget` and `rawset` are here DELIBERATELY (D3 step 3a): they
// bypass metatables and reach nothing outside the state. `rawlen` is NOT here
// and never was — it is a Lua 5.2 name and gopher-lua implements 5.1, which is
// a divergence from the gate document, found by reading baselib.go.
var frozenGlobals = []string{
	"_G", "_GOPHER_LUA_VERSION", "_VERSION", "_printregs", "assert",
	"error", "getmetatable", "ipairs", "math", "mock", "next", "os",
	"pairs", "pcall", "rawequal", "rawget", "rawset", "select",
	"setmetatable", "string", "table", "tonumber", "tostring", "type",
	"unpack", "xpcall",
}

func TestSandbox_globalsMatchTheFrozenAllowlist(t *testing.T) {
	l := newState()
	defer l.Close()
	installMock(l, t.Context(), nil)

	got := globalNames(l)
	want := append([]string(nil), frozenGlobals...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the surviving _G set changed.\n got: %v\nwant: %v\n"+
			"A name that appeared is a gopher-lua upgrade this sandbox has not decided about; "+
			"a name that vanished is a removal somebody made without moving this literal.", got, want)
	}
}

func TestSandbox_osKeepsOnlyTheFourReadOnlyNames(t *testing.T) {
	l := newState()
	defer l.Close()

	tbl, ok := l.GetGlobal("os").(*lua.LTable)
	if !ok {
		t.Fatal("os is not a table")
	}
	var got []string
	tbl.ForEach(func(k, _ lua.LValue) {
		if name, ok := k.(lua.LString); ok {
			got = append(got, string(name))
		}
	})
	sort.Strings(got)
	want := []string{"clock", "date", "difftime", "time"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("os keys = %v, want %v — `setenv` is a WRITER to the process environment "+
			"that the gate document's removal list does not name, so this literal is the record of it", got, want)
	}
}

// TestSandbox_everyRemovedNameIsNil enumerates rather than samples: the list is
// every name this sandbox takes back, and a test that spot-checks three of them
// passes over the fourth.
func TestSandbox_everyRemovedNameIsNil(t *testing.T) {
	names := []string{
		// OpenBase registers all of these; the sandbox removes them.
		"collectgarbage", "dofile", "getfenv", "load", "loadfile",
		"loadstring", "module", "newproxy", "print", "require", "setfenv",
		// Never opened at all.
		"io", "package", "debug", "coroutine", "channel",
		// Removed from their own tables.
		"os.execute", "os.exit", "os.getenv", "os.remove", "os.rename",
		"os.setenv", "os.setlocale", "os.tmpname",
		"string.dump",
		"math.randomseed",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			l := newState()
			defer l.Close()
			if err := l.DoString("if " + name + " ~= nil then error('" + name + " is reachable') end"); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

// TestSandbox_rawgetCannotResurrectARemovedGlobal is the discriminating case
// and not the vacuous one: `io` is never opened, so a rawget for it returns nil
// under every implementation, correct or broken. `load` is a name OpenBase DID
// register and the sandbox removed, so only an implementation that hid it
// behind a metatable instead of deleting it fails here.
func TestSandbox_rawgetCannotResurrectARemovedGlobal(t *testing.T) {
	l := newState()
	defer l.Close()
	if err := l.DoString(`if rawget(_G, "load") ~= nil then error("load survived rawget") end`); err != nil {
		t.Fatal(err)
	}
}

// TestSandbox_eachVMDrawsFromItsOwnGenerator is the observation a "seed per
// VM" implementation cannot pass. gopher-lua's own math.random calls Go's
// PACKAGE-GLOBAL math/rand (mathlib.go:186-205), so a sandbox that shared one
// source would have VM A's draw advance VM B's sequence.
//
// It is written against a seeded hook rather than against the process global
// because rand.Seed is a NO-OP on Go 1.27 — probed 2026-09-04, two Seed(1)
// calls drew two different numbers — so a test comparing global sequences
// compares two unpredictable things and passes or fails for no reason.
func TestSandbox_eachVMDrawsFromItsOwnGenerator(t *testing.T) {
	restore := newRandSource
	newRandSource = func() *rand.Rand { return rand.New(rand.NewSource(42)) }
	defer func() { newRandSource = restore }()

	draw := func(interleave bool) string {
		a := newState()
		defer a.Close()
		if interleave {
			b := newState()
			defer b.Close()
			if err := b.DoString(`local burn = math.random(); burn = math.random()`); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.DoString(`out = tostring(math.random()) .. "," .. tostring(math.random())`); err != nil {
			t.Fatal(err)
		}
		return a.GetGlobal("out").String()
	}

	alone, interleaved := draw(false), draw(true)
	if alone != interleaved {
		t.Fatalf("one VM's draws moved another's: alone %s, interleaved %s \u2014 the two share a generator", alone, interleaved)
	}
	if alone == "" {
		t.Fatal("math.random produced nothing")
	}
}

func TestSandbox_dateIsUTCUnderANonDefaultTimezone(t *testing.T) {
	// A NON-DEFAULT zone on purpose: under this box's own UTC a pinned
	// implementation and a process-following one emit identical bytes, so the
	// observation would pass against a sandbox that pins nothing.
	t.Setenv("TZ", "Asia/Tokyo")
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = time.UTC }()

	l := newState()
	defer l.Close()
	if err := l.DoString(`result = os.date("%H", 0)`); err != nil {
		t.Fatal(err)
	}
	if got := l.GetGlobal("result").String(); got != "00" {
		t.Fatalf("os.date(\"%%H\", 0) = %q, want \"00\" — the process zone reached a function's output", got)
	}
	// And with no argument at all, which is the call gopher-lua's own osDate
	// never routes through its UTC branch.
	if err := l.DoString(`bare = os.date()`); err != nil {
		t.Fatal(err)
	}
	if got := l.GetGlobal("bare").String(); strings.Contains(got, "JST") {
		t.Fatalf("os.date() = %q, want a UTC rendering", got)
	}
}

func TestSandbox_mockTableHoldsExactlyThreeKeys(t *testing.T) {
	l := newState()
	defer l.Close()
	installMock(l, t.Context(), nil)

	tbl, ok := l.GetGlobal("mock").(*lua.LTable)
	if !ok {
		t.Fatal("mock is not a table")
	}
	var got []string
	tbl.ForEach(func(k, _ lua.LValue) {
		if name, ok := k.(lua.LString); ok {
			got = append(got, string(name))
		}
	})
	sort.Strings(got)
	if !reflect.DeepEqual(got, mockTableKeys) {
		t.Fatalf("mock keys = %v, want %v — a writer named anything at all would pass a grep for `mock.write` and fail here", got, mockTableKeys)
	}
}

// TestSandbox_statelessAcrossInvocations is the guarantee D10 refuses VM
// pooling on account of, so it is observed rather than assumed.
func TestSandbox_statelessAcrossInvocations(t *testing.T) {
	// The stored source IS the body: `req` is a local the runner supplies,
	// and there is no `function ... end` ceremony for an author to get wrong.
	const src = `if leaked ~= nil then return 500, {saw = leaked} end
		leaked = "from the first call"
		return 200, {ok = true}`
	for i := 0; i < 2; i++ {
		resp, err := Run(context.Background(), src, Request{Method: "GET"}, nil)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if resp.Status != 200 {
			t.Fatalf("call %d saw a global set by the call before it: %s", i, resp.Body)
		}
	}
}

func TestSandbox_timeoutInterruptsAnInfiniteLoop(t *testing.T) {
	// The package const is 2 s; the caller's own deadline is what makes this
	// test fast, and the classification below is what it is really about.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Run(ctx, `while true do end`, Request{}, nil)
	if err == nil {
		t.Fatal("an infinite loop returned")
	}
	// The CALLER's context expired, so this is a client that went away, not
	// the operator's own budget: ErrCanceled and not ErrTimeout.
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the loop ran for %v after its context expired", elapsed)
	}
}
