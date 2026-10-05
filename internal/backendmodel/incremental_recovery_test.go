package backendmodel

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func incrementalInput(t *testing.T, p *Project, s *ImportSession, changes ChangeManifest) BeginImportInput {
	t.Helper()
	in := source6Input(t, p)
	in.IdempotencyKey = "incremental"
	in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace}
	in.SyncPolicy = IncrementalSourcePolicy
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	members["changeManifest"] = changes
	raw, err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func emptyIncrementalChanges() ChangeManifest {
	return ChangeManifest{Scope: "affected-subgraph", Files: []ChangeManifestFile{}, AffectedRoots: []SourceSubjectRef{}}
}

func TestIncrementalRecoveryReadyRestartAndExactReplay(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "incremental")
	baseSession, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &base.Project
	before, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	in := incrementalInput(t, p, baseSession, emptyIncrementalChanges())
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: s.Version, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if preview.State != "ready" {
		t.Fatalf("preview=%+v", preview)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r = NewRepo(reopened)
	saved, err := loadSession(t.Context(), r.db.R, p.ID, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate, diagnostics, err := prepareComposedGraph(t.Context(), r.db.R, saved)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) > 0 {
		t.Fatalf("diagnostics=%+v", diagnostics)
	}
	hash, err := source6CandidateHash(saved, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if hash != *preview.CandidateHash {
		t.Fatal("restart changed incremental candidate")
	}
	commit := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "incremental-commit"}
	result, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	after, err := r.ResolveSourceGraph(t.Context(), p.ID, result.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for id, raw := range before.RawEvidence {
		if !bytes.Equal(raw, after.RawEvidence[id]) {
			t.Fatal("untouched raw proof changed")
		}
	}
	for _, current := range after.Currentness {
		if current.Own.Status != "current" || current.Own.ConfirmedSnapshotID != baseSession.SnapshotID {
			t.Fatalf("currentness=%+v", current)
		}
	}
	nextInput := source6Input(t, &result.Project)
	nextInput.IdempotencyKey = "newer-head"
	nextInput.Manifest.RepositoryName = "other"
	_, _ = commitSource6Fixture(t, r, &result.Project, nextInput)
	again, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(result)
	got, _ := json.Marshal(again)
	if !bytes.Equal(want, got) {
		t.Fatal("commit receipt changed after newer head")
	}
	replay, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	firstRaw, _ := json.Marshal(s)
	replayRaw, _ := json.Marshal(replay)
	if !bytes.Equal(firstRaw, replayRaw) {
		t.Fatal("begin receipt changed after newer head")
	}
	changed := in
	changed.SyncPolicy = WholeSourcePolicy
	if _, err := r.BeginImport(t.Context(), p.ID, changed); err == nil {
		t.Fatal("policy reused old begin idempotency key")
	} else {
		assertFault(t, err, "backend_idempotency_conflict")
	}
}

func TestIncrementalRecoveryCancellationWritesNothing(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "cancel")
	baseSession, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &base.Project
	s, err := r.BeginImport(t.Context(), p.ID, incrementalInput(t, p, baseSession, emptyIncrementalChanges()))
	if err != nil {
		t.Fatal(err)
	}
	before := immutableBytes(t, r)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = r.PreviewImport(ctx, p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: s.Version, BaseRevisionID: p.CurrentRevisionID})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	after := immutableBytes(t, r)
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("cancelled preparation changed %s", key)
		}
	}
	saved, err := loadSession(t.Context(), r.db.R, p.ID, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != s.Version || saved.State != "collecting" {
		t.Fatalf("cancelled session=%+v", saved)
	}
}

func TestIncrementalScopeRepoUpdateRequiresDeclaredRoot(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "scope")
	baseSession, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &base.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	a := graph.Assertions[0]
	for _, root := range []bool{false, true} {
		t.Run(map[bool]string{false: "outside", true: "declared"}[root], func(t *testing.T) {
			changes := emptyIncrementalChanges()
			if root {
				changes.AffectedRoots = []SourceSubjectRef{{RecordType: a.RecordType, ID: a.RecordID}}
			}
			in := incrementalInput(t, p, baseSession, changes)
			in.IdempotencyKey = t.Name()
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			batch := putFixture(t, r, p, s)
			preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if root {
				if err != nil {
					t.Fatal(err)
				}
				if preview.State != "ready" {
					t.Fatalf("explicit root preview=%+v", preview)
				}
			} else if err == nil && preview.State == "ready" {
				t.Fatal("untouched existing update authorized itself")
			}
		})
	}
}

func TestIncrementalDeletionExplicitAndVerified(t *testing.T) {
	for _, variant := range []string{"retain omitted", "explicit delete", "partial scope", "unverified"} {
		t.Run(variant, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "deletion")
			baseSession, base := commitSource6Fixture(t, r, p, source6Input(t, p))
			p = &base.Project
			graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			a := graph.Assertions[0]
			changes := emptyIncrementalChanges()
			changes.Files = []ChangeManifestFile{{Kind: "deleted", Path: "main.go", BeforeHash: fixtureHash}}
			in := incrementalInput(t, p, baseSession, changes)
			in.Manifest.Snapshot.Files = []ManifestFile{}
			for i := range in.Inventory {
				if in.Inventory[i].Category == "files" {
					in.Inventory[i].KnownCount = 0
					in.Inventory[i].Denominator = new(int64(0))
				}
			}
			if variant == "partial scope" {
				in.ScopeStatus = &SourceScopeStatus{Status: "partial", Gaps: []string{"scope incomplete"}}
			}
			if variant == "unverified" {
				in.Manifest.Snapshot.Consistency = "unverified"
			}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			version := s.Version
			if variant != "retain omitted" {
				batch := sendCommands(t, r, p, s, version, "delete", ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: a.RecordType, ExternalKey: a.ExternalKey, ExpectedID: a.RecordID, Reason: "source removed"}})
				version = batch.AcceptedVersion
			}
			preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: version, BaseRevisionID: p.CurrentRevisionID})
			if err != nil {
				t.Fatal(err)
			}
			if variant == "partial scope" || variant == "unverified" {
				if preview.State == "ready" {
					t.Fatal("unsafe deletion became ready")
				}
				return
			}
			if preview.State != "ready" {
				t.Fatalf("preview=%+v", preview)
			}
			out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"})
			if err != nil {
				t.Fatal(err)
			}
			after, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			if variant == "retain omitted" {
				if len(after.Assertions) != 1 || after.Currentness[0].Own.Status != "stale" {
					t.Fatal("deleted source silently deleted or refreshed omitted assertion")
				}
			} else if len(after.Assertions) != 0 {
				t.Fatal("explicit deletion not applied")
			}
			if len(after.SourceVector.Partitions) != 1 {
				t.Fatal("empty active partition lost")
			}
		})
	}
}

func TestIncrementalCurrentnessCannotOverwriteProofWithoutReobservedClaim(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "proof-only")
	baseSession, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &base.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	a := graph.Assertions[0]
	changes := emptyIncrementalChanges()
	changes.AffectedRoots = []SourceSubjectRef{{RecordType: a.RecordType, ID: a.RecordID}}
	s, err := r.BeginImport(t.Context(), p.ID, incrementalInput(t, p, baseSession, changes))
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	batch := sendCommands(t, r, p, s, s.Version, "proof-only", commands[1])
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err == nil && preview.State == "ready" {
		t.Fatal("proof bytes overwritten while retained claim/hash remained unchanged")
	}
}

func TestIncrementalRecoveryCandidateFencesManifestAndCAS(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "fences")
	baseSession, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &base.Project
	in := incrementalInput(t, p, baseSession, emptyIncrementalChanges())
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: s.Version, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := loadSession(t.Context(), r.db.R, p.ID, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate, _, err := prepareComposedGraph(t.Context(), r.db.R, saved)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	root := SourceSubjectRef{RecordType: graph.Assertions[0].RecordType, ID: graph.Assertions[0].RecordID}
	changed := *saved
	changes := *saved.ChangeManifest
	changes.AffectedRoots = []SourceSubjectRef{root}
	changed.ChangeManifest = &changes
	hash, err := source6CandidateHash(&changed, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if hash == *preview.CandidateHash {
		t.Fatal("changed manifest did not change candidate fence")
	}
	altered := in
	altered.ChangeManifest = &changes
	if _, err := r.BeginImport(t.Context(), p.ID, altered); err == nil {
		t.Fatal("changed manifest replay accepted")
	} else {
		assertFault(t, err, "backend_idempotency_conflict")
	}
	newer := source6Input(t, p)
	newer.IdempotencyKey = "advance"
	newer.Manifest.RepositoryName = "other"
	_, _ = commitSource6Fixture(t, r, p, newer)
	before := immutableBytes(t, r)
	_, err = r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "stale-head"})
	if err == nil {
		t.Fatal("stale project CAS accepted")
	}
	assertFault(t, err, "backend_version_conflict")
	after := immutableBytes(t, r)
	if len(before) != len(after) {
		t.Fatal("failed CAS wrote immutable rows")
	}
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("failed CAS changed %s", key)
		}
	}
}

func TestIncrementalCurrentnessForeignCallerRetainsProofAndGetsDependencyGap(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "foreign")
	selected, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	target := sourceAssertionRef(graph.Assertions[0])
	foreignInput := source6Input(t, p)
	foreignInput.IdempotencyKey = "foreign"
	foreignInput.Manifest.RepositoryName = "foreign-repository"
	foreign, err := r.BeginImport(t.Context(), p.ID, foreignInput)
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(foreign)
	commands[0].Node.ExternalKey = "caller"
	commands[1].Evidence.SubjectKey = "caller"
	proof := *commands[1].Evidence
	proof.ExternalKey = "call-proof"
	proof.SubjectType = "edge"
	proof.SubjectKey = "call"
	commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "call", Kind: "calls", FromRef: &ImportRecordRef{LocalKey: "caller"}, ToRef: &ImportRecordRef{Base: &target}, Attributes: commands[0].Node.Attributes, EvidenceKeys: []string{"call-proof"}}}, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	batch := sendCommands(t, r, p, foreign, foreign.Version, "caller", commands...)
	withCaller := commitStaged(t, r, p, foreign, batch.AcceptedVersion, "foreign-commit")
	p = &withCaller.Project
	before, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	changes := emptyIncrementalChanges()
	changes.Files = []ChangeManifestFile{{Kind: "modified", Path: "main.go", BeforeHash: fixtureHash, AfterHash: incrementalFile("main.go", "b").ContentHash}}
	in := incrementalInput(t, p, selected, changes)
	in.Manifest.Snapshot.Files[0].ContentHash = changes.Files[0].AfterHash
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	result := commitStaged(t, r, p, s, s.Version, "incremental")
	after, err := r.ResolveSourceGraph(t.Context(), p.ID, result.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range before.Assertions {
		if a.Owner.RepositoryID != foreign.RepositoryID {
			continue
		}
		for _, eid := range a.EvidenceIDs {
			if !bytes.Equal(before.RawEvidence[eid], after.RawEvidence[eid]) {
				t.Fatal("foreign proof bytes rewritten")
			}
		}
		for _, current := range after.Currentness {
			if current.RecordID == a.RecordID && current.RepositoryID == foreign.RepositoryID {
				if current.Own.Status != "current" || current.Own.ConfirmedSnapshotID != foreign.SnapshotID || current.Dependency.Status != "stale" {
					t.Fatalf("foreign %s currentness=%+v", a.ExternalKey, current)
				}
			}
		}
	}
}

func TestIncrementalDeletionRetainedMappingPreventsPortRemoval(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "ports")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	commands := runtimeSource6Commands(t, s, true)
	stepKey := structuralID("step")
	mappingKey := "mapping"
	for _, command := range commands {
		if command.Node != nil && command.Node.ExternalKey == stepKey {
			command.Node.Attributes["outputs"] = relationalRaw(t, []any{map[string]any{"key": "value", "name": "value", "nativeType": known("string")}, map[string]any{"key": "mapped", "name": "mapped", "nativeType": known("string")}})
		}
	}
	value := func(key string) map[string]any {
		return map[string]any{"kind": "port", "nodeRef": ImportRecordRef{LocalKey: stepKey}, "collection": "outputs", "portKey": key}
	}
	attrs := runtimeAttrs(t, map[string]any{"sources": []any{value("value")}, "destination": value("mapped"), "transform": LineageTransform{Kind: "copy", Description: "copy local value"}, "analysisStatus": "complete", "gaps": []string{}})
	commands = append(commands, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: mappingKey, Kind: "field_mapping", Name: "Copy", ParentRef: &ImportRecordRef{LocalKey: stepKey}, Attributes: attrs, EvidenceKeys: []string{"mapping-proof"}}}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "mapping-contains", Kind: "contains", FromRef: &ImportRecordRef{LocalKey: stepKey}, ToRef: &ImportRecordRef{LocalKey: mappingKey}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"contains-proof"}}})
	for _, item := range []struct{ typ, key, proof string }{{typ: "node", key: mappingKey, proof: "mapping-proof"}, {typ: "edge", key: "mapping-contains", proof: "contains-proof"}} {
		e := *fixtureCommands(s)[1].Evidence
		e.SubjectType = item.typ
		e.SubjectKey = item.key
		e.ExternalKey = item.proof
		e.Source.StartLine = new(int64(1))
		e.Source.EndLine = new(int64(2))
		commands = append(commands, ImportCommand{Op: "upsert_evidence", Evidence: &e})
	}
	batch := sendCommands(t, r, p, s, s.Version, "base", commands...)
	base := commitStaged(t, r, p, s, batch.AcceptedVersion, "base")
	p = &base.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var step ProviderAssertion
	for _, a := range graph.Assertions {
		if a.ExternalKey == stepKey && a.RecordType == "node" {
			step = a
		}
	}
	changes := emptyIncrementalChanges()
	changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: step.RecordID}}
	next, err := r.BeginImport(t.Context(), p.ID, incrementalInput(t, p, s, changes))
	if err != nil {
		t.Fatal(err)
	}
	updated := relationalSelect(runtimeSource6Commands(t, next, true), stepKey, "proof:"+stepKey)
	for _, command := range updated {
		if command.Node != nil {
			command.Node.Attributes["outputs"] = relationalRaw(t, []any{map[string]any{"key": "mapped", "name": "mapped", "nativeType": known("string")}})
		}
	}
	batch = sendCommands(t, r, p, next, next.Version, "remove-port", updated...)
	preview, err := r.PreviewImport(t.Context(), p.ID, next.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if preview.State == "ready" {
		t.Fatal("removed port still selected by retained mapping was accepted")
	}
}

func TestIncrementalRecoveryRevisionQuotaIncludesManifestAndClosure(t *testing.T) {
	p := &composedGraphPreparation{session: &ImportSession{SourceScope: &SourceScope{Kind: "reconcile"}, ChangeManifest: new(emptyIncrementalChanges())}, candidate: &composedCandidate{Source: &SourceGraphSnapshot{Assertions: []ProviderAssertion{}}, Graph: &graphCandidate{}, IncrementalScope: &IncrementalAffectedScope{Affected: []SourceSubjectRef{{RecordType: "node", ID: "subject"}}}}}
	if err := p.validateLimits(MaxRevisionBytes); err == nil {
		t.Fatal("persisted incremental manifest/closure bypassed revision byte limit")
	}
	if err := p.validateLimits(0); err != nil {
		t.Fatal(err)
	}
}
