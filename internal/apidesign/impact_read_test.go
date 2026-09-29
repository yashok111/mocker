package apidesign

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/specs"
)

func TestAnalyzeImpactReadsExactSnapshotsWithoutWrites(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "Impact", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	base := d.Draft
	d, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: strings.Replace(base.Document, "Orders", "Updated", 1), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	counts := func() []int64 {
		t.Helper()
		out := []int64{}
		for _, query := range []string{"SELECT COUNT(*) FROM api_design_revisions", "SELECT COUNT(*) FROM workspaces", "SELECT COUNT(*) FROM checkpoints", "SELECT SUM(revision) FROM workspaces", "SELECT COUNT(*) FROM specs"} {
			var n int64
			if err := db.R.QueryRowContext(ctx, query).Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		return out
	}
	prior := counts()
	raw := " \n" + strings.Replace(base.Document, "9007199254740993", "9007199254740995", 1) + "\n"
	local, pair, err := r.AnalyzeImpactWithDocuments(ctx, d.Design.ID, ImpactInput{FromRevisionID: base.ID, Document: &raw})
	if err != nil {
		t.Fatal(err)
	}
	if pair.Before != base.Document || pair.Proposed != raw {
		t.Fatal("scenario comparison must use the exact API snapshots")
	}
	encoded, err := jsonx.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := jsonx.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Before", "Proposed", "before", "proposed", "document", "documents"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("raw snapshot leaked into report: %s", key)
		}
	}
	if local.FromRevisionID != base.ID || local.ToRevisionID != nil || local.Version != 2 || local.DesignID != d.Design.ID {
		t.Fatalf("wrong snapshot: %+v", local)
	}
	if local.FromHash != fmt.Sprintf("%x", sha256.Sum256([]byte(base.Document))) || local.ProposedHash != fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) {
		t.Fatal("hash did not use exact input")
	}
	found := false
	for _, c := range local.Changes {
		if c.AfterJSON != nil && *c.AfterJSON == "9007199254740995" {
			found = true
		}
	}
	if !found {
		t.Fatalf("large value lost: %+v", local.Changes)
	}
	historical, err := r.AnalyzeImpact(ctx, d.Design.ID, ImpactInput{FromRevisionID: base.ID, ToRevisionID: &d.Draft.ID})
	if err != nil || historical.ToRevisionID == nil || *historical.ToRevisionID != d.Draft.ID || len(historical.Changes) == 0 {
		t.Fatalf("history: %+v %v", historical, err)
	}
	same, err := r.AnalyzeImpact(ctx, d.Design.ID, ImpactInput{FromRevisionID: base.ID, ToRevisionID: &base.ID})
	if err != nil || same.Changes == nil || len(same.Changes) != 0 {
		t.Fatalf("same revision: %+v %v", same, err)
	}
	after, err := r.Detail(ctx, d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prior, counts()) || after.Design.Version != d.Design.Version || after.Design.DraftRevisionID != d.Design.DraftRevisionID || after.Design.PublishedRevisionID != d.Design.PublishedRevisionID {
		t.Fatal("analysis changed persisted state")
	}
}

func TestAnalyzeImpactRejectsInvalidTargetsAndForeignRevisions(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "Impact", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.Create(ctx, CreateInput{Name: "Other", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []ImpactInput{
		{FromRevisionID: d.Draft.ID},
		{FromRevisionID: d.Draft.ID, Document: new("{}"), ToRevisionID: &d.Draft.ID},
		{Document: new("{}")},
		{FromRevisionID: d.Draft.ID, ToRevisionID: new(int64(0))},
		{FromRevisionID: d.Draft.ID, Document: new("openapi: 3.1.0")},
		{FromRevisionID: d.Draft.ID, Document: new("null")},
		{FromRevisionID: d.Draft.ID, Document: new("[]")},
		{FromRevisionID: d.Draft.ID, Document: new("{")},
	} {
		if _, err := r.AnalyzeImpact(ctx, d.Design.ID, in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
	for _, in := range []ImpactInput{
		{FromRevisionID: other.Draft.ID, ToRevisionID: &d.Draft.ID},
		{FromRevisionID: d.Draft.ID, ToRevisionID: &other.Draft.ID},
	} {
		if _, err := r.AnalyzeImpact(ctx, d.Design.ID, in); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign revision: %v", err)
		}
	}
	if _, err := r.AnalyzeImpact(ctx, 99999, ImpactInput{FromRevisionID: d.Draft.ID, Document: new("{}")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing design: %v", err)
	}
	if _, err := r.AnalyzeImpact(ctx, d.Design.ID, ImpactInput{FromRevisionID: d.Draft.ID, Document: new(strings.Repeat(" ", int(r.cfg.MaxBody)+1))}); !errors.Is(err, specs.ErrTooLarge) {
		t.Fatalf("size: %v", err)
	}
}

func TestAnalyzeImpactPreservesRawHistoricalDocuments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{
			name: "legacy without operation keys",
			raw:  " \n" + testDocument + "\n",
		},
		{
			name: "duplicate operation keys",
			raw: ` {"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{` +
				`"/a":{"get":{"x-mocker-canvas-operation-id":"same","responses":{"200":{"description":"OK"}}}},` +
				`"/b":{"get":{"x-mocker-canvas-operation-id":"same","responses":{"200":{"description":"OK"}}}}}} `,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := testRepo(t)
			created, err := r.Create(t.Context(), CreateInput{Name: "Impact", Document: testDocument, Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			// Restore the bytes of a pre-identity or externally migrated revision;
			// normal writes would already have inserted operation keys.
			err = db.Write(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(
					t.Context(),
					"UPDATE api_design_revisions SET document=? WHERE id=?",
					tc.raw,
					created.Draft.ID,
				)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range []ImpactInput{
				{FromRevisionID: created.Draft.ID, Document: &tc.raw},
				{FromRevisionID: created.Draft.ID, ToRevisionID: &created.Draft.ID},
			} {
				report, err := r.AnalyzeImpact(t.Context(), created.Design.ID, input)
				if err != nil {
					t.Fatal(err)
				}
				wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(tc.raw)))
				if report.FromHash != wantHash || report.ProposedHash != wantHash {
					t.Fatalf("saved bytes were normalized: from=%s to=%s want=%s",
						report.FromHash, report.ProposedHash, wantHash)
				}
				if len(report.Changes) != 0 {
					t.Fatalf("identical saved bytes produced changes: %+v", report.Changes)
				}
			}
			var saved string
			if err := db.R.QueryRowContext(t.Context(),
				"SELECT document FROM api_design_revisions WHERE id=?", created.Draft.ID).Scan(&saved); err != nil {
				t.Fatal(err)
			}
			if saved != tc.raw {
				t.Fatal("analysis changed the stored revision")
			}
		})
	}
}

func TestAnalyzeImpactChecksSavedSizeBeforeDecoding(t *testing.T) {
	t.Parallel()
	for _, oversizedTarget := range []bool{false, true} {
		t.Run(fmt.Sprintf("target=%t", oversizedTarget), func(t *testing.T) {
			r, db := testRepo(t)
			created, err := r.Create(t.Context(), CreateInput{Name: "Impact", Document: testDocument, Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			saved, err := r.Save(t.Context(), created.Design.ID, SaveInput{
				ExpectedVersion: 1, Document: strings.Replace(testDocument, "Orders", "New", 1), Source: "ui",
			})
			if err != nil {
				t.Fatal(err)
			}
			oversizedID := created.Draft.ID
			if oversizedTarget {
				oversizedID = saved.Draft.ID
			}
			err = db.Write(t.Context(), func(tx *sql.Tx) error {
				// Invalid JSON distinguishes a pre-load size check from a decode
				// error. This can occur after lowering the configured body limit.
				_, err := tx.ExecContext(
					t.Context(),
					"UPDATE api_design_revisions SET document=? WHERE id=?",
					strings.Repeat("x", int(r.cfg.MaxBody)+1),
					oversizedID,
				)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.AnalyzeImpact(t.Context(), created.Design.ID, ImpactInput{
				FromRevisionID: created.Draft.ID, ToRevisionID: &saved.Draft.ID,
			})
			if !errors.Is(err, specs.ErrTooLarge) {
				t.Fatalf("saved size was not checked before decoding: %v", err)
			}
		})
	}
}
