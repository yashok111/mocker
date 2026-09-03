package customep_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// TestRepo_Create_invalidRow covers the task's ErrInvalidRow list: a bad
// method, a relative-looking-but-malformed path, a bad status key, and an
// undecodable base64 body — Create is the only entry point into
// normalizeAndValidate, so every case runs through it against a real
// workspace rather than a bare unit call.
func TestRepo_Create_invalidRow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	tests := []struct {
		name string
		row  *customep.Row
	}{
		{
			name: "unknown method",
			row:  &customep.Row{Method: "FETCH", Path: "/a"},
		},
		{
			name: "path without leading slash",
			row:  &customep.Row{Method: "GET", Path: "a"},
		},
		{
			name: "path carries a query string",
			row:  &customep.Row{Method: "GET", Path: "/a?x=1"},
		},
		{
			name: "path carries a fragment",
			row:  &customep.Row{Method: "GET", Path: "/a#frag"},
		},
		{
			name: "path contains a double slash",
			row:  &customep.Row{Method: "GET", Path: "/a//b"},
		},
		{
			name: "responses key is not a 3-digit status",
			row: &customep.Row{Method: "GET", Path: "/a", Responses: map[string]overrides.Variant{
				"ok": {Mode: "generated"},
			}},
		},
		{
			name: "base64 body does not decode",
			row: &customep.Row{Method: "GET", Path: "/a", Responses: map[string]overrides.Variant{
				"200": {Mode: "pinned", BodyEncoding: "base64", Body: raw(`"not-valid-base64!!"`)},
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := repo.Create(t.Context(), wsID, tc.row)
			if !errors.Is(err, customep.ErrInvalidRow) {
				t.Errorf("Create() err = %v, want ErrInvalidRow", err)
			}
		})
	}
}

// TestRepo_Create_responsesRoundTripUnchanged covers the task's "a
// responses map carrying when[] and schemaPatch round-trips unchanged"
// case: this slice's evaluator reads when[] at serve time and P2's applies
// schemaPatch, but the repo itself must not reshape either on the way in
// or out.
func TestRepo_Create_responsesRoundTripUnchanged(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	want := map[string]overrides.Variant{
		"200": {
			Mode: "pinned",
			When: []overrides.Condition{
				{In: "query", Name: "verbose", Op: "equals", Value: "true"},
				{In: "header", Name: "X-Debug", Op: "exists"},
			},
			Body:        raw(`{"id":1,"name":"widget"}`),
			MediaType:   "application/json",
			Headers:     map[string]string{"X-Extra": "1"},
			SchemaPatch: raw(`{"add":{"/properties/extra":{"type":"string"}}}`),
			Recipes: map[string]recipes.Recipe{
				"widget.id": {Kind: recipes.KindIdentity, Field: "id"},
			},
		},
		"404": {Mode: "generated"},
	}

	stored, err := repo.Create(t.Context(), wsID, &customep.Row{
		Method: "GET", Path: "/widgets/{id}", Responses: want,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	got, err := repo.Get(t.Context(), wsID, stored.ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}

	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	gotJSON, err := json.Marshal(got.Responses)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	if string(wantJSON) != string(gotJSON) {
		t.Errorf("responses round-trip changed:\n want %s\n got  %s", wantJSON, gotJSON)
	}
}

// TestRepo_Create_reqSchemaAndFailDirectiveRoundTripAsRawBytes covers the
// task's "fail_directive and req_schema round-trip byte-for-byte as raw
// JSON": both are PRESERVED ONLY (P2 gives them meaning), and copying them
// as plain strings rather than re-encoding through encoding/json is what
// keeps insignificant whitespace from being silently reformatted.
func TestRepo_Create_reqSchemaAndFailDirectiveRoundTripAsRawBytes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	// Deliberately irregular whitespace: a Marshal/Unmarshal round trip
	// through encoding/json would compact this, so byte-for-byte equality
	// on read-back is proof the value was copied, not re-encoded.
	reqSchema := raw(`{ "type":  "object",  "required":["id"] }`)
	failDirective := raw(`{"status":503,  "mode": "next_n","n":2}`)

	stored, err := repo.Create(t.Context(), wsID, &customep.Row{
		Method:        "POST",
		Path:          "/widgets",
		ReqSchema:     reqSchema,
		FailDirective: failDirective,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	got, err := repo.Get(t.Context(), wsID, stored.ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if string(got.ReqSchema) != string(reqSchema) {
		t.Errorf("ReqSchema = %s, want byte-identical %s", got.ReqSchema, reqSchema)
	}
	if string(got.FailDirective) != string(failDirective) {
		t.Errorf("FailDirective = %s, want byte-identical %s", got.FailDirective, failDirective)
	}
}

// TestRepo_Create_pinnedBase64BodyRoundTrips proves a VALID base64 pinned
// body (the sibling case of the invalid one in
// TestRepo_Create_invalidRow) is accepted and comes back unchanged — the
// same overrides.ValidateVariant gate op_overrides writes through, reused
// rather than reimplemented.
func TestRepo_Create_pinnedBase64BodyRoundTrips(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	encoded := base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`))
	body := raw(`"` + encoded + `"`)

	stored, err := repo.Create(t.Context(), wsID, &customep.Row{
		Method: "GET", Path: "/pinned",
		Responses: map[string]overrides.Variant{
			"200": {Mode: "pinned", BodyEncoding: "base64", Body: body},
		},
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	got, err := repo.Get(t.Context(), wsID, stored.ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if string(got.Responses["200"].Body) != string(body) {
		t.Errorf("Body = %s, want byte-identical %s", got.Responses["200"].Body, body)
	}
}
