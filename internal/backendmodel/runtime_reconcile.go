package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

func runtimeReferences(kind string, attrs map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	refs := []relationalReference{}
	if !runtimeSubject(kind, edge) {
		return refs, nil
	}
	if err := validateRuntimeAttributes(kind, attrs, edge, persisted); err != nil {
		return nil, err
	}
	add := func(path, typ, value string) {
		r := relationalReference{Path: "/attributes/" + path, Kind: typ, Key: value}
		if persisted {
			r.Key, r.ID = "", value
		}
		refs = append(refs, r)
	}
	switch kind {
	case "flow":
		key := runtimeReferenceName("entryStepKey", persisted)
		add(key, "flow_step", runtimeString(attrs[key]))
		key = map[bool]string{false: "exitStepKeys", true: "exitStepIds"}[persisted]
		values, _ := relationalStrings(attrs[key], MaxRuntimeExitReferences, persisted)
		for i, value := range values {
			add(fmt.Sprintf("%s/%d", key, i), "flow_step", value)
		}
	case "flow_step":
		m, _ := relationalObject(attrs["transactionContext"])
		if runtimeString(m["status"]) == "known" {
			key := runtimeReferenceName("transactionKey", persisted)
			add("transactionContext/"+key, "transaction", runtimeString(m[key]))
		}
	case "transaction", "reads", "writes", "deletes":
		key := runtimeReferenceName("datastoreKey", persisted)
		add(key, "datastore", runtimeString(attrs[key]))
	}
	return refs, nil
}

func sourceAttributeReferences(kind string, attrs map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	if representationSubject(kind, edge) || representationMapping(kind, attrs, edge) {
		return representationReferences(kind, attrs, edge, persisted)
	}
	if eventsSubject(kind, edge) || eventsEmit(kind, attrs, edge) {
		return eventsReferences(kind, attrs, edge, persisted)
	}
	if contextualLineageMapping(kind, attrs, edge) {
		return eventsLineageReferences(kind, attrs, edge, persisted)
	}
	if lineageSubject(kind, edge) {
		return lineageReferences(kind, attrs, edge, persisted)
	}
	if runtimeSubject(kind, edge) {
		return runtimeReferences(kind, attrs, edge, persisted)
	}
	return relationalReferences(kind, attrs, edge, persisted)
}

func sourceActiveReferenceTo(kind string, attrs map[string]jsontext.Value, edge bool, id string) bool {
	return sourceActiveRecordReferenceTo(kind, attrs, edge, "node", id)
}
func sourceActiveRecordReferenceTo(kind string, attrs map[string]jsontext.Value, edge bool, recordType, id string) bool {
	refs, err := sourceAttributeReferences(kind, attrs, edge, true)
	if err != nil {
		return eventsSubject(kind, edge) || eventsEmit(kind, attrs, edge) || lineageSubject(kind, edge) || runtimeSubject(kind, edge) || relationalSubject(kind, attrs, edge)
	}
	return activeRecordReferenceTo(refs, recordType, id)
}
func activeRecordReferenceTo(refs []relationalReference, recordType, id string) bool {
	return slices.ContainsFunc(refs, func(ref relationalReference) bool {
		typ := ref.RecordType
		if typ == "" {
			typ = "node"
		}
		return ref.Kind != "evidence" && ref.HistoricalRevisionID == "" && typ == recordType && ref.ID == id
	})
}

func resolveRuntimeAttributes(kind string, attrs map[string]jsontext.Value, edge bool, resolve func(string, string, string) string) (map[string]jsontext.Value, error) {
	if !runtimeSubject(kind, edge) {
		return attrs, nil
	}
	refs, err := runtimeReferences(kind, attrs, edge, false)
	if err != nil {
		return nil, err
	}
	values := map[string]jsontext.Value{}
	for _, ref := range refs {
		values[ref.Path], err = json.Marshal(resolve("node", ref.Key, ref.Path))
		if err != nil {
			return nil, err
		}
	}
	var walk func(jsontext.Value, string) (jsontext.Value, error)
	walk = func(raw jsontext.Value, path string) (jsontext.Value, error) {
		if value, ok := values[path]; ok {
			return value, nil
		}
		switch bytesPrefix := strings.TrimSpace(string(raw)); {
		case strings.HasPrefix(bytesPrefix, "{"):
			m, err := relationalObject(raw)
			if err != nil {
				return nil, err
			}
			out := map[string]jsontext.Value{}
			for key, value := range m {
				converted, err := walk(value, path+"/"+key)
				if err != nil {
					return nil, err
				}
				target := key
				if slices.Contains([]string{"entryStepKey", "exitStepKeys", "datastoreKey", "transactionKey"}, key) {
					if strings.HasSuffix(key, "Keys") {
						target = strings.TrimSuffix(key, "Keys") + "Ids"
					} else {
						target = runtimeReferenceName(key, true)
					}
				}
				out[target] = converted
			}
			return json.Marshal(out)
		case strings.HasPrefix(bytesPrefix, "["):
			var a []jsontext.Value
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			for i := range a {
				a[i], err = walk(a[i], fmt.Sprintf("%s/%d", path, i))
				if err != nil {
					return nil, err
				}
			}
			return json.Marshal(a)
		default:
			return raw, nil
		}
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	raw, err = walk(raw, "/attributes")
	if err != nil {
		return nil, err
	}
	return relationalObject(raw)
}

func runtimeContains(from, to Node) (bool, bool) {
	switch to.Kind {
	case "flow":
		return from.Kind == "handler" || from.Kind == "symbol", true
	case "flow_step", "transaction":
		return from.Kind == "flow", true
	case "query":
		return slices.Contains([]string{"module", "handler", "symbol"}, from.Kind), true
	default:
		return false, false
	}
}

func runtimeEndpoints(e Edge, from, to Node) (bool, bool) {
	switch e.Kind {
	case "contains":
		return runtimeContains(from, to)
	case "calls":
		if from.Kind == "flow_step" {
			step := runtimeString(from.Attributes["stepKind"])
			return (step == "call" || step == "query") && slices.Contains([]string{"symbol", "handler", "external_system", "query", "unresolved_target"}, to.Kind), true
		}
	case "next", "branch", "error", "returns":
		return from.Kind == "flow_step" && to.Kind == "flow_step", true
	case "reads", "writes", "deletes":
		return from.Kind == "query" && slices.Contains([]string{"table", "column", "view", "unresolved_target"}, to.Kind), true
	case "begins", "commits", "rolls_back":
		return from.Kind == "flow_step" && to.Kind == "transaction", true
	}
	return false, false
}

func decodeRuntimeAttributes(attrs map[string]jsontext.Value) runtimeAttributes {
	var out runtimeAttributes
	raw, _ := json.Marshal(attrs)
	_ = json.Unmarshal(raw, &out)
	return out
}

func validateRuntimeGraph(ctx context.Context, _ importReader, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	return validateRuntimeGraphRules(ctx, selectedProfile(s.Profile), s, g, diagnostics)
}

// A nil admission session selects the shared structural rules only.
func validateRuntimeGraphRules(ctx context.Context, profile string, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	r := newRuntimeRules(profile, s, g, diagnostics)
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.check(n.ID, n.Kind, n.Attributes, false, n.EvidenceIDs, n.Freshness)
		if runtimeSubject(n.Kind, false) {
			r.checkNode(n)
		}
	}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.check(e.ID, e.Kind, e.Attributes, true, e.EvidenceIDs, e.Freshness)
		r.checkEdge(e)
	}
	return nil
}

// runtimeRules is the state one validateRuntimeGraphRules pass shares across
// its checks: the candidate indexed by node, by edge direction and by
// containment, plus the owner-to-flow registry that makes a second source
// flow under one owner visible.
type runtimeRules struct {
	profile     string
	s           *ImportSession
	g           *graphCandidate
	diagnostics *[]ImportDiagnostic
	nodes       map[string]Node
	attributes  map[string]runtimeAttributes
	proofs      map[string]Evidence
	children    map[string][]string
	outgoing    map[string][]Edge
	incoming    map[string][]Edge
	contains    map[string][]Edge
	owners      map[string]string
}

func newRuntimeRules(profile string, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) *runtimeRules {
	r := &runtimeRules{
		profile: profile, s: s, g: g, diagnostics: diagnostics,
		nodes: map[string]Node{}, attributes: map[string]runtimeAttributes{}, proofs: map[string]Evidence{},
		children: map[string][]string{}, outgoing: map[string][]Edge{}, incoming: map[string][]Edge{},
		contains: map[string][]Edge{}, owners: map[string]string{},
	}
	for _, n := range g.Nodes {
		r.nodes[n.ID] = n
		r.attributes[n.ID] = decodeRuntimeAttributes(n.Attributes)
		if n.ParentID != nil {
			r.children[*n.ParentID] = append(r.children[*n.ParentID], n.ID)
		}
	}
	for _, e := range g.Evidence {
		r.proofs[e.ID] = e
	}
	for _, e := range g.Edges {
		r.outgoing[e.From] = append(r.outgoing[e.From], e)
		r.incoming[e.To] = append(r.incoming[e.To], e)
		if e.Kind == "contains" {
			r.contains[e.To] = append(r.contains[e.To], e)
		}
	}
	return r
}

func (r *runtimeRules) add(path, message string) {
	*r.diagnostics = append(*r.diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
}

func (r *runtimeRules) datastore(id string) string {
	seen := map[string]bool{}
	for !seen[id] {
		seen[id] = true
		n, ok := r.nodes[id]
		if !ok {
			return ""
		}
		if n.Kind == "datastore" {
			return id
		}
		if n.ParentID == nil {
			return ""
		}
		id = *n.ParentID
	}
	return ""
}

// boundedProof requires subject id to cite at least one proof that is its
// own, line-bounded and found in an analyzed file of a candidate snapshot.
func (r *runtimeRules) boundedProof(id string, ids []string, fresh *AssertionFreshness) {
	if r.s == nil {
		return
	}
	if !slices.ContainsFunc(ids, func(eid string) bool { return r.memberProof(id, eid, fresh) }) {
		r.add("subjects/"+id+"/evidenceIds", "Runtime source assertions require a member proof in an analyzed snapshot file with physical line bounds")
	}
}

func (r *runtimeRules) memberProof(id, eid string, fresh *AssertionFreshness) bool {
	e, ok := r.proofs[eid]
	if !ok || e.SubjectID != id || e.Source.RepositoryID != r.s.RepositoryID || e.Source.StartLine == nil || e.Source.EndLine == nil || *e.Source.StartLine <= 0 || *e.Source.EndLine < *e.Source.StartLine {
		return false
	}
	if fresh != nil && fresh.Status == "current" && e.Source.SnapshotID != r.s.SnapshotID {
		return false
	}
	return r.analyzedSourceFile(e)
}

// analyzedSourceFile reports whether the proof's file, at its content hash,
// was analyzed in the snapshot the proof names.
func (r *runtimeRules) analyzedSourceFile(e Evidence) bool {
	for _, snapshot := range r.g.Sources {
		if snapshot.ID != e.Source.SnapshotID || snapshot.RepositoryID != e.Source.RepositoryID {
			continue
		}
		for _, file := range snapshot.Files {
			if file.Path == e.Source.File && file.ContentHash == e.Source.ContentHash && file.AnalysisStatus == "analyzed" {
				return true
			}
		}
	}
	return false
}

func (r *runtimeRules) check(id, kind string, attrs map[string]jsontext.Value, edge bool, ids []string, fresh *AssertionFreshness) {
	if !runtimeSubject(kind, edge) {
		return
	}
	path := "nodes/" + id
	if edge {
		path = "edges/" + id
	}
	validator := validateRuntimeAttributes
	if r.profile == EventsProfile {
		validator = validateEventsAttributes
	}
	if err := validator(kind, attrs, edge, true); err != nil {
		r.add(path, err.Error())
		return
	}
	r.boundedProof(id, ids, fresh)
	refs, err := sourceAttributeReferences(kind, attrs, edge, true)
	if err != nil {
		r.add(path, err.Error())
		return
	}
	for _, ref := range refs {
		target, ok := r.nodes[ref.ID]
		if !ok || target.Kind != ref.Kind || r.s != nil && (target.Ownership == nil || target.Ownership.RepositoryID != r.s.RepositoryID) {
			r.add(path+ref.Path, "Nested reference must survive with its declared kind in the same repository")
		}
	}
}

// checkNode validates a runtime node's place in the structure, then the
// rules of its kind.
func (r *runtimeRules) checkNode(n Node) {
	a := r.attributes[n.ID]
	path := "nodes/" + n.ID
	if n.ParentID == nil || len(r.contains[n.ID]) != 1 {
		r.add(path+"/parentId", "Runtime nodes require exactly one parent and contains edge")
		return
	}
	parent := r.nodes[*n.ParentID]
	if valid, _ := runtimeContains(parent, n); !valid {
		r.add(path+"/parentId", "Runtime parent kind is invalid")
	}
	switch n.Kind {
	case "flow":
		r.checkFlow(n, a, path)
	case "flow_step":
		r.checkFlowStep(n, a, path)
	case "transaction":
		r.checkTransaction(n, a, path)
	case "query":
		r.checkQuery(n, a, path)
	}
}

func (r *runtimeRules) terminal(id string) bool {
	return !slices.ContainsFunc(r.outgoing[id], func(e Edge) bool { return runtimeControlKind(e.Kind) })
}

func (r *runtimeRules) checkFlow(n Node, a runtimeAttributes, path string) {
	if prior := r.owners[*n.ParentID]; prior != "" && prior != n.ID {
		r.add(path, "An owner has at most one source flow")
	}
	r.owners[*n.ParentID] = n.ID
	entry := r.nodes[a.EntryStepID]
	if entry.Kind != "flow_step" || entry.ParentID == nil || *entry.ParentID != n.ID {
		r.add(path+"/attributes/entryStepId", "Entry must be a direct step in this flow")
	}
	exits := map[string]bool{}
	for _, id := range a.ExitStepIDs {
		exits[id] = true
		step := r.nodes[id]
		if step.Kind != "flow_step" || step.ParentID == nil || *step.ParentID != n.ID {
			r.add(path+"/attributes/exitStepIds", "Exits must be direct steps in this flow")
		}
		if !r.terminal(id) {
			r.add(path+"/attributes/exitStepIds", "Every declared exit must be terminal")
		}
	}
	for _, id := range r.children[n.ID] {
		if r.nodes[id].Kind == "flow_step" {
			r.checkFlowChild(a, path, id, exits)
		}
	}
	if a.AnalysisStatus == "complete" && a.ExitStatus != "complete" {
		r.add(path, "Complete flow requires complete exit inventory")
	}
}

// checkFlowChild holds a flow's completeness claims against one direct step.
func (r *runtimeRules) checkFlowChild(a runtimeAttributes, path, id string, exits map[string]bool) {
	if a.ExitStatus == "complete" && r.terminal(id) && !exits[id] {
		r.add(path+"/attributes/exitStepIds", "Complete exits must list every terminal step")
	}
	if a.AnalysisStatus != "complete" {
		return
	}
	if r.attributes[id].AnalysisStatus != "complete" {
		r.add(path, "Complete flow requires complete direct steps")
	}
	for _, e := range r.outgoing[id] {
		if e.Kind == "calls" && r.nodes[e.To].Kind == "unresolved_target" {
			r.add(path, "Complete flow cannot hide unresolved direct call/query remainder")
		}
	}
}

func (r *runtimeRules) checkFlowStep(n Node, a runtimeAttributes, path string) {
	calls := []Edge{}
	boundaries := []Edge{}
	for _, e := range r.outgoing[n.ID] {
		if e.Kind == "calls" {
			calls = append(calls, e)
		}
		if runtimeBoundaryKind(e.Kind) {
			boundaries = append(boundaries, e)
		}
	}
	if a.TransactionContext.Status == "known" {
		tx := r.nodes[a.TransactionContext.TransactionID]
		if tx.Kind != "transaction" || tx.ParentID == nil || *tx.ParentID != *n.ParentID {
			r.add(path+"/attributes/transactionContext", "Known transaction context must reference a transaction in this flow")
		}
	}
	if a.StepKind == "query" {
		if len(calls) != 1 || !slices.Contains([]string{"query", "unresolved_target"}, r.nodes[callsFirst(calls)].Kind) {
			r.add(path, "Query steps require exactly one query or unresolved call target")
		}
	}
	if a.StepKind == "call" {
		r.checkCallStep(a, path, calls)
	}
	r.checkStepBoundaries(a, path, boundaries)
}

func (r *runtimeRules) checkCallStep(a runtimeAttributes, path string, calls []Edge) {
	if len(calls) == 0 || len(calls) > MaxRuntimeCallCandidates {
		r.add(path, "Call steps require one to 500 candidates")
	}
	unresolved := slices.ContainsFunc(calls, func(e Edge) bool { return r.nodes[e.To].Kind == "unresolved_target" })
	if a.DispatchStatus != "complete" && !unresolved || a.DispatchStatus == "complete" && unresolved {
		r.add(path, "Dispatch status must disclose an explicit unresolved remainder")
	}
}

// checkStepBoundaries pairs a transaction boundary step kind with exactly one
// matching boundary edge; any other step kind carries none.
func (r *runtimeRules) checkStepBoundaries(a runtimeAttributes, path string, boundaries []Edge) {
	expected := map[string]string{"transaction_begin": "begins", "transaction_commit": "commits", "transaction_rollback": "rolls_back"}[a.StepKind]
	if expected != "" {
		if len(boundaries) != 1 || boundaries[0].Kind != expected || a.TransactionContext.Status != "known" || boundaries[0].To != a.TransactionContext.TransactionID {
			r.add(path, "Transaction boundary steps require one matching edge and agreeing known context")
		}
	} else if len(boundaries) > 0 {
		r.add(path, "Only matching transaction boundary step kinds may carry boundary edges")
	}
}

func (r *runtimeRules) checkTransaction(n Node, a runtimeAttributes, path string) {
	if a.BoundaryStatus != "complete" {
		return
	}
	begin, end := false, false
	for _, e := range r.incoming[n.ID] {
		if e.To == n.ID {
			begin = begin || e.Kind == "begins"
			end = end || e.Kind == "commits" || e.Kind == "rolls_back"
		}
	}
	if !begin || !end {
		r.add(path, "Complete transaction boundaries require at least one begin and one commit/rollback")
	}
}

func (r *runtimeRules) checkQuery(n Node, a runtimeAttributes, path string) {
	if a.ColumnScope != "complete" {
		return
	}
	for _, e := range r.outgoing[n.ID] {
		if runtimeAccessEdgeKind(e.Kind) && r.nodes[e.To].Kind != "column" || e.Kind == "calls" && r.nodes[e.To].Kind == "unresolved_target" {
			r.add(path, "Complete query column inventory requires known columns and no unresolved remainder")
		}
	}
}

// checkEdge validates the rules every edge kind carries between runtime
// subjects: proofs, unresolved call remainders, flow locality and access.
func (r *runtimeRules) checkEdge(e Edge) {
	from, to := r.nodes[e.From], r.nodes[e.To]
	a := decodeRuntimeAttributes(e.Attributes)
	path := "edges/" + e.ID
	if !runtimeSubject(e.Kind, true) && (runtimeSubject(from.Kind, false) || runtimeSubject(to.Kind, false)) {
		r.boundedProof(e.ID, e.EvidenceIDs, e.Freshness)
	}
	if e.Kind == "calls" && from.Kind == "flow_step" && to.Kind == "unresolved_target" {
		r.checkUnresolvedCall(from, to, path)
	}
	if runtimeControlKind(e.Kind) || runtimeBoundaryKind(e.Kind) {
		if from.ParentID == nil || to.ParentID == nil || *from.ParentID != *to.ParentID {
			r.add(path, "Control and transaction boundaries must stay in one flow")
		}
	}
	if runtimeAccessEdgeKind(e.Kind) {
		r.checkAccess(to, a, path)
	}
}

func (r *runtimeRules) checkUnresolvedCall(from, to Node, path string) {
	expected := runtimeString(to.Attributes["expectedKind"])
	valid := slices.Contains([]string{"symbol", "handler", "external_system", "query"}, expected) || r.profile == EventsProfile && expected == "http_operation"
	if r.attributes[from.ID].StepKind == "query" {
		valid = expected == "query"
	}
	if !valid {
		r.add(path, "Unresolved call remainder must declare a compatible expected kind")
	}
}

// checkAccess requires a read/write/delete edge to select an existing
// relational datastore facet, and a target and column scope that agree with it.
func (r *runtimeRules) checkAccess(to Node, a runtimeAttributes, path string) {
	if to.Kind == "unresolved_target" && !slices.Contains([]string{"table", "column", "view"}, runtimeString(to.Attributes["expectedKind"])) {
		r.add(path, "Unresolved access must declare a table, column or view target")
	}
	ds := r.nodes[a.DatastoreID]
	fs, _, err := relationalFacetObject("datastore", ds.Attributes)
	if ds.Kind != "datastore" || err != nil || fs[a.FacetKey] == nil {
		r.add(path, "Access must select an existing relational datastore facet")
	}
	if to.Kind == "column" {
		if a.ColumnScope != "listed" {
			r.add(path, "Concrete column access uses listed scope")
		}
	} else if a.ColumnScope != "unknown" {
		r.add(path, "Table, view and unresolved access requires unknown column scope")
	}
	if to.Kind != "unresolved_target" {
		targetFacets, _, err := relationalFacetObject(to.Kind, to.Attributes)
		if r.datastore(to.ID) != a.DatastoreID || err != nil || targetFacets[a.FacetKey] == nil {
			r.add(path, "Access target must belong to selected datastore and facet")
		}
	}
}

func runtimeControlKind(kind string) bool {
	return slices.Contains([]string{"next", "branch", "error", "returns"}, kind)
}
func runtimeAccessEdgeKind(kind string) bool {
	return slices.Contains([]string{"reads", "writes", "deletes"}, kind)
}
func runtimeBoundaryKind(kind string) bool {
	return slices.Contains([]string{"begins", "commits", "rolls_back"}, kind)
}
func callsFirst(calls []Edge) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].To
}
