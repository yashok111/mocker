package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"math"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

func rebaseHistoryBytes(t *testing.T, r *Repo) map[string]string {
	t.Helper()
	out := immutableBytes(t, r)
	for _, query := range []string{
		`SELECT 'draft:'||id,document FROM backend_change_proposal_revisions`,
		`SELECT 'batch:'||revision_id,document||char(10)||commands||char(10)||commands_hash FROM backend_change_proposal_batches`,
		`SELECT 'event:'||proposal_id||':'||version,document FROM backend_change_proposal_events`,
		`SELECT 'identity:'||proposal_id||':'||id,document FROM backend_change_proposal_identities`,
		`SELECT 'command:'||proposal_id||':'||command_id,document FROM backend_change_proposal_commands`,
		`SELECT 'receipt:'||scope||':'||key,response FROM backend_command_receipts WHERE scope LIKE 'change-proposal-%'`,
	} {
		rows, err := r.db.R.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var key, value string
			if err = rows.Scan(&key, &value); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			out[key] = value
		}
		scanErr := rows.Err()
		closeErr := rows.Close()
		if scanErr != nil {
			t.Fatal(scanErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	return out
}
func TestChangeRebaseReadyNoopRepairReplayQuotaAndRestart(t *testing.T) {
	r, base, d := changeFixture(t)
	before := rebaseHistoryBytes(t, r)
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_change_proposals SET status='ready',ready_reference='{}' WHERE id=?`, d.Proposal.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	noop := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []ChangeCriterion{}})
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID, RepairCommands: []ChangeProposalCommand{noop}}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash == nil {
		t.Fatalf("ready rebase rejected: %+v", preview)
	}
	// A source head advance must not invalidate an explicitly selected immutable N.
	nextInput := source6Input(t, &base.Project)
	nextInput.IdempotencyKey = "head-advance"
	nextInput.Manifest.RepositoryName = "other"
	commitSource6Fixture(t, r, &base.Project, nextInput)
	apply := ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: "ready-rebase"}
	out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	if out.Proposal.Status != "draft" || out.Proposal.Version != 2 || len(out.Changes) != 1 {
		t.Fatalf("ready/noop result: %+v", out)
	}
	var ready sql.NullString
	var status string
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT status,ready_reference FROM backend_change_proposals WHERE id=?`, d.Proposal.ID).Scan(&status, &ready); err != nil {
		t.Fatal(err)
	}
	if status != "draft" || ready.Valid {
		t.Fatal("current ready association survived rebase")
	}
	_, err = r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalRebaseInput{ExpectedVersion: out.Proposal.Version, ProposalRevisionID: out.Revision.ID, NewBaseRevisionID: base.Revision.ID, RepairCommands: []ChangeProposalCommand{noop}})
	assertFault(t, err, "backend_change_command_conflict")
	current := rebaseHistoryBytes(t, r)
	for key, value := range before {
		if current[key] != value {
			t.Fatalf("historical bytes changed %s", key)
		}
	}
	// Fill immutable revision retention and move status after acknowledgement.
	if err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		p := out.Proposal
		for i := range MaxChangeProposalRevisions {
			rev := out.Revision
			rev.ID = uuid.NewV7().String()
			rev.ParentRevisionID = new(out.Revision.ID)
			rev.AcceptedBatchRevisionID = rev.ID
			p.Version = int64(i + 10)
			if err := persistChangeRevision(t.Context(), tx, p, rev, "rebase", "", []ChangeProposalCommand{}, nil); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_change_proposals SET status='archived' WHERE id=?`, d.Proposal.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertFault(t, checkChangeProposalQuota(t.Context(), r.db.R, base.Project.ID, d.Proposal.ID), "backend_change_quota_exceeded")
	path := r.db.Path()
	if err = r.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	r = NewRepo(reopened)
	replay, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(out)
	got, _ := json.Marshal(replay)
	if string(want) != string(got) {
		t.Fatal("acknowledged receipt changed across status/quota/restart")
	}
	after := rebaseHistoryBytes(t, r)
	for key, value := range current {
		if after[key] != value {
			t.Fatalf("restart changed immutable bytes %s", key)
		}
	}
}
func TestChangeRebaseVersionOverflowAndCandidateMismatchReserveNothing(t *testing.T) {
	r, base, d := changeFixture(t)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	before := rebaseHistoryBytes(t, r)
	_, err = r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash + "stale", IdempotencyKey: "mismatch"})
	assertFault(t, err, "backend_change_preview_conflict")
	after := rebaseHistoryBytes(t, r)
	if len(before) != len(after) {
		t.Fatal("failed apply reserved history")
	}
	if err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_change_proposals SET version=? WHERE id=?`, int64(math.MaxInt64), d.Proposal.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	in.ExpectedVersion = math.MaxInt64
	_, err = r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	assertFault(t, err, "backend_change_version_conflict")
}
func TestChangeRebaseSource5UpgradeAndDowngradeRejection(t *testing.T) {
	service, old, _, _ := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "source5", BaseRevisionID: base.Revision.ID, IdempotencyKey: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	nextInput := source6Input(t, &base.Project)
	nextInput.IdempotencyKey = "source6"
	nextInput.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	nextInput.Manifest.RepositoryName = "additional-source"
	_, next := commitSource6Fixture(t, r, &base.Project, nextInput)
	for index, rid := range []string{base.Revision.ID, next.Revision.ID} {
		in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: rid}
		preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if preview.CandidateHash == nil {
			t.Fatalf("source5/6 blocked: %+v", preview)
		}
		key := "same-source5"
		if index == 1 {
			key = "upgrade-source6"
		}
		out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		d = &ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}
	}
	_, err = r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID})
	assertFault(t, err, "backend_unsupported_scope")
}
