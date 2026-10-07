package backendportable

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
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

// review 2026-10-06, F15: the contract states idempotencyKey as a string of
// 1–200 characters and nothing else. The server used to count BYTES and to
// refuse leading or trailing whitespace, so a valid key such as " abc" or
// 150 two-byte characters got an unexplained 422.
func TestPortableIdempotencyKeyMatchesContract(t *testing.T) {
	db := testkit.NewDB(t)
	s := NewStaging(db)
	m, _ := bundleFixture(t)
	for i, key := range []string{" leading", "trailing ", strings.Repeat("я", 200)} {
		if _, err := s.Begin(t.Context(), BeginInput{Manifest: m, IdempotencyKey: key}); err != nil {
			t.Fatalf("contract-valid key %d refused: %v", i, err)
		}
		s2 := NewService(db, bm.NewRepo(db))
		if _, err := s2.Preview(t.Context(), "00000000-0000-4000-8000-000000000000", PreviewInput{ExpectedVersion: 1, IdempotencyKey: key}); err == nil || err.Error() == "Invalid idempotency key" {
			t.Fatalf("contract-valid key %d refused by the service: %v", i, err)
		}
		// Abort each session so the five-session staging quota stays free.
		if _, err := db.W.Exec(`UPDATE backend_portable_sessions SET state='aborted' WHERE state='staging'`); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"", strings.Repeat("a", 201)} {
		if _, err := s.Begin(t.Context(), BeginInput{Manifest: m, IdempotencyKey: key}); err == nil {
			t.Fatalf("out-of-contract key of %d characters accepted", len(key))
		}
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
	// A same-parent duplicate in a LATER chunk is no longer refused at Put
	// (review 2026-10-06, F2/F5/F33/F68: that check re-decoded every earlier
	// chunk under the writer). Preview refuses it over the whole bundle,
	// before any domain work, and leaves the session where it was.
	session, err = s.Put(t.Context(), session.ID, PutInput{3, 2, string(third), "third"})
	if err != nil {
		t.Fatal("put must not re-scan earlier chunks", err)
	}
	service := NewService(db, bm.NewRepo(db))
	_, err = service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, ArtifactMappings: []bm.PortableArtifactMapping{}, IdempotencyKey: "preview"})
	var f *bm.FaultError
	if !errors.As(err, &f) || f.Status != 422 || f.Message != "Duplicate exact record" {
		t.Fatal("same-parent duplicate across chunks must fail Preview", err)
	}
	var state string
	var version int64
	if err = db.R.QueryRow("SELECT state,version FROM backend_portable_sessions WHERE id=?", session.ID).Scan(&state, &version); err != nil || state != "staging" || version != session.Version {
		t.Fatal("failed preview advanced the session", state, version, err)
	}
}

// The exporter's half of the cross-chunk uniqueness guarantee: Export runs
// uniqueRecords over the whole record list before splitting, so two copies
// that would land in different chunks are still refused.
func TestUniqueRecordsRejectsDuplicateAcrossChunks(t *testing.T) {
	a := sizedRecord(t, 1, 900_000)
	b := sizedRecord(t, 2, 900_000)
	_, descriptors, err := splitRecords(t.Context(), []Record{a, b, a})
	if err != nil || len(descriptors) != 3 {
		t.Fatal("fixture must spread the copies over chunks", len(descriptors), err)
	}
	if err := uniqueRecords(t.Context(), []Record{a, b, a}); err == nil {
		t.Fatal("duplicate across chunk boundary exported")
	}
}

func TestSplitRecordsStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := splitRecords(ctx, []Record{sizedRecord(t, 1, 10)}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled export kept packing", err)
	}
}

// review 2026-10-06, F32: splitRecords keeps a running canonical size instead
// of re-canonicalising the pending chunk per record. This pins the identity
// that makes it exact: each chunk equals what EncodeChunk produced from the
// records, never exceeds the bound, and is maximal (the next record would not
// have fit), on sizes that straddle the 1 MiB boundary several times.
func TestSplitRecordsRunningSizeMatchesCanonical(t *testing.T) {
	sizes := []int{300_000, 400_000, 340_000, 9, 1_048_000 - 200, 1, 700_000, 347_000, 2, 3}
	records := make([]Record, 0, len(sizes)+1200)
	for i, n := range sizes {
		records = append(records, sizedRecord(t, i+1, n))
	}
	// Many tiny records so the record-count bound is crossed too.
	for i := range 1200 {
		records = append(records, sizedRecord(t, 100+i, i%7))
	}
	chunks, descriptors, err := splitRecords(t.Context(), records)
	if err != nil {
		t.Fatal(err)
	}
	at := 0
	for i, body := range chunks {
		n := descriptors[i].Records
		want, _, err := EncodeChunk(i, records[at:at+n])
		if err != nil || string(want) != string(body) {
			t.Fatal("chunk differs from canonical encoding", i, err)
		}
		if len(body) > MaxChunkBytes || n > MaxChunkRecords {
			t.Fatal("chunk over bound", i, len(body), n)
		}
		if next := at + n; next < len(records) {
			if n < MaxChunkRecords {
				grown, err := canonical(records[at : next+1])
				if err != nil || len(grown) <= MaxChunkBytes {
					t.Fatal("chunk not maximal", i, len(grown), err)
				}
			}
		}
		at += n
	}
	if at != len(records) {
		t.Fatal("records lost", at, len(records))
	}
}

func sizedRecord(t *testing.T, n, pad int) Record {
	t.Helper()
	doc, err := canonical(map[string]string{"pad": strings.Repeat("x", pad), "u": "é <"})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("20000000-0000-4000-8000-%012d", n)
	r := Record{Kind: "diagram_version", Identity: Identity{pid, pid, "diagram_version", id, "1"}, Document: jsontext.Value(doc)}
	r.ContentHash, _ = DocumentHash(r.Document)
	return r
}
