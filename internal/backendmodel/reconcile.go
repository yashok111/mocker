package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
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
	if err := q.QueryRowContext(ctx, `SELECT document FROM backend_revisions_documents WHERE project_id=? AND id=?`, pid, rid).Scan(&doc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound()
		}
		return nil, err
	}
	state := &RevisionState{Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}, Sources: []SourceSnapshot{}, Inventory: []InventoryItem{}}
	if err := json.Unmarshal([]byte(doc), &state.Revision); err != nil {
		return nil, err
	}
	if err := q.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources_documents WHERE revision_id=?`, rid).Scan(&doc); err == nil {
		var c RevisionCoverage
		if err := json.Unmarshal([]byte(doc), &c); err != nil {
			return nil, err
		}
		state.Sources = c.Snapshots
		state.Inventory = c.Inventory
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if state.Revision.SchemaVersion != ComposedSchemaVersion && len(state.Sources) == 1 && state.Sources[0].Role == "" {
		state.Sources[0].Role = "primary"
	}
	frozen, err := loadVersionedArtifactContext(ctx, q, rid, state.Revision.ArtifactPins)
	if err != nil {
		return nil, err
	}
	if frozen != nil {
		state.ArtifactContext = frozen.Legacy
		state.ArtifactContextV3 = frozen.V3
		state.APIArtifactContext = legacyArtifactContext(frozen.Legacy)
	}
	if frozen == nil && slices.ContainsFunc(state.Revision.ArtifactPins, func(pin ArtifactPin) bool { return pin.Kind == "api_design" || pin.Kind == "design_scenario" }) {
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
	rows, err := q.QueryContext(ctx, `SELECT record_type,document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? ORDER BY record_type,id`, pid, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
	if state.Revision.SchemaVersion == ComposedSchemaVersion {
		return
	}
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
	o := &graphOverlay{ctx: ctx, q: q, s: s, base: base, g: g, commands: commands, diagnostics: diagnostics}
	o.stampSubmitted(ids)
	if s.Mode != "reconcile" {
		return nil
	}
	decisions, err := loadDecisions(ctx, q, s.ID)
	if err != nil {
		return err
	}
	o.decisions = decisions
	o.mappedTargets = map[string]bool{}
	for _, c := range decisions {
		if c.Identity != nil {
			o.mappedTargets[c.Identity.RecordType+"\x00"+c.Identity.ToExternalKey] = true
		}
	}
	o.checkBaseOwnership()
	// Revalidate every acknowledged allocation against this exact base and the registry.
	if err := o.revalidateAllocations(ids); err != nil {
		return err
	}
	o.retainUnsubmitted()
	if hasRelationalProfile(selectedProfile(s.Profile)) {
		overlayRelationalFacets(base, g, o.submitted, diagnostics)
	}
	if err := o.applyDecisions(); err != nil {
		return err
	}
	// Reject dangling deletions before removing anything, retaining their original bundles.
	o.rejectDanglingDeletions()
	o.removeDeleted()
	return o.finish()
}

// graphOverlay is one reconcile overlay of a session onto its base revision.
// overlayGraph runs its phases in a fixed order; the indexes live here so
// each phase reads what the earlier ones built instead of a dozen closures
// over one long function's locals.
type graphOverlay struct {
	ctx         context.Context
	q           importReader
	s           *ImportSession
	base        *RevisionState
	g           *graphCandidate
	commands    []ImportCommand
	diagnostics *[]ImportDiagnostic

	submitted         map[string]bool // "<type>\x00<id>" upserted by this session
	submittedKeys     map[string]bool // "<type>\x00<external key>" upserted by this session
	decisions         []ImportCommand
	mappedTargets     map[string]bool
	owned             map[string]bool
	evidenceBySubject map[string][]Evidence
	files             map[string]ManifestFile
	deleted           map[string]bool
}

func (o *graphOverlay) add(code, path, message string) {
	*o.diagnostics = append(*o.diagnostics, ImportDiagnostic{Code: code, Path: path, Message: message})
}

// stampSubmitted indexes what the session submitted and stamps every
// submitted record as current and owned by this session's partition.
func (o *graphOverlay) stampSubmitted(ids map[string]string) {
	s, g := o.s, o.g
	owner := &AssertionOwnership{RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace, Profile: GraphProfile}
	current := func() *AssertionFreshness {
		return &AssertionFreshness{Status: "current", ConfirmedSnapshotID: s.SnapshotID, Reasons: []string{}}
	}
	g.Sources = []SourceSnapshot{{Role: "primary", ID: s.SnapshotID, RepositoryID: s.RepositoryID, ManifestHash: s.ManifestHash, Provider: s.Manifest.Provider, SnapshotManifest: s.Manifest.Snapshot}}
	g.SourceChanges = []SourceChange{}
	g.IdentityDecisions = []IdentityDecision{}
	g.DeletionDecisions = []DeletionDecision{}
	g.ReconciliationGaps = []string{}
	o.submitted = map[string]bool{}
	o.submittedKeys = map[string]bool{}
	usedIDs := map[string]bool{}
	for _, c := range o.commands {
		typ, key, _ := commandAddress(c)
		id := ids[typ+"\x00"+key]
		o.submitted[typ+"\x00"+id] = true
		o.submittedKeys[typ+"\x00"+key] = true
		if usedIDs[typ+"\x00"+id] {
			o.add("backend_identity_conflict", typ+"/"+key, "Multiple active keys for one UUID")
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
		assignRelationalOwnership(o.base, g, s)
	}
}

// sessionOwns reports whether a base assertion sits in the ownership
// partition this session may rewrite: its repository, its provider
// namespace, and a profile the session selected.
func sessionOwns(s *ImportSession, o *AssertionOwnership) bool {
	if o == nil || o.RepositoryID != s.RepositoryID || o.ProviderNamespace != s.Manifest.Provider.Namespace {
		return false
	}
	profile := selectedProfile(s.Profile)
	switch o.Profile {
	case GraphProfile:
		return true
	case RelationalProfile:
		return hasRelationalProfile(profile)
	case RuntimeProfile:
		return hasRuntimeProfile(profile)
	case LineageProfile:
		return hasLineageProfile(profile)
	case EventsProfile:
		return profile == EventsProfile
	}
	return false
}

func (o *graphOverlay) checkBaseOwnership() {
	o.owned = map[string]bool{}
	checkOwner := func(typ, id string, owner *AssertionOwnership) {
		valid := sessionOwns(o.s, owner)
		o.owned[typ+"\x00"+id] = valid
		if !valid {
			o.add("backend_unsupported_scope", typ+"/"+id, "Base assertion belongs to another ownership partition")
		}
	}
	for _, n := range o.base.Nodes {
		checkOwner("node", n.ID, n.Ownership)
	}
	for _, e := range o.base.Edges {
		checkOwner("edge", e.ID, e.Ownership)
	}
	for _, e := range o.base.Evidence {
		checkOwner("evidence", e.ID, e.Ownership)
	}
}

func (o *graphOverlay) revalidateAllocations(ids map[string]string) error {
	baseNodes := map[string]Node{}
	for _, n := range o.base.Nodes {
		baseNodes[n.ID] = n
	}
	baseEdges := map[string]Edge{}
	for _, e := range o.base.Edges {
		baseEdges[e.ID] = e
	}
	for _, c := range o.commands {
		typ, key, _ := commandAddress(c)
		id := ids[typ+"\x00"+key]
		b, err := binding(o.ctx, o.q, o.s, typ, key)
		if err != nil {
			return err
		}
		if b != nil && (b.ID != id || b.State != "active" && b.State != "reserved" && !o.mappedTargets[typ+"\x00"+key]) {
			o.add("backend_identity_conflict", typ+"/"+key, "Acknowledged UUID conflicts with the current key binding")
		}
		o.checkKeyStability(c, typ, key, id, baseNodes, baseEdges)
	}
	return nil
}

// checkKeyStability rejects a submitted record that silently re-keys or
// re-kinds an identity the base already holds; a re-key needs a mapping.
func (o *graphOverlay) checkKeyStability(c ImportCommand, typ, key, id string, baseNodes map[string]Node, baseEdges map[string]Edge) {
	mapped := o.mappedTargets[typ+"\x00"+key]
	if n, ok := baseNodes[id]; ok {
		if typ == "node" && n.ID == id && n.ExternalKey != key && !mapped {
			o.add("backend_identity_conflict", typ+"/"+key, "Changing an active key requires a mapping decision")
		}
		if typ == "node" && n.ID == id && c.Node.Kind != n.Kind {
			o.add("backend_identity_conflict", typ+"/"+key, "Identity kind cannot change")
		}
	}
	if e, ok := baseEdges[id]; ok {
		if typ == "edge" && e.ID == id && e.ExternalKey != key && !mapped {
			o.add("backend_identity_conflict", typ+"/"+key, "Changing an active key requires a mapping decision")
		}
		if typ == "edge" && e.ID == id && c.Edge.Kind != e.Kind {
			o.add("backend_identity_conflict", typ+"/"+key, "Identity kind cannot change")
		}
	}
}

// staleFreshness marks a record the session did not re-observe as stale,
// with a reason for each evidence locator the new manifest no longer backs.
func (o *graphOverlay) staleFreshness(previous *AssertionFreshness, evidence []Evidence) *AssertionFreshness {
	f := &AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
	if previous != nil {
		f.ConfirmedSnapshotID = previous.ConfirmedSnapshotID
	}
	for _, e := range evidence {
		file, ok := o.files[e.Source.File]
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

// retainUnsubmitted carries every base record the session did not
// resubmit into the candidate as stale, together with its evidence.
func (o *graphOverlay) retainUnsubmitted() {
	o.evidenceBySubject = map[string][]Evidence{}
	for _, e := range o.base.Evidence {
		o.evidenceBySubject[e.SubjectID] = append(o.evidenceBySubject[e.SubjectID], e)
	}
	o.files = map[string]ManifestFile{}
	for _, file := range o.s.Manifest.Snapshot.Files {
		o.files[file.Path] = file
	}
	g := o.g
	retainedSubjects := map[string]*AssertionFreshness{}
	for _, n := range o.base.Nodes {
		if !o.submitted["node\x00"+n.ID] {
			n.Freshness = o.staleFreshness(n.Freshness, o.evidenceBySubject[n.ID])
			g.Nodes = append(g.Nodes, n)
			retainedSubjects[n.ID] = n.Freshness
		}
	}
	for _, e := range o.base.Edges {
		if !o.submitted["edge\x00"+e.ID] {
			e.Freshness = o.staleFreshness(e.Freshness, o.evidenceBySubject[e.ID])
			g.Edges = append(g.Edges, e)
			retainedSubjects[e.ID] = e.Freshness
		}
	}
	for _, e := range g.Evidence {
		if _, retained := retainedSubjects[e.SubjectID]; retained {
			o.add("backend_graph_invalid", "evidence/"+e.ID, "Evidence requires an explicitly upserted subject")
		}
	}
	for _, e := range o.base.Evidence {
		if f, ok := retainedSubjects[e.SubjectID]; ok && !o.submitted["evidence\x00"+e.ID] {
			e.Freshness = f
			g.Evidence = append(g.Evidence, e)
		}
	}
}

func (o *graphOverlay) historicalRefs(id string) []HistoricalEvidenceRef {
	out := []HistoricalEvidenceRef{}
	for _, e := range o.base.Evidence {
		if e.SubjectID == id || e.ID == id {
			out = append(out, HistoricalEvidenceRef{RevisionID: o.base.Revision.ID, EvidenceID: e.ID})
		}
	}
	return out
}

func (o *graphOverlay) applyDecisions() error {
	o.deleted = map[string]bool{}
	for _, c := range o.decisions {
		if err := o.ctx.Err(); err != nil {
			return err
		}
		if c.Identity != nil {
			if err := o.identityDecision(c.Identity); err != nil {
				return err
			}
		} else if c.Deletion != nil {
			o.deletionDecision(c.Deletion)
		}
	}
	return nil
}

func (o *graphOverlay) identityDecision(x *ImportIdentityMap) error {
	d := IdentityDecision{Command: *x, OldEvidenceRefs: []HistoricalEvidenceRef{}, EvidenceRefs: []StagedEvidenceRef{}}
	id, _ := stateIdentity(*o.base, x.RecordType, x.FromExternalKey)
	valid := id != "" && id == x.ExpectedID && o.owned[x.RecordType+"\x00"+id]
	if id != "" {
		d.OldSubject = &HistoricalSubjectRef{RevisionID: o.base.Revision.ID, RecordType: x.RecordType, ID: id}
		d.OldEvidenceRefs = o.historicalRefs(id)
	}
	target, err := binding(o.ctx, o.q, o.s, x.RecordType, x.ToExternalKey)
	if err != nil {
		return err
	}
	if target != nil && (target.ID != x.ExpectedID || target.State != "retired") {
		valid = false
	}
	if !o.submittedKeys[x.RecordType+"\x00"+x.ToExternalKey] || o.submittedKeys[x.RecordType+"\x00"+x.FromExternalKey] {
		valid = false
	}
	refs, allFound := o.stagedEvidenceRefs(x.EvidenceKeys, id)
	d.EvidenceRefs = append(d.EvidenceRefs, refs...)
	if !allFound {
		valid = false
	}
	d.Resolved = valid
	o.g.IdentityDecisions = append(o.g.IdentityDecisions, d)
	if !valid {
		o.add("backend_identity_conflict", "identity/"+x.ToExternalKey, "Mapping requires the same base identity, an available target and current evidence on its submitted target")
	}
	return nil
}

// stagedEvidenceRefs finds the current evidence a mapping cites on its
// submitted target, and reports whether every cited key was found.
func (o *graphOverlay) stagedEvidenceRefs(keys []string, id string) ([]StagedEvidenceRef, bool) {
	var refs []StagedEvidenceRef
	allFound := true
	for _, key := range keys {
		found := false
		for _, e := range o.g.Evidence {
			if e.ExternalKey == key && e.SubjectID == id && e.Source.SnapshotID == o.s.SnapshotID && o.submitted["evidence\x00"+e.ID] {
				found = true
				refs = append(refs, StagedEvidenceRef{SnapshotID: o.s.SnapshotID, EvidenceKey: key})
			}
		}
		if !found {
			allFound = false
		}
	}
	return refs, allFound
}

func (o *graphOverlay) deletionDecision(x *ImportDeletion) {
	d := DeletionDecision{Command: *x, OldEvidenceRefs: []HistoricalEvidenceRef{}}
	id, _ := stateIdentity(*o.base, x.RecordType, x.ExternalKey)
	valid := id != "" && id == x.ExpectedID && o.owned[x.RecordType+"\x00"+id]
	if id != "" {
		d.OldSubject = &HistoricalSubjectRef{RevisionID: o.base.Revision.ID, RecordType: x.RecordType, ID: id}
		d.OldEvidenceRefs = o.historicalRefs(id)
	}
	if o.submitted[x.RecordType+"\x00"+id] {
		valid = false
	}
	if !deletionScopeValid(o.s, o.commands) {
		valid = false
	}
	previous := o.evidenceBySubject[id]
	if x.RecordType == "evidence" {
		var subjectKept bool
		previous, subjectKept = o.deletedEvidence(id)
		if !subjectKept {
			valid = false
		}
	}
	if !o.locatorsAvailable(previous) {
		valid = false
	}
	d.Resolved = valid
	o.g.DeletionDecisions = append(o.g.DeletionDecisions, d)
	if !valid {
		o.add("backend_unsafe_deletion", "deletion/"+x.ExternalKey, "Deletion requires compatible complete scope, verified inventory and available previous evidence locators")
	} else {
		o.deleted[x.RecordType+"\x00"+id] = true
	}
}

// deletedEvidence returns the base evidence record a deletion names, and
// false when its subject is neither resubmitted nor deleted too: evidence
// may only leave with, or be replaced on, its subject.
func (o *graphOverlay) deletedEvidence(id string) ([]Evidence, bool) {
	var previous []Evidence
	ok := true
	for _, e := range o.base.Evidence {
		if e.ID != id {
			continue
		}
		previous = []Evidence{e}
		if !o.submitted["node\x00"+e.SubjectID] && !o.submitted["edge\x00"+e.SubjectID] && !o.subjectDeleted(e.SubjectID) {
			ok = false
		}
	}
	return previous, ok
}

func (o *graphOverlay) subjectDeleted(subjectID string) bool {
	deleted := false
	for _, other := range o.decisions {
		if other.Deletion != nil && other.Deletion.ExpectedID == subjectID {
			deleted = true
		}
	}
	return deleted
}

// locatorsAvailable reports whether every previous evidence file was
// analyzed in this snapshot; a deletion is only proved where we looked.
func (o *graphOverlay) locatorsAvailable(previous []Evidence) bool {
	ok := true
	for _, e := range previous {
		for _, file := range o.s.Manifest.Snapshot.Files {
			if file.Path == e.Source.File && file.AnalysisStatus != "analyzed" {
				ok = false
			}
		}
	}
	return ok
}

func (o *graphOverlay) rejectDanglingDeletions() {
	for {
		changed := false
		for _, n := range o.g.Nodes {
			address := "node\x00" + n.ID
			if o.deleted[address] && o.nodeDeletionDangles(n) {
				o.keepDeleted(address, n.ID, "deletion/"+n.ExternalKey, "Surviving edges or parent links prevent deletion")
				changed = true
			}
		}

		// Edge assertions can be active nested dependencies (event route proof).
		// Reject their removal through the same closure as node dependencies.
		if selectedProfile(o.s.Profile) == EventsProfile {
			for _, e := range o.g.Edges {
				address := "edge\x00" + e.ID
				if o.deleted[address] && o.edgeDeletionDangles(e) {
					o.keepDeleted(address, e.ID, "deletion/"+e.ExternalKey, "Surviving nested edge references prevent deletion")
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
}

// keepDeleted withdraws one deletion that would leave a dangling reference
// and unresolves the decision that asked for it.
func (o *graphOverlay) keepDeleted(address, id, path, message string) {
	delete(o.deleted, address)
	for i := range o.g.DeletionDecisions {
		if o.g.DeletionDecisions[i].Command.ExpectedID == id {
			o.g.DeletionDecisions[i].Resolved = false
		}
	}
	o.add("backend_unsafe_deletion", path, message)
}

func (o *graphOverlay) nodeDeletionDangles(n Node) bool {
	g := o.g
	dangling := false
	for _, e := range g.Edges {
		if !o.deleted["edge\x00"+e.ID] && (e.From == n.ID || e.To == n.ID) {
			dangling = true
			break
		}
	}
	for _, child := range g.Nodes {
		if !o.deleted["node\x00"+child.ID] && child.ParentID != nil && *child.ParentID == n.ID {
			dangling = true
			break
		}
	}
	if hasRelationalProfile(selectedProfile(o.s.Profile)) {
		for _, subject := range g.Nodes {
			if !o.deleted["node\x00"+subject.ID] && sourceActiveReferenceTo(subject.Kind, subject.Attributes, false, n.ID) {
				dangling = true
			}
		}
		for _, subject := range g.Edges {
			if !o.deleted["edge\x00"+subject.ID] && sourceActiveReferenceTo(subject.Kind, subject.Attributes, true, n.ID) {
				dangling = true
			}
		}
	}
	return dangling
}

func (o *graphOverlay) edgeDeletionDangles(e Edge) bool {
	dangling := false
	for _, n := range o.g.Nodes {
		if !o.deleted["node\x00"+n.ID] && sourceActiveRecordReferenceTo(n.Kind, n.Attributes, false, "edge", e.ID) {
			dangling = true
		}
	}
	for _, subject := range o.g.Edges {
		if !o.deleted["edge\x00"+subject.ID] && sourceActiveRecordReferenceTo(subject.Kind, subject.Attributes, true, "edge", e.ID) {
			dangling = true
		}
	}
	return dangling
}

func (o *graphOverlay) removeDeleted() {
	g, deleted := o.g, o.deleted
	g.Nodes = slices.DeleteFunc(g.Nodes, func(n Node) bool { return deleted["node\x00"+n.ID] })
	g.Edges = slices.DeleteFunc(g.Edges, func(e Edge) bool { return deleted["edge\x00"+e.ID] })
	g.Evidence = slices.DeleteFunc(g.Evidence, func(e Evidence) bool {
		return deleted["evidence\x00"+e.ID] || deleted["node\x00"+e.SubjectID] || deleted["edge\x00"+e.SubjectID]
	})
	// Deletion never cascades to edges or parent/contains references.
	for _, e := range g.Edges {
		if deleted["node\x00"+e.From] || deleted["node\x00"+e.To] {
			o.add("backend_unsafe_deletion", "edges/"+e.ID, "Explicitly remove or rebind every edge to the deleted subject")
		}
	}
	for _, n := range g.Nodes {
		if n.ParentID != nil && deleted["node\x00"+*n.ParentID] {
			o.add("backend_unsafe_deletion", "nodes/"+n.ID, "Parent reference prevents deletion")
		}
	}
}

func (o *graphOverlay) finish() error {
	s, g, base := o.s, o.g, o.base
	if !inventoryCountsValid(s, o.commands) {
		o.add("backend_unsafe_deletion", "inventory", "Complete inventory counts must match the manifest and submitted endpoint/datastore contributions")
	}
	if hasLineageProfile(selectedProfile(s.Profile)) {
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
	retainProvenanceSources(base, g, s.SnapshotID)
	slices.SortFunc(g.Nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(g.Edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(g.Evidence, func(a, b Evidence) int { return strings.Compare(a.ID, b.ID) })
	after := RevisionState{Revision: Revision{ArtifactPins: base.Revision.ArtifactPins}, APIArtifactContext: base.APIArtifactContext, ArtifactContext: base.ArtifactContext, Nodes: g.Nodes, Edges: g.Edges, Evidence: g.Evidence, Sources: g.Sources, Inventory: s.Inventory}
	g.SourceChanges = sourceChanges(*base, after)
	delta, err := CompareRevisionStates(o.ctx, *base, after)
	if err != nil {
		return err
	}
	g.ComparisonSummary = &delta.Summary
	return nil
}

// retainProvenanceSources keeps every base snapshot some surviving record
// still confirms, so its provenance stays resolvable in the new revision.
func retainProvenanceSources(base *RevisionState, g *graphCandidate, snapshotID string) {
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
		if usedSnapshots[src.ID] && src.ID != snapshotID {
			src.Role = "retained_provenance"
			g.Sources = append(g.Sources, src)
		}
	}
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
	fresh, dependents := freshnessDependents(g)
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

// freshnessDependents indexes each record's freshness and, for every
// record, the records whose currentness depends on it: active attribute
// references, parent links, edge endpoints and containment.
func freshnessDependents(g *graphCandidate) (map[string]*AssertionFreshness, map[string][]string) {
	fresh := map[string]*AssertionFreshness{}
	dependents := map[string][]string{}
	for _, n := range g.Nodes {
		fresh[n.ID] = n.Freshness
		addReferenceDependents(dependents, n.Kind, n.Attributes, false, n.ID)
		if n.ParentID != nil {
			dependents[*n.ParentID] = append(dependents[*n.ParentID], n.ID)
		}
	}
	for _, e := range g.Edges {
		fresh[e.ID] = e.Freshness
		addReferenceDependents(dependents, e.Kind, e.Attributes, true, e.ID)
		dependents[e.From] = append(dependents[e.From], e.ID)
		dependents[e.To] = append(dependents[e.To], e.ID)
		if e.Kind == "contains" {
			dependents[e.ID] = append(dependents[e.ID], e.To)
			dependents[e.From] = append(dependents[e.From], e.To)
		}
	}
	return fresh, dependents
}

// addReferenceDependents records id as a dependent of every current,
// non-evidence record its attributes reference. Unreadable attributes add
// nothing: validation reports them, staleness does not guess.
func addReferenceDependents(dependents map[string][]string, kind string, attrs map[string]jsontext.Value, edge bool, id string) {
	refs, err := sourceAttributeReferences(kind, attrs, edge, true)
	if err != nil {
		return
	}
	for _, ref := range refs {
		if ref.Kind != "evidence" && ref.HistoricalRevisionID == "" {
			dependents[ref.ID] = append(dependents[ref.ID], id)
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
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.ID)
	}
	return out
}
