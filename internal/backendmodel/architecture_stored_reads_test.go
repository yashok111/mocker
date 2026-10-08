package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/testkit"
)

func storedArchitectureFixture(t testing.TB, nodes, edges, evidence int) (*Repo, string, DiagramQueryInput) {
	t.Helper()
	r := NewRepo(testkit.NewDB(t))
	p, err := r.Create(t.Context(), CreateInput{Name: "Synthetic read fixture", IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	g := &graphCandidate{Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}, Coverage: Coverage{Status: "partial", Gaps: []string{"Synthetic inert performance fixture"}}}
	for i := range nodes {
		g.Nodes = append(g.Nodes, Node{ID: fmt.Sprintf("20000000-0000-4000-8000-%012d", i), ExternalKey: fmt.Sprint("node/", i), Kind: "symbol", Name: fmt.Sprint("Node ", i), EvidenceIDs: []string{}})
	}
	for i := range edges {
		from, to := g.Nodes[0].ID, g.Nodes[1].ID
		if i%3 == 1 {
			to = from
		}
		if i%3 == 2 {
			from = g.Nodes[2].ID
		}
		kind := "calls"
		if edges > 10000 && i >= 6189 {
			kind = "contains"
		}
		g.Edges = append(g.Edges, Edge{ID: fmt.Sprintf("30000000-0000-4000-8000-%012d", i), ExternalKey: fmt.Sprint("edge/", i), Kind: kind, From: from, To: to, EvidenceIDs: []string{}})
	}
	for i := range evidence {
		id := fmt.Sprintf("40000000-0000-4000-8000-%012d", i)
		e := &g.Edges[i%edges]
		e.EvidenceIDs = append(e.EvidenceIDs, id)
		g.Evidence = append(g.Evidence, Evidence{ID: id, ExternalKey: fmt.Sprint("proof/", i), SubjectID: e.ID, Method: "manual", Status: "explicit", Explanation: "Synthetic inert proof", Snippet: new(strings.Repeat("inert source\n", 40))})
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	rev := Revision{ID: uuid.NewV7().String(), ProjectID: p.ID, SchemaVersion: EventsSchemaVersion, SemanticHash: hashBytes(raw), SourceSnapshotIDs: []string{}, ArtifactPins: []ArtifactPin{}, Coverage: g.Coverage, Author: "test", Summary: "Synthetic read fixture", CreatedAt: time.Now().UTC()}
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		if err := writeImportedRevision(t.Context(), tx, p.ID, rev, g); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), "UPDATE backend_projects SET current_revision_id=?,version=version+1 WHERE id=?", rev.ID, p.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d := diagramTestDocument(rev.ID)
	app := "10000000-0000-4000-8000-000000000003"
	d.Payload.Elements = append(d.Payload.Elements, ArchitectureElement{ID: app, Label: "App", Role: "application", ParentID: d.Payload.PrimarySystemID, Origin: DiagramOrigin{Kind: "authored", Reason: "Synthetic responsibility"}, Refs: []DiagramRef{}})
	for i := range 2 {
		d.Payload.Elements = append(d.Payload.Elements, ArchitectureElement{ID: fmt.Sprintf("10000000-0000-4000-8000-%012d", i+4), Label: fmt.Sprint("Component ", i), Role: "component", ParentID: app, Origin: DiagramOrigin{Kind: "authored", Reason: "Synthetic responsibility"}, Refs: []DiagramRef{{Kind: "record", RecordType: "node", ID: g.Nodes[i].ID}}})
	}
	// Persist the inert fixture through canonical owner storage without spending
	// setup time bootstrapping source6 assertions unrelated to the read benchmark.
	graph, err := r.readArchitectureGraph(t.Context(), p.ID, d.Target)
	if err != nil {
		t.Fatal(err)
	}
	d, err = normalizeDiagram(d)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := requestDigest(d)
	if err != nil {
		t.Fatal(err)
	}
	v := &DiagramVersion{ProjectID: p.ID, Document: d, Pin: DiagramPin{ID: uuid.NewV7().String(), Version: 1, ContentHash: hash}, TargetHash: graph.Pins.TargetHash, Author: "test", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Gaps: []DiagramGap{}}
	diagramProvenance(v, nil, "create", "")
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		return persistDiagramVersion(t.Context(), tx, diagramMutation{pid: p.ID, op: "create", key: "fixture", digest: hash, document: d}, v)
	}); err != nil {
		t.Fatal(err)
	}
	return r, p.ID, DiagramQueryInput{Pin: v.Pin, Level: "components", RootID: app, Origin: "all", Section: "links", Limit: 2, ResponseMode: "compact-v1"}
}

func TestArchitectureStoredParallelReadsAndCancellation(t *testing.T) {
	t.Parallel()
	r, pid, in := storedArchitectureFixture(t, 30, 90, 270)
	var wg sync.WaitGroup
	var mu sync.Mutex
	completed, busy := 0, 0
	start := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			<-start
			page, err := r.QueryDiagram(t.Context(), pid, in)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				completed++
				if len(page.Items) != 2 || page.GapSummary == nil || page.GapSummary.Total != 30 {
					t.Errorf("projection changed: %+v", page)
				}
			} else if fault, ok := errors.AsType[*FaultError](err); ok && fault.Code == "backend_projection_busy" && fault.Retryable {
				busy++
			} else {
				t.Errorf("unexpected read failure: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if completed == 0 || completed+busy != 4 {
		t.Fatalf("completed=%d busy=%d", completed, busy)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.QueryDiagram(ctx, pid, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
	if _, err := r.QueryDiagram(t.Context(), pid, in); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkArchitectureStoredRead(b *testing.B) {
	r, pid, in := storedArchitectureFixture(b, 25228, 32348, 106398)
	for _, warm := range []bool{false, true} {
		b.Run(fmt.Sprint("warm=", warm), func(b *testing.B) {
			if warm {
				if _, err := r.QueryDiagram(b.Context(), pid, in); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if !warm {
					r.architectureReads.key = ""
					r.architectureReads.projection = nil
				}
				page, err := r.QueryDiagram(b.Context(), pid, in)
				if err != nil {
					b.Fatal(err)
				}
				raw, err := json.Marshal(page)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(raw)), "response-bytes")
			}
		})
	}
}
