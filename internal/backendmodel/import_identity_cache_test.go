package backendmodel

import (
	"context"
	"errors"
	"testing"
)

func TestImportBaseIdentityIndexKeepsExactNativeIdentities(t *testing.T) {
	r, base, _ := effectiveFiveRelationalFixture(t)
	state, err := loadRevisionState(t.Context(), r.db.R, base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, _ := diagramCountReads(t, r)
	for attempt := range 2 {
		reader.reads.Store(0)
		index, err := r.loadImportBaseIdentityIndex(t.Context(), r.db.R, base.Project.ID, base.Revision.ID)
		if err != nil {
			t.Fatal(err)
		}
		identities := &batchIdentities{index: index}
		for _, n := range state.Nodes {
			id, kind := identities.identity("node", n.ExternalKey)
			if id != n.ID || kind != n.Kind {
				t.Fatalf("node identity changed: %s %s", id, kind)
			}
		}
		for _, e := range state.Edges {
			id, kind := identities.identity("edge", e.ExternalKey)
			if id != e.ID || kind != e.Kind {
				t.Fatalf("edge identity changed: %s %s", id, kind)
			}
		}
		for _, e := range state.Evidence {
			id, kind := identities.identity("evidence", e.ExternalKey)
			if id != e.ID || kind != "" {
				t.Fatalf("evidence identity changed: %s %s", id, kind)
			}
		}
		if attempt == 1 && reader.reads.Load() != 1 {
			t.Fatalf("warm index reread base records: %d queries", reader.reads.Load())
		}
	}
	foreign := createProject(t, r, "other-project")
	if _, err := r.loadImportBaseIdentityIndex(t.Context(), r.db.R, foreign.ID, base.Revision.ID); err == nil {
		t.Fatal("cache leaked a foreign revision")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.loadImportBaseIdentityIndex(ctx, r.db.R, base.Project.ID, base.Revision.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
