package designscenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestArtifactInspectionVerifiedAndUnsupported(t *testing.T) {
	r := newTestRepo(t)
	created, err := r.Create(t.Context(), CreateInput{Document: validDocument("Inspection"), FormDrafts: map[string]string{"panel": "unfinished"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	strict, err := r.ArtifactSnapshot(t.Context(), created.Scenario.ID, created.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	normal, err := r.ArtifactInspectionSnapshot(t.Context(), created.Scenario.ID, created.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if normal.TypedStatus != "supported" || normal.EnvelopeVerification != "verified" || normal.ContentHash != strict.ContentHash || normal.StoredContentHash != strict.ContentHash || normal.DocumentJSON != strict.DocumentJSON || normal.FormDraftsJSON != strict.FormDraftsJSON || normal.DocumentHash != strict.DocumentHash {
		t.Fatalf("normal inspection %+v", normal)
	}
	dropScenarioRevisionFences(t, r)
	raw := strings.Replace(strict.DocumentJSON, `{"formatVersion"`, `{"future":{"n":9007199254740993},"formatVersion"`, 1)
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=? WHERE id=?`, raw, created.Draft.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := artifactOwnerRows(t, r)
	got, err := r.ArtifactInspectionSnapshot(t.Context(), created.Scenario.ID, created.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TypedStatus != "unsupported" || got.EnvelopeVerification != "unavailable" || got.ContentHash != "" || got.StoredContentHash != strict.ContentHash || got.DocumentJSON != raw || got.FormDraftsJSON != strict.FormDraftsJSON || got.DocumentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) {
		t.Fatalf("raw qualification %+v", got)
	}
	if _, err := r.ArtifactSnapshot(t.Context(), created.Scenario.ID, created.Draft.ID); err == nil {
		t.Fatal("strict snapshot weakened")
	}
	if artifactOwnerRows(t, r) != before {
		t.Fatal("inspection mutated rows")
	}
}
func TestArtifactInspectionHardErrors(t *testing.T) {
	for _, tc := range []struct{ name, column, value string }{
		{"invalid JSON", "document", `{`}, {"duplicate top", "document", `{"future":1,"future":2}`}, {"duplicate nested", "document", `{"future":{"a":1,"a":2}}`},
		{"draft array", "form_drafts", `[]`}, {"draft number", "form_drafts", `{"panel":4}`}, {"draft duplicate", "form_drafts", `{"p":"a","p":"b"}`},
		{"invalid digest", "hash", "INVALID"}, {"verified mismatch", "hash", strings.Repeat("0", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRepo(t)
			d, err := r.Create(t.Context(), CreateInput{Document: validDocument("Hard errors"), Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			dropScenarioRevisionFences(t, r)
			if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET `+tc.column+`=? WHERE id=?`, tc.value, d.Draft.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if got, err := r.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID); err == nil || got != nil {
				t.Fatalf("hard failure downgraded to unsupported: %+v %v", got, err)
			}
		})
	}
}
func TestArtifactInspectionBoundsAndLegacyDrafts(t *testing.T) {
	r := newTestRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Document: validDocument("Legacy"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	dropScenarioRevisionFences(t, r)
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET form_drafts='null' WHERE id=?`, d.Draft.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if err != nil || got.EnvelopeVerification != "verified" || got.FormDraftsJSON != "null" || got.ContentHash != d.Draft.Hash {
		t.Fatalf("legacy %+v %v", got, err)
	}
	size := int64(len(got.DocumentJSON) + len(got.FormDraftsJSON))
	r.cfg.MaxBody = size - 1
	if _, err := r.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	for _, bound := range []int64{size, 0, -1} {
		r.cfg.MaxBody = bound
		if _, err := r.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.ArtifactInspectionSnapshot(ctx, d.Scenario.ID, d.Draft.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestArtifactInspectionDraftNullMemberParity(t *testing.T) {
	r := newTestRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Document: validDocument("Null member"), FormDrafts: map[string]string{"panel": ""}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	dropScenarioRevisionFences(t, r)
	_, hash, err := encodeScenarioEnvelope(d.Draft.Document, map[string]string{"panel": ""})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET form_drafts='{"panel":null}',hash=? WHERE id=?`, hash, d.Draft.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	strict, strictErr := r.ArtifactSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	inspection, inspectionErr := r.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if strictErr != nil || inspectionErr != nil {
		t.Fatalf("strict=%v inspection=%v", strictErr, inspectionErr)
	}
	if strict.ContentHash != inspection.ContentHash || inspection.EnvelopeVerification != "verified" || inspection.FormDraftsJSON != strict.FormDraftsJSON {
		t.Fatal("null member parity changed")
	}
}
