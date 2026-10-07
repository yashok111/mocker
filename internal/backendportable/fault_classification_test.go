package backendportable

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

// TestPortableFaultCodeFollowsStatus pins review 2026-10-06, F72: every
// portable refusal carried backend_portable_invalid, so an agent recovering
// from a lost reply could not tell a missing session (404) from a stale
// expectedVersion or reused key (409) or a quota refusal (413) by code.
func TestPortableFaultCodeFollowsStatus(t *testing.T) {
	t.Parallel()
	for status, code := range map[int]string{
		404: "backend_portable_not_found",
		409: "backend_portable_conflict",
		413: "backend_portable_limit",
		422: "backend_portable_invalid",
	} {
		f, ok := errors.AsType[*bm.FaultError](fault(status, "x"))
		if !ok || f.Status != status || f.Code != code {
			t.Errorf("fault(%d) = %+v, want code %s", status, f, code)
		}
	}
	s := makeRoundtripFixture(t).service
	_, err := s.Abort(t.Context(), "01900000-0000-7000-8000-000000000009", SessionInput{ExpectedVersion: 1, IdempotencyKey: "missing"})
	if f, ok := errors.AsType[*bm.FaultError](err); !ok || f.Code != "backend_portable_not_found" {
		t.Errorf("abort of a missing session: %v", err)
	}
}

// TestPortableMalformedRawEvidenceIs422 pins review 2026-10-06, F69: an
// evidence record whose raw proof is empty or not a known JSON object passes
// the envelope hash, and decodeModel returned the bare decoder error, which
// the admin plane answers as a logged 500 backend_internal. Every sibling
// failure in decodeModel is fault(422).
func TestPortableMalformedRawEvidenceIs422(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	export, err := f.service.Export(t.Context(), ExportInput{Selection: f.selection, IdempotencyKey: "raw-evidence-export"})
	check(t, err)
	var model *bm.PortableModel
	check(t, f.service.db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		model, err = f.service.exportModelTx(t.Context(), tx, f.selection)
		return err
	}))
	for _, raw := range []string{"", "[]", `{"unknown":1}`} {
		t.Run(raw, func(t *testing.T) {
			records, err := modelRecords(*model, export.Manifest.OriginInstallationID)
			check(t, err)
			found := false
			for i := range records {
				if records[i].Kind != "evidence" {
					continue
				}
				var v sourceRecord
				check(t, json.Unmarshal(records[i].Document, &v))
				v.Raw = raw
				records[i].Document, err = canonical(v)
				check(t, err)
				records[i].ContentHash, err = DocumentHash(records[i].Document)
				check(t, err)
				found = true
				break
			}
			if !found {
				t.Fatal("fixture has no evidence record")
			}
			_, err = decodeModel(records, export.Manifest)
			if fault, ok := errors.AsType[*bm.FaultError](err); !ok || fault.Status != 422 {
				t.Fatalf("err = %T %v, want a 422 FaultError", err, err)
			}
		})
	}
}
