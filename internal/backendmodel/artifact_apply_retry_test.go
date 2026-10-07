package backendmodel

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
)

// review 2026-10-06, F66: an identical retry that passed the receipt pre-check
// before the first apply committed must replay the stored receipt, not report
// the first apply's own commit as a version conflict.
func TestArtifactApplyOverlappingRetryReplaysReceipt(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	first, request := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "overlap")
	digest, err := requestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	// Model the retry's state: its pre-check already missed the receipt.
	again, err := s.applyAfterReceiptMiss(t.Context(), base.Project.ID, request, "artifact-pins:"+base.Project.ID, digest)
	if err != nil {
		t.Fatalf("overlapping retry: %v", err)
	}
	if again.Revision.ID != first.Revision.ID || string(again.ReceiptBytes()) != string(first.ReceiptBytes()) {
		t.Fatal("retry did not replay the original receipt")
	}
}

// review 2026-10-06, F98: the API pins Apply has the same overlap window.
func TestAPIPinsApplyOverlappingRetryReplaysReceipt(t *testing.T) {
	t.Parallel()
	s, base, ids, api := apiPinFixture(t)
	first, request := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "overlap")
	digest, err := requestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.applyAfterReceiptMiss(t.Context(), base.Project.ID, request, "api-pins:"+base.Project.ID, digest)
	if err != nil {
		t.Fatalf("overlapping retry: %v", err)
	}
	if again.Revision.ID != first.Revision.ID || string(again.ReceiptBytes()) != string(first.ReceiptBytes()) {
		t.Fatal("retry did not replay the original receipt")
	}
}

var errStorageBusy = errors.New("database is locked (5) (SQLITE_BUSY)")

type busyScenarioDigests struct{ ScenarioArtifactReader }

func (busyScenarioDigests) ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error) {
	return "", errStorageBusy
}

type busyAPIDigests struct{ APIArtifactReader }

func (busyAPIDigests) ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error) {
	return "", errStorageBusy
}

type busyAPISnapshots struct{ APIArtifactReader }

func (busyAPISnapshots) ArtifactSnapshot(context.Context, int64, int64) (*apidesign.ArtifactSnapshot, error) {
	return nil, errStorageBusy
}

// review 2026-10-06, F94: a storage fault while reading a pinned snapshot is a
// server error, not a 200 item with resolution "broken" (or a blocking preview
// diagnostic) that invites the agent to remove a healthy pin.
func TestAPIPinsSnapshotStorageFaultIsNotBroken(t *testing.T) {
	t.Parallel()
	s, base, ids, api := apiPinFixture(t)
	in := pinTestInput(base, ids, api)
	pinned, _ := applyPinTest(t, s, base.Project.ID, in, "pinned")
	busy := NewAPIArtifactService(s.repo, busyAPISnapshots{s.artifacts})
	page, err := busy.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: pinned.Revision.ID})
	if !errors.Is(err, errStorageBusy) {
		t.Fatalf("query: %+v %v", page, err)
	}
	in.BaseRevisionID, in.ExpectedVersion = pinned.Revision.ID, pinned.Project.Version
	preview, err := busy.Preview(t.Context(), base.Project.ID, in)
	if !errors.Is(err, errStorageBusy) {
		t.Fatalf("preview: %+v %v", preview, err)
	}
}

// review 2026-10-06, F65 and F94: a storage fault while reading an owner digest
// inside the apply transaction is a server error, not a 409 "digest changed"
// that tells the client to abandon a valid apply.
func TestArtifactApplyDigestStorageFaultIsNotConflict(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	busy := NewArtifactService(s.repo, s.api, busyScenarioDigests{s.scenarios})
	in := scenarioSet(base, ids, d)
	p, err := busy.Preview(t.Context(), base.Project.ID, in)
	if err != nil || !p.CanApply {
		t.Fatal(p, err)
	}
	_, err = busy.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, "busy"})
	if !errors.Is(err, errStorageBusy) {
		t.Fatalf("editor apply: %v", err)
	}

	a, abase, aids, api := apiPinFixture(t)
	busyAPI := NewAPIArtifactService(a.repo, busyAPIDigests{a.artifacts})
	pin := pinTestInput(abase, aids, api)
	preview, err := busyAPI.Preview(t.Context(), abase.Project.ID, pin)
	if err != nil || !preview.CanApply {
		t.Fatal(preview, err)
	}
	_, err = busyAPI.Apply(t.Context(), abase.Project.ID, ApplyAPIPinsInput{pin.BaseRevisionID, pin.ExpectedVersion, pin.Commands, preview.CandidateHash, "busy"})
	if !errors.Is(err, errStorageBusy) {
		t.Fatalf("api apply: %v", err)
	}
}
