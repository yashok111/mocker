package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func TestSource6InventoryUsesFinalSelectedClaims(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "inventory")
	in := source6Input(t, p)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "datastores" {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	commands[0].Node.Kind = "datastore"
	commands[0].Node.Attributes = map[string]jsontext.Value{"technology": jsontext.Value(`"postgresql"`)}
	b := sendCommands(t, r, p, s, 1, "database", commands...)
	base := commitStaged(t, r, p, s, b.AcceptedVersion, "base")
	for _, valid := range []bool{true, false} {
		name := "valid"
		if !valid {
			name = "bad-count"
		}
		t.Run(name, func(t *testing.T) {
			next := source6Input(t, &base.Project)
			next.IdempotencyKey = name
			next.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace}
			if valid {
				for i := range next.Inventory {
					if next.Inventory[i].Category == "datastores" {
						next.Inventory[i].KnownCount = 1
						next.Inventory[i].Denominator = new(int64(1))
					}
				}
			}
			session, err := r.BeginImport(t.Context(), p.ID, next)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: base.Revision.ID})
			if err != nil {
				t.Fatal(err)
			}
			if valid && preview.State != "ready" {
				t.Fatalf("retained membership incorrectly ignored: %+v", preview)
			}
			if !valid && preview.State == "ready" {
				t.Fatal("complete inventory count ignored retained selected claim")
			}
		})
	}
}
