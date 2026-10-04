package backendmodel

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"uuid"
)

func TestSource6QuotaCountsDecisionDocuments(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "quota")
	a, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, a.RepositoryID)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	decision := SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "same", EvidenceKeys: []string{"proof"}}
	sendCommands(t, r, p, s, 1, "decision", ImportCommand{Op: "claim_identity", ClaimIdentity: &decision})
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		n, err := stagingBytes(t.Context(), tx, p.ID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE backend_import_source_decisions SET document=json_set(document,'$.claimIdentity.reason',?) WHERE session_id=?`, strings.Repeat("expanded ", 1000), s.ID); err != nil {
			return err
		}
		return r.checkStaging(t.Context(), tx, p.ID, MaxProjectStagingBytes-n)
	})
	if err == nil {
		t.Fatal("source decision bytes escaped project staging quota")
	}
	assertFault(t, err, "backend_import_limit")
}

func TestSource6CommitRollback(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "rollback")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	before := immutableBytes(t, r)
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER source6_fail BEFORE INSERT ON backend_revision_assertions BEGIN SELECT RAISE(ABORT,'injected source6 failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "failure"})
	if err == nil {
		t.Fatal("trigger failure ignored")
	}
	after := immutableBytes(t, r)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("commit failure published partial source rows")
	}
	for _, table := range []string{"backend_repositories", "backend_identity_bindings", "backend_revision_assertions"} {
		var count int
		if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback %s rows=%d err=%v", table, count, err)
		}
	}
}

func TestSource6RevisionQuotaIncludesArtifactContext(t *testing.T) {
	pins, bindings := apiTestVector()
	p := &composedGraphPreparation{candidate: &composedCandidate{Source: &SourceGraphSnapshot{Assertions: []ProviderAssertion{}}, Graph: &graphCandidate{ArtifactPins: pins, APIArtifactContext: &APIArtifactContext{SourceContentHash: fixtureHash, SourceSemanticHash: fixtureHash, Bindings: bindings}}}}
	if err := p.validateLimits(MaxRevisionBytes); err == nil {
		t.Fatal("exact artifact pins/context were excluded from revision byte quota")
	}
	if err := p.validateLimits(0); err != nil {
		t.Fatal(err)
	}
}
