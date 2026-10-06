package backendportable

import (
	"encoding/json/jsontext"
	"github.com/yashok111/mocker/internal/testkit"
	"testing"
)

func bundleFixture(t *testing.T) (Manifest, []byte) {
	t.Helper()
	r := Record{Kind: "diagram_version", Identity: Identity{pid, pid, "diagram_version", did, "3"}, Document: jsontext.Value(`{"test":true}`)}
	r.ContentHash, _ = DocumentHash(r.Document)
	body, c, err := EncodeChunk(0, []Record{r})
	if err != nil {
		t.Fatal(err)
	}
	return Manifest{Format: Format, OriginInstallationID: pid, Schemas: []string{"5", "artifact-context-v3"}, Selection: Selection{ProjectID: pid, Target: svgFixture("architecture").diagram.Document.Target, TargetHash: hash, DiagramViews: []SVGInput{}}, Chunks: []ChunkDescriptor{c}}, body
}
func TestPortableStagingDurableReplayCASAbort(t *testing.T) {
	db := testkit.NewDB(t)
	s := NewStaging(db)
	m, body := bundleFixture(t)
	in := BeginInput{Manifest: m, IdempotencyKey: "begin"}
	a, err := s.Begin(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	p := PutInput{ExpectedVersion: 1, Index: 0, Body: string(body), IdempotencyKey: "put"}
	b, err := s.Put(t.Context(), a.ID, p)
	if err != nil || b.Version != 2 {
		t.Fatal(b, err)
	}
	s = NewStaging(db)
	replay, err := s.Put(t.Context(), a.ID, p)
	if err != nil || *replay != *b {
		t.Fatal("durable replay", err)
	}
	p.Body += " "
	if _, err = s.Put(t.Context(), a.ID, p); err == nil {
		t.Fatal("changed key input accepted")
	}
	p.Body = string(body)
	p.IdempotencyKey = "stale"
	if _, err = s.Put(t.Context(), a.ID, p); err == nil {
		t.Fatal("stale CAS accepted")
	}
	aborted, err := s.Abort(t.Context(), a.ID, SessionInput{ExpectedVersion: 2, IdempotencyKey: "abort"})
	if err != nil || aborted.State != "aborted" {
		t.Fatal(aborted, err)
	}
	again, err := s.Begin(t.Context(), in)
	if err != nil || *again != *a {
		t.Fatal("begin receipt replay after abort", err)
	}
	var n int
	if err = db.R.QueryRow("SELECT count(*) FROM backend_portable_chunks").Scan(&n); err != nil || n != 0 {
		t.Fatal("abort cleanup", n, err)
	}
}
func TestPortableFrozenExport(t *testing.T) {
	db := testkit.NewDB(t)
	s := NewStaging(db)
	m, body := bundleFixture(t)
	session, err := s.FreezeExport(t.Context(), m, [][]byte{body}, "export")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ExportChunk(t.Context(), session.ID, session.ManifestHash, 0)
	if err != nil || string(got) != string(body) {
		t.Fatal("frozen chunk", err)
	}
	if _, err = s.ExportChunk(t.Context(), session.ID, hash, 0); err == nil {
		t.Fatal("wrong manifest accepted")
	}
}

func TestPortableMemberScopesAcrossChunks(t *testing.T) {
	db := testkit.NewDB(t)
	s := NewStaging(db)
	m, _ := bundleFixture(t)
	member := func(parentID string) Record {
		raw, _ := canonical(struct {
			Parent Identity `json:"parent"`
		}{Identity{pid, pid, "diagram", parentID, "0"}})
		h, _ := DocumentHash(jsontext.Value(raw))
		return Record{Kind: "diagram_element", Identity: Identity{pid, pid, "diagram_element", nid, "0"}, Document: raw, ContentHash: h}
	}
	first, a, err := EncodeChunk(0, []Record{member(did)})
	if err != nil {
		t.Fatal(err)
	}
	second, b, err := EncodeChunk(1, []Record{member(vid)})
	if err != nil {
		t.Fatal(err)
	}
	third, c, err := EncodeChunk(2, []Record{member(did)})
	if err != nil {
		t.Fatal(err)
	}
	m.Chunks = []ChunkDescriptor{a, b, c}
	session, err := s.Begin(t.Context(), BeginInput{m, "members"})
	if err != nil {
		t.Fatal(err)
	}
	session, err = s.Put(t.Context(), session.ID, PutInput{1, 0, string(first), "first"})
	if err != nil {
		t.Fatal(err)
	}
	session, err = s.Put(t.Context(), session.ID, PutInput{2, 1, string(second), "second"})
	if err != nil {
		t.Fatal("different parent must succeed", err)
	}
	if _, err = s.Put(t.Context(), session.ID, PutInput{3, 2, string(third), "third"}); err == nil {
		t.Fatal("same-parent duplicate across chunks accepted")
	}
	var count int
	if err = db.R.QueryRow("SELECT count(*) FROM backend_portable_chunks WHERE session_id=?", session.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("failed put leaked chunk", count, err)
	}
}
