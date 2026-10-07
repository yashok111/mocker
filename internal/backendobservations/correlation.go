package backendobservations

import (
	"context"
	"encoding/json/v2"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"reflect"
	"slices"
	"strings"
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
	if !p.ValidID(pid) || !p.ValidID(sid) || in.Observation.SetID != sid || in.Observation.Version < 1 || !p.ValidHash(in.Observation.ContentHash) || !p.ValidID(in.RevisionID) || !p.ValidHash(in.SourceHash) || !p.ValidHash(in.TargetGraphHash) || in.Policy != CorrelationPolicy || in.ExpectedCorrelationVersion < 0 || !bounded(in.IdempotencyKey, 128) || len(in.Overrides) > 500 || !bounded(in.ServiceID, 256) {
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
	var diagram *bm.DiagramVersion
	members := map[string][]bm.DiagramRef{}
	if in.DiagramScope != nil {
		if !p.ValidHash(in.ScopeHash) {
			return nil, invalid()
		}
		out.DiagramScope, e = s.Graphs.ResolveDiagramScope(ctx, pid, *in.DiagramScope)
		if e != nil {
			return nil, e
		}
		sc := out.DiagramScope
		if sc.ScopeHash != in.ScopeHash || sc.TargetHash != in.TargetGraphHash || sc.Target.RevisionID != in.RevisionID || sc.Truncated {
			return nil, fault(422, "diagram_pin_mismatch")
		}
		diagram, e = s.Graphs.GetDiagram(ctx, pid, sc.Pin)
		if e != nil {
			return nil, e
		}
		for _, sel := range sc.Selectors {
			part, e := s.Graphs.ResolveDiagramScope(ctx, pid, bm.DiagramScopeInput{Pin: sc.Pin, Selectors: []bm.DiagramScopeSelector{sel}})
			if e != nil {
				return nil, e
			}
			if part.Truncated {
				return nil, fault(422, "incomplete_scope")
			}
			members[selectorKey(sel)] = part.SourceRefs
			members[sel.ID] = part.SourceRefs
		}
	} else if in.ScopeHash != "" {
		return nil, invalid()
	}
	refs := bm.NewObservationRefResolver(ctx, g)
	index := &correlationIndex{graph: g, repository: ver.Context.Source.RepositoryID}
	overrides := map[string]Override{}
	for _, o := range in.Overrides {
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
	budget := 0
	for cursor := ""; ; {
		page, e := s.Repo.Records(ctx, pid, sid, ver.Version, 500, cursor)
		if e != nil {
			return nil, e
		}
		for _, rec := range page.Items {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			row := CorrelationRow{RecordID: rec.ID, Outcome: "unresolved", Candidates: []bm.DiagramRef{}, Reasons: []string{}, Method: "none"}
			// row.Method names only a strategy that actually produced candidates;
			// a requested strategy that matched nothing leaves "none". It used
			// to be set whenever the input was present, so an unmatched
			// identityRef said instrumentation_id and a fingerprint pass
			// overwrote a real source_locator match (review 2026-10-06, F157).
			if rec.BackendRef != nil {
				ok, e := refs.Resolve(*rec.BackendRef)
				if e != nil {
					return nil, e
				}
				if ok {
					addCandidate(&row, *rec.BackendRef)
					row.Method = "instrumentation_id"
					if out.SourceCompatible {
						row.Outcome = "explicit"
						row.Selected = rec.BackendRef
					}
				} else {
					// A record's ref is producer data, not a human decision:
					// evidence captured against an older revision must stay
					// inspectable against a newer one that dropped the object.
					// One stale ref aborted the whole correlation with 422
					// foreign_ref (review 2026-10-06, F187). Override refs keep
					// the hard 422 above.
					row.Reasons = append(row.Reasons, "stale_backend_ref")
				}
			}
			if len(row.Candidates) == 0 && rec.IdentityRef != nil && g.Source != nil {
				id := rec.IdentityRef
				for _, candidate := range index.identity(correlationIdentityKey{id.RepositoryID, id.ProviderNamespace, id.ExternalKey, id.RecordType}) {
					addCandidate(&row, bm.DiagramRef{Kind: "record", RecordType: id.RecordType, ID: candidate})
				}
				if len(row.Candidates) > 0 {
					row.Method = "instrumentation_id"
				}
				if len(row.Candidates) == 1 && out.SourceCompatible {
					row.Selected = &row.Candidates[0]
					row.Outcome = "explicit"
				}
			}
			if len(row.Candidates) == 0 && rec.Attrs != nil {
				a := rec.Attrs
				if in.Settings.InferSourceLocator && a.SourcePath != "" {
					if a.SourceLine > 0 {
						for _, ref := range index.locator(a.SourcePath, a.SourceLine) {
							addCandidate(&row, ref)
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
				if in.Settings.InferFingerprint && a.Fingerprint != "" && len(row.Candidates) == 0 {
					row.Reasons = append(row.Reasons, "query_fingerprint_unsupported")
				}
			}
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
			if o, ok := overrides[rec.ID]; ok {
				row.Selected = &o.Ref
				row.Candidates = []bm.DiagramRef{o.Ref}
				row.Outcome = "inferred"
				row.Method = "manual"
				row.Reasons = append(row.Reasons, o.Reason)
				delete(overrides, rec.ID)
			}
			if row.Selected == nil {
				row.Reasons = append(row.Reasons, "No unambiguous compatible source identity")
			}
			rowBytes, _ := canonical(row)
			budget += len(rowBytes)
			if budget > 60<<20 {
				return nil, fault(413, "correlation_quota")
			}
			out.Rows = append(out.Rows, row)
			if diagram != nil {
				protected := diagram.Document.Kind == "lifecycle"
				drow := mapDiagramRow(row, out.SourceCompatible, out.DiagramScope, members, protected)
				if rec.DiagramWitness != nil {
					w := rec.DiagramWitness
					if w.Pin != out.DiagramScope.Pin || !slices.ContainsFunc(out.DiagramScope.Selectors, func(sel bm.DiagramScopeSelector) bool { return reflect.DeepEqual(sel, w.Selector) }) {
						return nil, fault(422, "witness_pin_mismatch")
					}
					supported := !protected || w.Kind == "state_change"
					if diagram.Document.Kind == "business_map" && w.Kind != "business_event" {
						supported = false
					}
					if w.Kind == "replay_assertion" {
						supported = false
						if s.Replay != nil && rec.RunPins != nil {
							run, e := s.Replay.Get(ctx, pid, rec.RunPins.RunID)
							if e != nil {
								return nil, e
							}
							if run.Report != nil {
								h, _ := p.Hash("backend-replay-report-v1", run.Report)
								if h != rec.RunPins.ReportHash {
									return nil, fault(409, "report_pin_mismatch")
								}
								for _, b := range run.Report.Bindings {
									if b.Binding.Diagram == w.Pin && b.Binding.ElementID == w.Selector.ID && slices.Contains(b.AssertionIDs, w.AssertionID) && b.Status == "executed" {
										supported = true
									}
								}
							}
						}
					}
					if out.SourceCompatible && supported {
						drow = DiagramCorrelationRow{RecordID: rec.ID, Selectors: []bm.DiagramScopeSelector{w.Selector}, Basis: "explicit", Gaps: []string{}}
					}
				}
				out.DiagramRows = append(out.DiagramRows, drow)
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(overrides) != 0 {
		return nil, fault(422, "unknown_record")
	}
	out.Total = len(out.Rows)
	return s.Repo.saveCorrelation(ctx, pid, sid, hash, out)
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
