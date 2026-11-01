package luafn

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidatePatch_refusesUnparseableSourceAndAcceptsBothNames(t *testing.T) {
	err := ValidatePatch("return body..(")
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if err := ValidatePatch("if status == 500 then return nil end return body"); err != nil {
		t.Fatalf("a source reading both bound names must compile: %v", err)
	}
}

func TestRunPatch_jsonBodyArrivesAsTableAndPatches(t *testing.T) {
	patched, changed, err := RunPatch(t.Context(), `body.patched = true; return body`, []byte(`{"a":1}`), 200, nil)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if string(patched) != `{"a":1,"patched":true}` {
		t.Fatalf("patched=%s", patched)
	}
}

func TestRunPatch_stringReturnIsRawBytes(t *testing.T) {
	patched, changed, err := RunPatch(t.Context(), `return "[]"`, []byte(`{"a":1}`), 200, nil)
	if err != nil || !changed || string(patched) != "[]" {
		t.Fatalf("patched=%s changed=%v err=%v", patched, changed, err)
	}
}

func TestRunPatch_nilAndNothingMeanUnchanged(t *testing.T) {
	for _, source := range []string{"return nil", "if status == 200 then return nil end"} {
		patched, changed, err := RunPatch(t.Context(), source, []byte(`{"a":1}`), 200, nil)
		if err != nil || changed || patched != nil {
			t.Fatalf("%s: patched=%s changed=%v err=%v", source, patched, changed, err)
		}
	}
	// An `if` with no else returning nothing is the decline, as with a tick.
	patched, changed, err := RunPatch(t.Context(), `if status ~= 200 then return body end`, []byte(`{"a":1}`), 200, nil)
	if err != nil || changed || patched != nil {
		t.Fatalf("decline: patched=%s changed=%v err=%v", patched, changed, err)
	}
}

func TestRunPatch_statusReachesTheFunction(t *testing.T) {
	patched, changed, err := RunPatch(t.Context(), `return {code = status}`, []byte(`{}`), 503, nil)
	if err != nil || !changed || string(patched) != `{"code":503}` {
		t.Fatalf("patched=%s changed=%v err=%v", patched, changed, err)
	}
}

func TestRunPatch_refusesEveryOtherShapeByName(t *testing.T) {
	_, _, err := RunPatch(t.Context(), `return 7`, []byte(`{}`), 200, nil)
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "a patch must return a table, a string or nil, got number") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunPatch_aFailureInsideTheFunctionIsErrFailed(t *testing.T) {
	_, _, err := RunPatch(t.Context(), `error("boom")`, []byte(`{}`), 200, nil)
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunPatch_aSelfReferencingTableIsARefusalNotACrash(t *testing.T) {
	_, _, err := RunPatch(t.Context(), `local t = {}; t.self = t; return t`, []byte(`{}`), 200, nil)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunPatch_timeoutIsClassifiedApartFromAFailure(t *testing.T) {
	restore := Timeout
	Timeout = 20 * time.Millisecond
	defer func() { Timeout = restore }()
	_, _, err := RunPatch(context.Background(), `while true do end`, []byte(`{}`), 200, nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

// TestRunPatch_aNullFieldIsMockNullAndNotNil is the test-and-produce half of
// the patch-body null rule: a JSON null the body carries arrives as
// `mock.null` — equal to it, not equal to nil — while an ABSENT key is still
// nil, so a patch can tell "the upstream said null" from "the upstream said
// nothing".
func TestRunPatch_aNullFieldIsMockNullAndNotNil(t *testing.T) {
	const src = `return {
		isNull = body.a == mock.null,
		isNil = body.a == nil,
		missingIsNull = body.gone == mock.null,
		missingIsNil = body.gone == nil,
		holeIsNull = body.list[2] == mock.null,
		len = #body.list,
	}`
	patched, changed, err := RunPatch(t.Context(), src, []byte(`{"a":null,"list":[1,null,3]}`), 200, nil)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := `{"holeIsNull":true,"isNil":false,"isNull":true,"len":3,"missingIsNil":true,"missingIsNull":false}`
	if string(patched) != want {
		t.Fatalf("patched=%s\nwant   %s", patched, want)
	}
}

// TestRunPatch_assigningMockNullEmitsNull is the produce half: `mock.null`
// is how a patch writes a JSON null, where assigning nil deletes the key.
func TestRunPatch_assigningMockNullEmitsNull(t *testing.T) {
	patched, changed, err := RunPatch(t.Context(), `body.a = mock.null; body.b = nil; body.c = {mock.null}; return body`, []byte(`{"a":1,"b":2}`), 200, nil)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if string(patched) != `{"a":null,"c":[null]}` {
		t.Fatalf("patched=%s", patched)
	}
}

// TestRunPatch_aTopLevelNullBodyRoundTrips: a body that is JSON `null`
// itself arrives as mock.null, and returning it is the null it was rather
// than a refusal of a userdata.
func TestRunPatch_aTopLevelNullBodyRoundTrips(t *testing.T) {
	patched, changed, err := RunPatch(t.Context(), `if body == mock.null then return body end return {}`, []byte(`null`), 200, nil)
	if err != nil || !changed || string(patched) != `null` {
		t.Fatalf("patched=%s changed=%v err=%v", patched, changed, err)
	}
}

// TestRunPatch_aBigIntegerIsExactAndReadableAsText pins what an author sees
// for an integer a float64 cannot hold: an opaque value that re-encodes as
// the upstream's own digits, prints and concatenates as those digits, and
// equals another big integer with the same digits — and nothing else (no
// arithmetic; it is not a Lua number).
func TestRunPatch_aBigIntegerIsExactAndReadableAsText(t *testing.T) {
	const src = `return {
		kind = type(body.id),
		text = tostring(body.id),
		key = "u:" .. body.id,
		suffix = body.id .. ":x",
		same = body.id == body.again,
		other = body.id == body.small,
		small = body.small + 1,
		id = body.id,
	}`
	patched, changed, err := RunPatch(t.Context(), src, []byte(`{"id":12345678901234567890,"again":12345678901234567890,"small":7}`), 200, nil)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := `{"id":12345678901234567890,"key":"u:12345678901234567890","kind":"userdata","other":false,"same":true,"small":8,"suffix":"12345678901234567890:x","text":"12345678901234567890"}`
	if string(patched) != want {
		t.Fatalf("patched=%s\nwant   %s", patched, want)
	}
}

// TestRunPatch_otherNumbersStayLuaNumbers draws the big-integer line from the
// other side: a fraction, an exponent and an integer inside 2^53 are plain
// Lua numbers (arithmetic works) and re-encode through the float, while a
// literal past float64's range keeps its own text like a big integer.
func TestRunPatch_otherNumbersStayLuaNumbers(t *testing.T) {
	const src = `return {
		kinds = type(body.f) .. type(body.e) .. type(body.edge),
		sum = body.f + body.e,
		huge = body.huge,
		hugeKind = type(body.huge),
		edge = body.edge,
	}`
	patched, changed, err := RunPatch(t.Context(), src, []byte(`{"f":1.5,"e":2e3,"edge":-9007199254740992,"huge":1e400}`), 200, nil)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := `{"edge":-9007199254740992,"huge":1e400,"hugeKind":"userdata","kinds":"numbernumbernumber","sum":2001.5}`
	if string(patched) != want {
		t.Fatalf("patched=%s\nwant   %s", patched, want)
	}
}

// TestRunPatch_aBigIntegerRefusesWhatItCannotBe: concatenating one with a
// table is Lua's own error, and adding to one is too — the patch fails by
// name rather than serving a rounded guess.
func TestRunPatch_aBigIntegerRefusesWhatItCannotBe(t *testing.T) {
	for src, want := range map[string]string{
		`return {k = body.id .. {}}`: "attempt to concatenate a table value",
		`return {k = body.id + 1}`:   "userdata",
	} {
		_, _, err := RunPatch(t.Context(), src, []byte(`{"id":12345678901234567890}`), 200, nil)
		if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v, want %q", src, err, want)
		}
	}
}

// TestRunPatch_aValueWithNoJSONRenderingIsNull: a function stored in the
// body is null, as before the sentinel existed.
func TestRunPatch_aValueWithNoJSONRenderingIsNull(t *testing.T) {
	patched, _, err := RunPatch(t.Context(), `return {fn = function() end, list = {function() end}}`, []byte(`{}`), 200, nil)
	if err != nil || string(patched) != `{"fn":null,"list":[null]}` {
		t.Fatalf("patched=%s err=%v", patched, err)
	}
}
