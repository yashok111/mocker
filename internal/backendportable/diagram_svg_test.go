package backendportable

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

const pid = "10000000-0000-4000-8000-000000000001"
const did = "10000000-0000-4000-8000-000000000002"
const vid = "10000000-0000-4000-8000-000000000003"
const nid = "10000000-0000-4000-8000-000000000004"
const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type svgReader struct {
	view    bm.DiagramView
	diagram bm.DiagramVersion
	nodes   []bm.DiagramRow
	edges   []bm.DiagramRow
	head    int
}

func (r *svgReader) GetDiagramView(_ context.Context, p, id string, v int64) (*bm.DiagramView, error) {
	if p != pid || id != vid || v != 2 {
		panic("unpinned view read")
	}
	return &r.view, nil
}
func (r *svgReader) GetDiagram(_ context.Context, p string, pin bm.DiagramPin) (*bm.DiagramVersion, error) {
	if p != pid || pin != r.diagram.Pin {
		panic("unpinned diagram read")
	}
	return &r.diagram, nil
}
func (r *svgReader) QueryDiagram(_ context.Context, p string, q bm.DiagramQueryInput) (*bm.DiagramPage, error) {
	if p != pid || q.Pin != r.diagram.Pin {
		panic("unpinned projection")
	}
	rows := r.nodes
	if q.Section == "links" {
		rows = r.edges
	}
	return &bm.DiagramPage{Pin: q.Pin, TargetHash: hash, Items: rows, Total: len(rows)}, nil
}
func svgFixture(kind string) *svgReader {
	pin := bm.DiagramPin{ID: did, Version: 3, ContentHash: hash}
	label := `<script onload="x">& malicious</script>`
	origin := bm.DiagramOrigin{Kind: "authored", Reason: "design intent"}
	r := &svgReader{view: bm.DiagramView{ID: vid, Version: 2, Name: "Saved", State: bm.DiagramViewState{Diagram: pin, Origin: "all", Positions: []bm.DiagramPosition{}, CollapsedIDs: []string{}}}, diagram: bm.DiagramVersion{Pin: pin, ProjectID: pid, TargetHash: hash, Document: bm.DiagramDocument{Kind: kind, Target: bm.BackendReadTarget{RevisionID: pid}}}}
	switch kind {
	case "architecture":
		r.nodes = []bm.DiagramRow{{ArchitectureElement: &bm.ArchitectureElement{ID: nid, Label: label, Origin: origin}}}
	case "interactions":
		r.nodes = []bm.DiagramRow{{Participant: &bm.InteractionParticipant{ID: nid, Label: label, Origin: origin}}}
	case "lifecycle":
		r.nodes = []bm.DiagramRow{{State: &bm.LifecycleState{ID: nid, Label: label, Origin: origin}}}
	case "business_map":
		r.nodes = []bm.DiagramRow{{BusinessElement: &bm.BusinessElement{ID: nid, Label: label, Origin: origin}}}
	}
	return r
}
func TestDiagramSVGExactSafeFourKinds(t *testing.T) {
	for _, kind := range []string{"architecture", "interactions", "lifecycle", "business_map"} {
		t.Run(kind, func(t *testing.T) {
			r := svgFixture(kind)
			in := SVGInput{ViewID: vid, ViewVersion: 2}
			a, err := ExportDiagramSVG(t.Context(), r, pid, in)
			if err != nil {
				t.Fatal(err)
			}
			r.head = 999
			b, err := ExportDiagramSVG(t.Context(), r, pid, in)
			if err != nil || string(a.Body) != string(b.Body) {
				t.Fatal("mutable head affected exact export", err)
			}
			for _, s := range []string{vid, did, hash, "authored intent", "deterministic", "coverage", "unknown"} {
				if !strings.Contains(string(a.Body), s) {
					t.Errorf("missing legend %q", s)
				}
			}
			dec := xml.NewDecoder(strings.NewReader(string(a.Body)))
			for {
				tok, err := dec.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if e, ok := tok.(xml.StartElement); ok {
					if e.Name.Local == "script" || e.Name.Local == "foreignObject" {
						t.Fatal("executable element")
					}
					for _, a := range e.Attr {
						if strings.HasPrefix(a.Name.Local, "on") || a.Name.Local == "href" {
							t.Fatal("executable attribute")
						}
					}
				}
			}
			if a.ContentType != "image/svg+xml" || !strings.HasSuffix(a.Filename, ".svg") {
				t.Fatal("attachment metadata missing")
			}
		})
	}
}
func TestDiagramSVGRejectsBoundsAndNonfinite(t *testing.T) {
	r := svgFixture("architecture")
	r.view.State.Positions = []bm.DiagramPosition{{ID: nid, X: math.Inf(1)}}
	if _, err := ExportDiagramSVG(t.Context(), r, pid, SVGInput{vid, 2}); err == nil {
		t.Fatal("nonfinite accepted")
	}
	r = svgFixture("architecture")
	for len(r.nodes) <= 200 {
		r.nodes = append(r.nodes, bm.DiagramRow{ArchitectureElement: &bm.ArchitectureElement{ID: fmt.Sprintf("20000000-0000-4000-8000-%012d", len(r.nodes)), Label: "node"}})
	}
	if _, err := ExportDiagramSVG(t.Context(), r, pid, SVGInput{vid, 2}); err == nil {
		t.Fatal("node quota accepted")
	}
}

type lifecycleSVGReader struct{ *svgReader }

func (r lifecycleSVGReader) QueryDiagram(ctx context.Context, _ string, q bm.DiagramQueryInput) (*bm.DiagramPage, error) {
	return bm.ProjectLifecycle(ctx, &r.diagram, q)
}
func TestDiagramSVGLifecycleProjectsTransitionOnce(t *testing.T) {
	r := svgFixture("lifecycle")
	r.diagram.Document.Lifecycle = &bm.LifecyclePayload{States: []bm.LifecycleState{{ID: nid, Label: "Paid", Origin: bm.DiagramOrigin{Kind: "authored", Reason: "intent"}}}, Transitions: []bm.LifecycleTransition{{ID: vid, From: nid, To: nid, Label: "retry", Origin: bm.DiagramOrigin{Kind: "authored", Reason: "intent"}, Guard: bm.LifecycleGuard{Kind: "unknown"}}}}
	out, err := ExportDiagramSVG(t.Context(), lifecycleSVGReader{r}, pid, SVGInput{vid, 2})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out.Body), "<path ") != 1 || !strings.Contains(string(out.Body), "1 edges") {
		t.Fatal("duplicate transition in elements/links")
	}
}
