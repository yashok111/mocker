package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func source6Input(t *testing.T, p *Project) BeginImportInput {
	t.Helper()
	in := eventsProfileInput(firstImportFixture(p), false)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "files" {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	in.Profile, in.Mode = "composed-source-v1", "composed"
	in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, in.Profile)
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(b, &object); err != nil {
		t.Fatal(err)
	}
	object["sourceScope"] = map[string]any{"kind": "add_repository"}
	object["scopeStatus"] = map[string]any{"status": "complete", "gaps": []string{}}
	object["syncPolicy"] = "whole-source-v1"
	b, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestSource6ProfileAdmission(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "source6")
	in := source6Input(t, p)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatalf("source6 empty-project admission: %v", err)
	}
	if s.Profile != "composed-source-v1" || s.Mode != "composed" {
		t.Fatalf("wrong session: %+v", s)
	}
	if !ValidID(s.RepositoryID) || !ValidID(s.SnapshotID) {
		t.Fatal("begin did not reserve source identities")
	}
	again, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil || again.ID != s.ID || again.RepositoryID != s.RepositoryID || again.SnapshotID != s.SnapshotID {
		t.Fatalf("begin replay changed identities: %+v %v", again, err)
	}
}

func TestSource6BatchHashUsesRefOnlyWire(t *testing.T) {
	raw := []byte(`[{"op":"upsert_edge","edge":{"externalKey":"call","kind":"calls","fromRef":{"localKey":"from"},"toRef":{"localKey":"to"},"attributes":{},"evidenceKeys":["proof"]}}]`)
	var commands []ImportCommand
	if err := json.Unmarshal(raw, &commands, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	actual, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	if actual != hashBytes(canonical) {
		t.Fatal("source6 command hashing invented forbidden empty legacy key fields")
	}
}
