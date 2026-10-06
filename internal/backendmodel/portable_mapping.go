package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type portableMapper struct {
	ids                map[PortableIdentity]string
	seen               map[PortableIdentity]bool
	kinds              map[string]string
	collect            bool
	err                error
	originInstallation string
	installation       string
	artifacts          []PortableArtifactMapping
}

func newPortableMapper(model *PortableModel, in PortableRemap, collect bool) *portableMapper {
	m := &portableMapper{ids: map[PortableIdentity]string{}, seen: map[PortableIdentity]bool{}, kinds: map[string]string{}, collect: collect, originInstallation: in.OriginInstallationID, installation: in.InstallationID, artifacts: in.Artifacts}
	local := map[PortableIdentity]bool{}
	for _, entry := range in.IDs {
		if !ValidID(entry.Origin.ID) || !ValidID(entry.LocalID) || m.ids[entry.Origin] != "" {
			m.err = invalid("idMap", "Invalid or duplicate typed identity")
			break
		}
		key := PortableIdentity{Kind: entry.Origin.Kind, ID: entry.LocalID, Parent: entry.Origin.Parent}
		if local[key] {
			m.err = invalid("idMap", "Non-injective identity map")
			break
		}
		local[key] = true
		m.ids[entry.Origin] = entry.LocalID
	}
	add := func(kind, id string) {
		if prior, ok := m.kinds[id]; ok && prior != kind {
			m.kinds[id] = "ambiguous"
		} else {
			m.kinds[id] = kind
		}
	}
	for _, s := range model.Sources {
		for _, n := range s.Nodes {
			add("node", n.ID)
		}
		for _, e := range s.Edges {
			add("edge", e.ID)
		}
	}
	for _, p := range model.Proposals {
		for _, v := range p.FullRevisions {
			for _, c := range v.Delta.Created {
				add(c.RecordType, c.ID)
			}
			for _, c := range v.Delta.Removed {
				add(c.RecordType, c.ID)
			}
		}
		for _, v := range p.LegacyRevisions {
			for _, o := range v.Overlays {
				add(o.RecordType, o.SubjectID)
			}
		}
	}
	return m
}
func (m *portableMapper) id(kind string, value *string, parent ...string) {
	if value == nil || *value == "" || m.err != nil {
		return
	}
	key := PortableIdentity{Kind: kind, ID: *value}
	if len(parent) > 0 {
		key.Parent = parent[0]
	}
	if !ValidID(*value) {
		m.err = invalid("idMap", "Invalid typed identity "+kind)
		return
	}
	m.seen[key] = true
	if m.collect {
		return
	}
	next, ok := m.ids[key]
	if !ok {
		m.err = invalid("idMap", "Missing typed mapping: "+kind+" "+*value)
		return
	}
	*value = next
}
func (m *portableMapper) record(value *string) {
	if value == nil || *value == "" {
		return
	}
	kind := m.kinds[*value]
	if kind != "node" && kind != "edge" {
		m.err = invalid("reference", "Missing or ambiguous typed source record")
		return
	}
	m.id(kind, value)
}
func (m *portableMapper) list(kind string, ids []string, parent ...string) {
	for i := range ids {
		m.id(kind, &ids[i], parent...)
	}
}
func (m *portableMapper) target(t *BackendReadTarget) {
	m.id("revision", &t.RevisionID)
	if t.Proposal != nil {
		m.id("proposal", &t.Proposal.ProposalID)
		m.id("proposal_revision", &t.Proposal.ProposalRevisionID)
	}
	if t.ChangeProposal != nil {
		m.id("change_proposal", &t.ChangeProposal.ProposalID)
		m.id("change_proposal_revision", &t.ChangeProposal.ProposalRevisionID)
	}
	if t.ImportCandidate != nil {
		m.err = invalid("target", "Portable targets must be immutable")
	}
}
func (m *portableMapper) owner(o *AssertionOwnership) {
	if o != nil {
		m.id("repository", &o.RepositoryID)
	}
}
func (m *portableMapper) fresh(f *AssertionFreshness) {
	if f != nil {
		m.id("source_snapshot", &f.ConfirmedSnapshotID)
	}
}
func (m *portableMapper) snapshot(s *SourceSnapshot) {
	m.id("source_snapshot", &s.ID)
	m.id("repository", &s.RepositoryID)
}
func (m *portableMapper) vector(v *SourceVector) {
	if v == nil {
		return
	}
	for i := range v.Partitions {
		p := &v.Partitions[i]
		m.id("repository", &p.RepositoryID)
		m.id("source_snapshot", &p.SnapshotID)
	}
	for i := range v.Snapshots {
		m.snapshot(&v.Snapshots[i])
	}
}
func (m *portableMapper) assertionRef(r *BaseAssertionRef) {
	m.id("repository", &r.RepositoryID)
	m.id(r.RecordType, &r.ExpectedID)
}
func (m *portableMapper) valueRef(r *LineageValueRef) {
	if r == nil {
		return
	}
	m.id("node", &r.NodeID)
	m.id("node", &r.EndpointID)
	m.id("edge", &r.RouteID)
}
func (m *portableMapper) attributes(kind string, attrs map[string]jsontext.Value, edge bool) map[string]jsontext.Value {
	if m.err != nil {
		return attrs
	}
	var refs []relationalReference
	var err error
	if relationalSubject(kind, attrs, edge) {
		refs, err = relationalReferencesMode(kind, attrs, edge, true, false)
	} else {
		refs, err = sourceAttributeReferences(kind, attrs, edge, true)
	}
	if err != nil {
		m.err = err
		return attrs
	}
	replacements := map[string]jsontext.Value{}
	for _, ref := range refs {
		id := ref.ID
		typ := ref.RecordType
		if typ == "" {
			typ = "node"
		}
		if ref.Kind == "evidence" {
			typ = "evidence"
		}
		m.id(typ, &id)
		replacements[ref.Path], _ = json.Marshal(id)
		if ref.HistoricalRevisionID != "" {
			rid := ref.HistoricalRevisionID
			m.id("revision", &rid)
			path, _, _ := strings.CutLast(ref.Path, "/")
			replacements[path+"/revisionId"], _ = json.Marshal(rid)
		}
	}
	// Proof fields are declared by the relational facet codec, never discovered
	// by searching arbitrary native definitions or string values.
	if relationalSubject(kind, attrs, edge) {
		facets, root, err := relationalFacetObject(kind, attrs)
		if err != nil {
			m.err = err
			return attrs
		}
		for key, raw := range facets {
			fields, err := relationalObject(raw)
			if err != nil {
				m.err = err
				return attrs
			}
			base := root + "/" + escapeRelationalPointer(key)
			if raw := fields["sourceSnapshotId"]; len(raw) > 0 {
				var id string
				if err := json.Unmarshal(raw, &id); err != nil {
					m.err = err
					return attrs
				}
				m.id("source_snapshot", &id)
				replacements[base+"/sourceSnapshotId"], _ = json.Marshal(id)
			}
			if raw := fields["freshness"]; len(raw) > 0 {
				var f AssertionFreshness
				if err := json.Unmarshal(raw, &f); err != nil {
					m.err = err
					return attrs
				}
				m.fresh(&f)
				replacements[base+"/freshness"], _ = json.Marshal(f)
			}
		}
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		m.err = err
		return attrs
	}
	raw, err = portableReplacePaths(raw, "/attributes", replacements)
	if err != nil {
		m.err = err
		return attrs
	}
	var out map[string]jsontext.Value
	if err := json.Unmarshal(raw, &out); err != nil {
		m.err = err
	}
	return out
}
func portableReplacePaths(raw jsontext.Value, path string, values map[string]jsontext.Value) (jsontext.Value, error) {
	if value, ok := values[path]; ok {
		return value, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return raw, invalid("document", "Empty JSON")
	}
	switch trimmed[0] {
	case '{':
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		for k, v := range fields {
			next, err := portableReplacePaths(v, path+"/"+escapeRelationalPointer(k), values)
			if err != nil {
				return nil, err
			}
			fields[k] = next
		}
		return json.Marshal(fields, json.Deterministic(true))
	case '[':
		var items []jsontext.Value
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		for i := range items {
			next, err := portableReplacePaths(items[i], fmt.Sprintf("%s/%d", path, i), values)
			if err != nil {
				return nil, err
			}
			items[i] = next
		}
		return json.Marshal(items)
	default:
		return raw, nil
	}
}
func (m *portableMapper) payload(p *SourceAssertionPayload) {
	m.id("node", p.ParentID)
	m.id("node", &p.From)
	m.id("node", &p.To)
	p.Attributes = m.attributes(p.Kind, p.Attributes, p.RecordType == "edge")
}
func (m *portableMapper) source(s *PortableSource) {
	r := &s.Revision
	m.id("revision", &r.ID)
	m.id("project", &r.ProjectID)
	m.id("revision", r.ParentRevisionID)
	m.list("source_snapshot", r.SourceSnapshotIDs)
	for i := range s.Coverage.Snapshots {
		m.snapshot(&s.Coverage.Snapshots[i])
	}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		m.id("node", &n.ID)
		m.id("node", n.ParentID)
		m.list("evidence", n.EvidenceIDs)
		m.owner(n.Ownership)
		m.fresh(n.Freshness)
		n.Attributes = m.attributes(n.Kind, n.Attributes, false)
		n.Source = nil
	}
	for i := range s.Edges {
		e := &s.Edges[i]
		m.id("edge", &e.ID)
		m.id("node", &e.From)
		m.id("node", &e.To)
		m.list("evidence", e.EvidenceIDs)
		m.owner(e.Ownership)
		m.fresh(e.Freshness)
		e.Attributes = m.attributes(e.Kind, e.Attributes, true)
		e.Source = nil
	}
	for i := range s.Evidence {
		e := &s.Evidence[i]
		m.id("evidence", &e.ID)
		m.record(&e.SubjectID)
		m.id("repository", &e.Source.RepositoryID)
		m.id("source_snapshot", &e.Source.SnapshotID)
		m.owner(e.Ownership)
		m.fresh(e.Freshness)
	}
	s.RawEvidence = map[string]jsontext.Value{}
	for _, e := range s.Evidence {
		raw, err := json.Marshal(e)
		if err != nil {
			m.err = err
			return
		}
		s.RawEvidence[e.ID] = raw
	}
	m.vector(s.SourceVector)
	for i := range s.Assertions {
		a := &s.Assertions[i]
		m.id(a.RecordType, &a.RecordID)
		m.owner(&a.Owner)
		m.payload(&a.Payload)
		m.list("evidence", a.EvidenceIDs)
		m.fresh(&a.Freshness)
		for j := range a.FieldCurrentness {
			m.fresh(&a.FieldCurrentness[j].Own)
			m.fresh(&a.FieldCurrentness[j].Dependency)
		}
		for j := range a.DependencyClaims {
			b := &a.DependencyClaims[j]
			m.id("revision", &b.BaseRevisionID)
			m.assertionRef(&b.Target)
			m.valueRef(b.ValueContext)
		}
	}
	for i := range s.Selections {
		v := &s.Selections[i]
		m.id(v.RecordType, &v.ID)
		m.id("repository", &v.Select.RepositoryID)
	}
	for i := range s.Currentness {
		v := &s.Currentness[i]
		m.id(v.RecordType, &v.RecordID)
		m.id("repository", &v.RepositoryID)
		m.fresh(&v.Own)
		m.fresh(&v.Dependency)
		for j := range v.Fields {
			m.fresh(&v.Fields[j].Own)
			m.fresh(&v.Fields[j].Dependency)
		}
	}
	for i := range s.LegacyProofBases {
		v := &s.LegacyProofBases[i]
		m.id("project", &v.ProjectID)
		m.id("revision", &v.SourceRevisionID)
		m.id(v.RecordType, &v.RecordID)
		m.id("evidence", &v.EvidenceID)
	}
	if len(s.ArtifactContext) > 0 {
		c, err := DecodeVersionedArtifactContext(s.ArtifactContext, r.ArtifactPins)
		if err != nil {
			m.err = err
			return
		}
		v := m.context(c, r.ArtifactPins)
		if m.err != nil || m.collect {
			return
		}
		s.ArtifactContext, err = EncodeArtifactContextV3(v)
		if err != nil {
			m.err = err
		}
		r.ArtifactPins = []ArtifactPin{}
	} else if len(r.ArtifactPins) > 0 {
		m.err = invalid("context", "Pins require their complete frozen context")
	}
}

func PortableModelIdentities(model PortableModel) ([]PortableIdentity, error) {
	copy, err := portableClone(model)
	if err != nil {
		return nil, err
	}
	m := newPortableMapper(&copy, PortableRemap{}, true)
	m.model(&copy)
	if m.err != nil {
		return nil, m.err
	}
	out := slices.Collect(maps.Keys(m.seen))
	slices.SortFunc(out, func(a, b PortableIdentity) int {
		return strings.Compare(a.Kind+":"+a.Parent+":"+a.ID, b.Kind+":"+b.Parent+":"+b.ID)
	})
	return out, nil
}
func RemapPortableModel(model PortableModel, remap PortableRemap) (*PortableModel, error) {
	if !ValidID(remap.OriginInstallationID) || !ValidID(remap.InstallationID) {
		return nil, invalid("namespace", "Exact installation IDs required")
	}
	copy, err := portableClone(model)
	if err != nil {
		return nil, err
	}
	m := newPortableMapper(&copy, remap, false)
	m.model(&copy)
	if m.err != nil {
		return nil, m.err
	}
	if len(m.seen) != len(m.ids) {
		return nil, invalid("idMap", "Extraneous identity map entries")
	}
	return &copy, nil
}
