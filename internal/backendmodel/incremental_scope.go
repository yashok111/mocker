package backendmodel

import (
	"context"
	"maps"
	"slices"
	"strings"
)

type incrementalScopeInput struct {
	base    *SourceGraphSnapshot
	session *ImportSession
	changes ChangeManifest
	staged  []ImportCommand
	ids     map[string]string
}

type incrementalVisit struct {
	claim      string
	value      LineageValueRef
	contextual bool
	ownerOnly  bool
}

type incrementalArc struct {
	claim   string
	binding SourceDependencyBinding
}

type incrementalClosure struct {
	readOnly        bool
	ctx             context.Context
	input           incrementalScopeInput
	claims          map[string]ProviderAssertion
	subjects        map[SourceSubjectRef][]string
	selectedKeys    map[string]string
	outgoing        map[string][]incrementalArc
	incoming        map[string][]incrementalArc
	incomingContext map[incrementalVisit][]incrementalArc
	seen            map[incrementalVisit]bool
	queue           []incrementalVisit
	validation      map[SourceSubjectRef]bool
	affected        map[SourceSubjectRef]bool
	foreign         map[SourceSubjectRef]bool
	result          *IncrementalAffectedScope
}

func computeIncrementalScope(ctx context.Context, in incrementalScopeInput) (*IncrementalAffectedScope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := incrementalBaseSnapshot(in.base, in.session)
	if err != nil {
		return nil, err
	}
	if err := ValidateChangeManifest(before, in.session.Manifest.Snapshot, in.changes); err != nil {
		return nil, err
	}
	c := &incrementalClosure{ctx: ctx, input: in, claims: map[string]ProviderAssertion{}, subjects: map[SourceSubjectRef][]string{}, selectedKeys: map[string]string{}, outgoing: map[string][]incrementalArc{}, incoming: map[string][]incrementalArc{}, incomingContext: map[incrementalVisit][]incrementalArc{}, seen: map[incrementalVisit]bool{}, affected: map[SourceSubjectRef]bool{}, validation: map[SourceSubjectRef]bool{}, foreign: map[SourceSubjectRef]bool{}, result: &IncrementalAffectedScope{Affected: []SourceSubjectRef{}, ValidationDependencies: []SourceSubjectRef{}, ForeignDependencies: []SourceSubjectRef{}, Gaps: []string{}, AvailabilityChanges: []string{}, claims: map[string]bool{}, writable: map[string]bool{}, dependentClaims: map[string]bool{}, wholeClaims: map[string]bool{}}}
	if err := c.index(); err != nil {
		return nil, err
	}
	c.result.beforeFiles, _ = incrementalManifestFiles(before.Files)
	c.result.afterFiles, _ = incrementalManifestFiles(in.session.Manifest.Snapshot.Files)
	changed := incrementalChangedFiles(before, in.session.Manifest.Snapshot)
	c.result.AvailabilityChanges = incrementalAvailabilityChanges(before, in.session.Manifest.Snapshot)
	if err := c.seed(changed); err != nil {
		return nil, err
	}
	if err := c.walk(); err != nil {
		return nil, err
	}
	if err := c.authorizeWrites(); err != nil {
		return nil, err
	}
	c.result.contexts = c.seen
	c.result.visitedCount = len(c.seen)
	c.result.Affected = sortedIncrementalSubjects(c.affected)
	for ref := range c.affected {
		delete(c.validation, ref)
	}
	c.result.ValidationDependencies = sortedIncrementalSubjects(c.validation)
	c.result.ForeignDependencies = sortedIncrementalSubjects(c.foreign)
	for key, a := range c.claims {
		if c.selected(a) && !c.result.claims[key] {
			c.result.UntouchedCount++
		}
	}
	return c.result, nil
}

func incrementalBaseSnapshot(base *SourceGraphSnapshot, s *ImportSession) (SourceSnapshot, error) {
	if base == nil || base.SourceVector == nil || s == nil || s.BasePartition == nil {
		return SourceSnapshot{}, semantic("changeManifest", "Incremental scope requires the exact selected base partition")
	}
	p := s.BasePartition
	if p.RepositoryID != s.RepositoryID || p.ProviderNamespace != s.Manifest.Provider.Namespace {
		return SourceSnapshot{}, semantic("changeManifest", "Incremental base partition differs from selected owner")
	}
	active := slices.ContainsFunc(base.SourceVector.Partitions, func(partition SourcePartition) bool {
		return partition.RepositoryID == p.RepositoryID && partition.ProviderNamespace == p.ProviderNamespace && partition.SnapshotID == p.SnapshotID
	})
	if !active {
		return SourceSnapshot{}, semantic("changeManifest", "Incremental base must be the exact active selected snapshot")
	}
	for _, snapshot := range base.SourceVector.Snapshots {
		if snapshot.ID == p.SnapshotID && snapshot.RepositoryID == p.RepositoryID && snapshot.Provider.Namespace == p.ProviderNamespace {
			return snapshot, nil
		}
	}
	return SourceSnapshot{}, semantic("changeManifest", "Selected active snapshot is absent")
}

func (c *incrementalClosure) selected(a ProviderAssertion) bool {
	return a.Owner.RepositoryID == c.input.session.RepositoryID && a.Owner.ProviderNamespace == c.input.session.Manifest.Provider.Namespace
}
func (c *incrementalClosure) index() error {
	for _, a := range c.input.base.Assertions {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		key := sourceAssertionKey(a)
		c.claims[key] = a
		subject := SourceSubjectRef{RecordType: a.RecordType, ID: a.RecordID}
		c.subjects[subject] = append(c.subjects[subject], key)
		if c.selected(a) {
			c.selectedKeys[a.RecordType+"\x00"+a.ExternalKey] = key
		}
	}
	for key, a := range c.claims {
		for _, binding := range a.DependencyClaims {
			target := binding.Target
			targetKey := sourceClaimKey(target.RecordType, target.ExpectedID, target.RepositoryID, target.ProviderNamespace)
			c.outgoing[key] = append(c.outgoing[key], incrementalArc{claim: targetKey, binding: binding})
			c.addIncoming(targetKey, incrementalArc{claim: key, binding: binding})
		}
	}
	return nil
}

func (c *incrementalClosure) addIncoming(target string, arc incrementalArc) {
	c.incoming[target] = append(c.incoming[target], arc)
	key := incrementalVisit{claim: target}
	if arc.binding.ValueContext != nil {
		key.contextual = true
		key.value = *arc.binding.ValueContext
	}
	c.incomingContext[key] = append(c.incomingContext[key], arc)
}

func incrementalVisitGroup(visit incrementalVisit, a ProviderAssertion) incrementalVisit {
	if !visit.contextual {
		return visit
	}
	value := visit.value
	target := a.RecordID == value.NodeID || a.RecordID == value.EndpointID || a.RecordID == value.RouteID
	if !target {
		visit.contextual = false
		visit.value = LineageValueRef{}
	}
	return visit
}

func (c *incrementalClosure) push(v incrementalVisit) error {
	if v.ownerOnly {
		full := v
		full.ownerOnly = false
		if c.seen[full] {
			return nil
		}
	}
	if c.seen[v] {
		return nil
	}
	// Count contextual work, including validation-only visits, before enqueueing.
	if len(c.seen) >= MaxIncrementalSubjects {
		return limitFault("Incremental affected scope exceeds 100000 visits; use whole-source-v1")
	}
	c.seen[v] = true
	c.queue = append(c.queue, v)
	return nil
}

func (c *incrementalClosure) chargeEvidence(id string) error {
	owner := c.input.session
	if err := c.push(incrementalVisit{claim: sourceClaimKey("evidence", id, owner.RepositoryID, owner.Manifest.Provider.Namespace)}); err != nil {
		return err
	}
	c.affected[SourceSubjectRef{RecordType: "evidence", ID: id}] = true
	return nil
}

func (c *incrementalClosure) seed(changed map[string]bool) error {
	evidenceOwners := map[string][]string{}
	for key, a := range c.claims {
		for _, id := range a.EvidenceIDs {
			evidenceOwners[id] = append(evidenceOwners[id], key)
		}
	}
	index, err := sourceProofs(c.input.base)
	if err != nil {
		return err
	}
	for id, e := range index.evidence {
		for _, key := range evidenceOwners[id] {
			a := c.claims[key]
			if !c.selected(a) || !changed[e.Source.File] || e.Source.RepositoryID != a.Owner.RepositoryID {
				continue
			}
			if err := c.push(incrementalVisit{claim: key}); err != nil {
				return err
			}
			if err := c.chargeEvidence(id); err != nil {
				return err
			}
			c.affected[SourceSubjectRef{RecordType: "evidence", ID: id}] = true
		}
	}
	if err := c.seedRoots(evidenceOwners); err != nil {
		return err
	}
	return c.seedNewSubjects()
}

func (c *incrementalClosure) seedRoots(evidenceOwners map[string][]string) error {
	for _, root := range c.input.changes.AffectedRoots {
		owners := c.subjects[root]
		if root.RecordType == "evidence" {
			owners = evidenceOwners[root.ID]
		}
		found := false
		for _, key := range owners {
			if c.selected(c.claims[key]) {
				found = true
				if err := c.push(incrementalVisit{claim: key}); err != nil {
					return err
				}
			}
		}
		if !found {
			return semantic("changeManifest.affectedRoots", "Root is absent from the exact selected base partition")
		}
		if root.RecordType == "evidence" {
			if err := c.chargeEvidence(root.ID); err != nil {
				return err
			}
			c.affected[root] = true
		}
	}
	return nil
}

func (c *incrementalClosure) chargeStagedEvidence() error {
	for _, command := range c.input.staged {
		if command.Evidence == nil {
			continue
		}
		id := c.input.ids["evidence\x00"+command.Evidence.ExternalKey]
		if !ValidID(id) {
			return semantic("evidence", "Evidence requires its exact durable reservation")
		}
		if err := c.chargeEvidence(id); err != nil {
			return err
		}
	}
	return nil
}

func (c *incrementalClosure) seedNewSubjects() error {
	if err := c.chargeStagedEvidence(); err != nil {
		return err
	}
	newlyAllocated := map[string]bool{}
	for _, command := range c.input.staged {
		typ, key, _ := commandAddress(command)
		if typ != "node" && typ != "edge" {
			continue
		}
		address := typ + "\x00" + key
		if c.selectedKeys[address] != "" {
			continue
		}
		id := c.input.ids[address]
		if !ValidID(id) {
			return semantic("changeManifest", "New subjects require their exact reserved identities")
		}
		subject := SourceSubjectRef{RecordType: typ, ID: id}
		existing := c.subjects[subject]
		for _, claim := range existing {
			if c.selected(c.claims[claim]) {
				c.selectedKeys[address] = claim
				break
			}
		}
		if c.selectedKeys[address] != "" {
			continue
		}
		if len(existing) > 0 {
			return semantic("changeManifest", "Foreign identity is not a new-subject seed")
		}
		claim := sourceClaimKey(typ, id, c.input.session.RepositoryID, c.input.session.Manifest.Provider.Namespace)
		if _, duplicate := c.claims[claim]; duplicate {
			return semantic("changeManifest", "Reserved identities must be unambiguous")
		}
		payload, err := sourceCommandPayload(command)
		if err != nil {
			return err
		}
		a := ProviderAssertion{RecordType: typ, RecordID: id, ExternalKey: key, Payload: payload, Owner: AssertionOwnership{RepositoryID: c.input.session.RepositoryID, ProviderNamespace: c.input.session.Manifest.Provider.Namespace}}
		c.claims[claim] = a
		c.selectedKeys[address] = claim
		newlyAllocated[claim] = true
		if err := c.push(incrementalVisit{claim: claim}); err != nil {
			return err
		}
	}
	for _, command := range c.input.staged {
		typ, key, _ := commandAddress(command)
		claim := c.selectedKeys[typ+"\x00"+key]
		if !newlyAllocated[claim] {
			continue
		}
		payload, bindings, err := normalizeSourcePayload(command, c.input.session, c.resolveNewReference, func(key string) (string, error) {
			id := c.input.ids["evidence\x00"+key]
			if id == "" {
				return "", semantic("evidenceKeys", "Proof reservation is missing")
			}
			return id, nil
		})
		if err != nil {
			return err
		}
		a := c.claims[claim]
		a.Payload = payload
		a.DependencyClaims = bindings
		c.claims[claim] = a
		for _, binding := range bindings {
			target := binding.Target
			targetKey := sourceClaimKey(target.RecordType, target.ExpectedID, target.RepositoryID, target.ProviderNamespace)
			c.outgoing[claim] = append(c.outgoing[claim], incrementalArc{claim: targetKey, binding: binding})
			c.addIncoming(targetKey, incrementalArc{claim: claim, binding: binding})
		}
	}
	return nil
}

func (c *incrementalClosure) resolveNewReference(site SourceReferenceSite) (BaseAssertionRef, error) {
	key := c.selectedKeys[site.RecordType+"\x00"+site.Ref.LocalKey]
	if target := site.Ref.Base; target != nil {
		key = sourceClaimKey(target.RecordType, target.ExpectedID, target.RepositoryID, target.ProviderNamespace)
	}
	a, ok := c.claims[key]
	if !ok {
		return BaseAssertionRef{}, semantic(site.Path, "Referenced source identity is absent")
	}
	if target := site.Ref.Base; target != nil && sourceAssertionRef(a) != *target {
		return BaseAssertionRef{}, semantic(site.Path, "Base reference is not exact")
	}
	return sourceAssertionRef(a), nil
}

func (c *incrementalClosure) walk() error {
	expanded := map[incrementalVisit]bool{}
	for offset := 0; offset < len(c.queue); offset++ {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		visit := c.queue[offset]
		a, ok := c.claims[visit.claim]
		if !ok {
			continue
		}
		group := c.markVisit(visit, a)
		fullGroup := incrementalVisit{claim: visit.claim, ownerOnly: visit.ownerOnly}
		if group.contextual && expanded[fullGroup] {
			continue
		}
		if expanded[group] {
			continue
		}
		expanded[group] = true
		if err := c.walkOutgoing(visit, group, a); err != nil {
			return err
		}
		if visit.ownerOnly {
			continue
		}
		if err := c.walkIncoming(visit, group); err != nil {
			return err
		}
	}
	return nil
}

func (c *incrementalClosure) markVisit(visit incrementalVisit, a ProviderAssertion) incrementalVisit {
	group := incrementalVisitGroup(visit, a)
	if !visit.ownerOnly && !group.contextual {
		c.result.wholeClaims[visit.claim] = true
	}
	ref := SourceSubjectRef{RecordType: a.RecordType, ID: a.RecordID}
	if !visit.ownerOnly {
		c.result.claims[visit.claim] = true
	}
	if !c.readOnly && !visit.ownerOnly && c.selected(a) {
		c.result.writable[visit.claim] = true
	}
	if c.selected(a) {
		if visit.ownerOnly || c.readOnly {
			c.validation[ref] = true
		} else {
			c.affected[ref] = true
		}
	} else {
		c.foreign[ref] = true
	}
	return group
}
func (c *incrementalClosure) walkOutgoing(visit, group incrementalVisit, a ProviderAssertion) error {
	for _, arc := range c.outgoing[visit.claim] {
		ownerOnly := arc.binding.Site == "/parentId" || a.Payload.Kind == "contains" && arc.binding.Site == "/from"
		if target, ok := c.claims[arc.claim]; ok && !group.contextual && incrementalValidationGroup(target.Payload.Kind) {
			ownerOnly = false
		}
		if visit.ownerOnly && !ownerOnly {
			continue
		}
		if incrementalLogicalConsumer(a, arc.binding, ownerOnly) {
			c.result.dependentClaims[arc.claim] = true
		}
		next := incrementalVisit{claim: arc.claim, ownerOnly: ownerOnly}
		if arc.binding.ValueContext != nil {
			next.value = *arc.binding.ValueContext
			next.contextual = true
		}
		if next == visit {
			continue
		}
		if err := c.push(next); err != nil {
			return err
		}
	}
	return nil
}
func (c *incrementalClosure) walkIncoming(visit, group incrementalVisit) error {
	fullGroup := incrementalVisit{claim: visit.claim, ownerOnly: visit.ownerOnly}
	incoming := c.incoming[visit.claim]
	if group.contextual {
		incoming = slices.Concat(c.incomingContext[group], c.incomingContext[fullGroup])
	}
	for _, arc := range incoming {
		next := incrementalVisit{claim: arc.claim}
		if arc.binding.ValueContext != nil {
			next.value = *arc.binding.ValueContext
			next.contextual = true
		}
		if err := c.push(next); err != nil {
			return err
		}
	}
	return nil
}

func incrementalLogicalConsumer(a ProviderAssertion, b SourceDependencyBinding, ownerOnly bool) bool {
	if ownerOnly {
		return false
	}
	return a.Payload.Kind == "calls" && b.Site == "/from" || b.Site == "/parentId" && (a.Payload.Kind == "field_mapping" || a.Payload.Kind == "flow_step")
}

func incrementalValidationGroup(kind string) bool {
	switch kind {
	case "flow", "message", "representation", "http_operation":
		return true
	default:
		return false
	}
}

func (c *incrementalClosure) authorizeWrites() error {
	for _, command := range c.input.staged {
		if command.Resolution != nil {
			ref := command.Resolution
			key := sourceClaimKey(ref.RecordType, ref.ID, c.input.session.RepositoryID, c.input.session.Manifest.Provider.Namespace)
			if !c.result.writable[key] {
				return semantic("resolution", "Resolution requires an affected selected assertion")
			}
			continue
		}
		if command.Deletion != nil && command.Deletion.RecordType == "evidence" {
			if err := c.authorizeEvidenceDeletion(*command.Deletion); err != nil {
				return err
			}
			continue
		}
		typ, key, _ := commandAddress(command)
		if command.Evidence != nil {
			typ, key = command.Evidence.SubjectType, command.Evidence.SubjectKey
		}
		if command.Deletion != nil {
			typ, key = command.Deletion.RecordType, command.Deletion.ExternalKey
		}
		if command.Identity != nil {
			typ, key = command.Identity.RecordType, command.Identity.FromExternalKey
		}
		if typ != "node" && typ != "edge" {
			continue
		}
		claimKey := c.selectedKeys[typ+"\x00"+key]
		if claimKey != "" && !c.result.writable[claimKey] {
			return semantic("changeManifest.affectedRoots", "Existing write lies outside affected scope; declare an exact selected root")
		}
	}
	return nil
}

func (c *incrementalClosure) authorizeEvidenceDeletion(deletion ImportDeletion) error {
	index, err := sourceProofs(c.input.base)
	if err != nil {
		return err
	}
	e, ok := index.evidence[deletion.ExpectedID]
	if !ok || e.ExternalKey != deletion.ExternalKey {
		return semantic("deletion", "Evidence deletion needs an exact selected identity")
	}
	for _, typ := range []string{"node", "edge"} {
		key := sourceClaimKey(typ, e.SubjectID, c.input.session.RepositoryID, c.input.session.Manifest.Provider.Namespace)
		a, owned := c.claims[key]
		if owned && slices.Contains(a.EvidenceIDs, e.ID) && c.result.writable[key] {
			return nil
		}
	}
	return semantic("deletion", "Evidence deletion lies outside affected selected scope")
}

func sortedIncrementalSubjects(set map[SourceSubjectRef]bool) []SourceSubjectRef {
	values := slices.Collect(maps.Keys(set))
	slices.SortFunc(values, func(a, b SourceSubjectRef) int {
		return strings.Compare(a.RecordType+"\x00"+a.ID, b.RecordType+"\x00"+b.ID)
	})
	return values
}

func incrementalAvailabilityChanges(before SourceSnapshot, after SnapshotManifest) []string {
	old := map[string]ManifestFile{}
	for _, file := range before.Files {
		old[file.Path] = file
	}
	out := []string{}
	for _, file := range after.Files {
		prior, ok := old[file.Path]
		if ok && prior.ContentHash == file.ContentHash && (prior.AnalysisStatus != file.AnalysisStatus || prior.FileType != file.FileType) {
			out = append(out, file.Path)
		}
	}
	slices.Sort(out)
	return out
}

func ComputeIncrementalScope(ctx context.Context, in IncrementalScopeInput) (*IncrementalAffectedScope, error) {
	if in.Session == nil || in.Session.ChangeManifest == nil {
		return nil, semantic("changeManifest", "Incremental scope requires a change manifest")
	}
	return computeIncrementalScope(ctx, incrementalScopeInput{base: in.Base, session: in.Session, changes: *in.Session.ChangeManifest, staged: in.Staged, ids: in.ReservedIDs})
}

func (p *composedGraphPreparation) prepareIncrementalScope(ctx context.Context) error {
	if p.session.SyncPolicy != IncrementalSourcePolicy {
		return nil
	}
	staged := slices.Concat(p.commands, p.legacyDecisions)
	for _, decision := range p.decisions {
		staged = append(staged, decision.Command)
	}
	scope, err := ComputeIncrementalScope(ctx, IncrementalScopeInput{Base: p.base, Session: p.session, Staged: staged, ReservedIDs: p.ids})
	if err != nil {
		return err
	}
	for key, current := range p.currentness {
		current.Fields = slices.Clone(current.Fields)
		p.currentness[key] = current
	}
	p.candidate.IncrementalScope = scope
	return nil
}
