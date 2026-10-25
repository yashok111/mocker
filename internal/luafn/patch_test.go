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
