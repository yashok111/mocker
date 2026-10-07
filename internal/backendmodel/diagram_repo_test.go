package backendmodel

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func diagramTestDocument(rid string) DiagramDocument {
	return DiagramDocument{Format: "backend-diagram-v1", Kind: "architecture", Target: BackendReadTarget{RevisionID: rid}, Payload: ArchitecturePayload{
		PrimarySystemID: "10000000-0000-4000-8000-000000000002",
		Elements:        []ArchitectureElement{{ID: "10000000-0000-4000-8000-000000000002", Label: "Orders", Role: "software_system", Responsibility: "Orders", Technology: "Go", Origin: DiagramOrigin{Kind: "authored", Reason: "Explicit system boundary"}, Refs: []DiagramRef{}}},
		Links:           []ArchitectureLink{},
	}}
}

func TestDiagramSaveReplayBeforeCAS(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "diagram-project")
	doc := diagramTestDocument(p.CurrentRevisionID)
	v1, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements[0].Label = "Orders v2"
	a := DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "A"}
	v2, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v2)
	b := a
	b.Document = diagramTestDocument(p.CurrentRevisionID)
	b.Document.Payload.Elements[0].Label = "Orders v3"
	b.ExpectedVersion, b.IdempotencyKey = 2, "B"
	if _, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, b); err != nil {
		t.Fatal(err)
	}
	replay, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	replayed, _ := json.Marshal(replay)
	if string(raw) != string(replayed) {
		t.Fatal("replay bytes changed after later save")
	}
	b.IdempotencyKey = "A"
	_, err = r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, b)
	assertFault(t, err, "backend_idempotency_conflict")
	var count int
	if err := db.R.QueryRow("SELECT count(*) FROM backend_diagram_versions_documents").Scan(&count); err != nil || count != 3 {
		t.Fatalf("versions=%d, %v", count, err)
	}
	b.ExpectedVersion, b.IdempotencyKey = 3, "noop"
	v, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, b)
	if err != nil || v.Pin.Version != 3 {
		t.Fatalf("noop: %+v, %v", v, err)
	}
}

func TestDiagramRaceAndImmutableRows(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "race-project")
	doc := diagramTestDocument(p.CurrentRevisionID)
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements[0].Label = "Changed"
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"race-a", "race-b"} {
		wg.Go(func() {
			_, err := r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: key})
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	success := 0
	for err := range errors {
		if err == nil {
			success++
		} else {
			assertFault(t, err, "backend_diagram_version_conflict")
		}
	}
	if success != 1 {
		t.Fatalf("successful writers: %d", success)
	}
	for _, sql := range []string{"UPDATE backend_diagram_versions SET author='forged'", "DELETE FROM backend_diagram_versions_documents", "DELETE FROM backend_diagram_receipts"} {
		if _, err := db.W.Exec(sql); err == nil {
			t.Fatalf("immutable mutation accepted: %s", sql)
		}
	}
}

func TestDiagramStrictWire(t *testing.T) {
	doc := diagramTestDocument("10000000-0000-4000-8000-000000000090")
	raw, _ := json.Marshal(doc)
	var fields map[string]jsontext.Value
	_ = json.Unmarshal(raw, &fields)
	for name, mutate := range map[string]func(map[string]jsontext.Value){
		"unknown":      func(m map[string]jsontext.Value) { m["provenance"] = jsontext.Value(`{}`) },
		"future kind":  func(m map[string]jsontext.Value) { m["kind"] = jsontext.Value(`"lifecycle"`) },
		"null payload": func(m map[string]jsontext.Value) { m["payload"] = jsontext.Value(`null`) },
		"mixed target": func(m map[string]jsontext.Value) {
			m["target"] = jsontext.Value(`{"revisionId":"10000000-0000-4000-8000-000000000090","changeProposal":{"proposalId":"10000000-0000-4000-8000-000000000091","proposalRevisionId":"10000000-0000-4000-8000-000000000092"}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var m map[string]jsontext.Value
			_ = json.Unmarshal(raw, &m)
			mutate(m)
			bad, _ := json.Marshal(m)
			var out DiagramDocument
			if json.Unmarshal(bad, &out) == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
}

func TestDiagramCreateForkFreshDB(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "fresh")
	var foreignKeys int
	if err := db.W.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d: %v", foreignKeys, err)
	}
	doc := diagramTestDocument(p.CurrentRevisionID)
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := r.ForkDiagram(t.Context(), p.ID, DiagramForkInput{Source: v.Pin, Target: doc.Target, Reason: "Independent mapping", IdempotencyKey: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	if fork.Pin.ID == v.Pin.ID || fork.Pin.Version != 1 {
		t.Fatal("fork reused identity")
	}
	before := diagramTableCounts(t, db)
	if _, err = db.W.Exec(`CREATE TRIGGER diagram_test_fail BEFORE INSERT ON backend_diagram_versions BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "failed-create"}); err == nil {
		t.Fatal("failure not injected")
	}
	if _, err = r.ForkDiagram(t.Context(), p.ID, DiagramForkInput{Source: v.Pin, Target: doc.Target, Reason: "Rollback", IdempotencyKey: "failed-fork"}); err == nil {
		t.Fatal("fork failure not injected")
	}
	if got := diagramTableCounts(t, db); got != before {
		t.Fatalf("partial write: %v != %v", got, before)
	}
	if _, err = db.W.Exec(`DROP TRIGGER diagram_test_fail`); err != nil {
		t.Fatal(err)
	}
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_diagrams(project_id,id,kind,version,target_json) VALUES(?,?,'architecture',1,?)`, p.ID, "10000000-0000-4000-8000-000000000099", `{"revisionId":"`+p.CurrentRevisionID+`"}`)
		return err
	})
	if err == nil {
		t.Fatal("incomplete head committed")
	}
	if got := diagramTableCounts(t, db); got != before {
		t.Fatal("deferred FK failure left rows")
	}
	if _, err = db.W.Exec(`CREATE TRIGGER diagram_test_receipt_fail BEFORE INSERT ON backend_diagram_receipts BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements[0].Label = "Changed"
	if _, err = r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, DiagramSaveInput{Document: doc, ExpectedVersion: 1, IdempotencyKey: "failed-save"}); err == nil {
		t.Fatal("receipt failure not injected")
	}
	if got := diagramTableCounts(t, db); got != before {
		t.Fatal("version inserted before failed receipt survived rollback")
	}
}

func diagramTableCounts(t *testing.T, db *store.DB) [5]int {
	t.Helper()
	var out [5]int
	for i, table := range []string{"backend_diagrams", "backend_diagram_versions", "backend_diagram_receipts", "backend_diagram_views", "backend_diagram_catalog"} {
		query := "SELECT count(*) FROM " + table
		if table == "backend_diagram_catalog" {
			query = "SELECT coalesce(sum(version),0) FROM " + table
		}
		if err := db.R.QueryRow(query).Scan(&out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestDiagramForkProvenanceRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(db)
	p := createProject(t, r, "provenance")
	doc := diagramTestDocument(p.CurrentRevisionID)
	original, err := r.CreateDiagram(WithDiagramActor(t.Context(), "A"), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := r.ForkDiagram(WithDiagramActor(t.Context(), "B"), p.ID, DiagramForkInput{Source: original.Pin, Target: doc.Target, Reason: "Reviewed fork", IdempotencyKey: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements = append(doc.Payload.Elements, ArchitectureElement{ID: "10000000-0000-4000-8000-000000000003", Label: "Payments", Role: "software_system", Origin: DiagramOrigin{Kind: "authored", Reason: "External boundary"}, Refs: []DiagramRef{}})
	saved, err := r.SaveDiagram(WithDiagramActor(t.Context(), "C"), p.ID, fork.Pin.ID, DiagramSaveInput{Document: doc, ExpectedVersion: 1, IdempotencyKey: "save"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	r = NewRepo(db)
	got, err := r.GetDiagram(t.Context(), p.ID, saved.Pin)
	if err != nil {
		t.Fatal(err)
	}
	x := got.Provenance.Elements[0]
	if x.Introduced.Pin != original.Pin || x.Introduced.Author != "A" || x.LastEdited.Pin != original.Pin || x.InheritedFrom == nil || x.InheritedFrom.Pin != original.Pin {
		t.Fatalf("lost introduction/inheritance: %+v", x)
	}
	if got.Provenance.Fork == nil || got.Provenance.Fork.Source != original.Pin || got.Provenance.Fork.Reason != "Reviewed fork" {
		t.Fatal("lost fork descriptor")
	}
	bad := doc
	bad.Payload.Elements[0].Refs = []DiagramRef{{Kind: "record", RecordType: "node", ID: "10000000-0000-4000-8000-000000000404"}}
	before := diagramTableCounts(t, db)
	if _, err = r.SaveDiagram(t.Context(), p.ID, fork.Pin.ID, DiagramSaveInput{Document: bad, ExpectedVersion: 2, IdempotencyKey: "forged"}); err == nil {
		t.Fatal("invented inherited ref accepted")
	}
	if diagramTableCounts(t, db) != before {
		t.Fatal("forged inherited ref wrote rows")
	}
}

func TestDiagramValidationBoundaries(t *testing.T) {
	base := diagramTestDocument("10000000-0000-4000-8000-000000000090")
	for name, modify := range map[string]func(*DiagramDocument){
		"null elements": func(d *DiagramDocument) { d.Payload.Elements = nil },
		"null links":    func(d *DiagramDocument) { d.Payload.Links = nil },
		"null refs":     func(d *DiagramDocument) { d.Payload.Elements[0].Refs = nil },
		"duplicate ID":  func(d *DiagramDocument) { d.Payload.Elements = append(d.Payload.Elements, d.Payload.Elements[0]) },
		"cycle":         func(d *DiagramDocument) { d.Payload.Elements[0].ParentID = d.Payload.Elements[0].ID },
		"unknown role":  func(d *DiagramDocument) { d.Payload.Elements[0].Role = "folder" },
		"label bound":   func(d *DiagramDocument) { d.Payload.Elements[0].Label = strings.Repeat("x", 257) },
		"element bound": func(d *DiagramDocument) { d.Payload.Elements = make([]ArchitectureElement, 1001) },
		"link bound":    func(d *DiagramDocument) { d.Payload.Links = make([]ArchitectureLink, 3001) },
		"reason bound":  func(d *DiagramDocument) { d.Payload.Elements[0].Origin.Reason = strings.Repeat("x", 4097) },
	} {
		t.Run(name, func(t *testing.T) {
			d, err := normalizeDiagram(base)
			if err != nil {
				t.Fatal(err)
			}
			modify(&d)
			if d.Validate() == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
	for _, raw := range []string{`{"id":"10000000-0000-4000-8000-000000000090","version":9223372036854775808,"contentHash":"` + strings.Repeat("a", 64) + `"}`, `{"id":"10000000-0000-4000-8000-000000000090","version":1,"version":2,"contentHash":"` + strings.Repeat("a", 64) + `"}`} {
		var pin DiagramPin
		if json.Unmarshal([]byte(raw), &pin) == nil {
			t.Fatal("unsafe/duplicate version accepted")
		}
	}
	r, db := testRepo(t)
	p := createProject(t, r, "foreign-proof")
	d := diagramTestDocument(p.CurrentRevisionID)
	d.Payload.Elements[0].Origin = DiagramOrigin{Kind: "source_assertion", Evidence: []DiagramEvidenceRef{{RevisionID: p.CurrentRevisionID, EvidenceID: "10000000-0000-4000-8000-000000000090", SubjectID: "10000000-0000-4000-8000-000000000091"}}}
	if _, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: d, IdempotencyKey: "proof"}); err == nil {
		t.Fatal("invented proof accepted")
	}
	if diagramTableCounts(t, db) != [5]int{} {
		t.Fatal("invalid evidence wrote data")
	}
}

func TestDiagramHistoricalEvidenceGapSurvivesUnrelatedSave(t *testing.T) {
	rid := "10000000-0000-4000-8000-000000000090"
	d := diagramTestDocument(rid)
	proof := DiagramEvidenceRef{RevisionID: rid, EvidenceID: "10000000-0000-4000-8000-000000000091", SubjectID: "10000000-0000-4000-8000-000000000092"}
	d.Payload.Elements[0].Origin = DiagramOrigin{Kind: "source_assertion", Evidence: []DiagramEvidenceRef{proof}}
	g := &EffectiveGraphSnapshot{Pins: EffectiveGraphPins{BaseRevisionID: rid}, State: RevisionState{Evidence: []Evidence{{ID: proof.EvidenceID, SubjectID: proof.SubjectID}}}}
	hash, _ := requestDigest(proof)
	gap := diagramGap(d.Payload.Elements[0].ID, "historical_evidence:"+hash, "Historical source assertion; current target is unverified")
	previous := &DiagramVersion{Document: d, Gaps: []DiagramGap{gap}}
	gaps, err := resolveDiagramEvidence(t.Context(), g, d, previous)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0] != gap {
		t.Fatalf("historical fork proof silently promoted to current: %+v", gaps)
	}
}

// Review 2026-10-06, F106: retired row IDs are now found by a JSON-path query
// over the stored versions instead of decoding, validating and re-hashing
// every version inside the writer. A save that reintroduces an ID an older
// version used must still be refused; a brand-new ID must still pass.
func TestDiagramRetiredRowIDsStayRetired(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "diagram-retired")
	doc := diagramTestDocument(p.CurrentRevisionID)
	v1, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	rename := func(id string) DiagramDocument {
		d := diagramTestDocument(p.CurrentRevisionID)
		d.Payload.PrimarySystemID, d.Payload.Elements[0].ID = id, id
		return d
	}
	const second, third = "10000000-0000-4000-8000-000000000003", "10000000-0000-4000-8000-000000000004"
	if _, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: rename(second), IdempotencyKey: "retire-first"}); err != nil {
		t.Fatal(err)
	}
	_, err = r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 2, Document: rename(doc.Payload.Elements[0].ID), IdempotencyKey: "reuse-first"})
	if err == nil || !strings.Contains(err.Error(), "Retired semantic IDs cannot be reused") {
		t.Fatalf("retired row ID reused: %v", err)
	}
	if _, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 2, Document: rename(third), IdempotencyKey: "fresh"}); err != nil {
		t.Fatalf("fresh row ID refused: %v", err)
	}
}
