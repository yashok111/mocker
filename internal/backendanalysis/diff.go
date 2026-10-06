package backendanalysis

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// The facet separates changes to behavior from changes to its proof or desired
// criteria. Proof-only changes must never manufacture behavioral seeds.
type DiffChange struct {
	Object    ObjectAddress  `json:"object"`
	Facet     string         `json:"facet"`
	Operation string         `json:"operation"`
	Kind      string         `json:"kind"`
	Paths     []string       `json:"paths"`
	Before    jsontext.Value `json:"before"`
	After     jsontext.Value `json:"after"`
}

func effectiveComparisonState(g *backendmodel.EffectiveGraphSnapshot) backendmodel.RevisionState {
	state := g.State
	// State.Revision belongs to the baseline; comparison uses the desired pins.
	state.Revision.ArtifactPins = g.Pins.ArtifactPins
	state.ArtifactContext = g.Pins.ArtifactContext
	return state
}
func structuralChanges(ctx context.Context, before, after *backendmodel.EffectiveGraphSnapshot) ([]DiffChange, error) {
	delta, err := backendmodel.CompareRevisionStates(ctx, effectiveComparisonState(before), effectiveComparisonState(after))
	if err != nil {
		return nil, err
	}
	left, err := rawRecords(before)
	if err != nil {
		return nil, err
	}
	right, err := rawRecords(after)
	if err != nil {
		return nil, err
	}
	changes := []DiffChange{}
	for _, d := range delta.Changes {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		address := ObjectAddress{RecordType: d.RecordType, ID: d.ID}
		a, b := left[address], right[address]
		if a == nil && b == nil {
			raw, e := canonical(d)
			if e != nil {
				return nil, e
			}
			changes = append(changes, DiffChange{Object: address, Facet: "proof", Operation: "modified", Paths: d.ChangedPaths, After: raw})
			continue
		}
		additions, e := recordChanges(address, a, b)
		if e != nil {
			return nil, e
		}
		changes = append(changes, additions...)
	}
	desired, err := desiredChanges(before, after)
	if err != nil {
		return nil, err
	}
	changes = append(changes, desired...)
	sortChanges(changes)
	return changes, nil
}
func rawRecords(g *backendmodel.EffectiveGraphSnapshot) (map[ObjectAddress]jsontext.Value, error) {
	out := map[ObjectAddress]jsontext.Value{}
	add := func(kind, id string, v any) error {
		raw, err := canonical(v)
		out[ObjectAddress{RecordType: kind, ID: id}] = raw
		return err
	}
	for _, n := range g.State.Nodes {
		if err := add("node", n.ID, n); err != nil {
			return nil, err
		}
	}
	for _, e := range g.State.Edges {
		if err := add("edge", e.ID, e); err != nil {
			return nil, err
		}
	}
	for _, e := range g.State.Evidence {
		if err := add("evidence", e.ID, e); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func recordChanges(address ObjectAddress, a, b jsontext.Value) ([]DiffChange, error) {
	operation := "modified"
	if a == nil {
		operation = "added"
	}
	if b == nil {
		operation = "removed"
	}
	kindRaw := b
	if b == nil {
		kindRaw = a
	}
	var meta struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(kindRaw, &meta); err != nil {
		return nil, err
	}
	paths, err := rawPaths(a, b, "")
	if err != nil {
		return nil, err
	}
	grouped := map[string][]string{}
	for _, path := range paths {
		facet := "behavior"
		if proofPath(path) || address.RecordType == "evidence" {
			facet = "proof"
		}
		grouped[facet] = append(grouped[facet], path)
	}
	changes := make([]DiffChange, 0, len(grouped))
	for _, facet := range slices.Sorted(maps.Keys(grouped)) {
		changes = append(changes, DiffChange{Object: address, Facet: facet, Operation: operation, Kind: meta.Kind, Paths: grouped[facet], Before: a, After: b})
	}
	return changes, nil
}
func proofPath(path string) bool {
	for part := range strings.SplitSeq(path, "/") {
		if slices.Contains([]string{"evidenceIds", "freshness", "sourceSnapshotId", "analysisStatus", "gaps", "source", "facetComparison", "ownership"}, part) {
			return true
		}
	}
	return false
}
func pointerPart(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}
func rawPaths(a, b jsontext.Value, path string) ([]string, error) {
	if bytes.Equal(a, b) {
		return nil, nil
	}
	if len(a) > 0 && len(b) > 0 && a[0] == '{' && b[0] == '{' {
		var left, right map[string]jsontext.Value
		if err := json.Unmarshal(a, &left); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &right); err != nil {
			return nil, err
		}
		keys := map[string]bool{}
		for k := range left {
			keys[k] = true
		}
		for k := range right {
			keys[k] = true
		}
		var paths []string
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			child, err := rawPaths(left[key], right[key], path+"/"+pointerPart(key))
			if err != nil {
				return nil, err
			}
			paths = append(paths, child...)
		}
		return paths, nil
	}
	return []string{path}, nil
}
func desiredChanges(before, after *backendmodel.EffectiveGraphSnapshot) ([]DiffChange, error) {
	// These vectors carry complete authored provenance, including immutable base
	// refs. They are reported independently of source assertions.
	fields := []struct {
		key, facet string
		a, b       any
	}{{"criteria", "intent", before.Criteria, after.Criteria}, {"origins", "proof", before.Origins, after.Origins}, {"identities", "intent", before.Identities, after.Identities}, {"edgeNames", "intent", before.EdgeNames, after.EdgeNames}, {"artifactPins", "behavior", before.Pins.ArtifactPins, after.Pins.ArtifactPins}, {"artifactContext", "intent", before.Pins.ArtifactContext, after.Pins.ArtifactContext}}
	var out []DiffChange
	for _, field := range fields {
		a, err := canonical(field.a)
		if err != nil {
			return nil, err
		}
		b, err := canonical(field.b)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(a, b) {
			continue
		}
		out = append(out, DiffChange{Object: ObjectAddress{RecordType: "desired", ID: field.key}, Facet: field.facet, Operation: "modified", Kind: field.key, Paths: []string{"/" + field.key}, Before: a, After: b})
	}
	return out, nil
}
func sortChanges(changes []DiffChange) {
	slices.SortFunc(changes, func(a, b DiffChange) int {
		if n := strings.Compare(a.Object.RecordType, b.Object.RecordType); n != 0 {
			return n
		}
		if n := strings.Compare(a.Object.ID, b.Object.ID); n != 0 {
			return n
		}
		return strings.Compare(a.Facet, b.Facet)
	})
}

type analysisEngine struct {
	graphs    GraphReader
	artifacts ArtifactProjectionReader
}

func NewEngine(graphs GraphReader, artifacts ArtifactProjectionReader) Engine {
	return &analysisEngine{graphs: graphs, artifacts: artifacts}
}
func (e *analysisEngine) Analyze(ctx context.Context, in *ImmutableInput, emit func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	if in.V2 != nil {
		return e.analyzeB43(ctx, in, emit)
	}
	if in.Kind == "diagnostics" {
		return e.analyzeDiagnostics(ctx, in)
	}
	var target backendmodel.BackendReadTarget
	var preview *backendmodel.AnalysisCommandPreviewInput
	if in.To != nil {
		target = *in.To
	} else if f := in.CommandPreview; f != nil {
		preview = &backendmodel.AnalysisCommandPreviewInput{ChangeProposal: f.ChangeProposal, CandidateHash: f.CandidateHash, Preview: backendmodel.PreviewChangeProposalInput{ExpectedVersion: f.ExpectedVersion, ProposalRevisionID: f.ChangeProposal.ProposalRevisionID, Commands: f.Commands}}
	} else {
		return nil, malformed("Saved analysis has no exact target")
	}
	footprint, err := e.graphs.AnalysisPairInputFootprint(ctx, in.ProjectID, in.From.RevisionID, target, preview)
	if err != nil {
		return nil, err
	}
	leased, reservation, err := e.graphs.ReserveAnalysisInput(ctx, in.ProjectID, footprint)
	if err != nil {
		return nil, err
	}
	defer reservation.Release()
	before, err := e.graphs.ResolveEffectiveGraph(leased, in.ProjectID, in.From)
	if err != nil {
		return nil, err
	}
	var after *backendmodel.EffectiveGraphSnapshot
	if in.CommandPreview != nil {
		after, err = e.graphs.ResolveFrozenChangePreview(leased, in.ProjectID, in.CommandPreview)
	} else {
		after, err = e.graphs.ResolveEffectiveGraph(leased, in.ProjectID, *in.To)
	}
	if err != nil {
		return nil, err
	}
	for _, pair := range []struct {
		saved, actual backendmodel.EffectiveGraphPins
	}{{in.BeforePins, before.Pins}, {in.AfterPins, after.Pins}} {
		a, err := encodePins(pair.saved)
		if err != nil {
			return nil, err
		}
		b, err := encodePins(pair.actual)
		if err != nil {
			return nil, err
		}
		x, _ := canonical(a)
		y, _ := canonical(b)
		if !bytes.Equal(x, y) {
			return nil, fault(409, "input_conflict", "Resolved immutable effective pins differ")
		}
	}
	var request *backendmodel.EditorArtifactRequest
	if e.artifacts != nil {
		request = e.artifacts(leased)
	}
	return analyzeGraphs(leased, in, before, after, request, emit)
}

type reportBuilder struct {
	endpoint                         bool
	endpointObjects                  map[ObjectAddress]bool
	seeds                            map[ObjectAddress]bool
	paths                            map[string]Witness
	before, after                    *backendmodel.EffectiveGraphSnapshot
	emit                             func(PreparedSnapshot) error
	publishErr                       error
	publishedManifestBytes           int64
	chunks                           []ResultChunk
	flushed                          map[string]int
	services                         map[ObjectAddress]string
	manifestAddressBytes             int64
	input                            *ImmutableInput
	records                          map[string][]jsontext.Value
	progress                         Progress
	bytes                            int64
	gaps, truncations                map[string]Diagnostic
	changed, covered                 map[ObjectAddress]bool
	potential, unknown, incompatible bool
}

func newReport(in *ImmutableInput) *reportBuilder {
	return &reportBuilder{paths: map[string]Witness{}, flushed: map[string]int{}, input: in, records: map[string][]jsontext.Value{}, gaps: map[string]Diagnostic{}, truncations: map[string]Diagnostic{}, changed: map[ObjectAddress]bool{}, covered: map[ObjectAddress]bool{}}
}
func (r *reportBuilder) truncate(code string) {
	r.truncations[code] = Diagnostic{ID: code, Code: code, Message: "Analysis stopped at the configured limit"}
}
func (r *reportBuilder) gap(code string, object ObjectAddress) {
	r.unknown = true
	d, exists := r.gaps[code]
	if !exists {
		d = Diagnostic{ID: code, Code: code, Message: "Static analysis cannot confirm this dependency or check"}
	}
	if len(d.Objects) < 8 && !slices.Contains(d.Objects, object) {
		d.Objects = append(d.Objects, object)
	}
	r.gaps[code] = d
}
func (r *reportBuilder) trackChange(object ObjectAddress) bool {
	if r.changed[object] {
		return true
	}
	raw, _ := canonical(object)
	cost := int64(2*len(raw) + 2)
	if r.manifestAddressBytes+cost > maxManifestBytes/2 {
		r.truncate("manifest_byte_limit")
		return false
	}
	r.manifestAddressBytes += cost
	r.changed[object] = true
	return true
}

func (r *reportBuilder) add(section string, object ObjectAddress, kind, certainty string, depth int, detail any) bool {
	if r.progress.Records >= r.input.Limits.Records {
		r.truncate("record_limit")
		return false
	}
	if section == "findings" && r.progress.Findings >= r.input.Limits.Findings {
		r.truncate("finding_limit")
		return false
	}
	if filter := r.input.Scope.Certainty; filter != "" && section != "gaps" && section != "changes" && filter != certainty {
		r.gap("scope_omitted_certainty", object)
		return false
	}
	raw, err := canonical(detail)
	if err != nil {
		r.gap("record_encoding", object)
		return false
	}
	record := ResultRecord{Service: r.services[object], ID: digest(append([]byte(section+":"), raw...)), Object: object, Kind: kind, Certainty: certainty, Direction: r.input.Scope.Direction, Depth: depth, Detail: raw}
	encoded, err := canonical(record)
	if err != nil {
		r.gap("record_encoding", object)
		return false
	}
	if r.bytes+int64(len(encoded))+2 > r.input.Limits.ResultBytes-terminalHeadroom-r.publishedManifestBytes {
		r.truncate("result_byte_limit")
		return false
	}
	if r.publishErr != nil {
		return false
	}
	r.records[section] = append(r.records[section], encoded)
	r.bytes += int64(len(encoded)) + 1
	r.progress.Records++
	if section == "findings" {
		r.progress.Findings++
	}
	if r.emit != nil && r.progress.Records%2048 == 0 {
		r.publishErr = r.publishProgress()
	}
	return r.publishErr == nil
}
func (r *reportBuilder) selected(c DiffChange, before, after *backendmodel.EffectiveGraphSnapshot) bool {
	if !scopeObjectSelected(r.input.Scope, c.Object, c.Kind) {
		return false
	}
	if service := r.input.Scope.Service; service != "" {
		if !objectInService(before, c.Object, service) && !objectInService(after, c.Object, service) {
			return false
		}
	}
	return true
}
func inService(g *backendmodel.EffectiveGraphSnapshot, id, service string) bool {
	nodes := map[string]backendmodel.Node{}
	for _, n := range g.State.Nodes {
		nodes[n.ID] = n
	}
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		n, ok := nodes[id]
		if !ok {
			return false
		}
		if n.Kind == "service" && (n.ID == service || n.Name == service) {
			return true
		}
		if n.ParentID == nil {
			return false
		}
		id = *n.ParentID
	}
	return false
}
func analyzeGraphs(ctx context.Context, in *ImmutableInput, before, after *backendmodel.EffectiveGraphSnapshot, request *backendmodel.EditorArtifactRequest, emit func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	r := newReport(in)
	r.before, r.after, r.emit = before, after, emit
	r.services = objectServices(before, after)
	changes, err := structuralChanges(ctx, before, after)
	if err != nil {
		return nil, err
	}
	claims := &backendmodel.RevisionDelta{}
	if err = backendmodel.AppendAnalysisSourceClaimDeltas(ctx, claims, before.Source, after.Source); err != nil {
		return nil, err
	}
	for _, claim := range claims.Changes {
		raw, err := canonical(claim)
		if err != nil {
			return nil, err
		}
		changes = append(changes, DiffChange{Object: ObjectAddress{RecordType: claim.RecordType, ID: claim.ID}, Facet: "proof", Kind: "source_claim", Operation: "modified", Paths: claim.ChangedPaths, After: raw})
	}
	sortChanges(changes)
	seeds, err := r.addChanges(ctx, changes, before, after)
	if err != nil {
		return nil, err
	}

	r.seeds = seeds
	if in.Kind == "impact" {
		if err = r.traverse(ctx, before, after, sortedAddresses(seeds)); err != nil {
			return nil, err
		}
	}
	for _, check := range declaredChecks(after) {
		if check.Status == "violation" {
			r.incompatible = true
		}
		if check.Status == "unknown" {
			r.unknown = true
		}
		if check.Status == "check_required" {
			r.potential = true
		}
		r.add("checks", check.Object, "criterion", check.Certainty, 0, check)
	}
	if err = r.artifactChanges(ctx, before, after, request); err != nil {
		return nil, err
	}
	return r.finish(before, after, emit)
}
func diagnostics(m map[string]Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(m))
	for _, key := range slices.Sorted(maps.Keys(m)) {
		out = append(out, m[key])
	}
	return out
}
func (r *reportBuilder) finish(before, after *backendmodel.EffectiveGraphSnapshot, emit func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	if before.State.Revision.Coverage.Status != "complete" || after.State.Revision.Coverage.Status != "complete" {
		r.gap("source_coverage", ObjectAddress{RecordType: "source", ID: "coverage"})
	}
	for _, g := range diagnostics(r.gaps) {
		r.add("gaps", ObjectAddress{RecordType: "diagnostic", ID: g.ID}, "gap", "unknown", 0, g)
	}
	return r.complete(before, after)
}

func (r *reportBuilder) complete(before, after *backendmodel.EffectiveGraphSnapshot) (*TerminalSnapshot, error) {
	verdict := "compatible_within_scope"
	if r.potential {
		verdict = "potential"
	}
	if r.unknown || len(r.truncations) > 0 {
		verdict = "unknown"
	}
	if r.incompatible {
		verdict = "incompatible"
	}
	manifest := ResultManifest{Complete: len(r.truncations) == 0, ChangedIDs: sortedAddresses(r.changed), CoveredChangedIDs: sortedAddresses(r.covered), Gaps: diagnostics(r.gaps), TruncationReasons: diagnostics(r.truncations), SourceCoverageBefore: before.State.Revision.Coverage, SourceCoverageAfter: after.State.Revision.Coverage, Scope: r.input.Scope, RuleSetVersion: r.input.RuleSetVersion, TraversalVersion: r.input.TraversalVersion, Verdict: verdict}
	s, err := r.snapshot(manifest)
	if err != nil {
		return nil, err
	}
	if r.publishErr != nil {
		return nil, r.publishErr
	}

	return &TerminalSnapshot{Status: "completed", Snapshot: s}, nil
}

func (r *reportBuilder) addChanges(ctx context.Context, changes []DiffChange, before, after *backendmodel.EffectiveGraphSnapshot) (map[ObjectAddress]bool, error) {
	var err error
	seeds := map[ObjectAddress]bool{}
	for _, change := range changes {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !r.trackChange(change.Object) {
			continue
		}
		if !r.selected(change, before, after) {
			r.gap("scope_omitted_change", change.Object)
			continue
		}
		r.covered[change.Object] = true
		if !r.add("changes", change.Object, change.Kind, "confirmed", 0, change) {
			continue
		}
		if change.Facet != "behavior" {
			continue
		}
		r.addChangeRule(change, before, after)
		if change.Object.RecordType == "node" || change.Object.RecordType == "edge" {
			seeds[change.Object] = true
		}
	}
	return seeds, nil
}

func (r *reportBuilder) addChangeRule(change DiffChange, before, after *backendmodel.EffectiveGraphSnapshot) {
	seen := map[string]bool{}
	paths := change.Paths
	if len(paths) == 0 {
		paths = []string{""}
	}
	for _, path := range paths {
		part := change
		part.Paths = []string{path}
		rule := ruleFor(part)
		if seen[rule.RuleID] {
			continue
		}
		seen[rule.RuleID] = true
		r.recordRule(rule, change, before, after)
	}
}
func (r *reportBuilder) recordRule(rule RuleResult, change DiffChange, before, after *backendmodel.EffectiveGraphSnapshot) {
	for _, side := range []string{"before", "after"} {
		g := before
		if side == "after" {
			g = after
		}
		if change.Operation == "removed" && side == "after" || change.Operation == "added" && side == "before" {
			continue
		}
		p := recordProof(g, change.Object)
		rule.Evidence = append(rule.Evidence, proofReference(side, g, p))
		if (change.Object.RecordType == "node" || change.Object.RecordType == "edge") && !supported(p) {
			r.gap("unsupported_change_proof", change.Object)
		}
	}
	if rule.Status == "unknown" {
		r.unknown = true
	}
	if rule.Status == "potential" || rule.Status == "check_required" {
		r.potential = true
	}
	section := "findings"
	if rule.Status == "check_required" {
		section = "checks"
	}
	r.add(section, change.Object, change.Kind, rule.Certainty, 0, rule)
}

func objectServices(graphs ...*backendmodel.EffectiveGraphSnapshot) map[ObjectAddress]string {
	out := map[ObjectAddress]string{}
	for _, g := range graphs {
		nodes := map[string]backendmodel.Node{}
		for _, n := range g.State.Nodes {
			nodes[n.ID] = n
		}
		for _, n := range g.State.Nodes {
			id := n.ID
			seen := map[string]bool{}
			for id != "" && !seen[id] {
				seen[id] = true
				owner, ok := nodes[id]
				if !ok {
					break
				}
				if owner.Kind == "service" {
					out[ObjectAddress{RecordType: "node", ID: n.ID}] = owner.ID
					break
				}
				if owner.ParentID == nil {
					break
				}
				id = *owner.ParentID
			}
		}
		for _, e := range g.State.Edges {
			out[ObjectAddress{RecordType: "edge", ID: e.ID}] = out[ObjectAddress{RecordType: "node", ID: e.From}]
		}
	}
	return out
}

func (r *reportBuilder) snapshot(manifest ResultManifest) (PreparedSnapshot, error) {
	for _, section := range sections {
		items := r.records[section][r.flushed[section]:]
		for len(items) > 0 {
			count := min(128, len(items))
			raw, err := canonical(items[:count])
			if err != nil {
				return PreparedSnapshot{}, err
			}
			r.chunks = append(r.chunks, ResultChunk{Sequence: int64(len(r.chunks) + 1), Section: section, ItemsJSON: raw})
			r.flushed[section] += count
			items = items[count:]
		}
	}
	return PreparedSnapshot{Manifest: manifest, Progress: r.progress, Chunks: slices.Clone(r.chunks)}, nil
}
func (r *reportBuilder) publishProgress() error {
	manifest := ResultManifest{Complete: false, ChangedIDs: sortedAddresses(r.changed), CoveredChangedIDs: sortedAddresses(r.covered), Gaps: diagnostics(r.gaps), TruncationReasons: diagnostics(r.truncations), Scope: r.input.Scope, RuleSetVersion: r.input.RuleSetVersion, TraversalVersion: r.input.TraversalVersion, Verdict: "unknown", SourceCoverageBefore: r.before.State.Revision.Coverage, SourceCoverageAfter: r.after.State.Revision.Coverage}
	raw, err := canonical(manifest)
	if err != nil {
		return err
	}
	cost := int64(len(raw) + 4096)
	if r.bytes+r.publishedManifestBytes+cost > r.input.Limits.ResultBytes-terminalHeadroom {
		r.truncate("result_byte_limit")
		return nil
	}
	snapshot, err := r.snapshot(manifest)
	if err != nil {
		return err
	}
	if err = r.emit(snapshot); err != nil {
		return err
	}
	r.publishedManifestBytes += cost
	return nil
}

func objectInService(g *backendmodel.EffectiveGraphSnapshot, object ObjectAddress, service string) bool {
	if object.RecordType == "edge" {
		for _, edge := range g.State.Edges {
			if edge.ID == object.ID {
				return inService(g, edge.From, service) || inService(g, edge.To, service)
			}
		}
		return false
	}
	return inService(g, object.ID, service)
}

func scopeObjectSelected(scope Scope, object ObjectAddress, kind string) bool {
	return (len(scope.ChangedIDs) == 0 || slices.Contains(scope.ChangedIDs, object)) && (scope.Kind == "" || scope.Kind == kind)
}
