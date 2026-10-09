package backendmodel

import (
	"encoding/json/v2"
	"maps"
	"strings"
)

func (m *portableMapper) model(model *PortableModel) {
	// Record row kinds before any IDs are changed, including equal member IDs in forks.
	for _, v := range model.Diagrams {
		m.diagramRowKinds(v)
	}
	// Proposal values use their original typed record payload to locate reference
	// positions. Free text that happens to contain a UUID never enters this map.
	payloads := map[string]SourceAssertionPayload{}
	for _, s := range model.Sources {
		for _, n := range s.Nodes {
			payloads[s.Revision.ID+":"+n.ID] = sourceNodePayload(n)
		}
		for _, e := range s.Edges {
			payloads[s.Revision.ID+":"+e.ID] = sourceEdgePayload(e)
		}
	}
	for i := range model.Proposals {
		m.proposal(&model.Proposals[i], payloads)
	}
	p := &model.Project
	m.id("project", &p.ID)
	m.id("revision", &p.CurrentRevisionID)
	if p.StartView != nil {
		if err := p.StartView.Validate(); err != nil {
			m.err = err
			return
		}
		m.id(p.StartView.Kind, &p.StartView.ID)
	}
	for i := range p.Repositories {
		m.id("repository", &p.Repositories[i].ID)
	}
	m.target(&model.Target)
	for i := range model.Sources {
		m.source(&model.Sources[i])
	}
	for i := range model.Diagrams {
		m.diagram(&model.Diagrams[i])
	}
	for i := range model.DiagramViews {
		m.diagramView(&model.DiagramViews[i])
	}
	for i := range model.SavedViews {
		m.savedView(&model.SavedViews[i])
	}
	for i := range model.Annotations {
		a := &model.Annotations[i]
		m.id("annotation", &a.ID)
		m.id(a.Target.RecordType, &a.Target.ID)
		m.id("revision", &a.Target.RevisionID)
	}
}
func (m *portableMapper) origin(o *EffectiveOrigin) {
	if o.BaseRef != nil {
		m.id("revision", &o.BaseRef.RevisionID)
		m.id(o.BaseRef.RecordType, &o.BaseRef.ID)
	}
	if o.RebaseResolution != nil {
		m.id("change_proposal_revision", &o.RebaseResolution.ProposalRevisionID)
	}
}
func (m *portableMapper) qualified(q *QualifiedSourceIdentity) {
	if q == nil {
		return
	}
	m.id(q.RecordType, &q.ID)
	m.id("repository", &q.RepositoryID)
}
func (m *portableMapper) identity(t *ChangeIdentityTarget) {
	m.qualified(t.Source)
	if t.Basis != nil {
		m.id("revision", &t.Basis.RevisionID)
	}
	if t.Intent != nil {
		m.id(t.Intent.RecordType, &t.Intent.ID)
		t.RecordType, t.ID = t.Intent.RecordType, t.Intent.ID
	} else if t.RecordType != "" {
		m.id(t.RecordType, &t.ID)
	}
}
func (m *portableMapper) propertyValue(p SourceAssertionPayload, selector TypedSourcePropertySelector, value SourcePropertyValue) SourcePropertyValue {
	if !value.Present {
		return value
	}
	full, err := ApplySourceProperty(p, selector, value)
	if err != nil {
		m.err = err
		return value
	}
	m.payload(&full)
	mapped, err := SelectSourceProperty(full, selector)
	if err != nil {
		m.err = err
		return value
	}
	return mapped
}
func (m *portableMapper) proposal(p *PortableProposal, payloads map[string]SourceAssertionPayload) {
	if p.Full != nil {
		h := p.Full
		m.id("change_proposal", &h.ID)
		m.id("project", &h.ProjectID)
		m.id("change_proposal_revision", &h.CurrentDraftRevisionID)
		h.ReadyReference = nil
		h.ImplementedReference = nil
		h.Status = "draft"
		for i := range p.FullRevisions {
			if !m.fullRevision(p, &p.FullRevisions[i], payloads) {
				return
			}
		}
	}
	for i := range p.Batches {
		b := &p.Batches[i]
		m.id("change_proposal", &b.ProposalID)
		m.id("change_proposal_revision", &b.RevisionID)
		m.id("change_proposal_revision", &b.RestoreRevisionID)

	}
	for i := range p.Identities {
		v := &p.Identities[i]
		m.id(v.RecordType, &v.ID)
		m.id("change_proposal_revision", &v.FirstRevisionID)
		m.origin(&v.Origin)
	}
	if p.Legacy != nil {
		m.legacyProposal(p)
	}
}

// fullRevision remaps one change proposal revision. It reports false when
// the revision's typed payloads cannot be rebuilt; m.err then says why and
// the proposal stops mapping, as one failed revision poisons the history.
func (m *portableMapper) fullRevision(p *PortableProposal, v *ChangeProposalRevision, payloads map[string]SourceAssertionPayload) bool {
	originalPayloads, err := revisionOriginalPayloads(v, payloads)
	if err != nil {
		m.err = err
		return false
	}
	for bi := range p.Batches {
		if p.Batches[bi].RevisionID == v.ID {
			for ci := range p.Batches[bi].Commands {
				m.changeCommand(&p.Batches[bi].Commands[ci], originalPayloads)
			}
		}
	}
	if v.Rebase != nil {
		for ci := range v.Rebase.Input.RepairCommands {
			m.changeCommand(&v.Rebase.Input.RepairCommands[ci], originalPayloads)
		}
	}
	m.revisionDelta(v, originalPayloads)
	for j := range v.Criteria {
		m.criterion(&v.Criteria[j], originalPayloads)
	}
	m.id("change_proposal_revision", &v.ID)
	m.id("change_proposal", &v.ProposalID)
	m.id("change_proposal_revision", v.ParentRevisionID)
	m.id("revision", &v.BaseRevisionID)
	m.id("change_proposal_revision", &v.AcceptedBatchRevisionID)
	m.list("source_snapshot", v.SourceSnapshotIDs)
	m.vector(&v.SourceVector)
	c := &VersionedArtifactContext{Legacy: &v.ArtifactContext, V3: v.ArtifactContextV3}
	if c.V3 != nil {
		c.Legacy = nil
	}
	mapped := m.context(c, v.ArtifactPins)
	if !m.collect {
		v.ArtifactContextV3 = &mapped
		v.ArtifactContext = ArtifactContext{}
		v.ArtifactPins = []ArtifactPin{}
	}
	// Rebase execution input is historical imported provenance, retained verbatim
	// in the immutable origin document. It cannot become a replayable local action.
	if v.Rebase != nil {
		m.id("change_proposal_revision", &v.Rebase.Input.ProposalRevisionID)
		m.id("revision", &v.Rebase.Input.NewBaseRevisionID)
		for j := range v.Rebase.Input.IdentityResolutions {
			r := &v.Rebase.Input.IdentityResolutions[j]
			m.record(&r.OldSourceID)
			m.record(&r.NewSourceID)
		}
	}
	return true
}

// revisionOriginalPayloads rebuilds the typed payload of every record the
// revision touches, as of that revision and before any ID is remapped: the
// base revision's records, the records it created, and its cumulative
// property changes applied on top.
func revisionOriginalPayloads(v *ChangeProposalRevision, payloads map[string]SourceAssertionPayload) (map[string]SourceAssertionPayload, error) {
	originalPayloads := map[string]SourceAssertionPayload{}
	for id, p := range payloads {
		if key, ok := strings.CutPrefix(id, v.BaseRevisionID+":"); ok {
			originalPayloads[key] = p
		}
	}
	for _, created := range v.Delta.Created {
		// A struct copy is not enough: ParentID is a pointer the
		// created-record pass below rewrites in place, so the
		// criteria pass would see the already local parent and fail
		// with "Missing typed mapping" on a valid bundle (review
		// 2026-10-06, F67). Attributes are cloned for the same reason.
		p := created.Payload
		if p.ParentID != nil {
			parent := *p.ParentID
			p.ParentID = &parent
		}
		p.Attributes = maps.Clone(p.Attributes)
		originalPayloads[created.ID] = p
	}
	// Apply cumulative property changes before extracting any remapped value.
	for _, property := range v.Delta.Properties {
		payload, ok := originalPayloads[property.ID]
		if !ok {
			return nil, invalid("delta", "Changed record has no typed source payload")
		}
		next, err := ApplySourceProperty(payload, property.Selector, property.Value)
		if err != nil {
			return nil, err
		}
		originalPayloads[property.ID] = next
	}
	return originalPayloads, nil
}

func (m *portableMapper) revisionDelta(v *ChangeProposalRevision, originalPayloads map[string]SourceAssertionPayload) {
	for j := range v.Delta.Properties {
		c := &v.Delta.Properties[j]
		c.Value = m.propertyValue(originalPayloads[c.ID], c.Selector, c.Value)
		m.id(c.RecordType, &c.ID)
		m.origin(&c.Origin)
	}
	for j := range v.Delta.Created {
		c := &v.Delta.Created[j]
		m.id(c.RecordType, &c.ID)
		m.payload(&c.Payload)
		m.origin(&c.Origin)
	}
	for j := range v.Delta.Removed {
		c := &v.Delta.Removed[j]
		m.id(c.RecordType, &c.ID)
		m.origin(&c.Origin)
	}
	for j := range v.Delta.IdentityIntents {
		c := &v.Delta.IdentityIntents[j]
		m.identity(&c.Target)
		m.origin(&c.Origin)
	}
	for j := range v.Delta.CarriedIdentities {
		c := &v.Delta.CarriedIdentities[j]
		m.qualified(&c.Source)
		m.id("revision", &c.Basis.RevisionID)
	}
	for j := range v.Delta.EdgeNames {
		c := &v.Delta.EdgeNames[j]
		m.id("edge", &c.ID)
		m.origin(&c.Origin)
	}
}
func (m *portableMapper) criterion(c *ChangeCriterion, payloads map[string]SourceAssertionPayload) {
	if a := c.Attachment; a != nil {
		m.criterionAttachment(a)
	}
	original := c.ID
	if c.RecordType != "" {
		m.id(c.RecordType, &c.ID)
	} else if c.Kind == "edge_exists" {
		m.id("edge", &c.ID)
	}
	m.id("node", &c.From)
	m.id("node", &c.To)
	for i := range c.TargetIDs {
		m.record(&c.TargetIDs[i])
	}
	if c.Kind == "field_equals" {
		if !m.fieldEqualsSelector(c, original, payloads) {
			return
		}
	}
	if !m.collect && c.Kind == "artifact_object_matches" && c.Artifact != nil {
		pin := m.scopedPin(NamespacedArtifactPin{Namespace: ArtifactNamespace{Scope: "local", InstallationID: m.originInstallation}, Pin: *c.Artifact})
		c.Kind = "artifact_object_matches_v3"
		c.NamespacedArtifact = &pin
		c.Artifact = nil
	} else if c.NamespacedArtifact != nil {
		pin := m.scopedPin(*c.NamespacedArtifact)
		c.NamespacedArtifact = &pin
	}
	// Artifact criteria are converted to the namespaced arm by the context import
	// pass; this visitor never treats their decimal owner IDs as graph UUIDs.
}

func (m *portableMapper) criterionAttachment(a *TestAttachmentRef) {
	if a.Kind == "source" {
		m.id("revision", &a.RevisionID)
		m.id("repository", &a.RepositoryID)
		m.id("source_snapshot", &a.SnapshotID)
	} else if a.Kind == "artifact" && a.Artifact != nil && !m.collect {
		pin := m.scopedPin(NamespacedArtifactPin{Namespace: ArtifactNamespace{Scope: "local", InstallationID: m.originInstallation}, Pin: *a.Artifact})
		a.Kind = "artifact_v3"
		a.NamespacedArtifact = &pin
		a.Artifact = nil
	} else if a.Kind == "artifact_v3" && a.NamespacedArtifact != nil {
		pin := m.scopedPin(*a.NamespacedArtifact)
		a.NamespacedArtifact = &pin
	}
}

// fieldEqualsSelector remaps a field_equals criterion's selector and its
// expected value through the record's typed payload. It reports false when
// mapping must stop; m.err then says why.
func (m *portableMapper) fieldEqualsSelector(c *ChangeCriterion, original string, payloads map[string]SourceAssertionPayload) bool {
	var selector EffectivePropertySelector
	if err := json.Unmarshal(c.Selector, &selector); err != nil {
		m.err = err
		return false
	}
	if selector.Source != nil && c.Expected != nil {
		p, ok := payloads[original]
		if !ok {
			m.err = invalid("criterion", "Typed criterion record is missing")
			return false
		}
		v := m.propertyValue(p, *selector.Source, *c.Expected)
		c.Expected = &v
	}
	if selector.SourceIdentity != nil {
		m.id(selector.SourceIdentity.RecordType, &selector.SourceIdentity.ID)
		m.id("repository", &selector.SourceIdentity.RepositoryID)
	}
	if selector.CarriedSourceIdentity != nil {
		m.qualified(&selector.CarriedSourceIdentity.Source)
		m.id("revision", &selector.CarriedSourceIdentity.Basis.RevisionID)
	}
	if selector.IntentIdentity != nil {
		m.id(selector.IntentIdentity.RecordType, &selector.IntentIdentity.ID)
	}
	var err error
	c.Selector, err = json.Marshal(selector)
	if err != nil {
		m.err = err
	}
	return true
}
func (m *portableMapper) savedView(v *SavedView) {
	v.receiptJSON = ""
	m.id("saved_view", &v.ID)
	m.id("project", &v.ProjectID)
	m.target(&v.Target)
	if f := v.State.Flow; f != nil {
		m.id("node", &f.Scope.EntrypointID)
		m.id("node", &f.Scope.FlowID)
		m.id("node", &f.Scope.DataNodeID)
		if f.Selection != nil {
			m.id(f.Selection.RecordType, &f.Selection.ID)
		}
		for i := range f.Positions {
			m.id("node", &f.Positions[i].NodeID)
		}
		m.list("node", f.CollapsedGroupIDs)
	}
	if d := v.State.Database; d != nil {
		m.id("node", &d.Scope.DatastoreID)
		m.id("node", &d.Filters.RelationshipTableID)
		if d.Selection != nil {
			m.id(d.Selection.RecordType, &d.Selection.ID)
		}
		for i := range d.Positions {
			m.id("node", &d.Positions[i].NodeID)
		}
		m.list("node", d.CollapsedGroupIDs)
	}
}

// diagramRowKinds records whether each row of one diagram is an element or
// a link, keyed by the diagram so equal member IDs in forks stay apart.
func (m *portableMapper) diagramRowKinds(v DiagramVersion) {
	parent := v.Pin.ID
	for id := range diagramSemanticRows(v.Document) {
		m.kinds["diagram:"+parent+":"+id] = "diagram_element"
	}
	link := func(id string) { m.kinds["diagram:"+parent+":"+id] = "diagram_link" }
	for _, r := range v.Document.Payload.Links {
		link(r.ID)
	}
	if p := v.Document.Interactions; p != nil {
		for _, r := range p.Order {
			link(r.ID)
		}
	}
	if p := v.Document.Lifecycle; p != nil {
		for _, r := range p.Transitions {
			link(r.ID)
		}
		for _, r := range p.Rules {
			link(r.ID)
		}
	}
	if p := v.Document.BusinessMap; p != nil {
		for _, r := range p.Links {
			link(r.ID)
		}
	}
}
