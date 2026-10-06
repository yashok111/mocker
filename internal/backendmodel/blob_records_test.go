package backendmodel

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/yashok111/mocker/internal/backendblob"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestBlobMigrationPreservesLegacyRawArtifactCAS(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "blob-cas")
	in := source6Input(t, p)
	_, base := commitSource6Fixture(t, r, p, in)
	var before string
	// Independent frozen Store26 algorithm reads physical TEXT before the named
	// migration runs. In particular it uses historical table names and raw ORDER
	// BY, not blob keys, codec reserialization, or the new read adapter.
	err := testkit.EditLegacyBackendFixture(t.Context(), r.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE backend_graph_records SET document=char(10)||'  '||document||char(10) WHERE revision_id=?`, base.Revision.ID); err != nil {
			return err
		}
		h := sha256.New()
		for _, table := range []string{"backend_graph_records", "backend_revision_sources", "backend_revision_decisions", "backend_revision_assertions", "backend_revision_assertion_resolutions", "backend_revision_legacy_proof_bases"} {
			fmt.Fprintf(h, "%s\x00", table)
			rows, err := tx.Query("SELECT document FROM "+table+" WHERE revision_id=? ORDER BY document", base.Revision.ID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var raw []byte
				if err = rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				fmt.Fprintf(h, "%d\x00", len(raw))
				h.Write(raw)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			h.Write([]byte{0})
		}
		before = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := source6ArtifactRowsDigest(t.Context(), r.db.R, base.Revision.ID)
	if err != nil || after != before {
		t.Fatalf("CAS changed %s -> %s (%v)", before, after, err)
	}
	if err = r.db.Read(t.Context(), func(tx *sql.Tx) error { return backendblob.Verify(t.Context(), tx) }); err != nil {
		t.Fatal(err)
	}
}

func TestBlobEditorArtifactCopyHasCanonicalMembership(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "canonical-copy")
	if err := s.repo.db.Read(t.Context(), func(tx *sql.Tx) error { return backendblob.Verify(t.Context(), tx) }); err != nil {
		t.Fatal(err)
	}
}
