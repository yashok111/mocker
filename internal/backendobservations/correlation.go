package backendobservations

import (
	"context"
	"encoding/json/v2"
	"reflect"
	"slices"
	"strings"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

type GraphReader interface {
	ResolveEffectiveGraph(context.Context, string, bm.BackendReadTarget) (*bm.EffectiveGraphSnapshot, error)
	ResolveDiagramScope(context.Context, string, bm.DiagramScopeInput) (*bm.DiagramScope, error)
	GetDiagram(context.Context, string, bm.DiagramPin) (*bm.DiagramVersion, error)
}
type Service struct {
	Repo   *Repo
	Graphs GraphReader
	Replay ReplayReader
}

func (s *Service) Correlate(ctx context.Context, pid, sid string, in CorrelateInput) (*CorrelationSnapshot, error) {
	if !correlateInputValid(pid, sid, in) {
		return nil, invalid()
	}
	raw, e := canonical(in)
	if e != nil || len(raw) > BatchBytes {
		return nil, fault(413, "batch_limit")
	}
	var valid CorrelateInput
	if json.Unmarshal(raw, &valid) != nil {
		return nil, invalid()
	}
	hash, _ := p.Hash(CorrelationPolicy+"/request", in)
	cached := new(CorrelationSnapshot)
	if ok, e := readCorrelationReceipt(ctx, s.Repo.db.R, pid, sid, in.IdempotencyKey, hash, cached); e != nil || ok {
		return cached, e
	}
	ver, e := s.Repo.Version(ctx, pid, sid, in.Observation.Version)
	if e != nil {
		return nil, e
	}
	if ver.VersionReceipt != in.Observation || ver.Context.Source.ServiceID != in.ServiceID {
		return nil, fault(409, "observation_pin_mismatch")
	}
	g, e := s.Graphs.ResolveEffectiveGraph(ctx, pid, bm.BackendReadTarget{RevisionID: in.RevisionID})
	if e != nil {
		return nil, e
	}
	if g.Pins.TargetHash != in.TargetGraphHash || g.Pins.BaseSemanticHash != in.SourceHash {
		return nil, fault(409, "source_pin_mismatch")
	}
	out := &CorrelationSnapshot{Input: in, SourceCompatible: sourceCompatible(ver.Context, g), Rows: []CorrelationRow{}, DiagramRows: []DiagramCorrelationRow{}, Gaps: []string{}}
	if !out.SourceCompatible {
		out.Gaps = append(out.Gaps, "Unknown or mismatched repository/build/service source-file identity; mappings remain exploratory")
	}
	c := &correlation{s: s, ctx: ctx, pid: pid, in: in, out: out, g: g, members: map[string][]bm.DiagramRef{}}
	if e = c.resolveScope(); e != nil {
		return nil, e
	}
	c.refs = bm.NewObservationRefResolver(ctx, g)
	c.index = &correlationIndex{graph: g, repository: ver.Context.Source.RepositoryID}
	if c.overrides, e = resolveOverrides(c.refs, in.Overrides); e != nil {
		return nil, e
	}
	if e = c.correlateRecords(sid, ver.Version); e != nil {
		return nil, e
	}
	if len(c.overrides) != 0 {
		return nil, fault(422, "unknown_record")
	}
	out.Total = len(out.Rows)
	return s.Repo.saveCorrelation(ctx, pid, sid, hash, out)
}

func correlateInputValid(pid, sid string, in CorrelateInput) bool {
	if !p.ValidID(pid) || !p.ValidID(sid) || in.Observation.SetID != sid || in.Observation.Version < 1 || !p.ValidHash(in.Observation.ContentHash) {
		return false
	}
	if !p.ValidID(in.RevisionID) || !p.ValidHash(in.SourceHash) || !p.ValidHash(in.TargetGraphHash) || in.Policy != CorrelationPolicy {
		return false
	}
	return in.ExpectedCorrelationVersion >= 0 && bounded(in.IdempotencyKey, 128) && len(in.Overrides) <= 500 && bounded(in.ServiceID, 256)
}

// correlation is one Correlate run's state, shared by its per-record steps.
type correlation struct {
	s         *Service
	ctx       context.Context
	pid       string
	in        CorrelateInput
	out       *CorrelationSnapshot
	g         *bm.EffectiveGraphSnapshot
	refs      *bm.ObservationRefResolver
	index     *correlationIndex
	overrides map[string]Override
	diagram   *bm.DiagramVersion
	members   map[string][]bm.DiagramRef
	budget    int
}

// resolveScope pins the optional diagram scope exactly and resolves each of
// its selectors' members; a scope hash without a scope is invalid.
func (c *correlation) resolveScope() error {
	in := c.in
	if in.DiagramScope == nil {
		if in.ScopeHash != "" {
			return invalid()
		}
		return nil
	}
	if !p.ValidHash(in.ScopeHash) {
		return invalid()
	}
	var e error
	c.out.DiagramScope, e = c.s.Graphs.ResolveDiagramScope(c.ctx, c.pid, *in.DiagramScope)
	if e != nil {
		return e
	}
	sc := c.out.DiagramScope
	if sc.ScopeHash != in.ScopeHash || sc.TargetHash != in.TargetGraphHash || sc.Target.RevisionID != in.RevisionID || sc.Truncated {
		return fault(422, "diagram_pin_mismatch")
	}
	c.diagram, e = c.s.Graphs.GetDiagram(c.ctx, c.pid, sc.Pin)
	if e != nil {
		return e
	}
	for _, sel := range sc.Selectors {
		part, e := c.s.Graphs.ResolveDiagramScope(c.ctx, c.pid, bm.DiagramScopeInput{Pin: sc.Pin, Selectors: []bm.DiagramScopeSelector{sel}})
		if e != nil {
			return e
		}
		if part.Truncated {
			return fault(422, "incomplete_scope")
		}
		c.members[selectorKey(sel)] = part.SourceRefs
		c.members[sel.ID] = part.SourceRefs
	}
	return nil
}

// resolveOverrides indexes the human overrides by record. Unlike a record's
// own ref, an override's ref is a decision and must resolve in the target.
func resolveOverrides(refs *bm.ObservationRefResolver, list []Override) (map[string]Override, error) {
	overrides := map[string]Override{}
	for _, o := range list {
		if !bounded(o.RecordID, 256) || !bounded(o.Reason, 4096) {
			return nil, invalid()
		}
		if _, ok := overrides[o.RecordID]; ok {
			return nil, invalid()
		}
		ok, e := refs.Resolve(o.Ref)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, fault(422, "foreign_ref")
		}
		overrides[o.RecordID] = o
	}
	return overrides, nil
}

// correlateRecords pages through every record of the pinned version.
func (c *correlation) correlateRecords(sid string, version int64) error {
	for cursor := ""; ; {
		page, e := c.s.Repo.Records(c.ctx, c.pid, sid, version, 500, cursor)
		if e != nil {
			return e
		}
		for _, rec := range page.Items {
			if e = c.ctx.Err(); e != nil {
				return e
			}
			if e = c.correlateRecord(rec); e != nil {
				return e
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

// correlateRecord maps one record to its row, and to its diagram row when a
// diagram is in scope, within the snapshot's byte quota.
func (c *correlation) correlateRecord(rec Record) error {
	row := CorrelationRow{RecordID: rec.ID, Outcome: "unresolved", Candidates: []bm.DiagramRef{}, Reasons: []string{}, Method: "none"}
	// row.Method names only a strategy that actually produced candidates;
	// a requested strategy that matched nothing leaves "none". It used
	// to be set whenever the input was present, so an unmatched
	// identityRef said instrumentation_id and a fingerprint pass
	// overwrote a real source_locator match (review 2026-10-06, F157).
	if e := c.matchBackendRef(rec, &row); e != nil {
		return e
	}
	if len(row.Candidates) == 0 && rec.IdentityRef != nil && c.g.Source != nil {
		c.matchIdentityRef(rec.IdentityRef, &row)
	}
	if len(row.Candidates) == 0 && rec.Attrs != nil {
		c.matchAttributes(rec.Attrs, &row)
	}
	c.settleRow(rec, &row)
	rowBytes, _ := canonical(row)
	c.budget += len(rowBytes)
	if c.budget > 60<<20 {
		return fault(413, "correlation_quota")
	}
	c.out.Rows = append(c.out.Rows, row)
	if c.diagram == nil {
		return nil
	}
	drow, e := c.diagramRow(rec, row)
	if e != nil {
		return e
	}
	c.out.DiagramRows = append(c.out.DiagramRows, drow)
	return nil
}

func (c *correlation) matchBackendRef(rec Record, row *CorrelationRow) error {
	if rec.BackendRef == nil {
		return nil
	}
	ok, e := c.refs.Resolve(*rec.BackendRef)
	if e != nil {
		return e
	}
	if !ok {
		// A record's ref is producer data, not a human decision:
		// evidence captured against an older revision must stay
		// inspectable against a newer one that dropped the object.
		// One stale ref aborted the whole correlation with 422
		// foreign_ref (review 2026-10-06, F187). Override refs keep
		// the hard 422 above.
		row.Reasons = append(row.Reasons, "stale_backend_ref")
		return nil
	}
	addCandidate(row, *rec.BackendRef)
	row.Method = "instrumentation_id"
	if c.out.SourceCompatible {
		row.Outcome = "explicit"
		row.Selected = rec.BackendRef
	}
	return nil
}

func (c *correlation) matchIdentityRef(id *IdentityRef, row *CorrelationRow) {
	for _, candidate := range c.index.identity(correlationIdentityKey{id.RepositoryID, id.ProviderNamespace, id.ExternalKey, id.RecordType}) {
		addCandidate(row, bm.DiagramRef{Kind: "record", RecordType: id.RecordType, ID: candidate})
	}
	if len(row.Candidates) > 0 {
		row.Method = "instrumentation_id"
	}
	if len(row.Candidates) == 1 && c.out.SourceCompatible {
		row.Selected = &row.Candidates[0]
		row.Outcome = "explicit"
	}
}

func (c *correlation) matchAttributes(a *Attributes, row *CorrelationRow) {
	if c.in.Settings.InferSourceLocator && a.SourcePath != "" {
		if a.SourceLine > 0 {
			for _, ref := range c.index.locator(a.SourcePath, a.SourceLine) {
				addCandidate(row, ref)
			}
		}
		if len(row.Candidates) > 0 {
			row.Method = "source_locator"
		}
	}
	// No node kind may carry a query fingerprint attribute
	// (validateAttributes rejects any unknown key), so fingerprint
	// inference can never match. Say so on the row instead of
	// scanning for an attribute that cannot exist; removing the
	// inferFingerprint setting is a contract change left to the
	// owner (review 2026-10-06, F157).
	if c.in.Settings.InferFingerprint && a.Fingerprint != "" && len(row.Candidates) == 0 {
		row.Reasons = append(row.Reasons, "query_fingerprint_unsupported")
	}
}

// settleRow bounds and orders the candidates, infers a single inferred
// candidate, and lets a human override replace whatever was found.
func (c *correlation) settleRow(rec Record, row *CorrelationRow) {
	slices.SortFunc(row.Candidates, func(a, b bm.DiagramRef) int {
		x, _ := canonical(a)
		y, _ := canonical(b)
		return strings.Compare(string(x), string(y))
	})
	row.Candidates = slices.CompactFunc(row.Candidates, func(a, b bm.DiagramRef) bool { return reflect.DeepEqual(a, b) })
	if len(row.Candidates) > 20 {
		row.Candidates = row.Candidates[:20]
		row.Reasons = append(row.Reasons, "candidate_limit")
	} else if len(row.Candidates) == 1 && row.Selected == nil && row.Method != "instrumentation_id" {
		row.Selected = &row.Candidates[0]
		row.Outcome = "inferred"
	}
	if o, ok := c.overrides[rec.ID]; ok {
		row.Selected = &o.Ref
		row.Candidates = []bm.DiagramRef{o.Ref}
		row.Outcome = "inferred"
		row.Method = "manual"
		row.Reasons = append(row.Reasons, o.Reason)
		delete(c.overrides, rec.ID)
	}
	if row.Selected == nil {
		row.Reasons = append(row.Reasons, "No unambiguous compatible source identity")
	}
}

// diagramRow maps a row onto the diagram scope; an explicit, supported
// diagram witness on the record takes precedence over the source mapping.
func (c *correlation) diagramRow(rec Record, row CorrelationRow) (DiagramCorrelationRow, error) {
	protected := c.diagram.Document.Kind == "lifecycle"
	drow := mapDiagramRow(row, c.out.SourceCompatible, c.out.DiagramScope, c.members, protected)
	if rec.DiagramWitness == nil {
		return drow, nil
	}
	w := rec.DiagramWitness
	if w.Pin != c.out.DiagramScope.Pin || !slices.ContainsFunc(c.out.DiagramScope.Selectors, func(sel bm.DiagramScopeSelector) bool { return reflect.DeepEqual(sel, w.Selector) }) {
		return drow, fault(422, "witness_pin_mismatch")
	}
	supported, e := c.witnessSupported(rec, w, protected)
	if e != nil {
		return drow, e
	}
	if c.out.SourceCompatible && supported {
		drow = DiagramCorrelationRow{RecordID: rec.ID, Selectors: []bm.DiagramScopeSelector{w.Selector}, Basis: "explicit", Gaps: []string{}}
	}
	return drow, nil
}

// witnessSupported decides whether the diagram kind admits the witness kind.
// A replay assertion counts only when the pinned run report executed exactly
// that binding.
func (c *correlation) witnessSupported(rec Record, w *DiagramWitness, protected bool) (bool, error) {
	supported := !protected || w.Kind == "state_change"
	if c.diagram.Document.Kind == "business_map" && w.Kind != "business_event" {
		supported = false
	}
	if w.Kind != "replay_assertion" {
		return supported, nil
	}
	if c.s.Replay == nil || rec.RunPins == nil {
		return false, nil
	}
	run, e := c.s.Replay.Get(c.ctx, c.pid, rec.RunPins.RunID)
	if e != nil {
		return false, e
	}
	if run.Report == nil {
		return false, nil
	}
	h, _ := p.Hash("backend-replay-report-v1", run.Report)
	if h != rec.RunPins.ReportHash {
		return false, fault(409, "report_pin_mismatch")
	}
	supported = false
	for _, b := range run.Report.Bindings {
		if b.Binding.Diagram == w.Pin && b.Binding.ElementID == w.Selector.ID && slices.Contains(b.AssertionIDs, w.AssertionID) && b.Status == "executed" {
			supported = true
		}
	}
	return supported, nil
}
func sourceCompatible(c ObservationContext, g *bm.EffectiveGraphSnapshot) bool {
	if c.Source.Status != "known" {
		return false
	}
	foundService := slices.ContainsFunc(g.State.Nodes, func(n bm.Node) bool { return n.ID == c.Source.ServiceID && n.Kind == "service" })
	if !foundService {
		return false
	}
	files := map[string]string{}
	for _, snapshot := range g.State.Sources {
		if snapshot.RepositoryID != c.Source.RepositoryID {
			continue
		}
		for _, f := range snapshot.Files {
			if old, ok := files[f.Path]; ok && old != f.ContentHash {
				return false
			}
			files[f.Path] = f.ContentHash
		}
	}
	list := []p.SourceFile{}
	for path, hash := range files {
		list = append(list, p.SourceFile{Path: path, SHA256: hash})
	}
	hash, e := p.SourceTreeHash(list)
	return e == nil && hash == c.Source.SourceFilesHash
}

// Keep the lexicographically first20 distinct refs while scanning. A bounded
// candidate result is still ambiguous; overflow never selects a winner.
func addCandidate(row *CorrelationRow, ref bm.DiagramRef) {
	for _, old := range row.Candidates {
		if reflect.DeepEqual(old, ref) {
			return
		}
	}
	row.Candidates = append(row.Candidates, ref)
	slices.SortFunc(row.Candidates, func(a, b bm.DiagramRef) int {
		x, _ := canonical(a)
		y, _ := canonical(b)
		return strings.Compare(string(x), string(y))
	})
	if len(row.Candidates) > 20 {
		row.Candidates = row.Candidates[:20]
		if !slices.Contains(row.Reasons, "candidate_limit") {
			row.Reasons = append(row.Reasons, "candidate_limit")
		}
	}
}
