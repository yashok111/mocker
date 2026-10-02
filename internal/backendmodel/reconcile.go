package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
)

func loadSourceState(ctx context.Context, q importReader, pid, rid string) (*RevisionState, error) {
	if !ValidID(pid) || !ValidID(rid) {
		return nil, notFound()
	}
	var doc string
	if err := q.QueryRowContext(ctx, `SELECT document FROM backend_revisions WHERE project_id=? AND id=?`, pid, rid).Scan(&doc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound()
		}
		return nil, err
	}
	state := &RevisionState{Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}, Sources: []SourceSnapshot{}, Inventory: []InventoryItem{}}
	if err := json.Unmarshal([]byte(doc), &state.Revision); err != nil {
		return nil, err
	}
	if err := q.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources WHERE revision_id=?`, rid).Scan(&doc); err == nil {
		var c RevisionCoverage
		if err := json.Unmarshal([]byte(doc), &c); err != nil {
			return nil, err
		}
		state.Sources = c.Snapshots
		state.Inventory = c.Inventory
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if len(state.Sources) == 1 && state.Sources[0].Role == "" {
		state.Sources[0].Role = "primary"
	}
	frozen, err := loadAPIArtifactContext(ctx, q, rid)
	if err != nil {
		return nil, err
	}
	state.APIArtifactContext = frozen
	if frozen == nil && slices.ContainsFunc(state.Revision.ArtifactPins, func(pin ArtifactPin) bool { return pin.Kind == "api_design" }) {
		return nil, invalid("context", "API pins require their frozen association context")
	}
	return state, nil
}
func loadRevisionState(ctx context.Context, q importReader, pid, rid string) (*RevisionState, error) {
	state, err := loadSourceState(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	var doc string
	rows, err := q.QueryContext(ctx, `SELECT record_type,document FROM backend_graph_records WHERE project_id=? AND revision_id=? ORDER BY record_type,id`, pid, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var typ string
		if err := rows.Scan(&typ, &doc); err != nil {
			return nil, err
		}
		switch typ {
		case "node":
			var n Node
			if err := json.Unmarshal([]byte(doc), &n); err != nil {
				return nil, err
			}
			deriveMetadata(*state, &n.Ownership, &n.Freshness)
			state.Nodes = append(state.Nodes, n)
		case "edge":
			var e Edge
			if err := json.Unmarshal([]byte(doc), &e); err != nil {
				return nil, err
			}
			deriveMetadata(*state, &e.Ownership, &e.Freshness)
			state.Edges = append(state.Edges, e)
		case "evidence":
			var e Evidence
			if err := json.Unmarshal([]byte(doc), &e); err != nil {
				return nil, err
			}
			deriveMetadata(*state, &e.Ownership, &e.Freshness)
			state.Evidence = append(state.Evidence, e)
		}
	}
	return state, rows.Err()
}
func deriveMetadata(state RevisionState, owner **AssertionOwnership, fresh **AssertionFreshness) {
	src := primarySource(state)
	if src == nil {
		return
	}
	if *owner == nil {
		*owner = &AssertionOwnership{RepositoryID: src.RepositoryID, ProviderNamespace: src.Provider.Namespace, Profile: GraphProfile}
	}
	if *fresh == nil {
		*fresh = &AssertionFreshness{Status: "current", ConfirmedSnapshotID: src.ID, Reasons: []string{}}
	}
}

func overlayGraph(ctx context.Context, q importReader, s *ImportSession, base *RevisionState, g *graphCandidate, commands []ImportCommand, ids map[string]string, diagnostics *[]ImportDiagnostic) error {
	add := func(code, path, message string) {
		*diagnostics = append(*diagnostics, ImportDiagnostic{Code: code, Path: path, Message: message})
	}
	owner := &AssertionOwnership{RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace, Profile: GraphProfile}
	current := func() *AssertionFreshness {
		return &AssertionFreshness{Status: "current", ConfirmedSnapshotID: s.SnapshotID, Reasons: []string{}}
	}
	g.Sources = []SourceSnapshot{{Role: "primary", ID: s.SnapshotID, RepositoryID: s.RepositoryID, ManifestHash: s.ManifestHash, Provider: s.Manifest.Provider, SnapshotManifest: s.Manifest.Snapshot}}
	g.SourceChanges = []SourceChange{}
	g.IdentityDecisions = []IdentityDecision{}
	g.DeletionDecisions = []DeletionDecision{}
	g.ReconciliationGaps = []string{}
	submitted := map[string]bool{}
	submittedKeys := map[string]bool{}
	usedIDs := map[string]bool{}
	for _, c := range commands {
		typ, key, _ := commandAddress(c)
		id := ids[typ+"\x00"+key]
		submitted[typ+"\x00"+id] = true
		submittedKeys[typ+"\x00"+key] = true
		if usedIDs[typ+"\x00"+id] {
			add("backend_identity_conflict", typ+"/"+key, "Multiple active keys for one UUID")
		}
		usedIDs[typ+"\x00"+id] = true
	}
	for i := range g.Nodes {
		g.Nodes[i].Ownership = owner
		g.Nodes[i].Freshness = current()
	}
	for i := range g.Edges {
		g.Edges[i].Ownership = owner
		g.Edges[i].Freshness = current()
	}
	for i := range g.Evidence {
		g.Evidence[i].Ownership = owner
		g.Evidence[i].Freshness = current()
	}
	if hasRelationalProfile(selectedProfile(s.Profile)) {
		assignRelationalOwnership(base, g, s)
	}
	if s.Mode != "reconcile" {
		return nil
	}
	decisions, err := loadDecisions(ctx, q, s.ID)
	if err != nil {
		return err
	}
	mappedTargets := map[string]bool{}
	for _, c := range decisions {
		if c.Identity != nil {
			mappedTargets[c.Identity.RecordType+"\x00"+c.Identity.ToExternalKey] = true
		}
	}
	baseNodes := map[string]Node{}
	for _, n := range base.Nodes {
		baseNodes[n.ID] = n
	}
	baseEdges := map[string]Edge{}
	for _, e := range base.Edges {
		baseEdges[e.ID] = e
	}
	owned := map[string]bool{}
	checkOwner := func(typ, id string, o *AssertionOwnership) {
		valid := o != nil && o.RepositoryID == s.RepositoryID && o.ProviderNamespace == s.Manifest.Provider.Namespace && (o.Profile == GraphProfile || hasRelationalProfile(selectedProfile(s.Profile)) && o.Profile == RelationalProfile || hasRuntimeProfile(selectedProfile(s.Profile)) && o.Profile == RuntimeProfile || selectedProfile(s.Profile) == LineageProfile && o.Profile == LineageProfile)
		owned[typ+"\x00"+id] = valid
		if !valid {
			add("backend_unsupported_scope", typ+"/"+id, "Base assertion belongs to another ownership partition")
		}
	}
	for _, n := range base.Nodes {
		checkOwner("node", n.ID, n.Ownership)
	}
	for _, e := range base.Edges {
		checkOwner("edge", e.ID, e.Ownership)
	}
	for _, e := range base.Evidence {
		checkOwner("evidence", e.ID, e.Ownership)
	}
	// Revalidate every acknowledged allocation against this exact base and the registry.
	for _, c := range commands {
		typ, key, _ := commandAddress(c)
		id := ids[typ+"\x00"+key]
		b, err := binding(ctx, q, s, typ, key)
		if err != nil {
			return err
		}
		if b != nil && (b.ID != id || b.State != "active" && b.State != "reserved" && !mappedTargets[typ+"\x00"+key]) {
			add("backend_identity_conflict", typ+"/"+key, "Acknowledged UUID conflicts with the current key binding")
		}
		if n, ok := baseNodes[id]; ok {
			if typ == "node" && n.ID == id && n.ExternalKey != key && !mappedTargets[typ+"\x00"+key] {
				add("backend_identity_conflict", typ+"/"+key, "Changing an active key requires a mapping decision")
			}
			if typ == "node" && n.ID == id && c.Node.Kind != n.Kind {
				add("backend_identity_conflict", typ+"/"+key, "Identity kind cannot change")
			}
		}
		if e, ok := baseEdges[id]; ok {
			if typ == "edge" && e.ID == id && e.ExternalKey != key && !mappedTargets[typ+"\x00"+key] {
				add("backend_identity_conflict", typ+"/"+key, "Changing an active key requires a mapping decision")
			}
			if typ == "edge" && e.ID == id && c.Edge.Kind != e.Kind {
				add("backend_identity_conflict", typ+"/"+key, "Identity kind cannot change")
			}
		}
	}
	evidenceBySubject := map[string][]Evidence{}
	for _, e := range base.Evidence {
		evidenceBySubject[e.SubjectID] = append(evidenceBySubject[e.SubjectID], e)
	}
	files := map[string]ManifestFile{}
	for _, file := range s.Manifest.Snapshot.Files {
		files[file.Path] = file
	}
	stale := func(previous *AssertionFreshness, evidence []Evidence) *AssertionFreshness {
		f := &AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
		if previous != nil {
			f.ConfirmedSnapshotID = previous.ConfirmedSnapshotID
		}
		for _, e := range evidence {
			file, ok := files[e.Source.File]
			switch {
			case !ok:
				f.Reasons = append(f.Reasons, "source_absent")
			case file.AnalysisStatus != "analyzed":
				f.Reasons = append(f.Reasons, "source_unavailable")
			case file.ContentHash != e.Source.ContentHash:
				f.Reasons = append(f.Reasons, "source_changed")
			}
		}
		slices.Sort(f.Reasons)
		f.Reasons = slices.Compact(f.Reasons)
		return f
	}
	retainedSubjects := map[string]*AssertionFreshness{}
	for _, n := range base.Nodes {
		if !submitted["node\x00"+n.ID] {
			n.Freshness = stale(n.Freshness, evidenceBySubject[n.ID])
			g.Nodes = append(g.Nodes, n)
			retainedSubjects[n.ID] = n.Freshness
		}
	}
	for _, e := range base.Edges {
		if !submitted["edge\x00"+e.ID] {
			e.Freshness = stale(e.Freshness, evidenceBySubject[e.ID])
			g.Edges = append(g.Edges, e)
			retainedSubjects[e.ID] = e.Freshness
		}
	}
	for _, e := range g.Evidence {
		if _, retained := retainedSubjects[e.SubjectID]; retained {
			add("backend_graph_invalid", "evidence/"+e.ID, "Evidence requires an explicitly upserted subject")
		}
	}
	for _, e := range base.Evidence {
		if f, ok := retainedSubjects[e.SubjectID]; ok && !submitted["evidence\x00"+e.ID] {
			e.Freshness = f
			g.Evidence = append(g.Evidence, e)
		}
	}
	if hasRelationalProfile(selectedProfile(s.Profile)) {
		overlayRelationalFacets(base, g, submitted, diagnostics)
	}
	refs := func(id string) []HistoricalEvidenceRef {
		out := []HistoricalEvidenceRef{}
		for _, e := range base.Evidence {
			if e.SubjectID == id || e.ID == id {
				out = append(out, HistoricalEvidenceRef{RevisionID: base.Revision.ID, EvidenceID: e.ID})
			}
		}
		return out
	}
	deleted := map[string]bool{}
	for _, c := range decisions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.Identity != nil {
			x := c.Identity
			d := IdentityDecision{Command: *x, OldEvidenceRefs: []HistoricalEvidenceRef{}, EvidenceRefs: []StagedEvidenceRef{}}
			id, _ := stateIdentity(*base, x.RecordType, x.FromExternalKey)
			valid := id != "" && id == x.ExpectedID && owned[x.RecordType+"\x00"+id]
			if id != "" {
				d.OldSubject = &HistoricalSubjectRef{RevisionID: base.Revision.ID, RecordType: x.RecordType, ID: id}
				d.OldEvidenceRefs = refs(id)
			}
			target, err := binding(ctx, q, s, x.RecordType, x.ToExternalKey)
			if err != nil {
				return err
			}
			if target != nil && (target.ID != x.ExpectedID || target.State != "retired") {
				valid = false
			}
			if !submittedKeys[x.RecordType+"\x00"+x.ToExternalKey] || submittedKeys[x.RecordType+"\x00"+x.FromExternalKey] {
				valid = false
			}
			for _, key := range x.EvidenceKeys {
				found := false
				for _, e := range g.Evidence {
					if e.ExternalKey == key && e.SubjectID == id && e.Source.SnapshotID == s.SnapshotID && submitted["evidence\x00"+e.ID] {
						found = true
						d.EvidenceRefs = append(d.EvidenceRefs, StagedEvidenceRef{SnapshotID: s.SnapshotID, EvidenceKey: key})
					}
				}
				if !found {
					valid = false
				}
			}
			d.Resolved = valid
			g.IdentityDecisions = append(g.IdentityDecisions, d)
			if !valid {
				add("backend_identity_conflict", "identity/"+x.ToExternalKey, "Mapping requires the same base identity, an available target and current evidence on its submitted target")
			}
		} else if c.Deletion != nil {
			x := c.Deletion
			d := DeletionDecision{Command: *x, OldEvidenceRefs: []HistoricalEvidenceRef{}}
			id, _ := stateIdentity(*base, x.RecordType, x.ExternalKey)
			valid := id != "" && id == x.ExpectedID && owned[x.RecordType+"\x00"+id]
			if id != "" {
				d.OldSubject = &HistoricalSubjectRef{RevisionID: base.Revision.ID, RecordType: x.RecordType, ID: id}
				d.OldEvidenceRefs = refs(id)
			}
			if submitted[x.RecordType+"\x00"+id] {
				valid = false
			}
			if !deletionScopeValid(s, commands) {
				valid = false
			}
			previous := evidenceBySubject[id]
			if x.RecordType == "evidence" {
				previous = nil
				for _, e := range base.Evidence {
					if e.ID == id {
						previous = []Evidence{e}
						if !submitted["node\x00"+e.SubjectID] && !submitted["edge\x00"+e.SubjectID] {
							subjectDeleted := false
							for _, other := range decisions {
								if other.Deletion != nil && other.Deletion.ExpectedID == e.SubjectID {
									subjectDeleted = true
								}
							}
							if !subjectDeleted {
								valid = false
							}
						}
					}
				}
			}
			for _, e := range previous {
				for _, file := range s.Manifest.Snapshot.Files {
					if file.Path == e.Source.File && file.AnalysisStatus != "analyzed" {
						valid = false
					}
				}
			}
			d.Resolved = valid
			g.DeletionDecisions = append(g.DeletionDecisions, d)
			if !valid {
				add("backend_unsafe_deletion", "deletion/"+x.ExternalKey, "Deletion requires compatible complete scope, verified inventory and available previous evidence locators")
			} else {
				deleted[x.RecordType+"\x00"+id] = true
			}
		}
	}
	// Reject dangling deletions before removing anything, retaining their original bundles.
	for {
		changed := false
		for _, n := range g.Nodes {
			address := "node\x00" + n.ID
			if !deleted[address] {
				continue
			}
			dangling := false
			for _, e := range g.Edges {
				if !deleted["edge\x00"+e.ID] && (e.From == n.ID || e.To == n.ID) {
					dangling = true
					break
				}
			}
			for _, child := range g.Nodes {
				if !deleted["node\x00"+child.ID] && child.ParentID != nil && *child.ParentID == n.ID {
					dangling = true
					break
				}
			}
			if hasRelationalProfile(selectedProfile(s.Profile)) {
				for _, subject := range g.Nodes {
					if !deleted["node\x00"+subject.ID] && sourceActiveReferenceTo(subject.Kind, subject.Attributes, false, n.ID) {
						dangling = true
					}
				}
				for _, subject := range g.Edges {
					if !deleted["edge\x00"+subject.ID] && sourceActiveReferenceTo(subject.Kind, subject.Attributes, true, n.ID) {
						dangling = true
					}
				}
			}
			if dangling {
				delete(deleted, address)
				changed = true
				for i := range g.DeletionDecisions {
					if g.DeletionDecisions[i].Command.ExpectedID == n.ID {
						g.DeletionDecisions[i].Resolved = false
					}
				}
				add("backend_unsafe_deletion", "deletion/"+n.ExternalKey, "Surviving edges or parent links prevent deletion")
			}
		}
		if !changed {
			break
		}
	}
	g.Nodes = slices.DeleteFunc(g.Nodes, func(n Node) bool { return deleted["node\x00"+n.ID] })
	g.Edges = slices.DeleteFunc(g.Edges, func(e Edge) bool { return deleted["edge\x00"+e.ID] })
	g.Evidence = slices.DeleteFunc(g.Evidence, func(e Evidence) bool {
		return deleted["evidence\x00"+e.ID] || deleted["node\x00"+e.SubjectID] || deleted["edge\x00"+e.SubjectID]
	})
	// Deletion never cascades to edges or parent/contains references.
	for _, e := range g.Edges {
		if deleted["node\x00"+e.From] || deleted["node\x00"+e.To] {
			add("backend_unsafe_deletion", "edges/"+e.ID, "Explicitly remove or rebind every edge to the deleted subject")
		}
	}
	for _, n := range g.Nodes {
		if n.ParentID != nil && deleted["node\x00"+*n.ParentID] {
			add("backend_unsafe_deletion", "nodes/"+n.ID, "Parent reference prevents deletion")
		}
	}
	if !inventoryCountsValid(s, commands) {
		add("backend_unsafe_deletion", "inventory", "Complete inventory counts must match the manifest and submitted endpoint/datastore contributions")
	}
	if selectedProfile(s.Profile) == LineageProfile {
		markLineageEndpointStaleness(g)
	}
	propagateStaleness(g)
	if hasRelationalProfile(selectedProfile(s.Profile)) {
		for i := range g.Evidence {
			if g.Evidence[i].Freshness != nil {
				g.Evidence[i].Freshness.ConfirmedSnapshotID = g.Evidence[i].Source.SnapshotID
			}
		}
	}
	usedSnapshots := map[string]bool{}
	for _, e := range g.Evidence {
		usedSnapshots[e.Source.SnapshotID] = true
	}
	for _, n := range g.Nodes {
		if n.Freshness != nil {
			usedSnapshots[n.Freshness.ConfirmedSnapshotID] = true
		}
	}
	for _, e := range g.Edges {
		if e.Freshness != nil {
			usedSnapshots[e.Freshness.ConfirmedSnapshotID] = true
		}
	}
	for _, src := range base.Sources {
		if usedSnapshots[src.ID] && src.ID != s.SnapshotID {
			src.Role = "retained_provenance"
			g.Sources = append(g.Sources, src)
		}
	}
	slices.SortFunc(g.Nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(g.Edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(g.Evidence, func(a, b Evidence) int { return strings.Compare(a.ID, b.ID) })
	after := RevisionState{Revision: Revision{ArtifactPins: base.Revision.ArtifactPins}, APIArtifactContext: base.APIArtifactContext, Nodes: g.Nodes, Edges: g.Edges, Evidence: g.Evidence, Sources: g.Sources, Inventory: s.Inventory}
	g.SourceChanges = sourceChanges(*base, after)
	delta, err := CompareRevisionStates(ctx, *base, after)
	if err != nil {
		return err
	}
	g.ComparisonSummary = &delta.Summary
	return nil
}

func inventoryCountsValid(s *ImportSession, commands []ImportCommand) bool {
	counts := map[string]int64{"files": int64(len(s.Manifest.Snapshot.Files)), "endpoints": 0, "datastores": 0}
	for _, c := range commands {
		if c.Node != nil {
			switch c.Node.Kind {
			case "http_operation":
				counts["endpoints"]++
			case "datastore":
				counts["datastores"]++
			}
		}
	}
	for _, x := range s.Inventory {
		expected, ok := counts[x.Category]
		if ok && x.Status == "complete" && (x.Denominator == nil || *x.Denominator != x.KnownCount || x.KnownCount != expected) {
			return false
		}
	}
	return true
}
func deletionScopeValid(s *ImportSession, commands []ImportCommand) bool {
	if s.GraphScope == nil || s.GraphScope.Status != "complete" || s.Manifest.Snapshot.Consistency != "verified" || !inventoryCountsValid(s, commands) {
		return false
	}
	for _, category := range []string{"files", "endpoints", "datastores"} {
		found := false
		for _, x := range s.Inventory {
			if x.Category == category && x.Status == "complete" && x.Denominator != nil && *x.Denominator == x.KnownCount {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func propagateStaleness(g *graphCandidate) {
	fresh := map[string]*AssertionFreshness{}
	dependents := map[string][]string{}
	for _, n := range g.Nodes {
		fresh[n.ID] = n.Freshness
		if refs, err := sourceAttributeReferences(n.Kind, n.Attributes, false, true); err == nil {
			for _, ref := range refs {
				if ref.Kind != "evidence" && ref.HistoricalRevisionID == "" {
					dependents[ref.ID] = append(dependents[ref.ID], n.ID)
				}
			}
		}
		if n.ParentID != nil {
			dependents[*n.ParentID] = append(dependents[*n.ParentID], n.ID)
		}
	}
	for _, e := range g.Edges {
		fresh[e.ID] = e.Freshness
		if refs, err := sourceAttributeReferences(e.Kind, e.Attributes, true, true); err == nil {
			for _, ref := range refs {
				if ref.Kind != "evidence" && ref.HistoricalRevisionID == "" {
					dependents[ref.ID] = append(dependents[ref.ID], e.ID)
				}
			}
		}
		dependents[e.From] = append(dependents[e.From], e.ID)
		dependents[e.To] = append(dependents[e.To], e.ID)
		if e.Kind == "contains" {
			dependents[e.ID] = append(dependents[e.ID], e.To)
			dependents[e.From] = append(dependents[e.From], e.To)
		}
	}
	queue := []string{}
	for id, f := range fresh {
		if f != nil && f.Status == "stale" {
			queue = append(queue, id)
		}
	}
	// Each record enters this queue at most once; cycles terminate naturally.
	for i := 0; i < len(queue); i++ {
		for _, id := range dependents[queue[i]] {
			if f := fresh[id]; f != nil && f.Status != "stale" {
				f.Status = "stale"
				f.Reasons = []string{"dependency_stale"}
				queue = append(queue, id)
			}
		}
	}
	for i := range g.Evidence {
		if f := fresh[g.Evidence[i].SubjectID]; f != nil {
			if previous := g.Evidence[i].Freshness; f.Status == "current" && previous != nil && previous.Status == "stale" {
				continue
			}
			copyFresh := *f
			g.Evidence[i].Freshness = &copyFresh
		}
	}
}
func finishReconciliation(s *ImportSession, g *graphCandidate) {
	for _, n := range g.Nodes {
		if n.Freshness != nil && n.Freshness.Status == "stale" {
			g.StaleCounts.Nodes++
		}
	}
	for _, e := range g.Edges {
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			g.StaleCounts.Edges++
		}
	}
	for _, e := range g.Evidence {
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			g.StaleCounts.Evidence++
		}
	}
	if g.StaleCounts.Nodes+g.StaleCounts.Edges+g.StaleCounts.Evidence > 0 {
		g.ReconciliationGaps = append(g.ReconciliationGaps, "Assertions not reobserved in this snapshot remain stale.")
	}
	if s.GraphScope != nil && s.GraphScope.Status == "partial" {
		g.ReconciliationGaps = append(g.ReconciliationGaps, s.GraphScope.Gaps...)
	}
	slices.Sort(g.ReconciliationGaps)
	g.ReconciliationGaps = slices.Compact(g.ReconciliationGaps)
	if len(g.ReconciliationGaps) > 0 {
		g.Coverage.Status = "partial"
		g.Coverage.Gaps = append(g.Coverage.Gaps, g.ReconciliationGaps...)
	}
}
func sourceIDs(sources []SourceSnapshot) []string {
	out := []string{}
	for _, s := range sources {
		out = append(out, s.ID)
	}
	return out
}
