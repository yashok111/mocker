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

// --- A19: mock.generate and the entity writers, the Lua side ---------------

// recordingHost is a Host that records what the helpers hand it and answers
// canned values, so these tests pin the ARGUMENT contract (what reaches the
// host for each Lua spelling) apart from what internal/mockplane does with it.
type recordingHost struct {
	schema  map[string]any
	family  string
	scope   []string
	key     string
	data    map[string]any
	created bool
}

func (h *recordingHost) JWT(context.Context, map[string]any) (string, error) { return "t", nil }
func (h *recordingHost) Entities(_ context.Context, family string, scope []string) ([]map[string]any, error) {
	h.family, h.scope = family, scope
	return []map[string]any{{"id": 1}}, nil
}
func (h *recordingHost) Generate(_ context.Context, schema map[string]any) (any, error) {
	h.schema = schema
	return map[string]any{"generated": true}, nil
}
func (h *recordingHost) EntityCreate(_ context.Context, family string, scope []string, data map[string]any) (map[string]any, error) {
	h.family, h.scope, h.data, h.created = family, scope, data, true
	return map[string]any{"id": 9, "text": data["text"]}, nil
}
func (h *recordingHost) EntityUpdate(_ context.Context, family string, scope []string, key string, patch map[string]any) (map[string]any, error) {
	h.family, h.scope, h.key, h.data = family, scope, key, patch
	return map[string]any{"id": key, "patched": true}, nil
}
func (h *recordingHost) EntityDelete(_ context.Context, family string, scope []string, key string) (bool, error) {
	h.family, h.scope, h.key = family, scope, key
	return true, nil
}

func TestMockGenerate_argumentShapes(t *testing.T) {
	t.Run("a #/ pointer becomes a $ref document", func(t *testing.T) {
		h := &recordingHost{}
		resp, err := run(t, `return 200, mock.generate("#/components/schemas/User")`, Request{}, h)
		if err != nil {
			t.Fatal(err)
		}
		if h.schema["$ref"] != "#/components/schemas/User" {
			t.Errorf("host schema = %v, want {$ref: #/components/schemas/User}", h.schema)
		}
		if string(resp.Body) != `{"generated":true}` {
			t.Errorf("body = %s", resp.Body)
		}
	})
	t.Run("a table is the inline schema", func(t *testing.T) {
		h := &recordingHost{}
		if _, err := run(t, `return 200, mock.generate({type = "object", properties = {n = {type = "integer"}}})`, Request{}, h); err != nil {
			t.Fatal(err)
		}
		if h.schema["type"] != "object" {
			t.Errorf("host schema = %v, want the inline document", h.schema)
		}
	})
	for name, src := range map[string]string{
		"a bare word": `return 200, {r = select(2, mock.generate("User"))}`,
		"a number":    `return 200, {r = select(2, mock.generate(7))}`,
		"an array":    `return 200, {r = select(2, mock.generate({1, 2}))}`,
		"no argument": `return 200, {r = select(2, mock.generate())}`,
	} {
		t.Run(name+" is bad_schema", func(t *testing.T) {
			resp, err := run(t, src, Request{}, &recordingHost{})
			if err != nil {
				t.Fatal(err)
			}
			if string(resp.Body) != `{"r":"bad_schema"}` {
				t.Errorf("body = %s", resp.Body)
			}
		})
	}
	t.Run("no host is no_host, as for entities", func(t *testing.T) {
		resp, err := run(t, `return 200, {r = select(2, mock.generate("#/x"))}`, Request{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != `{"r":"no_host"}` {
			t.Errorf("body = %s", resp.Body)
		}
	})
}

func TestMockEntities_isCallableAndCarriesThreeWriters(t *testing.T) {
	t.Run("the A18 spelling still reads", func(t *testing.T) {
		h := &recordingHost{}
		resp, err := run(t, `local rows = mock.entities("/teams", {"7"}); return 200, {n = #rows}`, Request{}, h)
		if err != nil {
			t.Fatal(err)
		}
		if h.family != "/teams" || len(h.scope) != 1 || h.scope[0] != "7" {
			t.Errorf("host got family=%q scope=%v", h.family, h.scope)
		}
		if string(resp.Body) != `{"n":1}` {
			t.Errorf("body = %s", resp.Body)
		}
	})
	t.Run("create hands the family, scope and data to the host", func(t *testing.T) {
		h := &recordingHost{}
		resp, err := run(t, `return 201, mock.entities.create("/msgs", {text = "hi"}, {"42"})`, Request{}, h)
		if err != nil {
			t.Fatal(err)
		}
		if !h.created || h.family != "/msgs" || h.data["text"] != "hi" || len(h.scope) != 1 || h.scope[0] != "42" {
			t.Errorf("host got created=%v family=%q data=%v scope=%v", h.created, h.family, h.data, h.scope)
		}
		if resp.Status != 201 || string(resp.Body) != `{"id":9,"text":"hi"}` {
			t.Errorf("status=%d body=%s", resp.Status, resp.Body)
		}
	})
	t.Run("create without a table is bad_data", func(t *testing.T) {
		resp, err := run(t, `return 200, {r = select(2, mock.entities.create("/msgs", "hi"))}`, Request{}, &recordingHost{})
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != `{"r":"bad_data"}` {
			t.Errorf("body = %s", resp.Body)
		}
	})
	t.Run("update takes a numeric key as its decimal text", func(t *testing.T) {
		h := &recordingHost{}
		if _, err := run(t, `return 200, mock.entities.update("/msgs", 7, {read = true})`, Request{}, h); err != nil {
			t.Fatal(err)
		}
		if h.key != "7" || h.data["read"] != true {
			t.Errorf("host got key=%q patch=%v", h.key, h.data)
		}
	})
	t.Run("a fractional or empty key is bad_key", func(t *testing.T) {
		for _, src := range []string{
			`return 200, {r = select(2, mock.entities.update("/msgs", 1.5, {}))}`,
			`return 200, {r = select(2, mock.entities.delete("/msgs", ""))}`,
			`return 200, {r = select(2, mock.entities.delete("/msgs", {}))}`,
		} {
			resp, err := run(t, src, Request{}, &recordingHost{})
			if err != nil {
				t.Fatal(err)
			}
			if string(resp.Body) != `{"r":"bad_key"}` {
				t.Errorf("%s: body = %s", src, resp.Body)
			}
		}
	})
	t.Run("delete answers the host's boolean", func(t *testing.T) {
		h := &recordingHost{}
		resp, err := run(t, `return 200, {gone = mock.entities.delete("/msgs", "k1")}`, Request{}, h)
		if err != nil {
			t.Fatal(err)
		}
		if h.key != "k1" || string(resp.Body) != `{"gone":true}` {
			t.Errorf("key=%q body=%s", h.key, resp.Body)
		}
	})
	t.Run("every writer is no_host in a preview", func(t *testing.T) {
		resp, err := run(t, `return 200, {
			c = select(2, mock.entities.create("/x", {})),
			u = select(2, mock.entities.update("/x", "1", {})),
			d = select(2, mock.entities.delete("/x", "1")),
		}`, Request{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != `{"c":"no_host","d":"no_host","u":"no_host"}` {
			t.Errorf("body = %s", resp.Body)
		}
	})
}
