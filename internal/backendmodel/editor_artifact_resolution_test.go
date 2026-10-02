package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/testkit"
)

type editorTestAPI struct {
	calls     []string
	snapshots map[string]*apidesign.ArtifactSnapshot
	heads     map[int64]int64
}

func (f *editorTestAPI) ArtifactSnapshot(_ context.Context, id, rev int64) (*apidesign.ArtifactSnapshot, error) {
	k := fmt.Sprintf("%d/%d", id, rev)
	f.calls = append(f.calls, k)
	if s := f.snapshots[k]; s != nil {
		return s, nil
	}
	return nil, sql.ErrNoRows
}
func (f *editorTestAPI) ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error) {
	panic("unexpected transaction read")
}
func (f *editorTestAPI) ArtifactHead(_ context.Context, id int64) (int64, error) {
	return f.heads[id], nil
}

type editorTestScenario struct {
	calls    []string
	snapshot *designscenario.ArtifactSnapshot
}

func (f *editorTestScenario) ArtifactSnapshot(_ context.Context, id, rev int64) (*designscenario.ArtifactSnapshot, error) {
	f.calls = append(f.calls, fmt.Sprintf("%d/%d", id, rev))
	if f.snapshot != nil && f.snapshot.ScenarioID == id && f.snapshot.RevisionID == rev {
		return f.snapshot, nil
	}
	return nil, sql.ErrNoRows
}
func (f *editorTestScenario) ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error) {
	panic("unexpected transaction read")
}
func TestEditorArtifactRequestDistinctBudget(t *testing.T) {
	t.Parallel()
	api := &editorTestAPI{}
	r := NewEditorArtifactRequest(t.Context(), api, nil)
	for i := 1; i <= 20; i++ {
		_, _ = r.SnapshotPin(ArtifactKey{"api_design", fmt.Sprint(i)}, "1")
	}
	if len(api.calls) != 20 {
		t.Fatal(api.calls)
	}
	_, _ = r.SnapshotPin(ArtifactKey{"api_design", "1"}, "1")
	if len(api.calls) != 20 {
		t.Fatal("failed snapshot retried")
	}
	_, err := r.SnapshotPin(ArtifactKey{"api_design", "21"}, "1")
	var fault *FaultError
	if !errorsAsEditorFault(err, &fault) || fault.Status != 413 || len(api.calls) != 20 {
		t.Fatalf("budget: %v calls=%v", err, api.calls)
	}
}
func errorsAsEditorFault(err error, target **FaultError) bool {
	f, ok := errors.AsType[*FaultError](err)
	*target = f
	return ok
}
func TestEditorEventMapReducedAmplificationWitness(t *testing.T) {
	t.Parallel()
	id := strings.Repeat("c", 2000)
	doc := designscenario.Document{Contracts: []designscenario.Contract{{ID: id, Document: []byte(`{"openapi":"3.1.0","paths":{"/x":{"get":{"x-mocker-canvas-operation-id":"op","responses":{}}}}}`)}}, EventModel: &designscenario.EventModel{Contracts: []designscenario.EventContract{{ID: "ec", Operations: []designscenario.EventOperation{{ID: "eo"}}}}}}
	op := &doc.EventModel.Contracts[0].Operations[0]
	for i := 0; i < 120; i++ {
		op.APILinks = append(op.APILinks, designscenario.EventAPILink{ContractID: id, OperationKey: "op"})
	}
	raw, _ := json.Marshal(doc)
	out, err := designscenario.AnalyzeEventMap(t.Context(), doc)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(out)
	// Each accepted repeated link appends before final sort/compact: an API node
	// with ID+HTTPContractID and an edge with target+HTTPContractID copy the long ID.
	lower := 120 * 4 * len(id)
	t.Logf("SAFE witness: input=%d final=%d intermediate repeated-ID lower bound=%d, links=120 id=2000", len(raw), len(encoded), lower)
	if len(out.Nodes) != 2 || lower <= len(raw)*3 {
		t.Fatalf("unexpected witness nodes=%d lower=%d input=%d", len(out.Nodes), lower, len(raw))
	}
}

func TestEditorEventMapAdmittedShortLinkAmplification(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	api := apidesign.NewRepo(db, cfg)
	repo := designscenario.NewRepo(db, cfg, api)
	path := "/" + strings.Repeat("p", 1600)
	embedded := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"Witness","version":"1"},"paths":{%q:{"get":{"x-mocker-canvas-operation-id":"op","responses":{"200":{"description":"OK"}}}}}}`, path)
	doc := designscenario.Document{FormatVersion: 3, Title: "SAFE admitted witness", Participants: []designscenario.Participant{{ID: "p", Kind: "service"}}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{{ID: "c", Document: []byte(embedded), Mode: "copy"}}, EventModel: &designscenario.EventModel{Servers: []designscenario.EventServer{}, Channels: []designscenario.EventChannel{{ID: "ch", Address: "topic", ServerIDs: []string{}, MessageIDs: []string{"msg"}}}, Messages: []designscenario.EventMessage{{ID: "msg", Examples: []designscenario.EventExample{}}}, Schemas: []designscenario.EventSchema{}, Contracts: []designscenario.EventContract{{ID: "ec", ParticipantID: "p", Operations: []designscenario.EventOperation{}}}}}
	doc.EventModel.Channels = []designscenario.EventChannel{}
	for i := 0; i < 120; i++ {
		doc.EventModel.Channels = append(doc.EventModel.Channels, designscenario.EventChannel{ID: fmt.Sprintf("ch%d", i), Address: fmt.Sprintf("topic%d", i), ServerIDs: []string{}, MessageIDs: []string{"msg"}})
		doc.EventModel.Contracts[0].Operations = append(doc.EventModel.Contracts[0].Operations, designscenario.EventOperation{ID: fmt.Sprintf("o%d", i), Action: "send", ChannelID: fmt.Sprintf("ch%d", i), MessageID: "msg", APILinks: []designscenario.EventAPILink{{ContractID: "c", OperationKey: "op"}}})
	}
	detail, err := repo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		if invalid, ok := errors.AsType[*designscenario.InvalidError](err); ok {
			t.Fatalf("owner rejected SAFE witness: %+v", invalid.Diagnostics)
		}
		t.Fatal(err)
	}
	snapshot, err := repo.ArtifactSnapshot(t.Context(), detail.Scenario.ID, detail.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := designscenario.AnalyzeEventMap(t.Context(), snapshot.Document)
	if err != nil {
		t.Fatal(err)
	}
	final, _ := json.Marshal(out)
	// Each apiLink call materializes a new label and full authored operation
	// pointer before b.node appends duplicate ID. Edge locators stay per-ref.
	lower := 120 * (len(path) + len("GET ") + len("/contracts/0/document/paths/") + len(path) + len("/get"))
	t.Logf("SAFE OWNER-ADMITTED short-link witness input=%d final=%d repeated label+pointer lower=%d nodes=%d edges=%d", len(snapshot.DocumentJSON), len(final), lower, len(out.Nodes), len(out.Edges))
	if lower < len(snapshot.DocumentJSON)*4 {
		t.Fatalf("witness unexpectedly weak: input=%d lower=%d", len(snapshot.DocumentJSON), lower)
	}
}

func (f *editorTestScenario) ArtifactInspectionSnapshot(ctx context.Context, id, rev int64) (*designscenario.ArtifactInspectionSnapshot, error) {
	s, err := f.ArtifactSnapshot(ctx, id, rev)
	if err != nil {
		return nil, err
	}
	out := &designscenario.ArtifactInspectionSnapshot{ScenarioID: s.ScenarioID, RevisionID: s.RevisionID, Version: s.Version, StoredContentHash: s.ContentHash, DocumentHash: s.DocumentHash, DocumentJSON: s.DocumentJSON, FormDraftsJSON: s.FormDraftsJSON, TypedStatus: "supported", EnvelopeVerification: "verified", ContentHash: s.ContentHash}
	if err := decodeEditorRaw([]byte(s.DocumentJSON), &out.Document); err != nil {
		out.TypedStatus = "unsupported"
		out.EnvelopeVerification = "unavailable"
		out.ContentHash = ""
	}
	return out, nil
}
