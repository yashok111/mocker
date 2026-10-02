package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
)

func TestArtifactRequiredOwnerLimitIs413AndNoWrites(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	s.scenarios = designscenario.NewRepo(s.repo.db, &config.Config{MaxBody: 1}, nil)
	_, err := s.Preview(t.Context(), base.Project.ID, scenarioSet(base, ids, d))
	fault, ok := errors.AsType[*FaultError](err)
	if !ok || fault.Status != 413 {
		t.Fatalf("required owner admission became ordinary broken artifact: %v", err)
	}
	current, _ := s.repo.Get(t.Context(), base.Project.ID)
	if current.Version != base.Project.Version || current.CurrentRevisionID != base.Revision.ID {
		t.Fatal("read limit wrote project")
	}
	var contexts int
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_api_artifacts`).Scan(&contexts)
	if contexts != 0 {
		t.Fatal("read limit persisted context")
	}
}

func TestArtifactOldObjectDeletionAndRetainedBrokenGroup(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	a, _ := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "original")
	owner := s.scenarios.(*designscenario.Repo)
	doc := d.Draft.Document
	doc.Messages = []designscenario.Message{}
	next, err := owner.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, FormDrafts: d.Draft.FormDrafts, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := scenarioSet(base, ids, next)
	in.BaseRevisionID = a.Revision.ID
	in.ExpectedVersion = a.Project.Version
	in.Commands[0].EditorBindings = in.Commands[0].EditorBindings[:1]
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil || !p.CanApply {
		t.Fatalf("deletion replacement: %+v %v", p, err)
	}
	if !slices.ContainsFunc(p.Diff, func(d ArtifactPinsDiff) bool {
		return d.Kind == "editor" && d.Status == "missing" && d.Before.Editor.Binding.Selector.Kind == "sequence_message" && len(d.Changes) > 0
	}) {
		t.Fatal("deleted old object was not resolved/shown", p.Diff)
	}
	b, _ := applyArtifactTest(t, s, base.Project.ID, in, "deleted")
	// An unrelated API group can be added even if the retained scenario owner disappears.
	s.scenarios = nil
	api := s.api
	pin, err := NewEditorArtifactRequest(t.Context(), api, nil).SnapshotPin(ArtifactKey{"api_design", "1"}, "1")
	if err != nil {
		t.Fatal(err)
	}
	unrelated := PreviewArtifactPinsInput{BaseRevisionID: b.Revision.ID, ExpectedVersion: b.Project.Version, Commands: []ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: artifactKey(pin), RevisionID: pin.RevisionID, APIBindings: []APIPinBindingInput{}, EditorBindings: []EditorBindingInput{}, Reason: "Projection only"}}}
	kept, _ := applyArtifactTest(t, s, base.Project.ID, unrelated, "unrelated")
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, kept.Revision.ID)
	if err != nil || len(state.ArtifactContext.EditorBindings) != 1 || len(state.Revision.ArtifactPins) != 2 {
		t.Fatal("retained broken group lost", err)
	}
}

func TestArtifactTransitionsProjectionOnlyAndV1(t *testing.T) {
	legacy, base, ids, api := apiPinFixture(t)
	s := NewArtifactService(legacy.repo, legacy.artifacts, nil)
	key := ArtifactKey{"api_design", strconv.FormatInt(api.Design.ID, 10)}
	c := ArtifactPinCommand{Type: "set_artifact_pin", Artifact: key, RevisionID: strconv.FormatInt(api.Draft.ID, 10), APIBindings: []APIPinBindingInput{}, EditorBindings: []EditorBindingInput{}, Reason: "Projection only"}
	a, _ := applyArtifactTest(t, s, base.Project.ID, PreviewArtifactPinsInput{base.Revision.ID, base.Project.Version, []ArtifactPinCommand{c}}, "projection")
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, a.Revision.ID)
	if err != nil || state.ArtifactContext.DocumentVersion != EditorArtifactDocumentVersion {
		t.Fatal("projection pin downgraded to invalid v1", err)
	}
	c.APIBindings = []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "orders-read"}}}
	b, _ := applyArtifactTest(t, s, base.Project.ID, PreviewArtifactPinsInput{a.Revision.ID, a.Project.Version, []ArtifactPinCommand{c}}, "v1")
	state, err = loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, b.Revision.ID)
	if err != nil || state.ArtifactContext.DocumentVersion != "" {
		t.Fatal("strict v1 did not restore", err)
	}
	c.APIBindings = []APIPinBindingInput{}
	aAgain, _ := applyArtifactTest(t, s, base.Project.ID, PreviewArtifactPinsInput{b.Revision.ID, b.Project.Version, []ArtifactPinCommand{c}}, "projection-again")
	if aAgain.Revision.SemanticHash != a.Revision.SemanticHash {
		t.Fatal("branch transition is path dependent")
	}
	remove := ArtifactPinCommand{Type: "remove_artifact_pin", Artifact: key, Reason: "Clear"}
	cleared, _ := applyArtifactTest(t, s, base.Project.ID, PreviewArtifactPinsInput{aAgain.Revision.ID, aAgain.Project.Version, []ArtifactPinCommand{remove}}, "clear")
	if cleared.Revision.SemanticHash != base.Revision.SemanticHash {
		t.Fatal("source anchor not restored")
	}
}

type artifactMutatingScenarioReader struct {
	ScenarioArtifactReader
	mutate func()
}

func (r artifactMutatingScenarioReader) ArtifactInspectionSnapshot(ctx context.Context, id, rid int64) (*designscenario.ArtifactInspectionSnapshot, error) {
	snapshot, err := r.ScenarioArtifactReader.ArtifactInspectionSnapshot(ctx, id, rid)
	if err == nil {
		r.mutate()
	}
	return snapshot, err
}
func TestArtifactFrozenBaseCASUnderWriterLock(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	in := scenarioSet(base, ids, d)
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	s.scenarios = artifactMutatingScenarioReader{s.scenarios, func() {
		if _, err := s.repo.db.W.ExecContext(t.Context(), `UPDATE backend_revisions SET document=char(10)||document WHERE id=?`, base.Revision.ID); err != nil {
			t.Fatal(err)
		}
	}}
	_, err = s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, "cas"})
	assertFault(t, err, "backend_artifact_pins_base_conflict")
	current, _ := s.repo.Get(t.Context(), base.Project.ID)
	if current.Version != base.Project.Version {
		t.Fatal("base CAS did not rollback")
	}
}

func TestArtifactFullContextLimitEscapeNoWrites(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	// Eleven authored objects and 100 safe source records; labels share one
	// escaped string. This is a reduced ~4.5MiB roster, not a huge allocation.
	doc := d.Draft.Document
	for i := 1; i < 11; i++ {
		doc.Participants = append(doc.Participants, designscenario.Participant{ID: "p" + strconv.Itoa(i), Name: "Service", Kind: "service"})
	}
	owner := s.scenarios.(*designscenario.Repo)
	d, err := owner.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, FormDrafts: d.Draft.FormDrafts, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.repo.Node(t.Context(), base.Project.ID, base.Revision.ID, ids["http"])
	if err != nil {
		t.Fatal(err)
	}
	label := strings.Repeat("\"", 4096)
	sourceIDs := make([]string, 0, 100)
	for i := range 100 {
		n := *node
		n.ID = "30000000-0000-4000-8000-" + fmtArtifactDigits(i)
		n.Name = label
		raw, e := jsonArtifactNode(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.repo.db.W.ExecContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,document) VALUES(?,?,'node',?,?,?,?)`, base.Project.ID, base.Revision.ID, n.ID, n.Kind, n.Name, raw); e != nil {
			t.Fatal(e)
		}
		sourceIDs = append(sourceIDs, n.ID)
	}
	in := scenarioSet(base, ids, d)
	in.Commands[0].EditorBindings = []EditorBindingInput{}
	for i := range 11 {
		id := "p"
		if i > 0 {
			id += strconv.Itoa(i)
		}
		in.Commands[0].EditorBindings = append(in.Commands[0].EditorBindings, EditorBindingInput{Selector: EditorSelector{Kind: "participant", ParticipantID: id}, SourceNodeIDs: sourceIDs})
	}
	_, err = s.Preview(t.Context(), base.Project.ID, in)
	fault, ok := errors.AsType[*FaultError](err)
	if !ok || fault.Status != 413 || fault.Code != "backend_artifact_context_limit" {
		t.Fatalf("escaped full context admission: %v", err)
	}
	_, err = s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, strings.Repeat("a", 64), "too-large"})
	if !errors.As(err, &fault) || fault.Status != 413 {
		t.Fatalf("apply oversized context: %v", err)
	}
	var contexts, receipts int
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_api_artifacts`).Scan(&contexts)
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_command_receipts WHERE scope LIKE 'artifact-pins:%'`).Scan(&receipts)
	if contexts != 0 || receipts != 0 {
		t.Fatal("413 wrote artifact state")
	}
}
func fmtArtifactDigits(i int) string {
	return strings.Repeat("0", 12-len(strconv.Itoa(i))) + strconv.Itoa(i)
}
func jsonArtifactNode(n Node) (string, error) { raw, err := json.Marshal(n); return string(raw), err }

func TestArtifactDistinctOldNewSnapshotsRequiredBudget(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	owner := s.scenarios.(*designscenario.Repo)
	owners := make([]*designscenario.Detail, 0, 11)
	owners = append(owners, d)
	for range 10 {
		created, err := owner.Create(t.Context(), designscenario.CreateInput{Document: d.Draft.Document, FormDrafts: d.Draft.FormDrafts, Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, created)
	}
	commands := make([]ArtifactPinCommand, 0, len(owners))
	for _, current := range owners {
		c := scenarioSet(base, ids, current).Commands[0]
		c.EditorBindings = c.EditorBindings[:1]
		commands = append(commands, c)
	}
	a, _ := applyArtifactTest(t, s, base.Project.ID, PreviewArtifactPinsInput{base.Revision.ID, base.Project.Version, commands}, "eleven")
	for i, current := range owners {
		doc := current.Draft.Document
		doc.Participants = slices.Clone(doc.Participants)
		doc.Participants[0].Name = "Changed service"
		changed, err := owner.Save(t.Context(), current.Scenario.ID, designscenario.SaveInput{ExpectedVersion: current.Scenario.Version, Document: doc, FormDrafts: current.Draft.FormDrafts, Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		commands[i].RevisionID = strconv.FormatInt(changed.Draft.ID, 10)
	}
	in := PreviewArtifactPinsInput{a.Revision.ID, a.Project.Version, commands}
	_, err := s.Preview(t.Context(), base.Project.ID, in)
	fault, ok := errors.AsType[*FaultError](err)
	if !ok || fault.Status != 413 || fault.Code != "backend_artifact_work_limit" {
		t.Fatalf("old/new attempts did not share E3 budget: %v", err)
	}
	current, _ := s.repo.Get(t.Context(), base.Project.ID)
	if current.CurrentRevisionID != a.Revision.ID || current.Version != a.Project.Version {
		t.Fatal("snapshot work limit wrote")
	}
}

func TestArtifactComparisonWideSelectorBoundedPagination(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	owner := s.scenarios.(*designscenario.Repo)
	doc := d.Draft.Document
	doc.Participants = slices.Clone(doc.Participants)
	doc.Messages = slices.Clone(doc.Messages)
	wide := strings.Repeat("p", 50000)
	doc.Participants[0].ID = wide
	doc.Messages[0].ToID = wide
	current, err := owner.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, FormDrafts: d.Draft.FormDrafts, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := scenarioSet(base, ids, current)
	in.Commands[0].EditorBindings[0].Selector.ParticipantID = wide
	a, _ := applyArtifactTest(t, s, base.Project.ID, in, "wide")
	query := CompareRevisionsInput{FromRevisionID: base.Revision.ID, ToRevisionID: a.Revision.ID, RecordType: "artifact", Limit: 1}
	cursor := ""
	seen := map[string]bool{}
	editors := 0
	groups := 0
	for pages := 0; pages < 4; pages++ {
		query.Cursor = cursor
		page, e := s.repo.CompareRevisions(t.Context(), base.Project.ID, query)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range page.Items {
			if seen[item.ID] || len(item.ID) > 100 {
				t.Fatal("duplicate/unbounded identity", item.ID)
			}
			seen[item.ID] = true
			if item.EditorArtifactAfter != nil {
				editors++
				if item.EditorArtifactAfter.Binding.Selector.Kind == "participant" && item.EditorArtifactAfter.Binding.Selector.ParticipantID != wide {
					t.Fatal("selector shortened")
				}
			}
			if item.ArtifactGroupAfter != nil {
				groups++
			}
		}
		cursor = page.NextCursor
		if len(cursor) > 512 {
			t.Fatal("selector leaked into comparison cursor", len(cursor))
		}
		if cursor == "" {
			break
		}
	}
	if len(seen) != 3 || editors != 2 || groups != 1 || cursor != "" {
		t.Fatalf("typed full-identity pagination: %d/%d/%d", len(seen), editors, groups)
	}
}

func TestArtifactCarryAndPersistenceAdmissionDoNotWrite(t *testing.T) {
	s, base, _, _ := artifactServiceFixture(t)
	c, pins := editorLimitFixture(t, 4<<20)
	for i := range c.EditorBindings {
		for j := range c.EditorBindings[i].SourceLabels {
			if len(c.EditorBindings[i].SourceLabels[j]) < 4096 {
				c.EditorBindings[i].SourceLabels[j] += "x"
				goto changed
			}
		}
	}
changed:
	err := s.repo.db.Write(t.Context(), func(tx *sql.Tx) error { return saveArtifactContext(t.Context(), tx, base.Revision.ID, c, pins) })
	fault, ok := errors.AsType[*FaultError](err)
	if !ok || fault.Status != 413 {
		t.Fatalf("persistence did not admit entire context: %v", err)
	}
	var rows int
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_api_artifacts`).Scan(&rows)
	if rows != 0 {
		t.Fatal("oversized context persisted")
	}
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.Revision.ArtifactPins = pins
	state.ArtifactContext = &c
	g := &graphCandidate{Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Sources: state.Sources, Coverage: state.Revision.Coverage}
	err = carryAPIArtifactContext(t.Context(), &ImportSession{ProjectID: base.Project.ID, Inventory: state.Inventory}, state, g)
	fault, ok = errors.AsType[*FaultError](err)
	if !ok || fault.Status != 413 || g.ArtifactContext != nil {
		t.Fatalf("carry admitted oversized full roster: %v", err)
	}
}

func TestArtifactCancelledRequiredReadRollsBack(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	in := scenarioSet(base, ids, d)
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.scenarios = artifactMutatingScenarioReader{s.scenarios, cancel}
	_, err = s.Apply(ctx, base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, "cancelled"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	var rows int
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_api_artifacts`).Scan(&rows)
	if rows != 0 {
		t.Fatal("cancelled request wrote context")
	}
}
