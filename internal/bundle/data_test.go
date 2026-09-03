package bundle_test

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/jsonx"
)

// dataRow is a small helper so every test below states only what it cares
// about; CreatedAt/UpdatedAt are irrelevant to canonicity, the version gate
// and every one of ValidateData's five refusals, so they are fixed here
// rather than repeated at every call site.
func dataRow(scopeKey, entityKey, data string) bundle.EntityRow {
	return bundle.EntityRow{
		ScopeKey:  scopeKey,
		EntityKey: entityKey,
		Data:      jsonx.RawMessage(data),
		CreatedAt: 1756370000,
		UpdatedAt: 1756370000,
	}
}

// dataValidBundle is one DataBundle every "this shape is refused" test
// below starts from and mutates exactly one thing — so a failing case is
// never accidentally invalid for TWO reasons at once, which would leave a
// mutation of the wrong ValidateData branch unable to redden it.
func dataValidBundle() bundle.DataBundle {
	return bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{
			{
				RouteFamily: "/quizzes",
				Rows: []bundle.EntityRow{
					dataRow("", "1", `{"id":1}`),
					dataRow("", "2", `{"id":2}`),
				},
			},
			{
				RouteFamily: "/subjects",
				Rows:        []bundle.EntityRow{},
			},
		},
	}
}

// TestEncodeData_isCanonicalAcrossFamilyAndRowOrder is the byte-stability
// property EncodeData's own doc comment promises: two DataBundle values
// that differ ONLY in the order of their families, or the order of one
// family's rows, must encode to IDENTICAL bytes. Built with 10 sorting
// numerically before 2 lexically — "2" must still land before "10" — so a
// regression to a plain string compare on EntityKey (the exact mistake
// compareEntityRows' own doc comment names) reddens this test rather than
// passing by accident on a fixture too small to tell the two orders apart.
func TestEncodeData_isCanonicalAcrossFamilyAndRowOrder(t *testing.T) {
	a := bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{
			{RouteFamily: "/subjects", Rows: []bundle.EntityRow{}},
			{
				RouteFamily: "/quizzes",
				Rows: []bundle.EntityRow{
					dataRow("", "10", `{"id":10}`),
					dataRow("", "2", `{"id":2}`),
				},
			},
		},
	}
	b := bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{
			{
				RouteFamily: "/quizzes",
				Rows: []bundle.EntityRow{
					dataRow("", "2", `{"id":2}`),
					dataRow("", "10", `{"id":10}`),
				},
			},
			{RouteFamily: "/subjects", Rows: []bundle.EntityRow{}},
		},
	}

	encodedA, err := bundle.EncodeData(a)
	if err != nil {
		t.Fatalf("EncodeData(a): %v", err)
	}
	encodedB, err := bundle.EncodeData(b)
	if err != nil {
		t.Fatalf("EncodeData(b): %v", err)
	}
	if string(encodedA) != string(encodedB) {
		t.Fatalf("EncodeData is not canonical across ordering:\na=%s\nb=%s", encodedA, encodedB)
	}
	// And the numeric ordering is observably "2" before "10" in the output,
	// not merely "the two calls agree with each other" — pin the actual
	// byte position so a shared-but-wrong comparator (e.g. both sides
	// sorting lexically) cannot pass this test vacuously.
	idx2 := dataIndexOf(t, string(encodedA), `"entityKey":"2"`)
	idx10 := dataIndexOf(t, string(encodedA), `"entityKey":"10"`)
	if idx10 < idx2 {
		t.Fatalf("entityKey \"10\" sorted before \"2\": not a decimal-integer sort\n%s", encodedA)
	}
}

func dataIndexOf(t *testing.T, haystack, needle string) int {
	t.Helper()
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	t.Fatalf("substring %q not found in %q", needle, haystack)
	return -1
}

// TestDecodeData_gatesVersionOnly is D15's own explicit contract: DecodeData
// rejects a bad mockerData but does NOT run ValidateData over what it
// decodes — a document whose version is right but whose shape ValidateData
// would refuse must still come back successfully, because the restore path
// this document exists for calls ValidateData itself, as its own mandated
// step (D14/D15's appendix). A DecodeData that quietly ran ValidateData too
// would make that call redundant and this test is what would catch a
// regression back to running it.
func TestDecodeData_gatesVersionOnly(t *testing.T) {
	t.Run("wrong version is refused", func(t *testing.T) {
		bad := bundle.DataBundle{MockerData: bundle.DataVersion + 1}
		raw, err := jsonx.Marshal(bad)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if _, err := bundle.DecodeData(raw); !errors.Is(err, bundle.ErrInvalid) {
			t.Fatalf("DecodeData(wrong version) = %v, want ErrInvalid", err)
		}
	})

	t.Run("a shape ValidateData would refuse still decodes", func(t *testing.T) {
		invalidButRightVersion := bundle.DataBundle{
			MockerData: bundle.DataVersion,
			Families: []bundle.FamilyEntry{
				{RouteFamily: "", Rows: nil}, // ValidateData refuses an empty routeFamily
			},
		}
		raw, err := jsonx.Marshal(invalidButRightVersion)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		decoded, err := bundle.DecodeData(raw)
		if err != nil {
			t.Fatalf("DecodeData: %v, want success (DecodeData must not call ValidateData)", err)
		}
		if len(decoded.Families) != 1 || decoded.Families[0].RouteFamily != "" {
			t.Fatalf("DecodeData did not round-trip the shape: %+v", decoded)
		}

		// And ValidateData itself, called separately, DOES refuse it — the
		// mandated call this design puts on the restore path, not on Decode.
		if err := bundle.ValidateData(decoded); !errors.Is(err, bundle.ErrInvalid) {
			t.Fatalf("ValidateData(empty routeFamily) = %v, want ErrInvalid", err)
		}
	})
}

// TestValidateData_refusals exercises the five shapes ValidateData's own
// doc comment enumerates as its complete specification, one subtest per
// refusal, each built by mutating exactly one field of dataValidBundle().
func TestValidateData_refusals(t *testing.T) {
	t.Run("valid bundle passes", func(t *testing.T) {
		if err := bundle.ValidateData(dataValidBundle()); err != nil {
			t.Fatalf("ValidateData(valid) = %v, want nil", err)
		}
	})

	t.Run("1 wrong mockerData version", func(t *testing.T) {
		d := dataValidBundle()
		d.MockerData = bundle.DataVersion + 1
		err := bundle.ValidateData(d)
		if !errors.Is(err, bundle.ErrInvalid) {
			t.Fatalf("ValidateData(wrong version) = %v, want ErrInvalid", err)
		}
	})

	t.Run("2 duplicate routeFamily across entries", func(t *testing.T) {
		d := dataValidBundle()
		d.Families = append(d.Families, bundle.FamilyEntry{
			RouteFamily: "/quizzes", // already present in dataValidBundle
			Rows:        []bundle.EntityRow{},
		})
		err := bundle.ValidateData(d)
		if !errors.Is(err, bundle.ErrInvalid) {
			t.Fatalf("ValidateData(duplicate routeFamily) = %v, want ErrInvalid", err)
		}
	})

	t.Run("3 empty routeFamily", func(t *testing.T) {
		d := dataValidBundle()
		d.Families[0].RouteFamily = ""
		err := bundle.ValidateData(d)
		if !errors.Is(err, bundle.ErrInvalid) {
			t.Fatalf("ValidateData(empty routeFamily) = %v, want ErrInvalid", err)
		}
	})

	t.Run("4 duplicate (scopeKey, entityKey) within a family", func(t *testing.T) {
		d := dataValidBundle()
		d.Families[0].Rows = append(d.Families[0].Rows, dataRow("", "1", `{"id":99}`))
		err := bundle.ValidateData(d)
		if !errors.Is(err, bundle.ErrInvalid) {
			t.Fatalf("ValidateData(duplicate row key) = %v, want ErrInvalid", err)
		}
	})

	t.Run("5 entityKey is not a decimal integer string", func(t *testing.T) {
		// Each of these would parse under a naive strconv.Atoi/ParseInt
		// call and must still be refused — CAST('abc' AS INTEGER) is 0 in
		// SQLite, and "+1"/"01" both parse but are not the canonical
		// decimal form strconv.Itoa ever produces.
		for _, key := range []string{"abc", "1.5", "+1", "01", ""} {
			d := dataValidBundle()
			d.Families[0].Rows = []bundle.EntityRow{dataRow("", key, `{}`)}
			err := bundle.ValidateData(d)
			if !errors.Is(err, bundle.ErrInvalid) {
				t.Errorf("ValidateData(entityKey=%q) = %v, want ErrInvalid", key, err)
			}
		}
	})
}

// TestValidateData_emptyFamilyIsCarriedNotOmitted pins D4's own rule that a
// confirmed-and-empty family is a VALID document (rows: []), never refused
// — the distinction this document draws between "no opinion" (family
// absent) and "there were none" (family present, empty) only holds if an
// empty family passes validation rather than being treated as degenerate.
func TestValidateData_emptyFamilyIsCarriedNotOmitted(t *testing.T) {
	d := bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{
			{RouteFamily: "/subjects", Rows: []bundle.EntityRow{}},
		},
	}
	if err := bundle.ValidateData(d); err != nil {
		t.Fatalf("ValidateData(empty-but-present family) = %v, want nil", err)
	}
	encoded, err := bundle.EncodeData(d)
	if err != nil {
		t.Fatalf("EncodeData: %v", err)
	}
	decoded, err := bundle.DecodeData(encoded)
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	if len(decoded.Families) != 1 || decoded.Families[0].Rows == nil {
		t.Fatalf("empty family did not round-trip as rows:[]: %+v", decoded.Families)
	}
}

// TestValidateData_baseScopeKeyDoesNotWidenUniqueness pins D9's own refusal
// to widen refusal #4 to (baseScopeKey, scopeKey, entityKey): two rows that
// differ ONLY in BaseScopeKey are still a duplicate under the narrower rule,
// which is what "mirrors the physical UNIQUE (resource_id, scope_key,
// entity_key)" means in code rather than in prose. This is the opposite of
// TestEncodeDecodeData_roundTripsATwoScopeFamily above, which shows two rows
// differing in ScopeKey are NOT a collision — the two tests together pin
// exactly which column is, and which is not, part of the key.
func TestValidateData_baseScopeKeyDoesNotWidenUniqueness(t *testing.T) {
	d := dataValidBundle()
	row := d.Families[0].Rows[0] // (scopeKey="", entityKey="1")
	differentBaseScopeOnly := row
	differentBaseScopeOnly.BaseScopeKey = "7"
	d.Families[0].Rows = []bundle.EntityRow{row, differentBaseScopeOnly}
	err := bundle.ValidateData(d)
	if !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("ValidateData(rows differing only in baseScopeKey) = %v, want ErrInvalid (D9: uniqueness stays narrow at (scopeKey, entityKey))", err)
	}
}

// TestDecodeData_v1DocumentDecodesEveryRowIntoTheEmptyBaseScope hand-builds
// the exact bytes a pre-P3h checkpoint carries — mockerData: 1, no
// baseScopeKey key on any row at all, because the field did not exist — and
// pins that DecodeData still accepts it (D9: refusing v1 would delete the
// only undo P3d shipped for a decline or a reset-data from every checkpoint
// taken before this slice) with every row landing at BaseScopeKey == "", the
// zero value jsonx.Unmarshal leaves an absent field at. Built as a raw JSON
// literal rather than by encoding a bundle.DataBundle{MockerData: 1, ...}
// through this build's own EncodeData: this build only ever WRITES
// DataVersion (2), so a hand-built literal is the only way to exercise what
// an actual on-disk v1 document looks like.
func TestDecodeData_v1DocumentDecodesEveryRowIntoTheEmptyBaseScope(t *testing.T) {
	v1 := []byte(`{
		"mockerData": 1,
		"families": [
			{
				"routeFamily": "/quizzes",
				"rows": [
					{"scopeKey": "", "entityKey": "1", "data": {"id": 1}, "createdAt": 1756370000, "updatedAt": 1756370000},
					{"scopeKey": "", "entityKey": "2", "data": {"id": 2}, "createdAt": 1756370000, "updatedAt": 1756370000}
				]
			}
		]
	}`)

	decoded, err := bundle.DecodeData(v1)
	if err != nil {
		t.Fatalf("DecodeData(v1 document) = %v, want success", err)
	}
	if err := bundle.ValidateData(decoded); err != nil {
		t.Fatalf("ValidateData(decoded v1 document) = %v, want nil", err)
	}
	if len(decoded.Families) != 1 || len(decoded.Families[0].Rows) != 2 {
		t.Fatalf("decoded v1 document = %+v, want one family with 2 rows", decoded.Families)
	}
	for _, row := range decoded.Families[0].Rows {
		if row.BaseScopeKey != "" {
			t.Fatalf("v1 row %+v has BaseScopeKey %q, want the empty base scope", row, row.BaseScopeKey)
		}
	}
}

// TestEncodeDecodeData_roundTripsBaseScopeKey is the P3h counterpart to
// TestEncodeDecodeData_roundTripsATwoScopeFamily above: a family whose rows
// carry a non-empty BaseScopeKey round-trips it byte-for-byte through
// EncodeData/DecodeData, and two rows sharing (scopeKey, entityKey) across
// DIFFERENT base scopes cannot even be built here — the fixture below gives
// each base scope its own scopeKey precisely because ValidateData's own
// unwidened rule (D9) would refuse anything else, which is the property the
// test above already pins; this one is about the wire field surviving the
// round trip, not about the uniqueness rule.
func TestEncodeDecodeData_roundTripsBaseScopeKey(t *testing.T) {
	row7 := dataRow("", "1", `{"id":1}`)
	row7.BaseScopeKey = "7"
	row8 := dataRow("", "1", `{"id":1}`)
	row8.BaseScopeKey = "8"
	// entityKey "1" repeats under two DIFFERENT scopeKeys so the fixture
	// stays valid under the unwidened rule without touching BaseScopeKey's
	// own round trip.
	row7.ScopeKey = "7"
	row8.ScopeKey = "8"

	d := bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{
			{RouteFamily: "/quizzes", Rows: []bundle.EntityRow{row8, row7}},
		},
	}
	if err := bundle.ValidateData(d); err != nil {
		t.Fatalf("ValidateData: %v, want nil", err)
	}
	encoded, err := bundle.EncodeData(d)
	if err != nil {
		t.Fatalf("EncodeData: %v", err)
	}
	decoded, err := bundle.DecodeData(encoded)
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	if len(decoded.Families) != 1 || len(decoded.Families[0].Rows) != 2 {
		t.Fatalf("round trip = %+v, want one family with 2 rows", decoded.Families)
	}
	for _, row := range decoded.Families[0].Rows {
		if row.BaseScopeKey != row.ScopeKey {
			// Fixture deliberately set BaseScopeKey == ScopeKey per row
			// ("7"/"7", "8"/"8") so a single field comparison proves the
			// round trip without a second lookup table.
			t.Fatalf("row %+v: BaseScopeKey did not round-trip", row)
		}
	}
}

// TestEncodeDecodeData_roundTripsATwoScopeFamily is D10.3's own codec test:
// a nested family's rows carry a non-empty ScopeKey, and the round trip
// through EncodeData/DecodeData/ValidateData preserves them — canonical
// order compound on (ScopeKey, EntityKey-as-decimal-integer), the ordering
// [compareEntityRows]'s own doc comment promises, not a plain string
// compare that would put "10" before "2" within one scope. No production
// line changes for this: D10.1 says the codec is already scope-aware since
// P3d, and this is the proof rather than the change.
func TestEncodeDecodeData_roundTripsATwoScopeFamily(t *testing.T) {
	d := bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{
			{
				RouteFamily: "/orgs/{}/users",
				Rows: []bundle.EntityRow{
					// Deliberately out of both orders — family sort is by
					// RouteFamily alone (one family here), row sort must
					// group by ScopeKey first and only then by the
					// decimal EntityKey within one scope.
					dataRow("8", "10", `{"id":10,"org":8}`),
					dataRow("7", "2", `{"id":2,"org":7}`),
					dataRow("8", "2", `{"id":2,"org":8}`),
					dataRow("7", "10", `{"id":10,"org":7}`),
				},
			},
		},
	}
	if err := bundle.ValidateData(d); err != nil {
		t.Fatalf("ValidateData(two-scope family) = %v, want nil — two rows sharing an entityKey across DIFFERENT scopes is not a collision", err)
	}

	encoded, err := bundle.EncodeData(d)
	if err != nil {
		t.Fatalf("EncodeData: %v", err)
	}
	decoded, err := bundle.DecodeData(encoded)
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	if len(decoded.Families) != 1 || len(decoded.Families[0].Rows) != 4 {
		t.Fatalf("round trip = %+v, want one family with 4 rows", decoded.Families)
	}

	// Canonical order: scope "7" entirely before scope "8" ("7" < "8"
	// lexically, which happens to agree with numeric order here — the
	// point under test is the GROUPING, not this coincidence), and within
	// each scope "2" before "10" (decimal, not lexical).
	type scopeKeyPair struct{ scope, key string }
	got := make([]scopeKeyPair, 0, len(decoded.Families[0].Rows))
	for _, r := range decoded.Families[0].Rows {
		got = append(got, scopeKeyPair{r.ScopeKey, r.EntityKey})
	}
	want := []scopeKeyPair{{"7", "2"}, {"7", "10"}, {"8", "2"}, {"8", "10"}}
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row order = %v, want %v (scope-then-decimal-key canonical order)", got, want)
		}
	}

	// Every row's ParentEntityID has no field on the wire at all — D9 keeps
	// the notion off the codec entirely rather than carrying a field this
	// build never sets, and this round trip is what proves EncodeData does
	// not invent one. bundle.EntityRow itself has no such field: a
	// compile-time check that the wire shape agrees with D9's claim, not a
	// runtime assertion — see the type's own doc comment for the full list
	// of what it deliberately excludes.
}
