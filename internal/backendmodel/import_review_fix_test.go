package backendmodel

import (
	"testing"
)

// Review 2026-10-06, F88a: a foundation initial import declares exactly the
// foundation profile; an extra profile would be committed into an
// immutable revision and block the documented foundation-to-relational
// extension.
func TestImportFoundationInitialRequiresExactProfile(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "foundation-profile")
	in := firstImportFixture(p)
	in.Manifest.Provider.Profiles = []string{GraphProfile, RelationalProfile}
	_, err := r.BeginImport(t.Context(), p.ID, in)
	assertFault(t, err, "backend_incompatible_provider")
}

// Review 2026-10-06, F87: removing a key that was never staged or bound
// changes staging only — it allocates no identity that commit would
// publish as a reserved binding.
func TestImportRemoveUnknownKeyAllocatesNothing(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "remove-unknown")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	b := sendCommands(t, r, p, s, s.Version, "remove", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "typo-key"}})
	if len(b.Identities) != 0 {
		t.Fatalf("remove reported identities %+v", b.Identities)
	}
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "records", fixtureCommands(s)...)
	commitStaged(t, r, p, s, b.AcceptedVersion, "commit")
	var count int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_identity_bindings WHERE external_key='typo-key'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("typo key bindings=%d err=%v", count, err)
	}
}

// Review 2026-10-06, F87 (composed path): the same holds for a source6
// session, whose reservation goes through reserveComposedIdentity.
func TestSource6RemoveUnknownKeyAllocatesNothing(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "remove-unknown-source6")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	b := sendCommands(t, r, p, s, s.Version, "remove", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "typo-key"}})
	if len(b.Identities) != 0 {
		t.Fatalf("remove reported identities %+v", b.Identities)
	}
	var count int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_import_identities WHERE session_id=? AND external_key='typo-key'`, s.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("typo key reservations=%d err=%v", count, err)
	}
}

// Review 2026-10-06, F89: acceptedBatches come in acceptance order, page
// after page, not in the lexical order of caller-chosen batch ids.
func TestImportAcceptedBatchesInAcceptanceOrder(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "batch-order")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	b := sendCommands(t, r, p, s, s.Version, "b2", commands[0])
	sendCommands(t, r, p, s, b.AcceptedVersion, "a1", commands[1])
	got := []string{}
	cursor := ""
	for range 3 {
		status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, batch := range status.AcceptedBatches {
			got = append(got, batch.BatchID)
		}
		if cursor = status.NextCursor; cursor == "" {
			break
		}
	}
	if len(got) != 2 || got[0] != "b2" || got[1] != "a1" {
		t.Fatalf("accepted batches %v, want [b2 a1]", got)
	}
}

// Review 2026-10-06, F88b: a colon is legal in a POSIX path; only a drive
// name (`C:`) is refused.
func TestImportManifestPathColon(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{"docs/a:b.md": true, "testdata/x:y": true, "C:/x.go": false, "c:x.go": false, "a://b": false} {
		if validPath(path) != want {
			t.Errorf("validPath(%q) = %v, want %v", path, !want, want)
		}
	}
	r, _ := testRepo(t)
	p := createProject(t, r, "colon-path")
	in := firstImportFixture(p)
	file := in.Manifest.Snapshot.Files[0]
	file.Path = "docs/a:b.md"
	in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, file)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "files" {
			in.Inventory[i].KnownCount = int64(len(in.Manifest.Snapshot.Files))
			in.Inventory[i].Denominator = new(int64(len(in.Manifest.Snapshot.Files)))
		}
	}
	if _, err := r.BeginImport(t.Context(), p.ID, in); err != nil {
		t.Fatalf("legal POSIX path refused: %v", err)
	}
}
