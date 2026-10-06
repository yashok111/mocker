package backendobservations

import (
	"encoding/json/v2"
	"fmt"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
	"strings"
	"testing"
)

func TestObservationSpanLinksRoundTrip(t *testing.T) {
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "links", IdempotencyKey: "links"})
	if e != nil {
		t.Fatal(e)
	}
	c := testContext()
	rec := testSpan()
	links := []SpanLink{}
	for i := 1; i <= 32; i++ {
		links = append(links, SpanLink{TraceID: strings.Repeat("3", 32), SpanID: fmt.Sprintf("%016x", i), Relation: "association"})
	}
	rec.Links = &links
	out, e := NewRepo(db).Import(t.Context(), project.ID, ImportInput{Mode: "create", Name: "links", Context: &c, BatchID: "a", IdempotencyKey: "a", Records: []Record{rec}})
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := store.Open(t.Context(), db.Path())
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	page, e := NewRepo(reopened).Records(t.Context(), project.ID, out.SetID, out.Version, 10, "")
	if e != nil || len(*page.Items[0].Links) != 32 {
		t.Fatal(page, e)
	}
	raw, _ := json.Marshal(rec)
	raw = []byte(strings.Replace(string(raw), `"relation":"association"`, `"relation":"association","unknown":"SECRET"`, 1))
	var invalidRecord Record
	if json.Unmarshal(raw, &invalidRecord) == nil {
		t.Fatal("unknown link accepted")
	}
	links = append(links, links[0])
	if ValidateRecord(c, rec) == nil {
		t.Fatal("33 links accepted")
	}
	links = links[:2]
	links[1] = links[0]
	if ValidateRecord(c, rec) == nil {
		t.Fatal("duplicate links accepted")
	}
}
func TestObservationQuotaRollbackAndHistoricalPaging(t *testing.T) {
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "quota", IdempotencyKey: "quota"})
	if e != nil {
		t.Fatal(e)
	}
	r := NewRepo(db)
	c := testContext()
	a := testSpan()
	b := testSpan()
	b.ID = "span-2"
	b.SpanID = strings.Repeat("3", 16)
	first, e := r.Import(t.Context(), project.ID, ImportInput{Mode: "create", Name: "quota", Context: &c, BatchID: "a", IdempotencyKey: "a", Records: []Record{a, b}})
	if e != nil {
		t.Fatal(e)
	}
	page, e := r.Records(t.Context(), project.ID, first.SetID, 1, 1, "")
	if e != nil || page.NextCursor == "" {
		t.Fatal(page, e)
	}
	// Force a quota at the database boundary; all membership/blob/version writes must rollback.
	_, e = db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_observation_version BEFORE INSERT ON backend_observation_versions WHEN NEW.version=2 BEGIN SELECT RAISE(ABORT,'quota oracle'); END`)
	if e != nil {
		t.Fatal(e)
	}
	b.ID = "span-3"
	_, e = r.Import(t.Context(), project.ID, ImportInput{Mode: "append", SetID: first.SetID, ExpectedVersion: 1, BatchID: "b", IdempotencyKey: "b", Records: []Record{b}})
	if e == nil {
		t.Fatal("accepted rejected transaction")
	}
	var n int
	if e = db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_observation_members`).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	next, e := r.Records(t.Context(), project.ID, first.SetID, 1, 1, page.NextCursor)
	if e != nil || len(next.Items) != 1 || next.Items[0].ID != "span-2" {
		t.Fatal(next, e)
	}
	if _, e = r.Records(t.Context(), project.ID, first.SetID, 1, 1, "forged"); e == nil {
		t.Fatal("forged cursor accepted")
	}
}
