// Package backendportable implements bounded portable artifacts, without execution.
package backendportable

import (
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"fmt"
	"math"
	"slices"
	"strings"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

const MaxSVGBytes = 2 << 20

type SVGInput struct {
	ViewID      string `json:"viewId"`
	ViewVersion int64  `json:"viewVersion"`
}
type SVGArtifact struct {
	Filename    string
	ContentType string
	Body        []byte
}

// DiagramReader resolves immutable saved pins and their projections. Repo implements it.
type DiagramReader interface {
	GetDiagramView(context.Context, string, string, int64) (*bm.DiagramView, error)
	GetDiagram(context.Context, string, bm.DiagramPin) (*bm.DiagramVersion, error)
	QueryDiagram(context.Context, string, bm.DiagramQueryInput) (*bm.DiagramPage, error)
}

// fault derives the code from the status. Review 2026-10-06, F72: every
// refusal used to carry backend_portable_invalid, so a lost-reply recovery
// could branch only on free text to tell a missing session (404) from a
// stale expectedVersion, reused key or immutable chunk (409) or a quota
// (413). The conflict and quota codes follow the other backend modules.
func fault(status int, message string) error {
	code := "backend_portable_invalid"
	switch status {
	case 404:
		code = "backend_portable_not_found"
	case 409:
		code = "backend_portable_conflict"
	case 413:
		code = "backend_portable_limit"
	}
	return &bm.FaultError{Status: status, Code: code, Message: message}
}

type svgNode struct {
	id, label, detail string
	x, y              float64
}
type svgEdge struct{ id, from, to, label string }

func ExportDiagramSVG(ctx context.Context, r DiagramReader, project string, in SVGInput) (*SVGArtifact, error) {
	view, d, err := exactDiagramView(ctx, r, project, in)
	if err != nil {
		return nil, err
	}
	m := &svgMembers{nodes: []svgNode{}, edges: []svgEdge{}, gaps: map[string]bool{}, rowHashes: map[string]string{}}
	for _, section := range []string{"elements", "links"} {
		q := bm.DiagramQueryInput{Pin: d.Pin, Level: view.State.Level, RootID: view.State.RootID, Search: view.State.Search, Origin: view.State.Origin, Section: section, Limit: 500}
		if err := m.collect(ctx, r, project, q, d); err != nil {
			return nil, err
		}
	}
	nodes, edges, gaps, truncated := m.nodes, m.edges, m.gaps, m.truncated
	slices.SortFunc(nodes, func(a, b svgNode) int { return strings.Compare(a.id, b.id) })
	slices.SortFunc(edges, func(a, b svgEdge) int { return strings.Compare(a.id, b.id) })
	positions, err := savedPositions(view.State.Positions)
	if err != nil {
		return nil, err
	}
	lay, err := layoutSVG(nodes, positions)
	if err != nil {
		return nil, err
	}
	ids, fallback := lay.ids, lay.fallback
	minX, minY := lay.minX, lay.minY
	width, height := lay.maxX-minX, lay.maxY-minY
	if math.IsInf(width, 0) || math.IsInf(height, 0) {
		return nil, fault(422, "Saved coordinate extent is not finite")
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="title description" viewBox="%g %g %g %g">`, minX, minY, width, height)
	b.WriteString(`<title id="title">` + escape(view.Name) + `</title><desc id="description">Exact saved diagram export; authored intent is not runtime proof.</desc>`)
	meta, err := json.Marshal(struct {
		Project string               `json:"projectId"`
		View    SVGInput             `json:"view"`
		Diagram bm.DiagramPin        `json:"diagram"`
		Target  bm.BackendReadTarget `json:"target"`
		Hash    string               `json:"targetHash"`
	}{project, in, d.Pin, d.Document.Target, d.TargetHash})
	if err != nil {
		return nil, err
	}
	b.WriteString(`<metadata>` + escape(string(meta)) + `</metadata>`)
	legend := []string{view.Name + " / " + d.Document.Kind, fmt.Sprintf("view %s v%d; diagram %s v%d", view.ID, view.Version, d.Pin.ID, d.Pin.Version), "diagram hash " + d.Pin.ContentHash, "target hash " + d.TargetHash, fmt.Sprintf("scope level=%s root=%s search=%s origin=%s; collapsed=%d (expanded in export)", view.State.Level, view.State.RootID, view.State.Search, view.State.Origin, len(view.State.CollapsedIDs)), fmt.Sprintf("coverage: %d nodes, %d edges, %d gaps; overflow=%t; deterministic grid fallback=%d", len(nodes), len(edges), len(gaps), truncated, fallback), "authored intent / source assertion; partial order only; unknown guards are not evaluated"}
	for i, line := range legend {
		svgText(&b, minX+20, minY+24+float64(i)*28, line)
	}
	omitted := 0
	for _, e := range edges {
		a, ok := ids[e.from]
		c, ok2 := ids[e.to]
		if !ok || !ok2 {
			omitted++
			continue
		}
		from, to := nodes[a], nodes[c]
		fmt.Fprintf(&b, `<g><title>%s</title><path d="M %g %g L %g %g" fill="none" stroke="#495057"/><text x="%g" y="%g" fill="#212529" font-size="12">%s</text></g>`, escape(e.id), from.x+120, from.y+80, to.x+120, to.y, from.x/2+to.x/2+120, from.y/2+to.y/2, escape(e.label+" ["+e.from+" → "+e.to+"]"))
	}
	svgText(&b, minX+20, minY+230, fmt.Sprintf("Edges outside selected scope: %d", omitted))
	for _, n := range nodes {
		fmt.Fprintf(&b, `<g><title>%s</title><rect x="%g" y="%g" width="250" height="95" rx="6" fill="#f8f9fa" stroke="#495057"/>`, escape(n.id), n.x, n.y)
		svgText(&b, n.x+8, n.y+22, n.label)
		svgText(&b, n.x+8, n.y+48, n.detail)
		b.WriteString(`</g>`)
		if b.Len() > MaxSVGBytes {
			return nil, fault(413, "SVG exceeds 2 MiB")
		}
	}
	b.WriteString(`</svg>`)
	if b.Len() > MaxSVGBytes {
		return nil, fault(413, "SVG exceeds 2 MiB")
	}
	return &SVGArtifact{Filename: fmt.Sprintf("backend-view-%s-v%d.svg", view.ID, view.Version), ContentType: "image/svg+xml", Body: []byte(b.String())}, nil
}

// exactDiagramView reads the saved view and the diagram it pins, both exactly
// as pinned, for a diagram kind the export can draw.
func exactDiagramView(ctx context.Context, r DiagramReader, project string, in SVGInput) (*bm.DiagramView, *bm.DiagramVersion, error) {
	if !bm.ValidID(project) || !bm.ValidID(in.ViewID) || in.ViewVersion <= 0 {
		return nil, nil, fault(422, "Exact saved view ID/version required")
	}
	view, err := r.GetDiagramView(ctx, project, in.ViewID, in.ViewVersion)
	if err != nil {
		return nil, nil, err
	}
	if view.ID != in.ViewID || view.Version != in.ViewVersion {
		return nil, nil, fault(409, "Saved view pin mismatch")
	}
	d, err := r.GetDiagram(ctx, project, view.State.Diagram)
	if err != nil {
		return nil, nil, err
	}
	if d.Pin != view.State.Diagram || d.ProjectID != project {
		return nil, nil, fault(409, "Diagram pin mismatch")
	}
	if !slices.Contains([]string{"architecture", "interactions", "lifecycle", "business_map"}, d.Document.Kind) {
		return nil, nil, fault(422, "Unsupported diagram kind")
	}
	return view, d, nil
}

// svgMembers accumulates the projection's nodes and edges across sections
// and pages; a member seen twice must be the same row.
type svgMembers struct {
	nodes     []svgNode
	edges     []svgEdge
	gaps      map[string]bool
	truncated bool
	rowHashes map[string]string
}

// collect pages through one section of the projection, refusing a page of
// another target and a cursor that repeats.
func (m *svgMembers) collect(ctx context.Context, r DiagramReader, project string, q bm.DiagramQueryInput, d *bm.DiagramVersion) error {
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := r.QueryDiagram(ctx, project, q)
		if err != nil {
			return err
		}
		if page.Pin != d.Pin || page.TargetHash != d.TargetHash {
			return fault(409, "Projection target mismatch")
		}
		m.truncated = m.truncated || page.Truncated
		for _, gap := range page.Gaps {
			m.gaps[gap.ID] = true
		}
		for _, row := range page.Items {
			if err := m.add(row); err != nil {
				return err
			}
		}
		if len(m.nodes) > 200 || len(m.edges) > 600 {
			return fault(413, "SVG exceeds 200 nodes or 600 edges; narrow the saved view")
		}
		if page.NextCursor == "" {
			return nil
		}
		if seen[page.NextCursor] {
			return fault(422, "Repeated projection cursor")
		}
		seen[page.NextCursor] = true
		q.Cursor = page.NextCursor
	}
}

func (m *svgMembers) add(row bm.DiagramRow) error {
	n, e, err := svgRow(row)
	if err != nil {
		return err
	}
	id := ""
	if n != nil {
		id = n.id
	}
	if e != nil {
		id = e.id
	}
	h, err := DocumentHash(row)
	if err != nil {
		return err
	}
	if prior, ok := m.rowHashes[id]; ok {
		if prior != h {
			return fault(422, "Conflicting SVG member")
		}
		return nil
	}
	m.rowHashes[id] = h
	if n != nil {
		m.nodes = append(m.nodes, *n)
	}
	if e != nil {
		m.edges = append(m.edges, *e)
	}
	return nil
}

func savedPositions(list []bm.DiagramPosition) (map[string]bm.DiagramPosition, error) {
	positions := map[string]bm.DiagramPosition{}
	for _, p := range list {
		if _, ok := positions[p.ID]; ok || math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			return nil, fault(422, "Saved positions must be unique and finite")
		}
		positions[p.ID] = p
	}
	return positions, nil
}

// svgLayout is where the nodes landed and the extent that holds them.
type svgLayout struct {
	ids                    map[string]int
	minX, minY, maxX, maxY float64
	fallback               int
}

// layoutSVG places each node at its saved position, or on a deterministic
// grid when it has none, and grows the extent around it.
func layoutSVG(nodes []svgNode, positions map[string]bm.DiagramPosition) (svgLayout, error) {
	l := svgLayout{ids: map[string]int{}, minX: 0, minY: 0, maxX: 1100, maxY: 240}
	for i := range nodes {
		n := &nodes[i]
		if _, ok := l.ids[n.id]; ok {
			return l, fault(422, "Duplicate SVG member")
		}
		l.ids[n.id] = i
		n.x = float64(i%4)*290 + 30
		n.y = float64(i/4)*140 + 320
		if p, ok := positions[n.id]; ok {
			n.x = p.X
			n.y = p.Y
		} else {
			l.fallback++
		}
		l.minX = math.Min(l.minX, n.x-30)
		l.minY = math.Min(l.minY, n.y-300)
		l.maxX = math.Max(l.maxX, n.x+270)
		l.maxY = math.Max(l.maxY, n.y+130)
	}
	return l, nil
}
func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func svgText(b *strings.Builder, x, y float64, s string) {
	fmt.Fprintf(b, `<text x="%g" y="%g" fill="#212529" font-family="sans-serif" font-size="12">%s</text>`, x, y, escape(s))
}
func svgRow(r bm.DiagramRow) (*svgNode, *svgEdge, error) {
	// Marshal enforces exactly one typed arm, including unsupported rows.
	if _, err := json.Marshal(r); err != nil {
		return nil, nil, err
	}
	origin := func(o bm.DiagramOrigin) string {
		if o.Kind == "authored" {
			return "authored intent: " + o.Reason
		}
		return "source assertion (not runtime proof)"
	}
	switch {
	case r.ArchitectureElement != nil:
		v := r.ArchitectureElement
		return &svgNode{id: v.ID, label: v.Label, detail: v.Role + "; " + origin(v.Origin)}, nil, nil
	case r.ArchitectureLink != nil:
		v := r.ArchitectureLink
		return nil, &svgEdge{v.ID, v.From, v.To, v.Label + "; " + origin(v.Origin)}, nil
	case r.BusinessElement != nil:
		v := r.BusinessElement
		return &svgNode{id: v.ID, label: v.Label, detail: v.Role + "; " + origin(v.Origin)}, nil, nil
	case r.BusinessLink != nil:
		v := r.BusinessLink
		return nil, &svgEdge{v.ID, v.From, v.To, v.Label + "; " + origin(v.Origin)}, nil
	case r.State != nil:
		v := r.State
		return &svgNode{id: v.ID, label: v.Label, detail: origin(v.Origin)}, nil, nil
	case r.Transition != nil:
		v := r.Transition
		return nil, &svgEdge{v.ID, v.From, v.To, v.Label + "; guard=" + v.Guard.Kind + " " + v.Guard.Text + "; " + origin(v.Origin)}, nil
	case r.Rule != nil:
		v := r.Rule
		return nil, &svgEdge{v.ID, v.From, v.To, "rule=" + v.Verdict + "; " + origin(v.Origin)}, nil
	case r.Participant != nil:
		v := r.Participant
		return &svgNode{id: v.ID, label: v.Label, detail: origin(v.Origin)}, nil, nil
	case r.Step != nil:
		v := r.Step
		return &svgNode{id: v.ID, label: v.Label, detail: v.Kind + " " + v.From + " → " + v.To + "; replyTo=" + v.ReplyTo + "; branches=" + strings.Join(v.BranchPath, ",") + "; " + origin(v.Origin)}, nil, nil
	case r.Branch != nil:
		v := r.Branch
		return &svgNode{id: v.ID, label: v.Label, detail: v.Kind + "; guard (unknown): " + v.GuardText + "; " + origin(v.Origin)}, nil, nil
	case r.Order != nil:
		v := r.Order
		return nil, &svgEdge{v.ID, v.From, v.To, "partial order; " + origin(v.Origin)}, nil
	default:
		return nil, nil, fault(422, "Unsupported SVG projection row")
	}
}
