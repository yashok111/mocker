package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

func replaceRelationalFacets(kind string, attrs map[string]jsontext.Value, facets map[string]jsontext.Value) map[string]jsontext.Value {
	out := maps.Clone(attrs)
	raw, _ := json.Marshal(facets)
	descriptor := ""
	if kind == "datastore" {
		descriptor = "relational"
	}
	if kind == "symbol" {
		descriptor = "databaseRoutine"
	}
	if descriptor == "" {
		out["facets"] = raw
	} else {
		out[descriptor], _ = json.Marshal(map[string]jsontext.Value{"facets": raw})
	}
	return out
}
func resolveRelationalAttributes(kind string, a map[string]jsontext.Value, edge bool, s *ImportSession, resolve func(string, string, string) string) (map[string]jsontext.Value, error) {
	if !relationalSubject(kind, a, edge) {
		return a, nil
	}
	fs, path, err := relationalFacetObject(kind, a)
	if err != nil {
		return nil, err
	}
	var walk func(jsontext.Value, string) (jsontext.Value, error)
	walk = func(raw jsontext.Value, path string) (jsontext.Value, error) {
		if len(raw) == 0 {
			return raw, nil
		}
		switch raw[0] {
		case '{':
			m, err := relationalObject(raw)
			if err != nil {
				return nil, err
			}
			out := map[string]jsontext.Value{}
			for k, v := range m {
				target, typ := relationalKeyTarget(k)
				if target != k {
					out[target] = resolveRelationalKeys(v, typ, path+"/"+target, resolve)
				} else {
					out[k], err = walk(v, path+"/"+k)
					if err != nil {
						return nil, err
					}
				}
			}
			return json.Marshal(out)
		case '[':
			var values []jsontext.Value
			if err := json.Unmarshal(raw, &values); err != nil {
				return nil, err
			}
			for i := range values {
				var err error
				values[i], err = walk(values[i], fmt.Sprintf("%s/%d", path, i))
				if err != nil {
					return nil, err
				}
			}
			return json.Marshal(values)
		}
		return raw, nil
	}
	for key, raw := range fs {
		converted, err := walk(raw, path+"/"+escapeRelationalPointer(key))
		if err != nil {
			return nil, err
		}
		m, err := relationalObject(converted)
		if err != nil {
			return nil, err
		}
		m["sourceSnapshotId"], _ = json.Marshal(s.SnapshotID)
		m["freshness"], _ = json.Marshal(&AssertionFreshness{Status: "current", ConfirmedSnapshotID: s.SnapshotID, Reasons: []string{}})
		fs[key], err = json.Marshal(m)
		if err != nil {
			return nil, err
		}
	}
	return replaceRelationalFacets(kind, a, fs), nil
}
func staleRelationalAttributes(kind string, attrs map[string]jsontext.Value) map[string]jsontext.Value {
	fs, _, err := relationalFacetObject(kind, attrs)
	if err != nil {
		return attrs
	}
	for key, raw := range fs {
		m, err := relationalObject(raw)
		if err != nil {
			continue
		}
		var f AssertionFreshness
		if json.Unmarshal(m["freshness"], &f) != nil {
			continue
		}
		f.Status = "stale"
		f.Reasons = append(f.Reasons, "not_reobserved")
		slices.Sort(f.Reasons)
		f.Reasons = slices.Compact(f.Reasons)
		m["freshness"], _ = json.Marshal(f)
		fs[key], _ = json.Marshal(m)
	}
	return replaceRelationalFacets(kind, attrs, fs)
}
func mergeRelationalFacets(kind string, old, current map[string]jsontext.Value, retainedProofs map[string]bool) (map[string]jsontext.Value, []string, bool) {
	oldFacets, _, err := relationalFacetObject(kind, old)
	if err != nil {
		return current, nil, false
	}
	newFacets, _, err := relationalFacetObject(kind, current)
	if err != nil {
		descriptor := ""
		switch kind {
		case "datastore":
			descriptor = "relational"
		case "symbol":
			descriptor = "databaseRoutine"
		}
		if _, provided := current[descriptor]; descriptor == "" || provided {
			return current, nil, false
		}
		// Omission of an optional descriptor does not retract committed facets.
		newFacets = map[string]jsontext.Value{}
	}
	stale := staleRelationalAttributes(kind, old)
	staleFacets, _, _ := relationalFacetObject(kind, stale)
	retained := false
	proofs := []string{}
	for key, raw := range oldFacets {
		if _, ok := newFacets[key]; ok {
			continue
		}
		retained = true
		newFacets[key] = staleFacets[key]
		var f relationalFacet
		if json.Unmarshal(raw, &f) == nil {
			for _, id := range f.EvidenceIDs {
				proofs = append(proofs, id)
				retainedProofs[id] = true
			}
		}
	}
	return replaceRelationalFacets(kind, current, newFacets), proofs, retained
}
func overlayRelationalFacets(base *RevisionState, g *graphCandidate, submitted map[string]bool, diagnostics *[]ImportDiagnostic) {
	proofs := map[string]bool{}
	baseNodes := map[string]Node{}
	baseEdges := map[string]Edge{}
	for _, n := range base.Nodes {
		baseNodes[n.ID] = n
	}
	for _, e := range base.Edges {
		baseEdges[e.ID] = e
	}
	union := relationalEvidenceUnion
	for i := range g.Nodes {
		n := &g.Nodes[i]
		old, exists := baseNodes[n.ID]
		if !relationalSubject(n.Kind, n.Attributes, false) && (!exists || old.Kind != n.Kind || !relationalSubject(old.Kind, old.Attributes, false)) {
			continue
		}
		if !submitted["node\x00"+n.ID] {
			n.Attributes = staleRelationalAttributes(n.Kind, n.Attributes)
			markRelationalFacetProofs(n.Kind, n.Attributes, proofs)
			continue
		}
		if exists && old.Kind == n.Kind {
			var added []string
			n.Attributes, added, _ = mergeRelationalFacets(n.Kind, old.Attributes, n.Attributes, proofs)
			n.EvidenceIDs = union(n.EvidenceIDs, added)
		}
	}
	for i := range g.Edges {
		e := &g.Edges[i]
		if !relationalSubject(e.Kind, e.Attributes, true) {
			continue
		}
		if !submitted["edge\x00"+e.ID] {
			e.Attributes = staleRelationalAttributes(e.Kind, e.Attributes)
			markRelationalFacetProofs(e.Kind, e.Attributes, proofs)
			continue
		}
		if old, ok := baseEdges[e.ID]; ok {
			var added []string
			var retained bool
			e.Attributes, added, retained = mergeRelationalFacets(e.Kind, old.Attributes, e.Attributes, proofs)
			e.EvidenceIDs = union(e.EvidenceIDs, added)
			if retained && (old.From != e.From || old.To != e.To) {
				*diagnostics = append(*diagnostics, ImportDiagnostic{Code: "backend_facet_endpoint_conflict", Path: "edges/" + e.ID, Message: "Retained facets prevent shared endpoint retargeting"})
			}
		}
	}
	retainRelationalFacetProofs(base, g, proofs, diagnostics)
}

// relationalEvidenceUnion adds a merge's retained proof IDs to a subject's
// own, sorted and without duplicates.
func relationalEvidenceUnion(ids, added []string) []string {
	out := slices.Clone(ids)
	for _, id := range added {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// markRelationalFacetProofs records every proof a stale subject's facets
// still cite, so the evidence it rests on is carried forward.
func markRelationalFacetProofs(kind string, attrs map[string]jsontext.Value, proofs map[string]bool) {
	fs, _, _ := relationalFacetObject(kind, attrs)
	for _, raw := range fs {
		var f relationalFacet
		_ = json.Unmarshal(raw, &f)
		for _, id := range f.EvidenceIDs {
			proofs[id] = true
		}
	}
}

// retainRelationalFacetProofs carries a retained facet's base evidence into
// the candidate as stale, and refuses a resubmission that rewrites it.
func retainRelationalFacetProofs(base *RevisionState, g *graphCandidate, proofs map[string]bool, diagnostics *[]ImportDiagnostic) {
	present := map[string]int{}
	for i, e := range g.Evidence {
		present[e.ID] = i
	}
	for _, old := range base.Evidence {
		if !proofs[old.ID] {
			continue
		}
		if i, ok := present[old.ID]; ok {
			before, after := old, g.Evidence[i]
			before.Ownership, before.Freshness = nil, nil
			after.Ownership, after.Freshness = nil, nil
			ob, _ := canonicalJSON(before)
			ab, _ := canonicalJSON(after)
			if string(ob) != string(ab) {
				*diagnostics = append(*diagnostics, ImportDiagnostic{Code: "backend_facet_evidence_conflict", Path: "evidence/" + old.ID, Message: "Retained facet proof cannot be overwritten"})
			}
		} else {
			old.Freshness = &AssertionFreshness{Status: "stale", ConfirmedSnapshotID: old.Source.SnapshotID, Reasons: []string{"not_reobserved"}}
			g.Evidence = append(g.Evidence, old)
		}
	}
}

// relationalKeyTarget maps a provider-key field of a facet to the ID field it
// becomes once keys are resolved, and the record type the key names. A field
// that is not a key maps to itself.
func relationalKeyTarget(k string) (target, typ string) {
	target, typ = k, "node"
	switch k {
	case "evidenceKeys":
		target, typ = "evidenceIds", "evidence"
	case "columnKeys":
		target = "columnIds"
	case "dependencyKeys":
		target = "dependencyIds"
	case "parentKeys":
		target = "parentIds"
	case "columnKey":
		target = "columnId"
	case "fromColumnKey":
		target = "fromColumnId"
	case "toColumnKey":
		target = "toColumnId"
	case "objectKey":
		target = "objectId"
	}
	return target, typ
}

// resolveRelationalKeys resolves one key or an array of keys to IDs; path is
// the target field's pointer, and each array element gets its own index.
func resolveRelationalKeys(v jsontext.Value, typ, path string, resolve func(string, string, string) string) jsontext.Value {
	if v[0] == '[' {
		var keys []string
		_ = json.Unmarshal(v, &keys)
		ids := make([]string, 0, len(keys))
		for i, key := range keys {
			ids = append(ids, resolve(typ, key, fmt.Sprintf("%s/%d", path, i)))
		}
		out, _ := json.Marshal(ids)
		return out
	}
	var key string
	_ = json.Unmarshal(v, &key)
	out, _ := json.Marshal(resolve(typ, key, path))
	return out
}
func assignRelationalOwnership(base *RevisionState, g *graphCandidate, s *ImportSession) {
	old := map[string]*AssertionOwnership{}
	current := map[string]*AssertionOwnership{}
	nodes := map[string]Node{}
	for _, n := range base.Nodes {
		old[n.ID] = n.Ownership
	}
	for _, e := range base.Edges {
		old[e.ID] = e.Ownership
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		n.Ownership = relationalOwnership(n.Kind, n.Attributes, false, s, old[n.ID])
		current[n.ID] = n.Ownership
		nodes[n.ID] = *n
	}
	for i := range g.Edges {
		e := &g.Edges[i]
		e.Ownership = relationalOwnership(e.Kind, e.Attributes, true, s, old[e.ID])
		if hasRuntimeProfile(selectedProfile(s.Profile)) && old[e.ID] == nil && (runtimeSubject(nodes[e.From].Kind, false) || runtimeSubject(nodes[e.To].Kind, false)) {
			e.Ownership.Profile = RuntimeProfile
		}
		if hasLineageProfile(selectedProfile(s.Profile)) && old[e.ID] == nil && (lineageSubject(nodes[e.From].Kind, false) || lineageSubject(nodes[e.To].Kind, false)) {
			e.Ownership.Profile = LineageProfile
		}
		if selectedProfile(s.Profile) == EventsProfile && eventsRelation(e.Kind, nodes[e.From], nodes[e.To]) {
			e.Ownership.Profile = EventsProfile
		}
		current[e.ID] = e.Ownership
	}
	for i := range g.Evidence {
		if owner := current[g.Evidence[i].SubjectID]; owner != nil {
			g.Evidence[i].Ownership = owner
		}
	}
}
func relationalOwnership(kind string, attrs map[string]jsontext.Value, edge bool, s *ImportSession, existing *AssertionOwnership) *AssertionOwnership {
	if existing != nil {
		owned := *existing
		if selectedProfile(s.Profile) == EventsProfile && (eventsSubject(kind, edge) || eventsEmit(kind, attrs, edge) || contextualLineageMapping(kind, attrs, edge)) {
			owned.Profile = EventsProfile
		}
		return &owned
	}
	profile := GraphProfile
	if relationalSubject(kind, attrs, edge) {
		profile = RelationalProfile
	}
	if hasRuntimeProfile(selectedProfile(s.Profile)) && runtimeSubject(kind, edge) {
		profile = RuntimeProfile
	}
	if hasLineageProfile(selectedProfile(s.Profile)) && lineageSubject(kind, edge) {
		profile = LineageProfile
	}
	if selectedProfile(s.Profile) == EventsProfile && (eventsSubject(kind, edge) || eventsEmit(kind, attrs, edge) || contextualLineageMapping(kind, attrs, edge)) {
		profile = EventsProfile
	}
	return &AssertionOwnership{RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace, Profile: profile}
}
func relationalContains(from, to Node) (bool, bool) {
	if !relationalSubject(to.Kind, to.Attributes, false) || to.Kind == "datastore" {
		return false, false
	}
	switch to.Kind {
	case "db_schema", "migration":
		return from.Kind == "datastore", true
	case "table", "view":
		return from.Kind == "db_schema", true
	case "column":
		return from.Kind == "table" || from.Kind == "view", true
	case "constraint", "index":
		return from.Kind == "table", true
	case "symbol":
		fs, _, err := relationalFacetObject(to.Kind, to.Attributes)
		if err != nil {
			return false, true
		}
		for _, raw := range fs {
			f, err := decodeRelationalFacetMode(to.Kind, raw, true, false)
			if err != nil {
				return false, true
			}
			if f.RoutineKind == "trigger" {
				if from.Kind != "table" {
					return false, true
				}
			} else if from.Kind != "db_schema" {
				return false, true
			}
		}
		return true, true
	}
	return false, true
}
func validateRelationalGraph(ctx context.Context, q importReader, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	return validateRelationalGraphRules(ctx, q, s, g, diagnostics)
}

// The import wrapper supplies its admission session and database reader. Pure
// final-graph validation has neither; historical ownership is checked by its caller.
func validateRelationalGraphRules(ctx context.Context, q importReader, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	r := &relationalRules{ctx: ctx, s: s, g: g, diagnostics: diagnostics, nodes: map[string]Node{}, evidence: map[string]Evidence{}}
	for _, n := range g.Nodes {
		r.nodes[n.ID] = n
	}
	for _, e := range g.Evidence {
		r.evidence[e.ID] = e
	}
	// History is consulted lazily and by key. Review 2026-10-06, F79/F80: the
	// ancestor chain was walked through loadSourceState on every preview and
	// commit, and a historical pin or a source_only change then loaded every
	// needed ancestor's full RevisionState (up to 256 MiB of nodes, edges and
	// evidence each) and kept them all until return, inside the writer. Now
	// the walk runs on first need, a pin reads its one node by primary key and
	// source_only keys are matched in SQL (relationalHistory).
	r.history = relationalHistory{ctx: ctx, q: q, s: s}
	r.names = map[string]string{}
	r.ordinals = map[string]string{}
	r.dialects = map[string]string{}
	r.parents = map[string][]string{}
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if relationalSubject(n.Kind, n.Attributes, false) && n.Kind != "datastore" && n.ParentID == nil {
			r.add("nodes/"+n.ID, "Relational subject requires structural parent")
		}
		if err := r.check(n.ID, n.Kind, n.Attributes, false, n.EvidenceIDs, ""); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.check(e.ID, e.Kind, e.Attributes, true, e.EvidenceIDs, e.From); err != nil {
			return err
		}
		if e.Kind == "references" {
			r.checkReferencesEdge(e)
		}
	}
	migrationFacets, sourceOnly, err := r.migrationFacets()
	if err != nil {
		return err
	}
	if err := r.checkSourceOnly(sourceOnly); err != nil {
		return err
	}
	for _, n := range g.Nodes {
		if s == nil || n.Kind == "migration" || !relationalSubject(n.Kind, n.Attributes, false) {
			continue
		}
		r.checkMigrationDerived(n, migrationFacets)
	}
	r.checkMigrationsAcyclic()
	return nil
}

// relationalRules is the state one validateRelationalGraphRules pass shares
// across its checks: the candidate's indexes, the lazily walked history and
// the per-facet name, ordinal and dialect registries that make a duplicate
// visible only across subjects.
type relationalRules struct {
	ctx         context.Context
	s           *ImportSession
	g           *graphCandidate
	diagnostics *[]ImportDiagnostic
	nodes       map[string]Node
	evidence    map[string]Evidence
	history     relationalHistory
	names       map[string]string
	ordinals    map[string]string
	dialects    map[string]string
	parents     map[string][]string
	// Active external keys, counted once instead of a scan of g.Nodes per
	// source_only change (review 2026-10-06, F79).
	activeKeys map[string]int
}

// relationalSourceOnlyChange is a migration change that names its target by
// external key only; whether history owns that key is answered in one batch.
type relationalSourceOnlyChange struct{ nodeID, key string }

func (r *relationalRules) add(path, message string) {
	*r.diagnostics = append(*r.diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
}

func (r *relationalRules) datastore(id string) string {
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

// check validates one subject's nested references and facets. from is the
// edge's source node ("" for a node). It used to be found by a linear scan of
// g.Edges per facet, called once per edge: O(E^2), about 4e10 comparisons at
// MaxRevisionEdges, under the writer (review 2026-10-06, F79).
func (r *relationalRules) check(id, kind string, attrs map[string]jsontext.Value, edge bool, proofs []string, from string) error {
	if !relationalSubject(kind, attrs, edge) {
		return nil
	}
	path := "nodes/" + id
	if edge {
		path = "edges/" + id
	}
	if err := validateRelationalAttributesMode(kind, attrs, edge, true, r.s != nil); err != nil {
		r.add(path, err.Error())
		return nil
	}
	fs, _, _ := relationalFacetObject(kind, attrs)
	refs, err := relationalReferencesMode(kind, attrs, edge, true, r.s != nil)
	if err != nil {
		r.add(path, err.Error())
		return nil
	}
	for _, ref := range refs {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if err := r.checkReference(id, path, edge, proofs, ref); err != nil {
			return err
		}
	}
	for fk, raw := range fs {
		f, err := decodeRelationalFacetMode(kind, raw, true, r.s != nil)
		if err != nil {
			r.add(path, err.Error())
			continue
		}
		r.checkFacet(id, kind, path, edge, from, fk, f)
	}
	return nil
}

// checkReference validates one nested reference of subject id; only a
// history read can fail it.
func (r *relationalRules) checkReference(id, path string, edge bool, proofs []string, ref relationalReference) error {
	if ref.Kind == "evidence" {
		if e, ok := r.evidence[ref.ID]; r.s != nil && (!ok || e.SubjectID != id || !slices.Contains(proofs, ref.ID)) {
			r.add(path+ref.Path, "Facet proof must belong to subject and top-level evidenceIds")
		}
		return nil
	}
	if ref.HistoricalRevisionID != "" {
		if r.s == nil {
			return nil
		}
		return r.checkHistoricalPin(path, ref)
	}
	r.checkNestedTarget(id, path, edge, ref)
	return nil
}

// checkNestedTarget validates a reference to a live node of the candidate.
func (r *relationalRules) checkNestedTarget(id, path string, edge bool, ref relationalReference) {
	target, ok := r.nodes[ref.ID]
	if !ok || r.s != nil && (target.Ownership == nil || target.Ownership.RepositoryID != r.s.RepositoryID) {
		r.add(path+ref.Path, "Nested reference must survive in the same repository")
		return
	}
	valid := ref.Kind == target.Kind || ref.Kind == "dependency" && slices.Contains([]string{"table", "view", "column", "symbol"}, target.Kind) || ref.Kind == "relational" && relationalSubject(target.Kind, target.Attributes, false)
	if !valid {
		r.add(path+ref.Path, "Nested reference has wrong target kind")
	}
	if ref.Kind == "column" && !edge {
		subject := r.nodes[id]
		if subject.ParentID == nil || target.ParentID == nil || *target.ParentID != *subject.ParentID {
			r.add(path+ref.Path, "Column reference has wrong table/view parent")
		}
	}
}

func (r *relationalRules) checkHistoricalPin(path string, ref relationalReference) error {
	isAncestor, err := r.history.ancestor(ref.HistoricalRevisionID)
	if err != nil {
		return err
	}
	if !isAncestor {
		r.add(path+ref.Path, "Historical pin must be a base or ancestor revision")
		return nil
	}
	found, err := r.history.relationalSubjectAt(ref.HistoricalRevisionID, ref.ID)
	if err != nil {
		return err
	}
	if !found {
		r.add(path+ref.Path, "Historical pin must identify a relational subject in the same repository")
	}
	return nil
}

// checkFacet validates one decoded facet fk of subject id against the
// registries shared by every subject of the candidate.
func (r *relationalRules) checkFacet(id, kind, path string, edge bool, from, fk string, f *relationalFacet) {
	subject := r.nodes[id]
	store := r.datastore(id)
	if edge {
		store = r.datastore(from)
	}
	dk := store + "\x00" + fk
	if prior, ok := r.dialects[dk]; ok && prior != f.Dialect {
		r.add(path, "Conflicting dialects in one datastore facet")
	}
	r.dialects[dk] = f.Dialect
	if !edge && subject.ParentID != nil {
		r.checkFacetNames(id, kind, path, fk, subject, f)
	}
	for _, proofID := range f.EvidenceIDs {
		if e, ok := r.evidence[proofID]; r.s != nil && ok && e.Source.SnapshotID != f.SourceSnapshotID {
			r.add(path, "Facet sourceSnapshotId must match its proof")
		}
	}
	if kind == "migration" {
		r.parents[id] = append(r.parents[id], f.ParentIDs...)
		if f.DerivationStatus == "complete" {
			for _, parentID := range f.ParentIDs {
				r.checkMigrationParent(path, fk, f, parentID)
			}
		}
	}
}

// claim registers id under key and reports whether another subject held it.
func claimRelationalKey(registry map[string]string, key, id string) bool {
	prior := registry[key]
	registry[key] = id
	return prior != "" && prior != id
}

// checkFacetNames enforces the uniqueness a structural parent gives its
// children within one facet: qualified names, column names and ordinals,
// constraint and index names.
func (r *relationalRules) checkFacetNames(id, kind, path, fk string, subject Node, f *relationalFacet) {
	scope := *subject.ParentID + "\x00" + fk + "\x00"
	if f.QualifiedName != "" && claimRelationalKey(r.names, scope+f.QualifiedName, id) {
		r.add(path, "Duplicate qualified name in schema/facet")
	}
	if kind == "column" {
		if claimRelationalKey(r.names, scope+"name:"+subject.Name, id) {
			r.add(path, "Duplicate column name in table/facet")
		}
		if f.Ordinal.Status == "known" {
			ordinal, _ := strconv.ParseInt(string(f.Ordinal.Value), 10, 64)
			if claimRelationalKey(r.ordinals, scope+strconv.FormatInt(ordinal, 10), id) {
				r.add(path, "Duplicate column ordinal in table/facet")
			}
		}
	}
	if (kind == "constraint" || kind == "index") && claimRelationalKey(r.names, scope+kind+":"+subject.Name, id) {
		r.add(path, "Duplicate constraint/index name")
	}
}

// checkMigrationParent checks that a complete migration derivation is backed
// by a complete, earlier-ordered parent facet of the same key.
func (r *relationalRules) checkMigrationParent(path, fk string, f *relationalFacet, parentID string) {
	p, ok := r.nodes[parentID]
	// A missing or non-migration parent is already a
	// diagnostic of the nested-reference pass above, which
	// does not stop this loop. Only a migration facet
	// carries Order, so reading a table's same-key facet
	// here dereferenced a nil Order (review 2026-10-06, F76).
	if !ok || p.Kind != "migration" {
		return
	}
	pfs, _, err := relationalFacetObject(p.Kind, p.Attributes)
	if err != nil {
		return
	}
	pr, ok := pfs[fk]
	if !ok {
		r.add(path, "Complete migration derivation requires supporting parent facet")
		return
	}
	pf, err := decodeRelationalFacetMode(p.Kind, pr, true, r.s != nil)
	if err != nil {
		return
	}
	if pf.DerivationStatus != "complete" || pf.Order.Status != "known" || string(pf.Order.Value) == "null" {
		r.add(path, "Incomplete migration parent prevents complete derivation")
	}
	if f.Order.Status == "known" && pf.Order.Status == "known" {
		a, _ := strconv.ParseInt(string(f.Order.Value), 10, 64)
		b, _ := strconv.ParseInt(string(pf.Order.Value), 10, 64)
		if a <= b {
			r.add(path, "Migration order must follow parent order")
		}
	}
}

// checkReferencesEdge checks a "references" edge against the foreign-key
// constraint it must originate from, facet by facet.
func (r *relationalRules) checkReferencesEdge(e Edge) {
	from, to := r.nodes[e.From], r.nodes[e.To]
	fs, _, err := relationalFacetObject(e.Kind, e.Attributes)
	if err != nil {
		return
	}
	cfs, _, _ := relationalFacetObject(from.Kind, from.Attributes)
	for fk, raw := range fs {
		f, err := decodeRelationalFacetMode(e.Kind, raw, true, r.s != nil)
		if err != nil {
			continue
		}
		constraint, err := decodeRelationalFacetMode(from.Kind, cfs[fk], true, r.s != nil)
		if err != nil || constraint.ConstraintKind != "foreign_key" || from.ParentID == nil {
			r.add("edges/"+e.ID, "References must originate from a foreign-key constraint with matching facet")
			continue
		}
		r.checkReferencePairs(e, from, to, f, constraint)
	}
}

func (r *relationalRules) checkReferencePairs(e Edge, from, to Node, f, constraint *relationalFacet) {
	if to.Kind == "unresolved_target" {
		var expected string
		_ = json.Unmarshal(to.Attributes["expectedKind"], &expected)
		if expected != "table" || len(f.ColumnPairs) != 0 || !nonblank(f.TargetReason) {
			r.add("edges/"+e.ID, "Unresolved reference requires table target, empty pairs and reason")
		}
		return
	}
	if to.Kind != "table" || len(f.ColumnPairs) == 0 {
		r.add("edges/"+e.ID, "References require table target and ordered pairs")
		return
	}
	source := make([]string, 0, len(f.ColumnPairs))
	for _, pair := range f.ColumnPairs {
		source = append(source, pair.FromColumnID)
		if !relationalPairParents(r.nodes, *from.ParentID, e.To, DatabaseColumnPair{FromColumnID: pair.FromColumnID, ToColumnID: pair.ToColumnID}) {
			r.add("edges/"+e.ID, "FK pair columns must belong to source and destination tables")
		}
	}
	if !slices.Equal(source, constraint.ColumnIDs) {
		r.add("edges/"+e.ID, "FK ordered columns must equal ordered pair source")
	}
}

// related reports containment either way between two subjects.
func (r *relationalRules) related(subjectID, targetID string) bool {
	// Containment is explicit identity; neither native names nor SQL text create links.
	for _, pair := range [][2]string{{subjectID, targetID}, {targetID, subjectID}} {
		id := pair[0]
		seen := map[string]bool{}
		for id != "" && !seen[id] {
			if id == pair[1] {
				return true
			}
			seen[id] = true
			n, ok := r.nodes[id]
			if !ok || n.ParentID == nil {
				break
			}
			id = *n.ParentID
		}
	}
	return false
}

// migrationFacets decodes every migration node's facets once, flagging a
// source_only target still active in the candidate and collecting the
// source_only changes for one batched history lookup.
func (r *relationalRules) migrationFacets() (map[string]map[string]*relationalFacet, []relationalSourceOnlyChange, error) {
	migrationFacets := map[string]map[string]*relationalFacet{}
	var sourceOnly []relationalSourceOnlyChange
	for _, n := range r.g.Nodes {
		if n.Kind != "migration" {
			continue
		}
		fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
		if err != nil {
			continue
		}
		migrationFacets[n.ID] = map[string]*relationalFacet{}
		for fk, raw := range fs {
			if err := r.ctx.Err(); err != nil {
				return nil, nil, err
			}
			f, err := decodeRelationalFacetMode(n.Kind, raw, true, r.s != nil)
			if err != nil {
				continue
			}
			migrationFacets[n.ID][fk] = f
			sourceOnly = r.collectSourceOnly(n.ID, f, sourceOnly)
		}
	}
	return migrationFacets, sourceOnly, nil
}

func (r *relationalRules) collectSourceOnly(nodeID string, f *relationalFacet, sourceOnly []relationalSourceOnlyChange) []relationalSourceOnlyChange {
	for _, change := range f.Changes {
		target := change.Target
		if target.Kind != "source_only" || r.s == nil {
			continue
		}
		if r.activeKeys == nil {
			r.activeKeys = make(map[string]int, len(r.g.Nodes))
			for _, active := range r.g.Nodes {
				r.activeKeys[active.ExternalKey]++
			}
		}
		for range r.activeKeys[target.ExternalKey] {
			r.add("nodes/"+nodeID, "Source-only target must be absent from the active candidate")
		}
		sourceOnly = append(sourceOnly, relationalSourceOnlyChange{nodeID: nodeID, key: target.ExternalKey})
	}
	return sourceOnly
}

func (r *relationalRules) checkSourceOnly(sourceOnly []relationalSourceOnlyChange) error {
	if len(sourceOnly) == 0 {
		return nil
	}
	keys := make([]string, 0, len(sourceOnly))
	for _, c := range sourceOnly {
		keys = append(keys, c.key)
	}
	owned, err := r.history.ownedKeysInAncestors(keys)
	if err != nil {
		return err
	}
	for _, c := range sourceOnly {
		if owned[c.key] {
			r.add("nodes/"+c.nodeID, "Available model history requires an exact historical target instead of source-only")
		}
	}
	return nil
}

// checkMigrationDerived requires a facet that claims a complete migration
// derivation to be backed by ordered, handled migration changes.
func (r *relationalRules) checkMigrationDerived(n Node, migrationFacets map[string]map[string]*relationalFacet) {
	fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
	if err != nil {
		return
	}
	for fk, raw := range fs {
		f, err := decodeRelationalFacetMode(n.Kind, raw, true, r.s != nil)
		if err != nil || f.SourceKind != "migration" {
			continue
		}
		complete := f.AnalysisStatus == "complete" || f.ColumnsStatus == "complete" || f.ConstraintsStatus == "complete" || f.DependenciesStatus == "complete" || f.BodyStatus == "complete"
		if !complete {
			continue
		}
		supported, incomplete := r.migrationSupport(n.ID, fk, f, migrationFacets)
		if !supported || incomplete {
			r.add("nodes/"+n.ID, "Complete migration-derived facets require ordered handled supporting migration changes; unsupported dependent changes remain incomplete")
		}
	}
}

// migrationSupport reports whether any same-dialect migration change touches
// the subject, and whether one of those changes is not fully derived.
func (r *relationalRules) migrationSupport(nodeID, fk string, f *relationalFacet, migrationFacets map[string]map[string]*relationalFacet) (supported, incomplete bool) {
	for _, facets := range migrationFacets {
		mf := facets[fk]
		if mf == nil || mf.Dialect != f.Dialect {
			continue
		}
		for _, change := range mf.Changes {
			if change.Target.Kind != "candidate" || !r.related(nodeID, change.Target.ObjectID) {
				continue
			}
			supported = true
			if mf.DerivationStatus != "complete" || mf.Order.Status != "known" || string(mf.Order.Value) == "null" || change.Operation == "unknown" {
				incomplete = true
			}
		}
	}
	return supported, incomplete
}

func (r *relationalRules) checkMigrationsAcyclic() {
	colors := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		if colors[id] == 1 {
			return false
		}
		if colors[id] == 2 {
			return true
		}
		colors[id] = 1
		for _, p := range r.parents[id] {
			if !visit(p) {
				return false
			}
		}
		colors[id] = 2
		return true
	}
	for id := range r.parents {
		if !visit(id) {
			r.add("nodes/"+id, "Migration parent graph must be acyclic")
			break
		}
	}
}
