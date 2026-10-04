package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func TestSource6WireRejectsLegacyMemberPresence(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"empty_from_key", `{"op":"upsert_edge","edge":{"externalKey":"edge","kind":"calls","fromRef":{"localKey":"a"},"toRef":{"localKey":"b"},"fromKey":"","attributes":{},"evidenceKeys":[]}}`},
		{"null_from_key", `{"op":"upsert_edge","edge":{"externalKey":"edge","kind":"calls","fromRef":{"localKey":"a"},"toRef":{"localKey":"b"},"fromKey":null,"attributes":{},"evidenceKeys":[]}}`},
		{"null_parent_key", `{"op":"upsert_node","node":{"externalKey":"node","kind":"handler","name":"node","parentKey":null,"attributes":{},"evidenceKeys":[]}}`},
		{"null_parent_ref", `{"op":"upsert_node","node":{"externalKey":"node","kind":"handler","name":"node","parentRef":null,"attributes":{},"evidenceKeys":[]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "wire")
			s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
			if err != nil {
				t.Fatal(err)
			}
			var c ImportCommand
			if err := json.Unmarshal([]byte(tc.raw), &c, json.RejectUnknownMembers(true)); err != nil {
				return
			}
			commands := []ImportCommand{c}
			hash, err := ImportBatchHash(commands)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "wire", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: hash, Commands: commands}); err == nil {
				t.Fatal("source6 accepted a supplied legacy or null reference member")
			}
		})
	}
}

func TestSource6WirePreservesLegacyNullParentAdmission(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "legacy-wire")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	var c ImportCommand
	if err := json.Unmarshal([]byte(`{"op":"upsert_node","node":{"externalKey":"node","kind":"handler","name":"node","parentKey":null,"attributes":{},"evidenceKeys":[]}}`), &c); err != nil {
		t.Fatal(err)
	}
	sendCommands(t, r, p, s, 1, "legacy", c)
}
