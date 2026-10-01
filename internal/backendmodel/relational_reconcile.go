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
				target, typ := k, "node"
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
				if target != k {
					if v[0] == '[' {
						var keys []string
						_ = json.Unmarshal(v, &keys)
						ids := []string{}
						for i, key := range keys {
							ids = append(ids, resolve(typ, key, fmt.Sprintf("%s/%s/%d", path, target, i)))
						}
						out[target], _ = json.Marshal(ids)
					} else {
						var key string
						_ = json.Unmarshal(v, &key)
						out[target], _ = json.Marshal(resolve(typ, key, path+"/"+target))
					}
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
	union := func(ids, added []string) []string {
		out := slices.Clone(ids)
		for _, id := range added {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		slices.Sort(out)
		return out
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		old, exists := baseNodes[n.ID]
		if !relationalSubject(n.Kind, n.Attributes, false) && (!exists || old.Kind != n.Kind || !relationalSubject(old.Kind, old.Attributes, false)) {
			continue
		}
		if !submitted["node\x00"+n.ID] {
			n.Attributes = staleRelationalAttributes(n.Kind, n.Attributes)
			fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
			for _, raw := range fs {
				var f relationalFacet
				_ = json.Unmarshal(raw, &f)
				for _, id := range f.EvidenceIDs {
					proofs[id] = true
				}
			}
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
			fs, _, _ := relationalFacetObject(e.Kind, e.Attributes)
			for _, raw := range fs {
				var f relationalFacet
				_ = json.Unmarshal(raw, &f)
				for _, id := range f.EvidenceIDs {
					proofs[id] = true
				}
			}
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
		if selectedProfile(s.Profile) == RuntimeProfile && old[e.ID] == nil && (runtimeSubject(nodes[e.From].Kind, false) || runtimeSubject(nodes[e.To].Kind, false)) {
			e.Ownership.Profile = RuntimeProfile
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
		copy := *existing
		return &copy
	}
	profile := GraphProfile
	if relationalSubject(kind, attrs, edge) {
		profile = RelationalProfile
	}
	if selectedProfile(s.Profile) == RuntimeProfile && runtimeSubject(kind, edge) {
		profile = RuntimeProfile
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
			f, err := decodeRelationalFacet(to.Kind, raw, true)
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
	add := func(path, message string) {
		*diagnostics = append(*diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
	}
	nodes := map[string]Node{}
	evidence := map[string]Evidence{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range g.Evidence {
		evidence[e.ID] = e
	}
	ancestors := map[string]bool{}
	historical := map[string]*RevisionState{}
	rid := s.BaseRevisionID
	for rid != "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ancestors[rid] {
			break
		}
		state, err := loadSourceState(ctx, q, s.ProjectID, rid)
		if err != nil {
			return err
		}
		ancestors[rid] = true
		if state.Revision.ParentRevisionID == nil {
			break
		}
		rid = *state.Revision.ParentRevisionID
	}
	names := map[string]string{}
	ordinals := map[string]string{}
	dialects := map[string]string{}
	parents := map[string][]string{}
	datastore := func(id string) string {
		seen := map[string]bool{}
		for !seen[id] {
			seen[id] = true
			n, ok := nodes[id]
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
	check := func(id, kind string, attrs map[string]jsontext.Value, edge bool, proofs []string) error {
		if !relationalSubject(kind, attrs, edge) {
			return nil
		}
		path := "nodes/" + id
		if edge {
			path = "edges/" + id
		}
		if err := validateRelationalAttributes(kind, attrs, edge, true); err != nil {
			add(path, err.Error())
			return nil
		}
		fs, _, _ := relationalFacetObject(kind, attrs)
		refs, err := relationalReferences(kind, attrs, edge, true)
		if err != nil {
			add(path, err.Error())
			return nil
		}
		for _, ref := range refs {
			if ref.Kind == "evidence" {
				if e, ok := evidence[ref.ID]; !ok || e.SubjectID != id || !slices.Contains(proofs, ref.ID) {
					add(path+ref.Path, "Facet proof must belong to subject and top-level evidenceIds")
				}
				continue
			}
			if ref.HistoricalRevisionID != "" {
				if !ancestors[ref.HistoricalRevisionID] {
					add(path+ref.Path, "Historical pin must be a base or ancestor revision")
					continue
				}
				state := historical[ref.HistoricalRevisionID]
				if state == nil {
					var err error
					state, err = loadRevisionState(ctx, q, s.ProjectID, ref.HistoricalRevisionID)
					if err != nil {
						return err
					}
					historical[ref.HistoricalRevisionID] = state
				}
				found := false
				for _, n := range state.Nodes {
					if n.ID == ref.ID && n.Ownership != nil && n.Ownership.RepositoryID == s.RepositoryID && relationalSubject(n.Kind, n.Attributes, false) {
						found = true
					}
				}
				if !found {
					add(path+ref.Path, "Historical pin must identify a relational subject in the same repository")
				}
				continue
			}
			target, ok := nodes[ref.ID]
			if !ok || target.Ownership == nil || target.Ownership.RepositoryID != s.RepositoryID {
				add(path+ref.Path, "Nested reference must survive in the same repository")
				continue
			}
			valid := ref.Kind == target.Kind || ref.Kind == "dependency" && slices.Contains([]string{"table", "view", "column", "symbol"}, target.Kind) || ref.Kind == "relational" && relationalSubject(target.Kind, target.Attributes, false)
			if !valid {
				add(path+ref.Path, "Nested reference has wrong target kind")
			}
			if ref.Kind == "column" && !edge {
				subject := nodes[id]
				if subject.ParentID == nil || target.ParentID == nil || *target.ParentID != *subject.ParentID {
					add(path+ref.Path, "Column reference has wrong table/view parent")
				}
			}
		}
		for fk, raw := range fs {
			f, _ := decodeRelationalFacet(kind, raw, true)
			subject := nodes[id]
			store := datastore(id)
			if edge {
				for _, e := range g.Edges {
					if e.ID == id {
						store = datastore(e.From)
						break
					}
				}
			}
			dk := store + "\x00" + fk
			if prior, ok := dialects[dk]; ok && prior != f.Dialect {
				add(path, "Conflicting dialects in one datastore facet")
			}
			dialects[dk] = f.Dialect
			if !edge && subject.ParentID != nil {
				if f.QualifiedName != "" {
					nk := *subject.ParentID + "\x00" + fk + "\x00" + f.QualifiedName
					if prior := names[nk]; prior != "" && prior != id {
						add(path, "Duplicate qualified name in schema/facet")
					}
					names[nk] = id
				}
				if kind == "column" {
					nk := *subject.ParentID + "\x00" + fk + "\x00name:" + subject.Name
					if prior := names[nk]; prior != "" && prior != id {
						add(path, "Duplicate column name in table/facet")
					}
					names[nk] = id
					if f.Ordinal.Status == "known" {
						ordinal, _ := strconv.ParseInt(string(f.Ordinal.Value), 10, 64)
						okey := *subject.ParentID + "\x00" + fk + "\x00" + strconv.FormatInt(ordinal, 10)
						if prior := ordinals[okey]; prior != "" && prior != id {
							add(path, "Duplicate column ordinal in table/facet")
						}
						ordinals[okey] = id
					}
				}
				if kind == "constraint" || kind == "index" {
					nk := *subject.ParentID + "\x00" + fk + "\x00" + kind + ":" + subject.Name
					if prior := names[nk]; prior != "" && prior != id {
						add(path, "Duplicate constraint/index name")
					}
					names[nk] = id
				}
			}
			for _, proofID := range f.EvidenceIDs {
				if e, ok := evidence[proofID]; ok && e.Source.SnapshotID != f.SourceSnapshotID {
					add(path, "Facet sourceSnapshotId must match its proof")
				}
			}
			if kind == "migration" {
				parents[id] = append(parents[id], f.ParentIDs...)
				if f.DerivationStatus == "complete" {
					for _, parentID := range f.ParentIDs {
						p := nodes[parentID]
						pfs, _, err := relationalFacetObject(p.Kind, p.Attributes)
						if err != nil {
							continue
						}
						pr, ok := pfs[fk]
						if !ok {
							add(path, "Complete migration derivation requires supporting parent facet")
							continue
						}
						pf, err := decodeRelationalFacet(p.Kind, pr, true)
						if err == nil && (pf.DerivationStatus != "complete" || pf.Order.Status != "known" || string(pf.Order.Value) == "null") {
							add(path, "Incomplete migration parent prevents complete derivation")
						}
						if err == nil && f.Order.Status == "known" && pf.Order.Status == "known" {
							a, _ := strconv.ParseInt(string(f.Order.Value), 10, 64)
							b, _ := strconv.ParseInt(string(pf.Order.Value), 10, 64)
							if a <= b {
								add(path, "Migration order must follow parent order")
							}
						}
					}
				}
			}
		}
		return nil
	}
	for _, n := range g.Nodes {
		if relationalSubject(n.Kind, n.Attributes, false) && n.Kind != "datastore" && n.ParentID == nil {
			add("nodes/"+n.ID, "Relational subject requires structural parent")
		}
		if err := check(n.ID, n.Kind, n.Attributes, false, n.EvidenceIDs); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		if err := check(e.ID, e.Kind, e.Attributes, true, e.EvidenceIDs); err != nil {
			return err
		}
		if e.Kind != "references" {
			continue
		}
		from, to := nodes[e.From], nodes[e.To]
		fs, _, err := relationalFacetObject(e.Kind, e.Attributes)
		if err != nil {
			continue
		}
		cfs, _, _ := relationalFacetObject(from.Kind, from.Attributes)
		for fk, raw := range fs {
			f, err := decodeRelationalFacet(e.Kind, raw, true)
			if err != nil {
				continue
			}
			constraint, err := decodeRelationalFacet(from.Kind, cfs[fk], true)
			if err != nil || constraint.ConstraintKind != "foreign_key" || from.ParentID == nil {
				add("edges/"+e.ID, "References must originate from a foreign-key constraint with matching facet")
				continue
			}
			if to.Kind == "unresolved_target" {
				var expected string
				_ = json.Unmarshal(to.Attributes["expectedKind"], &expected)
				if expected != "table" || len(f.ColumnPairs) != 0 || !nonblank(f.TargetReason) {
					add("edges/"+e.ID, "Unresolved reference requires table target, empty pairs and reason")
				}
				continue
			}
			if to.Kind != "table" || len(f.ColumnPairs) == 0 {
				add("edges/"+e.ID, "References require table target and ordered pairs")
				continue
			}
			source := []string{}
			for _, pair := range f.ColumnPairs {
				source = append(source, pair.FromColumnID)
				if !relationalPairParents(nodes, *from.ParentID, e.To, DatabaseColumnPair{FromColumnID: pair.FromColumnID, ToColumnID: pair.ToColumnID}) {
					add("edges/"+e.ID, "FK pair columns must belong to source and destination tables")
				}
			}
			if !slices.Equal(source, constraint.ColumnIDs) {
				add("edges/"+e.ID, "FK ordered columns must equal ordered pair source")
			}
		}
	}

	related := func(subjectID, targetID string) bool {
		// Containment is explicit identity; neither native names nor SQL text create links.
		for _, pair := range [][2]string{{subjectID, targetID}, {targetID, subjectID}} {
			id := pair[0]
			seen := map[string]bool{}
			for id != "" && !seen[id] {
				if id == pair[1] {
					return true
				}
				seen[id] = true
				n, ok := nodes[id]
				if !ok || n.ParentID == nil {
					break
				}
				id = *n.ParentID
			}
		}
		return false
	}
	migrationFacets := map[string]map[string]*relationalFacet{}
	for _, n := range g.Nodes {
		if n.Kind != "migration" {
			continue
		}
		fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
		if err != nil {
			continue
		}
		migrationFacets[n.ID] = map[string]*relationalFacet{}
		for fk, raw := range fs {
			f, err := decodeRelationalFacet(n.Kind, raw, true)
			if err != nil {
				continue
			}
			migrationFacets[n.ID][fk] = f
			for _, change := range f.Changes {
				target := change.Target
				if target.Kind != "source_only" {
					continue
				}
				for _, active := range g.Nodes {
					if active.ExternalKey == target.ExternalKey {
						add("nodes/"+n.ID, "Source-only target must be absent from the active candidate")
					}
				}
				for ancestor := range ancestors {
					state := historical[ancestor]
					if state == nil {
						var err error
						state, err = loadRevisionState(ctx, q, s.ProjectID, ancestor)
						if err != nil {
							return err
						}
						historical[ancestor] = state
					}
					for _, old := range state.Nodes {
						if old.ExternalKey == target.ExternalKey && old.Ownership != nil && old.Ownership.RepositoryID == s.RepositoryID {
							add("nodes/"+n.ID, "Available model history requires an exact historical target instead of source-only")
						}
					}
				}
			}
		}
	}
	for _, n := range g.Nodes {
		if n.Kind == "migration" || !relationalSubject(n.Kind, n.Attributes, false) {
			continue
		}
		fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
		if err != nil {
			continue
		}
		for fk, raw := range fs {
			f, err := decodeRelationalFacet(n.Kind, raw, true)
			if err != nil || f.SourceKind != "migration" {
				continue
			}
			complete := f.AnalysisStatus == "complete" || f.ColumnsStatus == "complete" || f.ConstraintsStatus == "complete" || f.DependenciesStatus == "complete" || f.BodyStatus == "complete"
			if !complete {
				continue
			}
			supported := false
			incomplete := false
			for _, facets := range migrationFacets {
				mf := facets[fk]
				if mf == nil || mf.Dialect != f.Dialect {
					continue
				}
				for _, change := range mf.Changes {
					if change.Target.Kind != "candidate" || !related(n.ID, change.Target.ObjectID) {
						continue
					}
					supported = true
					if mf.DerivationStatus != "complete" || mf.Order.Status != "known" || string(mf.Order.Value) == "null" || change.Operation == "unknown" {
						incomplete = true
					}
				}
			}
			if !supported || incomplete {
				add("nodes/"+n.ID, "Complete migration-derived facets require ordered handled supporting migration changes; unsupported dependent changes remain incomplete")
			}
		}
	}
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
		for _, p := range parents[id] {
			if !visit(p) {
				return false
			}
		}
		colors[id] = 2
		return true
	}
	for id := range parents {
		if !visit(id) {
			add("nodes/"+id, "Migration parent graph must be acyclic")
			break
		}
	}
	return nil
}
func relationalActiveReferenceTo(kind string, attrs map[string]jsontext.Value, edge bool, id string) bool {
	refs, err := relationalReferences(kind, attrs, edge, true)
	if err != nil {
		return relationalSubject(kind, attrs, edge)
	}
	return slices.ContainsFunc(refs, func(r relationalReference) bool {
		return r.Kind != "evidence" && r.HistoricalRevisionID == "" && r.ID == id
	})
}
func relationalProofReferenceTo(kind string, attrs map[string]jsontext.Value, edge bool, id string) bool {
	refs, err := relationalReferences(kind, attrs, edge, true)
	return err == nil && slices.ContainsFunc(refs, func(r relationalReference) bool { return r.Kind == "evidence" && r.ID == id })
}
