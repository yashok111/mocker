package backendportable

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

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
