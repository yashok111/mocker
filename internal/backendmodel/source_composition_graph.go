package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
)

type composedGraphPreparation struct {
	session            *ImportSession
	base               *SourceGraphSnapshot
	candidate          *composedCandidate
	commands           []ImportCommand
	ids                map[string]string
	decisions          []SourceDecision
	legacyDecisions    []ImportCommand
	claims             map[string]ProviderAssertion
	originalClaims     map[string]ProviderAssertion
	retainedFacetProof map[string]bool
	selectedKeys       map[string]string
	fresh              map[string]bool
	currentness        map[string]SourceClaimCurrentness
	submittedEvidence  map[string]bool
}

func prepareComposedGraph(ctx context.Context, q importReader, s *ImportSession) (*composedCandidate, []ImportDiagnostic, error) {
	if err := requireComposedBase(ctx, q, s); err != nil {
		return nil, nil, err
	}
	base, err := loadComposedBase(ctx, q, s.ProjectID, s.BaseRevisionID)
	if err != nil {
		return nil, nil, err
	}
	p := newComposedGraphPreparation(s, base)
	diagnostics, err := p.prepareClaims(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	graph, g := p.candidate.Source, p.candidate.Graph
	if err := sourceSemanticCurrentness(graph, p.currentness); err != nil {
		return nil, nil, err
	}
	p.applyIncrementalDependencyGaps()
	graph.Currentness = make([]SourceClaimCurrentness, 0, len(graph.Assertions))
	for _, a := range graph.Assertions {
		graph.Currentness = append(graph.Currentness, p.currentness[sourceAssertionKey(a)])
	}
	projection, err := sourceResolutionProjection(base, p.candidate, p.decisions)
	if err != nil {
		return nil, nil, err
	}
	changedClaims := applySourceEffectiveDependencyGaps(graph, projection, p.currentness)
	if err := p.includeIncrementalProjectionDependencies(ctx, changedClaims); err != nil {
		return nil, nil, err
	}
	// The read closure also discovers logical callers. Apply those seeds before
	// reverse currentness propagation and strict conflict/scope publication.
	p.applyIncrementalDependencyGaps()
	graph.Currentness = sourceDependencyCurrentness(graph, p.currentness)
	p.finalizeIncrementalGaps()
	diagnostics = append(diagnostics, sourceWholeInventoryDiagnostics(s, graph.Assertions)...)
	if err := validateComposedHistory(ctx, q, s, graph); err != nil {
		return nil, nil, err
	}
	graph.Identities = sourceIdentities(graph.Assertions)
	conflicts, err := mergeSourceAssertions(base, p.candidate, p.decisions)
	if err != nil {
		return nil, nil, err
	}
	diagnostics = append(diagnostics, conflicts...)
	if err := p.appendCurrentnessGaps(); err != nil {
		return nil, nil, err
	}
	if err := p.materializeEvidence(); err != nil {
		return nil, nil, err
	}
	retainSourceSnapshots(graph)
	g.Sources = graph.SourceVector.Snapshots
	p.reviewChanges()
	structure, err := ValidateSourceStructure(ctx, SourceStructuralGraph{
		SchemaVersion: ComposedSchemaVersion, Nodes: g.Nodes, Edges: g.Edges,
	})
	if err != nil {
		return nil, nil, err
	}
	diagnostics = append(diagnostics, structure...)
	if err := p.finalizeContent(); err != nil {
		return nil, nil, err
	}
	return p.candidate, diagnostics, nil
}

// reviewChanges fills the preview-only review lists: the changed files of
// every active partition and the staged map_identity decisions. They were
// left empty on the composed path, so sourceChangeCount and
// identityDecisionCount read 0 and get_backend_import_changes
// recordType=source/identity returned nothing while sync.md tells the
// reviewer to page exactly those lists (review 2026-10-06, F57). Neither
// list enters the source6 candidate hash (source6CandidateJSON) or the
// revision decisions document (source6RevisionDecisions), so this changes
// what the reviewer sees, not what is committed. A map_identity that fails
// its checks is a fatal error earlier in preparation, so every listed
// decision is resolved. staleCounts and comparisonSummary stay unset:
// staleCounts is stored in the revision coverage, so filling it would
// change committed bytes — deferred, see the fix report.
func (p *composedGraphPreparation) reviewChanges() {
	g := p.candidate.Graph
	after := RevisionState{Revision: Revision{SchemaVersion: ComposedSchemaVersion}, Sources: g.Sources}
	g.SourceChanges = composedSourceChanges(p.base.State, after)
	g.IdentityDecisions = []IdentityDecision{}
	for _, d := range p.legacyDecisions {
		x := d.Identity
		if x == nil {
			continue
		}
		decision := IdentityDecision{Command: *x, Resolved: true, OldSubject: &HistoricalSubjectRef{RevisionID: p.session.BaseRevisionID, RecordType: x.RecordType, ID: x.ExpectedID}, OldEvidenceRefs: []HistoricalEvidenceRef{}, EvidenceRefs: []StagedEvidenceRef{}}
		for _, key := range x.EvidenceKeys {
			decision.EvidenceRefs = append(decision.EvidenceRefs, StagedEvidenceRef{SnapshotID: p.session.SnapshotID, EvidenceKey: key})
		}
		g.IdentityDecisions = append(g.IdentityDecisions, decision)
	}
}

func newComposedGraphPreparation(s *ImportSession, base *SourceGraphSnapshot) *composedGraphPreparation {
	g := &graphCandidate{
		Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}, SourceChanges: []SourceChange{},
		IdentityDecisions: []IdentityDecision{}, DeletionDecisions: []DeletionDecision{}, ReconciliationGaps: []string{},
	}
	graph := &SourceGraphSnapshot{
		rawAssertions: maps.Clone(base.rawAssertions),
		State:         base.State,
		SourceVector: &SourceVector{
			DocumentVersion: "source-vector-v1",
			Partitions:      slices.Clone(base.SourceVector.Partitions), Snapshots: slices.Clone(base.SourceVector.Snapshots),
		},
		Assertions: []ProviderAssertion{}, Selections: []SourceAssertionResolution{},
		Currentness: []SourceClaimCurrentness{}, Identities: []QualifiedSourceIdentity{},
		LegacyProofBases: slices.Clone(base.LegacyProofBases), RawEvidence: maps.Clone(base.RawEvidence),
		legacyBasisBytes: base.legacyBasisBytes,
	}
	c := &composedCandidate{Graph: g, Source: graph}
	g.Composed = c
	return &composedGraphPreparation{
		session: s, base: base, candidate: c,
		claims: map[string]ProviderAssertion{}, originalClaims: map[string]ProviderAssertion{},
		retainedFacetProof: map[string]bool{}, selectedKeys: map[string]string{}, fresh: map[string]bool{},
		currentness: map[string]SourceClaimCurrentness{}, submittedEvidence: map[string]bool{},
	}
}

func (p *composedGraphPreparation) prepareClaims(ctx context.Context, q importReader) ([]ImportDiagnostic, error) {
	if err := p.loadStaging(ctx, q); err != nil {
		return nil, err
	}
	if err := p.prepareIncrementalScope(ctx); err != nil {
		return nil, err
	}
	p.applyAliases()
	if err := p.stageAssertions(); err != nil {
		return nil, err
	}
	diagnostics := p.applyDeletions()
	if err := p.normalizeAssertions(); err != nil {
		return nil, err
	}
	classifySourceClaimOwnership(p.claims, p.base.Assertions, p.fresh)
	if err := p.stageEvidence(); err != nil {
		return nil, err
	}
	p.updateSourceVector()
	if err := p.retainEvidence(); err != nil {
		return nil, err
	}
	// Intrinsic hashes must exist before any candidate-local target hash is bound.
	if err := p.hashAssertions(); err != nil {
		return nil, err
	}
	if err := p.bindCandidateReferences(); err != nil {
		return nil, err
	}
	if err := p.validateFreshReferenceContexts(); err != nil {
		return nil, err
	}
	err := validateSourceDecisionSubjects(
		p.session, p.base, p.candidate.Source, p.decisions, p.legacyDecisions, p.fresh,
	)
	if err != nil {
		diagnostics = append(diagnostics, ImportDiagnostic{
			Code: "backend_identity_incomplete", Path: "claimIdentity", Message: err.Error(),
		})
	}
	return diagnostics, nil
}

func (p *composedGraphPreparation) loadStaging(ctx context.Context, q importReader) error {
	var err error
	p.commands, p.ids, err = sourceStaging(ctx, q, p.session.ID)
	if err != nil {
		return err
	}
	p.decisions, err = loadSourceDecisions(ctx, q, p.session.ID)
	if err != nil {
		return err
	}
	p.candidate.Decisions = p.decisions
	p.legacyDecisions, err = loadDecisions(ctx, q, p.session.ID)
	p.candidate.LegacyDecisions = p.legacyDecisions
	if err == nil {
		p.candidate.BatchCommitments, err = loadSourceBatchCommitments(ctx, q, p.session.ID)
	}
	if err != nil {
		return err
	}
	p.seedBaseClaims()
	// Reservations win over old keys only for acknowledged explicit alias decisions.
	_, reservations, err := sourceStaging(ctx, q, p.session.ID)
	if err != nil {
		return err
	}
	maps.Copy(p.ids, reservations)
	return nil
}

func (p *composedGraphPreparation) selected(a ProviderAssertion) bool {
	return a.Owner.RepositoryID == p.session.RepositoryID && a.Owner.ProviderNamespace == p.session.Manifest.Provider.Namespace
}

func (p *composedGraphPreparation) seedBaseClaims() {
	for _, a := range p.base.Assertions {
		key := sourceAssertionKey(a)
		p.claims[key] = a
		p.originalClaims[key] = a
		p.currentness[key] = sourceCurrentness(a)
		if p.selected(a) {
			p.selectedKeys[a.RecordType+"\x00"+a.ExternalKey] = key
			p.ids[a.RecordType+"\x00"+a.ExternalKey] = a.RecordID
		}
	}
	for _, f := range p.base.Currentness {
		p.currentness[sourceClaimKey(f.RecordType, f.RecordID, f.RepositoryID, f.ProviderNamespace)] = f
	}
	for _, e := range p.base.State.Evidence {
		if e.Ownership == nil {
			continue
		}
		owner := e.Ownership
		if owner.RepositoryID == p.session.RepositoryID && owner.ProviderNamespace == p.session.Manifest.Provider.Namespace {
			if _, ok := p.ids["evidence\x00"+e.ExternalKey]; !ok {
				p.ids["evidence\x00"+e.ExternalKey] = e.ID
			}
		}
	}
}

func (p *composedGraphPreparation) applyAliases() {
	for _, d := range p.legacyDecisions {
		if d.Identity != nil {
			x := d.Identity
			oldKey := p.selectedKeys[x.RecordType+"\x00"+x.FromExternalKey]
			delete(p.claims, oldKey)
			delete(p.selectedKeys, x.RecordType+"\x00"+x.FromExternalKey)
		}
	}
}

func (p *composedGraphPreparation) stageAssertions() error {
	aliases, mappedSources := sourceIdentityAliases(p.legacyDecisions)
	for _, command := range p.commands {
		if err := validateComposedCommand(command, p.session); err != nil {
			return err
		}
		if command.Node == nil && command.Edge == nil {
			continue
		}
		typ, key, _ := commandAddress(command)
		id := p.ids[typ+"\x00"+key]
		payload := SourceAssertionPayload{RecordType: typ}
		if command.Node != nil {
			payload.Kind, payload.Name, payload.Attributes = command.Node.Kind, command.Node.Name, command.Node.Attributes
		} else {
			payload.Kind, payload.Attributes = command.Edge.Kind, command.Edge.Attributes
		}
		owner := AssertionOwnership{
			RepositoryID:      p.session.RepositoryID,
			ProviderNamespace: p.session.Manifest.Provider.Namespace,
			Profile:           sourcePayloadProfile(payload),
		}
		a := ProviderAssertion{
			RecordType: typ, RecordID: id, Owner: owner, ExternalKey: key, Payload: payload,
			Freshness: AssertionFreshness{
				Status: "current", ConfirmedSnapshotID: p.session.SnapshotID, Reasons: []string{},
			},
			EvidenceIDs: []string{}, DependencyClaims: []SourceDependencyBinding{},
			FieldCurrentness: []TypedFieldCurrentness{},
		}
		claimKey := sourceAssertionKey(a)
		if err := p.validateStagedIdentity(a, aliases, mappedSources); err != nil {
			return err
		}
		p.claims[claimKey] = a
		p.selectedKeys[typ+"\x00"+key] = claimKey
		p.fresh[claimKey] = true
	}
	return nil
}

func sourceIdentityAliases(decisions []ImportCommand) (map[string]ImportIdentityMap, map[string]bool) {
	aliases := map[string]ImportIdentityMap{}
	sources := map[string]bool{}
	for _, decision := range decisions {
		if x := decision.Identity; x != nil {
			aliases[x.RecordType+"\x00"+x.ToExternalKey] = *x
			sources[x.RecordType+"\x00"+x.FromExternalKey] = true
		}
	}
	return aliases, sources
}

func (p *composedGraphPreparation) validateStagedIdentity(a ProviderAssertion, aliases map[string]ImportIdentityMap, mappedSources map[string]bool) error {
	address := a.RecordType + "\x00" + a.ExternalKey
	if mappedSources[address] {
		return identityConflict("Mapped source cannot also be upserted")
	}
	key := sourceAssertionKey(a)
	if original, exists := p.originalClaims[key]; exists {
		if original.Payload.Kind != a.Payload.Kind {
			return identityConflict("Claim kind cannot change")
		}
		if original.ExternalKey != a.ExternalKey {
			mapping, ok := aliases[address]
			if !ok || mapping.FromExternalKey != original.ExternalKey || mapping.ExpectedID != a.RecordID {
				return identityConflict("Renaming an existing UUID requires its active identity mapping")
			}
		}
	}
	if previous, exists := p.claims[key]; exists && p.fresh[key] && previous.ExternalKey != a.ExternalKey {
		return identityConflict("Multiple active keys cannot address one selected UUID")
	}
	return nil
}

func (p *composedGraphPreparation) applyDeletions() []ImportDiagnostic {
	diagnostics := []ImportDiagnostic{}
	for _, d := range p.legacyDecisions {
		if d.Deletion == nil {
			continue
		}
		x := d.Deletion
		key := p.selectedKeys[x.RecordType+"\x00"+x.ExternalKey]
		a, exists := p.claims[key]
		valid := exists && a.RecordID == x.ExpectedID && !p.fresh[key] && sourceWholeDeletionSafe(p.session)
		if x.RecordType == "evidence" {
			valid = p.ids["evidence\x00"+x.ExternalKey] == x.ExpectedID && sourceWholeDeletionSafe(p.session)
		}
		for _, e := range p.base.State.Evidence {
			if e.SubjectID != x.ExpectedID && e.ID != x.ExpectedID {
				continue
			}
			for _, file := range p.session.Manifest.Snapshot.Files {
				if file.Path == e.Source.File && file.AnalysisStatus != "analyzed" {
					valid = false
				}
			}
		}
		p.candidate.Graph.DeletionDecisions = append(p.candidate.Graph.DeletionDecisions, DeletionDecision{
			Command: *x, Resolved: valid,
			OldSubject: &HistoricalSubjectRef{
				RevisionID: p.session.BaseRevisionID, RecordType: x.RecordType, ID: x.ExpectedID,
			},
			OldEvidenceRefs: []HistoricalEvidenceRef{},
		})
		if !valid {
			diagnostics = append(diagnostics, ImportDiagnostic{
				Code: "backend_unsafe_deletion", Path: "deletion/" + x.ExternalKey,
				Message: "Deletion needs complete verified selected scope and exact existing identity",
			})
			continue
		}
		if x.RecordType == "evidence" {
			delete(p.candidate.Source.RawEvidence, x.ExpectedID)
		} else {
			delete(p.claims, key)
			delete(p.selectedKeys, x.RecordType+"\x00"+x.ExternalKey)
		}
	}
	return diagnostics
}

func (p *composedGraphPreparation) normalizeAssertions() error {
	for _, command := range p.commands {
		if command.Node == nil && command.Edge == nil {
			continue
		}
		typ, key, _ := commandAddress(command)
		claimKey := p.selectedKeys[typ+"\x00"+key]
		a := p.claims[claimKey]
		payload, bindings, err := normalizeSourcePayload(command, p.session, p.resolve, p.evidenceID)
		if err != nil {
			return err
		}
		a.Payload, a.DependencyClaims = payload, bindings
		var keys []string
		if command.Node != nil {
			keys = command.Node.EvidenceKeys
		} else {
			keys = command.Edge.EvidenceKeys
		}
		for _, key := range keys {
			id, err := p.evidenceID(key)
			if err != nil {
				return err
			}
			a.EvidenceIDs = append(a.EvidenceIDs, id)
		}
		if old, ok := p.originalClaims[claimKey]; ok {
			a = retainSourceFacets(a, old, p.retainedFacetProof)
		}
		slices.Sort(a.EvidenceIDs)
		a.EvidenceIDs = slices.Compact(a.EvidenceIDs)
		p.claims[claimKey] = a
	}
	return nil
}

func (p *composedGraphPreparation) stageEvidence() error {
	for _, command := range p.commands {
		if command.Evidence == nil {
			continue
		}
		input := command.Evidence
		claimKey := p.selectedKeys[input.SubjectType+"\x00"+input.SubjectKey]
		a, ok := p.claims[claimKey]
		if !ok {
			return semantic("evidence.subjectKey", "Evidence requires a final selected-provider assertion")
		}
		if p.candidate.IncrementalScope != nil && !p.fresh[claimKey] {
			return semantic("evidence.subjectKey", "Incremental evidence upload requires its reobserved own assertion")
		}
		id := p.ids["evidence\x00"+input.ExternalKey]
		if p.retainedFacetProof[id] {
			return semantic("evidence", "Retained facet proof cannot be reuploaded or overwritten")
		}
		e := Evidence{
			ID: id, ExternalKey: input.ExternalKey, SubjectID: a.RecordID, PropertyPath: input.PropertyPath,
			Method: input.Method, Status: input.Status, Source: input.Source,
			Explanation: input.Explanation, Snippet: input.Snippet,
			Ownership: new(a.Owner), Freshness: new(a.Freshness),
		}
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		p.candidate.Source.RawEvidence[id] = raw
		p.submittedEvidence[id] = true
	}
	graph := p.candidate.Source
	graph.LegacyProofBases = slices.DeleteFunc(graph.LegacyProofBases, func(b LegacyProofBasis) bool {
		return p.submittedEvidence[b.EvidenceID]
	})
	return nil
}

func (p *composedGraphPreparation) updateSourceVector() {
	partition := SourcePartition{
		RepositoryID: p.session.RepositoryID, ProviderNamespace: p.session.Manifest.Provider.Namespace,
		SnapshotID: p.session.SnapshotID, Provider: p.session.Manifest.Provider,
		Inventory: p.session.Inventory, ScopeStatus: *p.session.ScopeStatus,
	}
	vector := p.candidate.Source.SourceVector
	replaced := false
	for i, prior := range vector.Partitions {
		if prior.RepositoryID == p.session.RepositoryID && prior.ProviderNamespace == p.session.Manifest.Provider.Namespace {
			vector.Partitions[i] = partition
			replaced = true
		}
	}
	if !replaced {
		vector.Partitions = append(vector.Partitions, partition)
	}
	vector.Snapshots = append(vector.Snapshots, SourceSnapshot{
		Role: "active_source", ID: p.session.SnapshotID, RepositoryID: p.session.RepositoryID,
		ManifestHash: p.session.ManifestHash, Provider: p.session.Manifest.Provider,
		SnapshotManifest: p.session.Manifest.Snapshot,
	})
}

func (p *composedGraphPreparation) retainEvidence() error {
	usedProof := map[string]bool{}
	for _, a := range p.claims {
		for _, id := range a.EvidenceIDs {
			usedProof[id] = true
		}
	}
	for id := range p.submittedEvidence {
		if !usedProof[id] {
			return semantic("evidence.subjectKey", "Uploaded evidence must be listed by its own assertion")
		}
	}
	graph := p.candidate.Source
	maps.DeleteFunc(graph.RawEvidence, func(id string, _ jsontext.Value) bool { return !usedProof[id] })
	graph.LegacyProofBases = slices.DeleteFunc(graph.LegacyProofBases, func(b LegacyProofBasis) bool {
		return !usedProof[b.EvidenceID]
	})
	if len(graph.LegacyProofBases) == 0 {
		graph.legacyBasisBytes = 0
	}
	return nil
}

func (p *composedGraphPreparation) hashAssertions() error {
	for _, key := range slices.Sorted(maps.Keys(p.claims)) {
		a := p.claims[key]
		if err := validateSourceOwnProof(p.session, a, p.candidate.Source, p.fresh[key], p.retainedFacetProof); err != nil {
			return err
		}
		if p.fresh[key] {
			index, err := sourceProofs(p.candidate.Source)
			if err != nil {
				return err
			}
			hash, err := sourceIntrinsicHash(a, p.candidate.Source.RawEvidence, sourceClaimLegacyBases(a, index))
			if err != nil {
				return err
			}
			a.AssertionHash = hash
			p.claims[key] = a
			p.currentness[key] = sourceCurrentness(a)
		} else if p.selected(a) {
			f := p.currentness[key]
			if p.candidate.IncrementalScope != nil {
				var err error
				f, err = incrementalRetainedCurrentness(p.session, p.base, a, f, p.candidate.IncrementalScope)
				if err != nil {
					return err
				}
			} else {
				// sourceStaleReason sorts and compacts: f was seeded from
				// the base currentness, so a claim already stale there
				// carries not_reobserved, and a bare append grew the list
				// by one per import — a spurious freshness_changed in every
				// diff and a contender digest that never matched again
				// (review 2026-10-06, F50).
				f.Own = sourceStaleReason(f.Own, "not_reobserved")
			}
			p.currentness[key] = f
		}
		if p.candidate.IncrementalScope == nil || p.fresh[key] {
			p.currentness[key] = populateSourceFields(a, p.currentness[key], p.selected(a), p.fresh[key], p.session.SnapshotID)
		}
		if p.fresh[key] {
			a.FieldCurrentness = slices.Clone(p.currentness[key].Fields)
			p.claims[key] = a
		}
	}
	return nil
}

func (p *composedGraphPreparation) bindCandidateReferences() error {
	for _, key := range slices.Sorted(maps.Keys(p.claims)) {
		a := p.claims[key]
		if p.fresh[key] {
			for i, binding := range a.DependencyClaims {
				if binding.Basis == "candidate" {
					targetKey := sourceClaimKey(
						binding.Target.RecordType,
						binding.Target.ExpectedID,
						binding.Target.RepositoryID,
						binding.Target.ProviderNamespace,
					)
					target, ok := p.claims[targetKey]
					if !ok {
						return semantic(binding.Site, "Candidate dependency was removed")
					}
					a.DependencyClaims[i].Target = sourceAssertionRef(target)
				}
			}
			p.claims[key] = a
		}
		p.candidate.Source.Assertions = append(p.candidate.Source.Assertions, a)
		if p.fresh[key] {
			if p.candidate.Source.rawAssertions == nil {
				p.candidate.Source.rawAssertions = map[string]jsontext.Value{}
			}
			raw, err := canonicalJSON(a)
			if err != nil {
				return err
			}
			p.candidate.Source.rawAssertions[key] = raw
		}
	}
	return nil
}

func (p *composedGraphPreparation) appendCurrentnessGaps() error {
	for _, current := range p.candidate.Source.Currentness {
		if current.Own.Status != "current" || current.Dependency.Status != "current" {
			p.candidate.Graph.ReconciliationGaps = append(p.candidate.Graph.ReconciliationGaps, "stale_assertion: "+current.RecordID)
		}
	}
	index, err := sourceProofs(p.candidate.Source)
	if err != nil {
		return err
	}
	for _, a := range p.candidate.Source.Assertions {
		if sourceMetadataOnly(a, index) {
			p.candidate.Graph.ReconciliationGaps = append(p.candidate.Graph.ReconciliationGaps, "legacy_metadata_only: "+a.RecordID)
		}
	}
	return nil
}

func (p *composedGraphPreparation) materializeEvidence() error {
	for _, raw := range p.candidate.Source.RawEvidence {
		var e Evidence
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		p.candidate.Graph.Evidence = append(p.candidate.Graph.Evidence, e)
	}
	slices.SortFunc(p.candidate.Graph.Evidence, func(a, b Evidence) int { return strings.Compare(a.ID, b.ID) })
	return nil
}

func (p *composedGraphPreparation) evidenceID(key string) (string, error) {
	id := p.ids["evidence\x00"+key]
	if id == "" {
		return "", semantic("evidenceKeys", "Selected-provider evidence key is missing")
	}
	return id, nil
}

func (p *composedGraphPreparation) resolve(site SourceReferenceSite) (BaseAssertionRef, error) {
	var target ProviderAssertion
	if site.Ref.Base != nil {
		var err error
		target, err = exactSourceAssertion(p.base.Assertions, *site.Ref.Base)
		if err != nil {
			return BaseAssertionRef{}, err
		}
	} else {
		key := p.selectedKeys[site.RecordType+"\x00"+site.Ref.LocalKey]
		var ok bool
		target, ok = p.claims[key]
		if !ok {
			return BaseAssertionRef{}, semantic(site.Path, "Local reference is absent from final selected partition")
		}
	}
	if target.RecordType != site.RecordType || !sourceExpectedKind(site.ExpectedKind, target) {
		return BaseAssertionRef{}, semantic(site.Path, "Reference has wrong record type or kind")
	}
	if site.Relation == "same_repository" && target.Owner.RepositoryID != p.session.RepositoryID {
		return BaseAssertionRef{}, semantic(site.Path, "Structural reference must remain in its repository")
	}
	return sourceAssertionRef(target), nil
}

func (p *composedGraphPreparation) finalizeContent() error {
	graph, g := p.candidate.Source, p.candidate.Graph
	g.Coverage = source6Coverage(graph, g)
	graph.State.Nodes, graph.State.Edges, graph.State.Evidence = g.Nodes, g.Edges, g.Evidence
	graph.State.Sources = g.Sources
	graph.State.Inventory = p.session.Inventory
	content, err := source6ContextJSON(graph)
	if err != nil {
		return err
	}
	graph.SourceContentHash = hashBytes(content)
	if err := source6Artifacts(p.base, p.candidate); err != nil {
		return err
	}
	return p.validateLimits(len(content))
}

func (p *composedGraphPreparation) validateLimits(contentBytes int) error {
	claimNodes, claimEdges := 0, 0
	for _, a := range p.candidate.Source.Assertions {
		if a.RecordType == "node" {
			claimNodes++
		} else {
			claimEdges++
		}
	}
	graph, g := p.candidate.Source, p.candidate.Graph
	// Claims and effective records are each bounded by the advertised cap,
	// not their sum: a single-provider import holds one claim per record,
	// so the sum halved maxRevisionNodes/maxRevisionEdges as advertised by
	// get_backend_capabilities, and staging (which counts records only)
	// accepted batches preview then refused (review 2026-10-06, F58). The
	// stored size of both stays bounded by MaxRevisionBytes below.
	tooManyNodes := claimNodes > MaxRevisionNodes || len(g.Nodes) > MaxRevisionNodes
	tooManyEdges := claimEdges > MaxRevisionEdges || len(g.Edges) > MaxRevisionEdges
	tooMuchEvidence := len(g.Evidence) > MaxRevisionEvidence
	artifactBytes, err := source6ArtifactBytes(g)
	if err != nil {
		return err
	}
	incrementalBytes, err := incrementalRevisionBytes(p.session, p.candidate)
	if err != nil {
		return err
	}
	tooManyBytes := contentBytes+graph.legacyBasisBytes+artifactBytes+incrementalBytes > MaxRevisionBytes
	if tooManyNodes || tooManyEdges || tooMuchEvidence || tooManyBytes {
		return limitFault("Composed claims and effective revision exceed semantic limits")
	}
	return nil
}

func sourceStaging(ctx context.Context, q importReader, sid string) ([]ImportCommand, map[string]string, error) {
	ids, err := sourceStagedIdentities(ctx, q, sid)
	if err != nil {
		return nil, nil, err
	}
	commands := []ImportCommand{}
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_import_records WHERE session_id=? ORDER BY record_type,external_key`, sid)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, nil, err
		}
		var c ImportCommand
		if err := json.Unmarshal([]byte(doc), &c); err != nil {
			return nil, nil, err
		}
		commands = append(commands, c)
	}
	return commands, ids, rows.Err()
}

func sourceStagedIdentities(ctx context.Context, q importReader, sid string) (map[string]string, error) {
	ids := map[string]string{}
	rows, err := q.QueryContext(ctx, `SELECT record_type,external_key,id FROM backend_import_identities WHERE session_id=?`, sid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var typ, key, id string
		if err := rows.Scan(&typ, &key, &id); err != nil {
			return nil, err
		}
		ids[typ+"\x00"+key] = id
	}
	return ids, rows.Err()
}

func sourcePayloadProfile(p SourceAssertionPayload) string {
	edge := p.RecordType == "edge"
	if representationSubject(p.Kind, edge) || representationMapping(p.Kind, p.Attributes, edge) {
		return ComposedProfile
	}
	if eventsSubject(p.Kind, edge) || eventsEmit(p.Kind, p.Attributes, edge) || contextualLineageMapping(p.Kind, p.Attributes, edge) {
		return EventsProfile
	}
	if lineageSubject(p.Kind, edge) {
		return LineageProfile
	}
	if runtimeSubject(p.Kind, edge) {
		return RuntimeProfile
	}
	if relationalSubject(p.Kind, p.Attributes, edge) {
		return RelationalProfile
	}
	return GraphProfile
}

func sourceWholeDeletionSafe(s *ImportSession) bool {
	if s.SourceScope.Kind != "reconcile" || s.ScopeStatus.Status != "complete" || s.Manifest.Snapshot.Consistency != "verified" {
		return false
	}
	for _, item := range s.Inventory {
		if item.Status != "complete" || item.Denominator == nil || *item.Denominator != item.KnownCount {
			return false
		}
	}
	return true
}

func validateSourceDecisionSubjects(
	s *ImportSession,
	base, graph *SourceGraphSnapshot,
	decisions []SourceDecision,
	legacy []ImportCommand,
	fresh map[string]bool,
) error {
	check := func(typ, key, id string, evidenceKeys []string) error {
		for _, a := range graph.Assertions {
			matchesKey := a.RecordType == typ && a.ExternalKey == key
			matchesOwner := a.Owner.RepositoryID == s.RepositoryID && a.Owner.ProviderNamespace == s.Manifest.Provider.Namespace
			if !matchesKey || !matchesOwner {
				continue
			}
			if a.RecordID != id || !fresh[sourceAssertionKey(a)] {
				break
			}
			if err := validateSourceDecisionEvidence(s, graph, a, evidenceKeys); err != nil {
				return err
			}
			return nil
		}
		return semantic("claimIdentity", "Decision requires the incoming own assertion")
	}
	for _, decision := range decisions {
		if x := decision.Command.ClaimIdentity; x != nil {
			if err := check(x.RecordType, x.ExternalKey, x.Target.ExpectedID, x.EvidenceKeys); err != nil {
				return err
			}
			target, err := exactSourceAssertion(base.Assertions, x.Target)
			if err != nil {
				return err
			}
			for _, a := range graph.Assertions {
				if a.RecordID == target.RecordID && a.Payload.Kind != target.Payload.Kind {
					return identityConflict("Shared identity kind differs from target")
				}
			}
		}
	}
	for _, decision := range legacy {
		if x := decision.Identity; x != nil {
			if err := check(x.RecordType, x.ToExternalKey, x.ExpectedID, x.EvidenceKeys); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSourceDecisionEvidence(
	s *ImportSession,
	graph *SourceGraphSnapshot,
	a ProviderAssertion,
	evidenceKeys []string,
) error {
	for _, evidenceKey := range evidenceKeys {
		found := false
		for _, eid := range a.EvidenceIDs {
			var e Evidence
			if err := json.Unmarshal(graph.RawEvidence[eid], &e); err != nil {
				return err
			}
			if e.ExternalKey == evidenceKey && e.Source.SnapshotID == s.SnapshotID {
				found = true
			}
		}
		if !found {
			return semantic("claimIdentity", "Decision requires own fresh subject evidence")
		}
	}
	return nil
}

func retainSourceSnapshots(graph *SourceGraphSnapshot) {
	active, needed := map[string]bool{}, map[string]bool{}
	for _, p := range graph.SourceVector.Partitions {
		active[p.SnapshotID] = true
		needed[p.SnapshotID] = true
	}
	for _, raw := range graph.RawEvidence {
		var e Evidence
		if json.Unmarshal(raw, &e) == nil {
			needed[e.Source.SnapshotID] = true
		}
	}
	out := []SourceSnapshot{}
	for _, snapshot := range graph.SourceVector.Snapshots {
		if !needed[snapshot.ID] {
			continue
		}
		snapshot.Role = "retained_provenance"
		if active[snapshot.ID] {
			snapshot.Role = "active_source"
		}
		out = append(out, snapshot)
	}
	slices.SortFunc(out, func(a, b SourceSnapshot) int { return strings.Compare(a.ID, b.ID) })
	graph.SourceVector.Snapshots = out
}

func source6Coverage(graph *SourceGraphSnapshot, g *graphCandidate) Coverage {
	c := Coverage{Status: "complete", KnownObjects: int64(len(g.Nodes)), Gaps: slices.Clone(g.ReconciliationGaps)}
	if unresolvedCount(g) > 0 {
		c.Gaps = append(c.Gaps, "Unresolved nodes or evidence remain.")
	}
	for _, p := range graph.SourceVector.Partitions {
		if p.ScopeStatus.Status != "complete" {
			c.Gaps = append(c.Gaps, p.ScopeStatus.Gaps...)
		}
		for _, i := range p.Inventory {
			if i.Status != "complete" {
				c.Gaps = append(c.Gaps, p.RepositoryID+"/"+p.ProviderNamespace+": "+i.Category+" "+i.Status)
			}
		}
	}
	for _, s := range graph.SourceVector.Snapshots {
		if s.Role == "active_source" && s.Consistency != "verified" {
			c.Gaps = append(c.Gaps, "unverified_source: "+s.ID)
		}
	}
	if len(c.Gaps) > 0 {
		c.Status = "partial"
	}
	slices.Sort(c.Gaps)
	c.Gaps = slices.Compact(c.Gaps)
	return c
}

func source6Artifacts(base *SourceGraphSnapshot, c *composedCandidate) error {
	g := c.Graph
	graph := c.Source
	g.ArtifactPins = slices.Clone(base.State.Revision.ArtifactPins)
	graph.State.Revision.ArtifactPins = g.ArtifactPins
	anchor, err := sourceDomainHash("backend-source6-semantic-v1", struct {
		Schema  string `json:"schema"`
		Content string `json:"sourceContentHash"`
	}{ComposedSchemaVersion, graph.SourceContentHash})
	if err != nil {
		return err
	}
	if context := base.State.ArtifactContextV3; context != nil {
		c := *context
		c.SourceContentHash, c.SourceSemanticHash = graph.SourceContentHash, anchor
		g.ArtifactContextV3 = &c
		graph.State.ArtifactContextV3 = &c
		if _, err := EncodeArtifactContextV3(c); err != nil {
			return err
		}
	} else if context := base.State.ArtifactContext; context != nil {
		artifactContext := *context
		artifactContext.SourceContentHash, artifactContext.SourceSemanticHash = graph.SourceContentHash, anchor
		g.ArtifactContext = &artifactContext
		graph.State.ArtifactContext = &artifactContext
		if _, err := EncodeArtifactContext(artifactContext, g.ArtifactPins); err != nil {
			return err
		}
	} else if context := base.State.APIArtifactContext; context != nil {
		artifactContext := *context
		artifactContext.SourceContentHash, artifactContext.SourceSemanticHash = graph.SourceContentHash, anchor
		g.APIArtifactContext = &artifactContext
		graph.State.APIArtifactContext = &artifactContext
	}
	return nil
}
