package backendportable

import (
	"database/sql"
	"encoding/json/v2"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

// Recompute record/chunk hashes after each edit: envelope integrity must not
// substitute for owner validation or exact historical closure.
func TestPortableRejectsForgedSemanticClosureAtomically(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	export, err := f.service.Export(t.Context(), ExportInput{Selection: f.selection, IdempotencyKey: "negative-export"})
	check(t, err)
	var original *bm.PortableModel
	check(t, f.service.db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		original, err = f.service.exportModelTx(t.Context(), tx, f.selection)
		return err
	}))
	cases := []struct {
		name   string
		mutate func(*bm.PortableModel)
	}{
		{"unsafe_path", func(m *bm.PortableModel) {
			for i := range m.Sources {
				if len(m.Sources[i].Coverage.Snapshots) > 0 {
					m.Sources[i].Coverage.Snapshots[0].SnapshotManifest.Files[0].Path = "../escape.go"
					return
				}
			}
		}},
		{"undeclared_schema", func(m *bm.PortableModel) {}},
		{"missing_history", func(m *bm.PortableModel) { m.Diagrams = m.Diagrams[1:] }},
		{"forged_provenance_hash", func(m *bm.PortableModel) { m.Diagrams[len(m.Diagrams)-1].ProvenanceHash = hash }},
		{"forged_author", func(m *bm.PortableModel) {
			v := &m.Diagrams[len(m.Diagrams)-1]
			v.Provenance.Elements[0].Introduced.Author = "Imposter"
			v.ProvenanceHash, _ = DocumentHash(v.Provenance)
		}},
		{"forged_time", func(m *bm.PortableModel) {
			v := &m.Diagrams[len(m.Diagrams)-1]
			v.Provenance.Elements[0].Introduced.At = "2000-01-01T00:00:00Z"
			v.ProvenanceHash, _ = DocumentHash(v.Provenance)
		}},
		{"missing_source_node", func(m *bm.PortableModel) {
			for i := range m.Sources {
				if len(m.Sources[i].Nodes) > 0 {
					m.Sources[i].Nodes = m.Sources[i].Nodes[1:]
					return
				}
			}
		}},
		{"duplicate_source_node", func(m *bm.PortableModel) {
			for i := range m.Sources {
				if len(m.Sources[i].Nodes) > 0 {
					m.Sources[i].Nodes = append(m.Sources[i].Nodes, m.Sources[i].Nodes[0])
					return
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(original)
			check(t, err)
			var model bm.PortableModel
			check(t, json.Unmarshal(raw, &model))
			tc.mutate(&model)
			records, err := modelRecords(model, export.Manifest.OriginInstallationID)
			check(t, err)
			chunks, descriptors, err := splitRecords(records)
			check(t, err)
			manifest := export.Manifest
			manifest.Chunks = descriptors
			if tc.name == "undeclared_schema" {
				manifest.Schemas = []string{"5", bm.ArtifactContextV3Version}
			}
			session, err := f.service.Begin(t.Context(), BeginInput{Manifest: manifest, IdempotencyKey: tc.name})
			check(t, err)
			rejected := false
			for i, chunk := range chunks {
				next, err := f.service.Put(t.Context(), session.ID, PutInput{ExpectedVersion: session.Version, Index: i, Body: string(chunk), IdempotencyKey: tc.name})
				if err != nil {
					rejected = true
					break
				}
				session = next
			}
			if !rejected {
				_, err = f.service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, ArtifactMappings: []bm.PortableArtifactMapping{}, IdempotencyKey: tc.name})
				rejected = err != nil
			}
			if !rejected {
				t.Fatal("forged semantic bundle accepted")
			}
			check(t, f.service.db.Write(t.Context(), func(tx *sql.Tx) error {
				for _, query := range []string{"SELECT count(*) FROM backend_projects", "SELECT count(*) FROM backend_portable_id_maps_documents", "SELECT count(*) FROM backend_portable_origins_documents", "SELECT count(*) FROM backend_portable_receipts WHERE operation='commit'"} {
					var count int
					if err := tx.QueryRowContext(t.Context(), query).Scan(&count); err != nil {
						return err
					}
					expected := 0
					if query == "SELECT count(*) FROM backend_projects" {
						expected = 1
					}
					if count != expected {
						t.Errorf("leaked rows: %s: %d", query, count)
					}
				}
				return nil
			}))
			_, err = f.service.Abort(t.Context(), session.ID, SessionInput{ExpectedVersion: session.Version, IdempotencyKey: "abort"})
			check(t, err)
		})
	}
}
