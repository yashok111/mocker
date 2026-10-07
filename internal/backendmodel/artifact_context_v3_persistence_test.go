package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestV3PersistentForeignContext(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "v3")
	id, err := r.InstallationID(t.Context())
	if err != nil || !ValidID(id) {
		t.Fatal(id, err)
	}
	c := ArtifactContextV3{
		DocumentVersion:   ArtifactContextV3Version,
		SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64),
		Groups: []ArtifactNamespaceGroup{{
			Namespace: ArtifactNamespace{Scope: "foreign", InstallationID: id},
			Pins:      []ArtifactPin{{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("c", 64)}},
		}},
	}
	raw, err := EncodeArtifactContextV3(c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = testkit.ExecBackendOwner(t.Context(), db.W, `INSERT INTO backend_revision_api_artifacts VALUES(?,?,?,?)`,
		p.CurrentRevisionID, c.SourceContentHash, c.SourceSemanticHash, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadRevisionState(t.Context(), db.R, p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ArtifactContextV3 == nil || state.ArtifactContext != nil || state.APIArtifactContext != nil || len(state.Revision.ArtifactPins) != 0 {
		t.Fatal("namespace lost or flattened")
	}
	graph, err := r.ResolveEffectiveGraph(t.Context(), p.ID, BackendReadTarget{RevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(graph.Pins)
	if err != nil {
		t.Fatal(err)
	}
	var pins EffectiveGraphPins
	if err := json.Unmarshal(wire, &pins); err != nil {
		t.Fatal(err)
	}
	if pins.ArtifactContextV3 == nil || pins.ArtifactContext != nil {
		t.Fatal("effective pins lost v3")
	}
	request := NewEditorArtifactRequest(t.Context(), nil, nil)
	if _, err := request.ResolveNamespacedPin(id, NamespacedArtifactPin{Namespace: c.Groups[0].Namespace, Pin: c.Groups[0].Pins[0]}); err == nil {
		t.Fatal("foreign numeric collision resolved")
	} else {
		assertFault(t, err, "backend_artifact_foreign_unresolved")
	}
}

func TestInstallationIdentityStableAndDistinct(t *testing.T) {
	t.Parallel()
	a, _ := testRepo(t)
	db, err := store.Open(t.Context(), t.TempDir()+"/independent.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	b := NewRepo(db)
	first, err := a.InstallationID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.InstallationID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.InstallationID(t.Context())
	if err != nil || first != again || first == other {
		t.Fatal(first, again, other, err)
	}
}
