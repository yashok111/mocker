package backendreplay

import (
	"context"
	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"testing"
)

const id = "01900000-0000-7000-8000-000000000001"

func TestDiagramReplayExactPackageMembership(t *testing.T) {
	pkg := Template()
	pkg.Profile = Pin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("profile"))}
	pkg.Target = backendmodel.BackendReadTarget{RevisionID: id}
	pkg.TargetHash = p.HashBytes([]byte("target"))
	b := DiagramBinding{Diagram: backendmodel.DiagramPin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("diagram"))}, ElementID: id, StepID: pkg.Steps[0].ID, AssertionIDs: []string{pkg.Assertions[0].ID}}
	pkg.DiagramBindings = []DiagramBinding{b}
	if err := pkg.Validate(); err != nil {
		t.Fatal(err)
	}
	h, _ := PackageHash(pkg)
	pkg.DiagramBindings[0].Diagram.Version = 2
	h2, _ := PackageHash(pkg)
	if h == h2 {
		t.Fatal("diagram pin excluded from hash")
	}
	pkg.DiagramBindings[0].StepID = id
	if err := pkg.Validate(); err == nil {
		t.Fatal("foreign step accepted")
	}
	pkg.DiagramBindings[0].StepID = b.StepID
	pkg.DiagramBindings[0].AssertionIDs = []string{id}
	if err := pkg.Validate(); err == nil {
		t.Fatal("foreign assertion accepted")
	}
}

type diagramReader struct {
	scope *backendmodel.DiagramScope
	calls int
}

func (r *diagramReader) ResolveDiagramScope(_ context.Context, _ string, in backendmodel.DiagramScopeInput) (*backendmodel.DiagramScope, error) {
	r.calls++
	return r.scope, nil
}
func TestDiagramReplayScopeReadOnly(t *testing.T) {
	pkg := Template()
	pkg.Profile = Pin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("profile"))}
	pkg.Target = backendmodel.BackendReadTarget{RevisionID: id}
	pkg.TargetHash = p.HashBytes([]byte("target"))
	pin := backendmodel.DiagramPin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("diagram"))}
	pkg.DiagramBindings = []DiagramBinding{{Diagram: pin, ElementID: id, StepID: pkg.Steps[0].ID, AssertionIDs: []string{}}}
	reader := &diagramReader{scope: &backendmodel.DiagramScope{Pin: pin, Target: pkg.Target, TargetHash: pkg.TargetHash}}
	if err := ValidateDiagramBindings(t.Context(), reader, id, pkg); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 {
		t.Fatal("no exact read")
	}
	reader.scope.TargetHash = p.HashBytes([]byte("other"))
	if err := ValidateDiagramBindings(t.Context(), reader, id, pkg); err == nil {
		t.Fatal("cross target accepted")
	}
	reader.scope.TargetHash = pkg.TargetHash
	reader.scope.Truncated = true
	if err := ValidateDiagramBindings(t.Context(), reader, id, pkg); err == nil {
		t.Fatal("truncated membership accepted")
	}
	// Reader has no transport or mutation capability; no connect/reset/start seam.
}
func TestDiagramReplayScopeHashFrozen(t *testing.T) {
	pkg := Template()
	pkg.Profile = Pin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("profile"))}
	pkg.Target = backendmodel.BackendReadTarget{RevisionID: id}
	pkg.TargetHash = p.HashBytes([]byte("target"))
	pin := backendmodel.DiagramPin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("diagram"))}
	pkg.DiagramScope = &backendmodel.DiagramScopeInput{Pin: pin, Selectors: []backendmodel.DiagramScopeSelector{{Kind: "semantic", ID: id}}}
	if err := pkg.Validate(); err == nil {
		t.Fatal("missing scope hash")
	}
	pkg.DiagramScopeHash = p.HashBytes([]byte("scope"))
	h, err := PackageHash(pkg)
	if err != nil {
		t.Fatal(err)
	}
	reader := &diagramReader{scope: &backendmodel.DiagramScope{Pin: pin, Target: pkg.Target, TargetHash: pkg.TargetHash, ScopeHash: pkg.DiagramScopeHash}}
	if err := ValidateDiagramBindings(t.Context(), reader, id, pkg); err != nil {
		t.Fatal(err)
	}
	pkg.DiagramScopeHash = p.HashBytes([]byte("other"))
	h2, _ := PackageHash(pkg)
	if h == h2 {
		t.Fatal("scope omitted from hash")
	}
	if err := ValidateDiagramBindings(t.Context(), reader, id, pkg); err == nil {
		t.Fatal("wrong scope hash accepted")
	}
}
