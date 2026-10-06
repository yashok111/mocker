package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func (m *portableMapper) legacyBasis(b *ProposalBasis) {
	if b == nil {
		return
	}
	m.id("revision", &b.RevisionID)
	m.record(&b.SubjectID)
}
func (m *portableMapper) legacyProposal(p *PortableProposal) {
	h := p.Legacy
	m.id("proposal", &h.ID)
	m.id("project", &h.ProjectID)
	m.id("revision", &h.BaseRevisionID)
	m.id("repository", &h.RepositoryID)
	m.id("node", &h.DatastoreID)
	m.id("proposal_revision", &h.DraftRevisionID)
	h.Status = "draft"
	for i := range p.LegacyRevisions {
		v := &p.LegacyRevisions[i]
		m.id("proposal_revision", &v.ID)
		m.id("proposal", &v.ProposalID)
		m.id("proposal_revision", v.ParentRevisionID)
		m.id("revision", &v.BaseRevisionID)
		m.list("source_snapshot", v.SourceSnapshotIDs)
		for j := range v.Commands {
			c := &v.Commands[j]
			m.id("node", &c.ColumnID)
			m.id("node", &c.TableID)
			m.id("node", &c.ConstraintID)
			m.id("node", &c.TargetTableID)
			for k := range c.ColumnPairs {
				m.id("node", &c.ColumnPairs[k].FromColumnID)
				m.id("node", &c.ColumnPairs[k].ToColumnID)
			}
			for k := range c.Criteria {
				for a := range c.Criteria[k].TargetIDs {
					m.record(&c.Criteria[k].TargetIDs[a])
				}
			}
		}
		for j := range v.Overlays {
			o := &v.Overlays[j]
			m.id(o.RecordType, &o.SubjectID)
			m.legacyBasis(o.Base)
			m.id("node", o.ParentID)
			m.id("node", &o.FromID)
			m.id("node", &o.ToID)
			o.Values = m.relationalValues(o.Values)
			for k, origin := range o.PropertyOrigins {
				m.legacyBasis(origin.Base)
				m.list("evidence", origin.EvidenceIDs)
				o.PropertyOrigins[k] = origin
			}
		}
		for j := range v.Criteria {
			for k := range v.Criteria[j].TargetIDs {
				m.record(&v.Criteria[j].TargetIDs[k])
			}
		}
	}
}
func (m *portableMapper) relationalValues(v map[string]jsontext.Value) map[string]jsontext.Value {
	for _, key := range []string{"columnIds", "dependencyIds", "parentIds", "evidenceIds"} {
		raw, ok := v[key]
		if !ok {
			continue
		}
		var ids []string
		if err := json.Unmarshal(raw, &ids); err != nil {
			m.err = err
			return v
		}
		kind := "node"
		if key == "evidenceIds" {
			kind = "evidence"
		}
		m.list(kind, ids)
		v[key], _ = json.Marshal(ids)
	}
	if raw, ok := v["terms"]; ok {
		var values []relationalIndexTerm
		if err := json.Unmarshal(raw, &values); err != nil {
			m.err = err
			return v
		}
		for i := range values {
			m.id("node", &values[i].ColumnID)
		}
		v["terms"], _ = json.Marshal(values)
	}
	if raw, ok := v["columnPairs"]; ok {
		var values []DatabaseColumnPair
		if err := json.Unmarshal(raw, &values); err != nil {
			m.err = err
			return v
		}
		for i := range values {
			m.id("node", &values[i].FromColumnID)
			m.id("node", &values[i].ToColumnID)
		}
		v["columnPairs"], _ = json.Marshal(values)
	}
	if raw, ok := v["changes"]; ok {
		var values []relationalMigrationChange
		if err := json.Unmarshal(raw, &values); err != nil {
			m.err = err
			return v
		}
		for i := range values {
			m.id("node", &values[i].Target.ObjectID)
			m.id("revision", &values[i].Target.RevisionID)
		}
		v["changes"], _ = json.Marshal(values)
	}
	return v
}
