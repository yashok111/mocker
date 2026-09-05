package luafn

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, src string, req Request, host Host) (Response, error) {
	t.Helper()
	return Run(context.Background(), src, req, host)
}

// TestRequest_carriesEveryFieldD3Names is acceptance clause 49: the request
// table had no observation at all until the gate's round 1 filed it as a
// blocker. Every one of the five fields is asserted, and the header half is
// asserted with the SAME name sent twice, which is what makes the join rule
// exercisable rather than merely stated.
func TestRequest_carriesEveryFieldD3Names(t *testing.T) {
	const src = `return 200, {
		method = req.method,
		id = req.pathParams.id,
		tags = req.query.tag,
		single = req.query.q,
		accept = req.headers["x-mixed"],
		body = req.body,
	}`
	resp, err := run(t, src, Request{
		Method:     "POST",
		Path:       "/orders/{id}",
		PathParams: map[string]string{"id": "7"},
		Query:      map[string][]string{"tag": {"a", "b"}, "q": {"one"}},
		Headers:    map[string][]string{"X-Mixed": {"first", "second"}},
		Body:       []byte("not json at all"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := string(resp.Body)
	for _, want := range []string{
		`"method":"POST"`,
		`"id":"7"`,
		`"tags":["a","b"]`,         // a repeated query key is an ARRAY
		`"single":"one"`,           // a single one is not
		`"accept":"first, second"`, // a repeated HEADER is joined, not an array
		`"body":"not json at all"`, // an unparseable body is the raw string
	} {
		if !strings.Contains(got, want) {
			t.Errorf("body %s does not contain %s", got, want)
		}
	}
}

func TestRequest_jsonBodyArrivesAsATable(t *testing.T) {
	resp, err := run(t, `return 200, {name = req.body.user.name}`, Request{
		Body: []byte(`{"user":{"name":"ada"}}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resp.Body); got != `{"name":"ada"}` {
		t.Fatalf("body = %s", got)
	}
}

// TestReturn_refusesEveryShapeD3Names is acceptance clause 50. D3 is explicit
// that these are failures and NOT silent coercions: a mock that turns `true`
// into `"true"` teaches its author the wrong contract, and the author finds out
// in production.
func TestReturn_refusesEveryShapeD3Names(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a string status", `return "200", {}`, "must be a number"},
		{"a fractional status", `return 200.5, {}`, "whole number"},
		{"a status below 100", `return 99, {}`, "100..599"},
		{"a status above 599", `return 600, {}`, "100..599"},
		{"a boolean body", `return 200, true`, "must be a table, a string or nil"},
		{"a number body", `return 200, 42`, "must be a table, a string or nil"},
		{"nothing at all", `local x = 1`, "returned nothing"},
		{"a non-string header value", `return 200, {}, {a = 1}`, "must be a string"},
		{"headers that are not a table", `return 200, {}, "x"`, "must be a table or nil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, tc.src, Request{}, nil)
			if !errors.Is(err, ErrFailed) {
				t.Fatalf("err = %v, want ErrFailed", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestReturn_nilBodyIsAnEmptyBody(t *testing.T) {
	resp, err := run(t, `return 204, nil`, Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 204 || len(resp.Body) != 0 || resp.MediaType != "" {
		t.Fatalf("resp = %+v, want 204 with an empty body and no media type", resp)
	}
}

func TestReturn_aTableIsJSONAndAStringIsRawBytes(t *testing.T) {
	table, err := run(t, `return 200, {ok = true}`, Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if table.MediaType != "application/json" || string(table.Body) != `{"ok":true}` {
		t.Fatalf("table return = %+v", table)
	}
	raw, err := run(t, `return 200, "<not json>"`, Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// An EMPTY media type and not a default: the caller reads it as "the
	// function chose no type" and applies its own rule.
	if raw.MediaType != "" || string(raw.Body) != "<not json>" {
		t.Fatalf("string return = %+v", raw)
	}
}

func TestReturn_headersReachTheCaller(t *testing.T) {
	// Acceptance clause 54: every other header clause observes a refusal, so
	// an implementation that dropped function-set headers entirely passed all
	// of them.
	resp, err := run(t, `return 200, {}, {["X-Mock-Case"] = "signed-in"}`, Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Headers["X-Mock-Case"] != "signed-in" {
		t.Fatalf("headers = %v", resp.Headers)
	}
}

// TestMockNow_isTheRealClockAndHonoursTheOffset is acceptance clause 52.
func TestMockNow_isTheRealClockAndHonoursTheOffset(t *testing.T) {
	resp, err := run(t, `local a = mock.now(); local b = mock.now(60); return 200, {a = a, delta = b - a}`, Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	if !strings.Contains(body, `"delta":60`) {
		t.Fatalf("body = %s, want the offset honoured", body)
	}
	// And it is the REAL clock, not the workspace seed's: D4 puts it out of
	// the determinism guarantee on purpose.
	var decoded struct {
		A int64 `json:"a"`
	}
	if err := stdjson.Unmarshal(resp.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	if now := time.Now().Unix(); decoded.A < now-5 || decoded.A > now+5 {
		t.Fatalf("mock.now() = %d, want within five seconds of %d", decoded.A, now)
	}
}

func TestValidate_refusesUnparseableSourceWithTheParsersOwnWords(t *testing.T) {
	// An error with a COORDINATE, not one at EOF: gopher-lua reports an
	// unterminated table as "at EOF" with no line at all, so that input would
	// pass this assertion vacuously.
	err := Validate("return 200, }")
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if !strings.Contains(err.Error(), "line:1") || !strings.Contains(err.Error(), "near '}'") {
		t.Fatalf("err = %q, want the parser's own line and token — the `local req = ...` prefix carries no newline exactly so the LINE is the author's (the column on line 1 is offset by the prefix, which wrap() documents)", err)
	}
	if err := Validate(`return 200, {ok = true}`); err != nil {
		t.Fatalf("a valid function was refused: %v", err)
	}
}

// TestNote_capsWhatReachesATrafficRow: a Lua error can embed request data, and
// a note is not a disclosure channel (D6). The message here is over the cap BY
// CONSTRUCTION, so the cap is exercised rather than merely present.
func TestNote_capsWhatReachesATrafficRow(t *testing.T) {
	long := strings.Repeat("secret-token-", 40)
	_, err := run(t, `error("`+long+`")`, Request{}, nil)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	note := Note(err)
	if len(note) != maxNoteBytes {
		t.Fatalf("note is %d bytes, want it capped at %d", len(note), maxNoteBytes)
	}
	if strings.Contains(note, "\n") {
		t.Fatal("the note carries more than the error's first line")
	}
}

func TestRun_timeoutIsClassifiedApartFromAFailure(t *testing.T) {
	// The package's OWN deadline, not the caller's: this is the 503 the
	// operator asked for, and the caller-cancelled case is ErrCanceled.
	restore := Timeout
	Timeout = 20 * time.Millisecond
	defer func() { Timeout = restore }()

	_, err := run(t, `while true do end`, Request{}, nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

// --- review findings 1 and 7: the converter's guards ------------------------

// TestReturn_aSelfReferencingTableIsARefusalNotACrash is review finding 1:
// the converter is Go recursion over a structure the function built, and
// before the guard `t.self = t` recursed until the runtime's stack ceiling —
// `fatal error: stack overflow`, unrecoverable, the whole process gone. The
// three contracts share one converter, so the request path stands for all
// three; the tick and onFrame paths are exercised through the same marshalLua.
func TestReturn_aSelfReferencingTableIsARefusalNotACrash(t *testing.T) {
	for name, src := range map[string]string{
		"direct":   `local t = {}; t.self = t; return 200, t`,
		"indirect": `local a, b = {}, {}; a.b = b; b.a = a; return 200, {a}`,
		"in-array": `local t = {1, 2}; t[3] = t; return 200, t`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := run(t, src, Request{}, nil)
			if !errors.Is(err, ErrFailed) {
				t.Fatalf("err = %v, want ErrFailed", err)
			}
			if !strings.Contains(err.Error(), "refers to itself") {
				t.Errorf("err = %v; the author must be told it is a cycle", err)
			}
		})
	}
}

// TestReturn_aTooDeepTableIsRefusedAndASharedOneIsNot pins the two edges of
// the guard: depth is bounded by maxTableDepth, and the guard tracks the
// PATH and not every table seen — `{a = u, b = u}` is a legal shape that
// encodes as two copies.
func TestReturn_aTooDeepTableIsRefusedAndASharedOneIsNot(t *testing.T) {
	deep := `local t = {}; local cur = t; for i = 1, 200 do cur.n = {}; cur = cur.n end; return 200, t`
	_, err := run(t, deep, Request{}, nil)
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "nests deeper") {
		t.Fatalf("200 levels: err = %v, want ErrFailed naming the depth", err)
	}

	shared := `local u = {x = 1}; return 200, {a = u, b = u}`
	resp, err := run(t, shared, Request{}, nil)
	if err != nil {
		t.Fatalf("a table referenced twice is not a cycle: %v", err)
	}
	if got := string(resp.Body); got != `{"a":{"x":1},"b":{"x":1}}` {
		t.Errorf("body = %s", got)
	}
}

// TestRequest_aNullInsideAnArrayKeepsItsPlace is review finding 7: gopher-lua's
// Append is a no-op for LNil, so `[1, null, 3]` used to arrive as `{1, 3}`
// with the third element at index 2. RawSetInt keeps the hole where JSON put
// it.
func TestRequest_aNullInsideAnArrayKeepsItsPlace(t *testing.T) {
	resp, err := run(t, `return 200, {third = req.body.ids[3], second = req.body.ids[2] == nil}`, Request{
		Body: []byte(`{"ids":[1,null,3]}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resp.Body); got != `{"second":true,"third":3}` {
		t.Errorf("body = %s; the null must hold index 2 and 3 must stay at index 3", got)
	}
}

// --- review finding 9: the hook's close reason ------------------------------

// TestOnFrame_closeReasonIsCappedLikeAReactiveRules pins MaxCloseReasonBytes
// on the per-frame path: a reason coder/websocket cannot put in a close frame
// is refused by the hook — its own sentinel, the shape ErrBadClose has, and the
// plane counts it under on_frame_errors — instead of reaching Close and
// turning into a 1006.
func TestOnFrame_closeReasonIsCappedLikeAReactiveRules(t *testing.T) {
	long := `return "close", 4001, string.rep("x", ` + strconv.Itoa(MaxCloseReasonBytes+1) + `)`
	_, err := RunOnFrame(context.Background(), long, []byte(`"ping"`), false, nil)
	if !errors.Is(err, ErrLongCloseReason) {
		t.Fatalf("err = %v, want ErrLongCloseReason", err)
	}

	exact := `return "close", 4001, string.rep("x", ` + strconv.Itoa(MaxCloseReasonBytes) + `)`
	act, err := RunOnFrame(context.Background(), exact, []byte(`"ping"`), false, nil)
	if err != nil {
		t.Fatalf("a reason of exactly the cap is legal: %v", err)
	}
	if act.Verb != FrameClose || act.Code != 4001 || len(act.Reason) != MaxCloseReasonBytes {
		t.Errorf("action = %+v", act)
	}
}
