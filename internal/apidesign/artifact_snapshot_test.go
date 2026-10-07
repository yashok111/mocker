package apidesign

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func artifactFixture(t *testing.T, raw string) *ArtifactSnapshot {
	t.Helper()
	identity, err := withOperationKeys(raw, "", 7)
	if err != nil {
		t.Fatal(err)
	}
	return &ArtifactSnapshot{DesignID: 7, RevisionID: 8, Document: raw, IdentityDocument: identity, ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))}
}
func TestResolveArtifactObject(t *testing.T) {
	raw := `{"paths":{"/a":{"$ref":"#/components/pathItems/P"},"/b":{"$ref":"#/components/pathItems/P"}},"components":{"pathItems":{"P":{"get":{"summary":"Read","responses":{},"x-user":true}}},"schemas":{"A":{"properties":{"x-field":false,"example":{"$ref":"#/components/schemas/A"}}}}}}`
	snapshot := artifactFixture(t, raw)
	root, err := impactDocument(snapshot.IdentityDocument)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := authoredOperations(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		key, _ := op.key()
		got, err := ResolveArtifactObject(t.Context(), snapshot, ArtifactSelector{ObjectKey: key.(string)})
		if err != nil {
			t.Fatal(err)
		}
		if got.ConsumerPointer != op.pointer || got.Pointer != "/components/pathItems/P/get" || !strings.Contains(got.Document, "x-user") {
			t.Fatalf("%+v", got)
		}
	}
	for _, pointer := range []string{"/components/schemas/A/properties/x-field", "/components/schemas/A/properties/example"} {
		got, err := ResolveArtifactObject(t.Context(), snapshot, ArtifactSelector{JSONPointer: pointer})
		if err != nil || got.Pointer != pointer {
			t.Fatalf("%+v %v", got, err)
		}
	}
}
func TestArtifactSnapshotOwnershipAndLimit(t *testing.T) {
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "API", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ArtifactSnapshot(t.Context(), d.Design.ID, d.Draft.ID)
	if err != nil || got.ContentHash != d.Draft.Hash {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := r.ArtifactSnapshot(t.Context(), d.Design.ID+1, d.Draft.ID); err == nil {
		t.Fatal("foreign revision")
	}
	r.cfg.MaxBody = 1
	if _, err := r.ArtifactSnapshot(t.Context(), d.Design.ID, d.Draft.ID); err == nil {
		t.Fatal("oversized document")
	}
}

func TestArtifactSnapshotRawLegacyAndDigest(t *testing.T) {
	r, db := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Legacy", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	raw := testDocument
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	// Test fixture models a pre-identity immutable revision; remove its write fence.
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		// The cursor is closed before the DROPs run, as before, but on every path.
		names, err := func() ([]string, error) {
			rows, err := tx.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name='api_design_revisions'`)
			if err != nil {
				return nil, err
			}
			defer func() { _ = rows.Close() }()
			names := []string{}
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					return nil, err
				}
				names = append(names, name)
			}
			return names, rows.Err()
		}()
		if err != nil {
			return err
		}
		for _, name := range names {
			if _, err := tx.ExecContext(t.Context(), `DROP TRIGGER "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(t.Context(), `UPDATE api_design_revisions SET document=?,hash=? WHERE id=?`, raw, hash, d.Draft.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ArtifactSnapshot(t.Context(), d.Design.ID, d.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Document != raw || got.ContentHash != hash || got.IdentityDocument == raw || !strings.Contains(got.IdentityDocument, "legacy-") {
		t.Fatalf("raw/hydrated mixed: %+v", got)
	}
	tx, err := db.R.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	digest, err := r.ArtifactDigestTx(t.Context(), tx, d.Design.ID, d.Draft.ID)
	if err != nil || digest != hash {
		t.Fatalf("digest %s %v", digest, err)
	}
	if _, err := r.ArtifactDigestTx(t.Context(), tx, d.Design.ID+1, d.Draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE api_design_revisions SET hash=? WHERE id=?`, strings.Repeat("0", 64), d.Draft.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ArtifactSnapshot(t.Context(), d.Design.ID, d.Draft.ID); err == nil {
		t.Fatal("raw hash mismatch accepted")
	}
}
func TestArtifactObjectAuthoredScopeAndUnsupported(t *testing.T) {
	raw := `{"paths":{"/a":{"$ref":"#/components/pathItems/P","servers":[{"url":"https://a"}]}},"components":{"pathItems":{"P":{"get":{"responses":{},"x-user":true}}}}}`
	a := artifactFixture(t, raw)
	root, _ := impactDocument(a.IdentityDocument)
	ops, _ := authoredOperations(root)
	key, _ := ops[0].key()
	selected, err := ResolveArtifactObject(t.Context(), a, ArtifactSelector{ObjectKey: key.(string)})
	if err != nil {
		t.Fatal(err)
	}
	b := artifactFixture(t, strings.Replace(raw, "https://a", "https://b", 1))
	changed, err := ResolveArtifactObject(t.Context(), b, ArtifactSelector{ObjectKey: key.(string)})
	if err != nil {
		t.Fatal(err)
	}
	if a.ContentHash == b.ContentHash || selected.ObjectHash != changed.ObjectHash || selected.Pointer != changed.Pointer || selected.ConsumerPointer != changed.ConsumerPointer || strings.Contains(selected.Document, "legacy-") {
		t.Fatal("authored object hash scope")
	}
	for _, ref := range []string{"https://example.test/api", "#/missing", "#/paths/~1a"} {
		snap := artifactFixture(t, `{"paths":{"/a":{"get":{"responses":{},"x-mocker-canvas-operation-id":"known"},"$ref":"`+ref+`"}}}`)
		if _, err := ResolveArtifactObject(t.Context(), snap, ArtifactSelector{ObjectKey: "known"}); err == nil {
			t.Fatalf("unsupported %s", ref)
		}
	}
}

func TestArtifactObjectHashSelectedMutation(t *testing.T) {
	a := artifactFixture(t, `{"components":{"schemas":{"A":{"type":"string","x-authored":true}}}}`)
	b := artifactFixture(t, `{"components":{"schemas":{"A":{"type":"integer","x-authored":true}}}}`)
	x, err := ResolveArtifactObject(t.Context(), a, ArtifactSelector{JSONPointer: "/components/schemas/A"})
	if err != nil {
		t.Fatal(err)
	}
	y, err := ResolveArtifactObject(t.Context(), b, ArtifactSelector{JSONPointer: "/components/schemas/A"})
	if err != nil {
		t.Fatal(err)
	}
	if x.ObjectHash == y.ObjectHash {
		t.Fatal("selected authored mutation did not change ObjectHash")
	}
}
func TestArtifactObjectHashKeyOrder(t *testing.T) {
	a := artifactFixture(t, `{"components":{"schemas":{"A":{"type":"string","x-authored":true}}}}`)
	b := artifactFixture(t, `{"components":{"schemas":{"A":{"x-authored":true,"type":"string"}}}}`)
	x, err := ResolveArtifactObject(t.Context(), a, ArtifactSelector{JSONPointer: "/components/schemas/A"})
	if err != nil {
		t.Fatal(err)
	}
	y, err := ResolveArtifactObject(t.Context(), b, ArtifactSelector{JSONPointer: "/components/schemas/A"})
	if err != nil {
		t.Fatal(err)
	}
	if x.ObjectHash != y.ObjectHash {
		t.Fatal("authored key order changed ObjectHash")
	}
}

func TestArtifactSnapshotExactHistoricalVersion(t *testing.T) {
	r, db := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Historic API", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: strings.Replace(testDocument, "Orders", "Newer", 1), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := r.ArtifactSnapshot(t.Context(), d.Design.ID, d.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := r.ArtifactSnapshot(t.Context(), d.Design.ID, newer.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Version != d.Draft.Version || old.Version != 1 || latest.Version != newer.Draft.Version || latest.Version != 2 || old.ContentHash != d.Draft.Hash {
		t.Fatalf("selected revision version old=%+v latest=%+v", old, latest)
	}
	var head, version int64
	if err := db.R.QueryRowContext(t.Context(), `SELECT draft_revision_id,version FROM api_designs WHERE id=?`, d.Design.ID).Scan(&head, &version); err != nil {
		t.Fatal(err)
	}
	if head != newer.Draft.ID || version != 2 {
		t.Fatal("snapshot changed owner head")
	}
}
