package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

// This file is C7 (context.md §J): the strict ingress gate a caller-supplied
// Variant.SchemaPatch passes through on PUT .../operations/{opKey}
// (gateSchemaPatch, override_handlers.go), exercised through the real HTTP
// handler — the only place a caller ever meets it (decisions.md D2(2)).
//
// seedSchemaPatchVariant is how (b), (c) and (d) below get a NON-CONFORMING
// stored patch into the table at all: none of the three could ever arrive
// there through a PUT under test, because the very first write to a fresh
// row compares the submitted document against a nil "stored" side and
// always reports "changed" (schemaPatchDoc(nil) is the empty document, and
// a real patch is never equal to it) — so the gate would refuse a
// non-conforming patch at the very seed. Writing straight through
// [overrides.Repo.Put] is legitimate for this: it is one of the tolerant
// carriers decisions.md D2(5) names by name (every writer of this column
// except the one admin ingress door stays tolerant), and
// overrides.ValidateVariant preserves SchemaPatch unread by its own doc
// comment (overrides.go:207-215) — nothing downstream of this call
// re-validates the bytes.
// seedSchemaPatchVariant returns the seeded row's edit_version (A3) so the
// PUT under test can send the correct compare-and-swap expectation — this
// seed bypasses the HTTP route entirely, so nothing else in this test hands
// that value back.
func seedSchemaPatchVariant(t *testing.T, ts *testServer, wsID int64, opKey string, patch json.RawMessage) int64 {
	t.Helper()
	repo := overrides.NewRepo(ts.db)
	row, _, err := repo.Put(t.Context(), wsID, opKey, func(cur *overrides.Row) error {
		cur.OverrideOn = false
		cur.Responses = map[string]overrides.Variant{
			"200": {SchemaPatch: patch},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed schemaPatch variant %s: %v", opKey, err)
	}
	return row.EditVersion
}

// putRawSchemaPatchRequest issues the PUT with rawBody sent VERBATIM as the
// request's wire bytes — never through json.Marshal/json.NewEncoder, whose
// default HTML escaping (SetEscapeHTML defaults to true) would silently
// rewrite a literal '&' back into & before the request ever left this
// process. Verified, not assumed: encoding a map[string]any{"note": "Salt &
// Pepper"} through the same encoder jsonRequest uses re-escapes it to "Salt
// & Pepper" on the wire, which would quietly undo the one thing case
// (b) exists to send — the bytes a BROWSER's JSON.stringify actually
// produces, which does not HTML-escape.
func putRawSchemaPatchRequest(t *testing.T, target, rawBody string, cookie *http.Cookie, csrfToken string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", adminOrigin)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	return req
}

// TestHandler_putOperation_schemaPatchGate is C7's four cases. Every
// sub-test uses its OWN opKey — (a) because a fresh opKey is what makes its
// stored side "no patch" by construction, and (b)/(c)/(d) because each is
// described in context.md §J as its own seeded variant, with (d)'s
// explicitly called out as the THIRD one and one no earlier case has PUT
// to: sharing an opKey across cases would mean one case's PUT becomes the
// next case's "stored" side, which is exactly the cross-contamination that
// would stop (d) from isolating the submitted-side decode failure it exists
// to observe.
func TestHandler_putOperation_schemaPatchGate(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "SchemaPatchGate")
	wsIDInt := int64(wsID)
	base := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations", wsIDInt)

	// (a) A COMPLETE, otherwise-valid RFC 6902 operation whose only fault is
	// its NAME: "move" is not one of the three internal/jsonpatch
	// implements (add/remove/replace — its own package doc comment), so
	// nothing but the op name can be what refuses this. No seeding: a fresh
	// opKey's stored side is "no patch" (schemaPatchDoc(nil) is nil), and
	// any real patch differs from that by construction, so the gate fires
	// on the very first write.
	t.Run("a: a complete but unsupported op is refused by name, not a generic 400", func(t *testing.T) {
		opKey := "PUT%20%2Fschema-patch-a"
		putBody := map[string]any{
			"responses": map[string]any{
				"200": map[string]any{
					"schemaPatch": json.RawMessage(`[{"op":"move","from":"/properties/name","path":"/properties/renamed"}]`),
				},
			},
			// A fresh opKey: no override row exists yet, so 0 is the legal
			// "I expect no row" expectation (D7).
			"editVersion": 0,
		}
		req := jsonRequest(t, http.MethodPut, base+"/"+opKey, putBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body: %v; body = %s", err, rec.Body.String())
		}
		if !strings.Contains(body.Error.Message, `"move"`) {
			t.Errorf("error message = %q, want it to name the offending operation \"move\" (a bare 400 also passes against a build with this slice deleted entirely — the generic decoder already answers 400 for a body it does not recognise)", body.Error.Message)
		}
	})

	// (b) THE STRUCTURAL PATH. The stored patch is NON-CONFORMING (a "move"
	// op, so a byte-different-therefore-reparse implementation would fail
	// it) and carries an ampersand spelled & and the number 1.0 — no
	// overflowing number anywhere, so BOTH sides decode cleanly and the
	// gate's only question is whether the two decoded documents compare
	// EQUAL. The submitted bytes are exactly what a browser's JSON.parse
	// followed by JSON.stringify produces from the stored ones: &
	// becomes a literal '&' (JS does not HTML-escape on stringify — Go's
	// encoding/json does, which is why the stored side is spelled
	// differently in the first place) and 1.0 becomes 1. overrideOn flips
	// false->true alongside it, so a 200 here proves the WRITE actually
	// went through and did not merely skip the whole request.
	t.Run("b: a re-spelled but structurally identical stored patch is not re-validated", func(t *testing.T) {
		opKey := "PUT%20%2Fschema-patch-b"
		seededVersion := seedSchemaPatchVariant(t, ts, wsIDInt, opKey,
			json.RawMessage(`[{"op":"move","from":"/a","path":"/b","note":"Salt & Pepper","weight":1.0}]`))

		rawBody := fmt.Sprintf(`{"overrideOn":true,"editVersion":%d,"responses":{"200":{"schemaPatch":`+
			`[{"op":"move","from":"/a","path":"/b","note":"Salt & Pepper","weight":1}]}}}`, seededVersion)
		req := putRawSchemaPatchRequest(t, base+"/"+opKey, rawBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the submitted patch is the SAME document as the stored one, only re-spelled by a browser round trip — 'the same bytes' would NOT fail this gate, and an unchanged patch is not re-validated however malformed its shape); body = %s",
				rec.Code, rec.Body.String())
		}
		doc := decodeOverrideDoc(t, rec)
		if !doc.OverrideOn {
			t.Errorf("overrideOn = false, want the unrelated field change (true) to have gone through alongside the structurally-unchanged schemaPatch")
		}
	})

	// (c) THE STORED-SIDE DECODE FAILURE, its own fixture and its own PUT
	// — kept separate from (b) because merging them would let the
	// decode-failure branch decide FIRST and short-circuit the structural
	// comparison, proving nothing about equality (context.md §J's own
	// "why (b) and (c) are separate"). The stored patch is ALSO
	// non-conforming (a "move" op) and additionally carries 1e400, which
	// Go's decode into `any` refuses as a numeric overflow — so the stored
	// side can never be compared at all. The submitted patch is the same
	// document with that number written the way a browser would:
	// JSON.parse turns 1e400 into +Inf, and JSON.stringify writes an
	// infinite number back as `null`. A stored decode failure means the
	// row is already inert on the read path, so this gate lets the write
	// through rather than making the row permanently un-editable.
	t.Run("c: a stored-side decode failure lets the write through", func(t *testing.T) {
		opKey := "PUT%20%2Fschema-patch-c"
		seededVersion := seedSchemaPatchVariant(t, ts, wsIDInt, opKey,
			json.RawMessage(`[{"op":"move","from":"/x","path":"/y","weight":1e400}]`))

		putBody := map[string]any{
			"responses": map[string]any{
				"200": map[string]any{
					"schemaPatch": json.RawMessage(`[{"op":"move","from":"/x","path":"/y","weight":null}]`),
				},
			},
			"editVersion": seededVersion,
		}
		req := jsonRequest(t, http.MethodPut, base+"/"+opKey, putBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the STORED side fails to decode — already inert on the read path — so this gate reports 'unchanged' rather than re-validating a row an operator did not touch); body = %s",
				rec.Code, rec.Body.String())
		}
	})

	// (d) THE SUBMITTED-SIDE DECODE FAILURE — the one input on which the
	// ORDER of the two decode checks matters, and the only case that
	// observes it. The submitted patch is well-formed JSON (it decodes
	// fine as raw bytes into the wire's schemaPatch field, so decodeJSON
	// accepts the request) but fails to decode into `any`: a "move" op
	// whose OWN "value" member carries 1e400. The stored side, on this
	// THIRD freshly seeded opKey that neither (a), (b) nor (c) has PUT to,
	// is ALSO undecodable. A "stored side decides" implementation would
	// see the stored decode fail and call this "unchanged" too — answering
	// 200, wrongly. Under the actual rule the SUBMITTED side is decoded
	// FIRST, fails, and the gate fires regardless of what the stored side
	// says; Parse then refuses the unsupported "move" op. Only the status
	// is asserted here — naming the operation is (a)'s job, and here the
	// patch fails to decode before anything can even look at its op name.
	t.Run("d: a submitted-side decode failure fires the gate before the stored side is even looked at", func(t *testing.T) {
		opKey := "PUT%20%2Fschema-patch-d"
		seededVersion := seedSchemaPatchVariant(t, ts, wsIDInt, opKey,
			json.RawMessage(`[{"op":"move","from":"/p","path":"/q","weight":1e400}]`))

		putBody := map[string]any{
			"responses": map[string]any{
				"200": map[string]any{
					"schemaPatch": json.RawMessage(`[{"op":"move","from":"/p","path":"/q","value":1e400}]`),
				},
			},
			"editVersion": seededVersion,
		}
		req := jsonRequest(t, http.MethodPut, base+"/"+opKey, putBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (the SUBMITTED side fails to decode, which fires the gate immediately regardless of the stored side — a stored-first implementation would answer 200 here instead); body = %s",
				rec.Code, rec.Body.String())
		}
	})
}
