package backendmodel

func (m *portableMapper) diagramOrigin(o *DiagramOrigin) {
	for i := range o.Evidence {
		e := &o.Evidence[i]
		m.id("revision", &e.RevisionID)
		m.id("evidence", &e.EvidenceID)
		m.record(&e.SubjectID)
	}
}
func (m *portableMapper) diagramRef(r *DiagramRef) {
	switch r.Kind {
	case "record":
		m.id(r.RecordType, &r.ID)
	case "artifact":
		if r.Locator == nil {
			m.err = invalid("ref", "Missing artifact locator")
			return
		}
		if m.collect {
			return
		}
		old := r.Locator
		pin := m.scopedPin(NamespacedArtifactPin{Namespace: ArtifactNamespace{Scope: "local", InstallationID: m.originInstallation}, Pin: old.Pin})
		locator := *old
		locator.Pin = pin.Pin
		r.Kind = "namespaced_artifact"
		r.Locator = nil
		r.NamespacedLocator = &NamespacedDiagramLocator{Namespace: pin.Namespace, Locator: locator}
	case "namespaced_artifact":
		if r.NamespacedLocator == nil {
			m.err = invalid("ref", "Missing namespaced locator")
			return
		}
		locator := r.NamespacedLocator
		pin := m.scopedPin(NamespacedArtifactPin{Namespace: locator.Namespace, Pin: locator.Locator.Pin})
		locator.Namespace, locator.Locator.Pin = pin.Namespace, pin.Pin
	default:
		m.err = invalid("ref", "Unsupported diagram reference")
	}
}
func (m *portableMapper) diagramRefs(refs []DiagramRef) {
	for i := range refs {
		m.diagramRef(&refs[i])
	}
}
func (m *portableMapper) member(kind, parent string, id *string) { m.id(kind, id, parent) }
func (m *portableMapper) diagram(v *DiagramVersion) {
	parent := v.Pin.ID
	m.id("diagram", &v.Pin.ID)
	m.id("project", &v.ProjectID)
	m.target(&v.Document.Target)
	v.receiptJSON = ""
	d := &v.Document
	if d.Kind == "architecture" {
		m.architectureMembers(parent, &d.Payload)
	}
	if p := d.Interactions; p != nil {
		m.interactionMembers(parent, p)
	}
	if p := d.Lifecycle; p != nil {
		m.lifecycleMembers(parent, p)
	}
	if p := d.BusinessMap; p != nil {
		m.businessMapMembers(parent, p)
	}
	m.provenanceMembers(parent, v)
	if v.Provenance.Previous != nil {
		m.id("diagram", &v.Provenance.Previous.ID)
	}
	if v.Provenance.Fork != nil {
		m.id("diagram", &v.Provenance.Fork.Source.ID)
	}
	// Gaps are recomputed by domain validation, never trusted as orphan authority.
	v.Gaps = []DiagramGap{}
}

func (m *portableMapper) architectureMembers(parent string, d *ArchitecturePayload) {
	m.member("diagram_element", parent, &d.PrimarySystemID)
	for i := range d.Elements {
		e := &d.Elements[i]
		m.member("diagram_element", parent, &e.ID)
		m.member("diagram_element", parent, &e.ParentID)
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
		if e.Membership != nil {
			for j := range e.Membership.NodeIDs {
				m.id("node", &e.Membership.NodeIDs[j])
			}
		}
		for j := range e.Navigation {
			navigation := &e.Navigation[j]
			if navigation.Diagram != nil {
				destination := navigation.Diagram.ID
				m.id("diagram", &navigation.Diagram.ID)
				m.member("diagram_element", destination, &navigation.RootID)
				m.member("diagram_element", destination, &navigation.FocusID)
			}
			if navigation.FlowID != "" {
				m.id("node", &navigation.FlowID)
			}
			if navigation.Target != nil {
				m.target(navigation.Target)
			}
		}
	}
	for i := range d.Links {
		e := &d.Links[i]
		m.member("diagram_link", parent, &e.ID)
		m.member("diagram_element", parent, &e.From)
		m.member("diagram_element", parent, &e.To)
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
	}
}

func (m *portableMapper) interactionMembers(parent string, p *InteractionPayload) {
	architecture := ""
	if p.Architecture != nil {
		architecture = p.Architecture.ID
		m.id("diagram", &p.Architecture.ID)
	}
	m.diagramRefs(p.ScopeRefs)
	for i := range p.Participants {
		e := &p.Participants[i]
		m.member("diagram_element", parent, &e.ID)
		if e.ArchitectureElementID != "" {
			m.member("diagram_element", architecture, &e.ArchitectureElementID)
		}
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
	}
	for i := range p.Steps {
		e := &p.Steps[i]
		m.member("diagram_element", parent, &e.ID)
		m.member("diagram_element", parent, &e.From)
		m.member("diagram_element", parent, &e.To)
		m.member("diagram_element", parent, &e.ReplyTo)
		m.list("diagram_element", e.BranchPath, parent)
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
	}
	for i := range p.Branches {
		e := &p.Branches[i]
		m.member("diagram_element", parent, &e.ID)
		m.member("diagram_element", parent, &e.ParentID)
		m.member("diagram_element", parent, &e.GroupID)
		m.diagramOrigin(&e.Origin)
	}
	for i := range p.Order {
		e := &p.Order[i]
		m.member("diagram_link", parent, &e.ID)
		m.member("diagram_element", parent, &e.From)
		m.member("diagram_element", parent, &e.To)
		m.diagramOrigin(&e.Origin)
	}
}

func (m *portableMapper) lifecycleMembers(parent string, p *LifecyclePayload) {
	m.diagramRef(&p.Entity)
	m.diagramRefs(p.StateFields)
	m.diagramOrigin(&p.CoverageOrigin)
	for i := range p.States {
		e := &p.States[i]
		m.member("diagram_element", parent, &e.ID)
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
	}
	for i := range p.Transitions {
		e := &p.Transitions[i]
		m.member("diagram_link", parent, &e.ID)
		m.member("diagram_element", parent, &e.From)
		m.member("diagram_element", parent, &e.To)
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
		m.diagramRefs(e.Triggers)
		m.diagramRefs(e.Writes)
		m.diagramRefs(e.Events)
	}
	for i := range p.Rules {
		e := &p.Rules[i]
		m.member("diagram_link", parent, &e.ID)
		m.member("diagram_element", parent, &e.From)
		m.member("diagram_element", parent, &e.To)
		m.diagramOrigin(&e.Origin)
		m.diagramRef(&e.Trigger)
	}
}

func (m *portableMapper) businessMapMembers(parent string, p *BusinessMapPayload) {
	architecture := ""
	if p.Architecture != nil {
		architecture = p.Architecture.ID
		m.id("diagram", &p.Architecture.ID)
	}
	for i := range p.Elements {
		e := &p.Elements[i]
		m.member("diagram_element", parent, &e.ID)
		if e.ArchitectureElementID != "" {
			m.member("diagram_element", architecture, &e.ArchitectureElementID)
		}
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
	}
	for i := range p.Links {
		e := &p.Links[i]
		m.member("diagram_link", parent, &e.ID)
		m.member("diagram_element", parent, &e.From)
		m.member("diagram_element", parent, &e.To)
		m.diagramOrigin(&e.Origin)
		m.diagramRefs(e.Refs)
	}
}

// provenanceMembers remaps provenance member IDs. Each event names a member
// of the diagram version it points at, so its row kind is looked up there.
func (m *portableMapper) provenanceMembers(parent string, v *DiagramVersion) {
	// Provenance event member IDs belong to the referenced diagram, not the
	// current fork. Determine their row kind before rewriting any identity.
	kindFor := func(id string) string {
		if _, ok := m.ids[PortableIdentity{Kind: "diagram_link", ID: id, Parent: parent}]; ok {
			return "diagram_link"
		}
		return "diagram_element"
	}
	rows := diagramSemanticRows(v.Document)
	_ = rows // Original row kinds are retained separately by model() below.
	for i := range v.Provenance.Elements {
		e := &v.Provenance.Elements[i]
		kind := m.diagramMemberKind(parent, e.ElementID)
		if kind == "" {
			kind = kindFor(e.ElementID)
		}
		m.member(kind, parent, &e.ElementID)
		for _, event := range []*DiagramProvenanceEvent{&e.Introduced, &e.LastEdited} {
			source := event.Pin.ID
			k := m.diagramMemberKind(source, event.ElementID)
			if k == "" {
				k = kind
			}
			m.member(k, source, &event.ElementID)
			m.id("diagram", &event.Pin.ID)
		}
		if e.InheritedFrom != nil {
			source := e.InheritedFrom.Pin.ID
			k := m.diagramMemberKind(source, e.InheritedFrom.ElementID)
			if k == "" {
				k = kind
			}
			m.member(k, source, &e.InheritedFrom.ElementID)
			m.id("diagram", &e.InheritedFrom.Pin.ID)
		}
	}
}
func (m *portableMapper) diagramMemberKind(parent, id string) string {
	return m.kinds["diagram:"+parent+":"+id]
}
func (m *portableMapper) diagramView(v *DiagramView) {
	v.receiptJSON = ""
	parent := v.State.Diagram.ID
	m.id("diagram_view", &v.ID)
	m.id("diagram", &v.State.Diagram.ID)
	m.member("diagram_element", parent, &v.State.RootID)
	for i := range v.State.Positions {
		p := &v.State.Positions[i]
		kind := m.diagramMemberKind(parent, p.ID)
		if kind == "" {
			kind = "diagram_element"
		}
		m.member(kind, parent, &p.ID)
	}
	m.list("diagram_element", v.State.CollapsedIDs, parent)
	if v.State.Selection != nil {
		kind := m.diagramMemberKind(parent, v.State.Selection.ID)
		if kind == "" {
			kind = "diagram_element"
		}
		m.member(kind, parent, &v.State.Selection.ID)
	}
}
