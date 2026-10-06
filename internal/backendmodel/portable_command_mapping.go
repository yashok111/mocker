package backendmodel

import "encoding/json/v2"

func (m *portableMapper) changeCommand(c *ChangeProposalCommand, payloads map[string]SourceAssertionPayload) {
	typ := c.RecordType
	switch c.Type {
	case "create_node", "update_node", "remove_node":
		typ = "node"
	case "upsert_edge", "remove_edge":
		typ = "edge"
	}
	if c.ID != "" {
		m.id(typ, &c.ID)
	}
	m.id("node", c.ParentID)
	m.id("node", &c.From)
	m.id("node", &c.To)
	for _, id := range []*string{&c.ColumnID, &c.ConstraintID, &c.IndexID, &c.TableID, &c.StepID, &c.MappingID} {
		m.id("node", id)
	}
	m.id("edge", &c.EdgeID)
	if c.Update != nil {
		m.id("node", c.Update.ParentID)
		if c.Update.Attributes != nil {
			c.Update.Attributes = m.attributes(c.Update.Kind, c.Update.Attributes, false)
		}
	}
	if c.Attributes != nil {
		kind := c.Kind
		if c.Type == "edit_flow_step" {
			kind = "flow_step"
		}
		c.Attributes = m.attributes(kind, c.Attributes, typ == "edge" || c.Type == "edit_branch")
	}
	if len(c.Definition) > 0 {
		fields, err := relationalObject(c.Definition)
		if err != nil {
			m.err = err
			return
		}
		fields = m.relationalValues(fields)
		c.Definition, err = json.Marshal(fields)
		if err != nil {
			m.err = err
		}
	}
	for i := range c.Sources {
		m.valueRef(&c.Sources[i])
	}
	m.valueRef(c.Destination)
	if c.Transport != nil {
		m.id("edge", &c.Transport.EmitsEdgeID)
		m.id("edge", &c.Transport.DeliveryEdgeID)
	}
	if c.Target != nil {
		m.identity(c.Target)
	}
	for i := range c.APIBindings {
		m.id("node", &c.APIBindings[i].SourceNodeID)
	}
	for i := range c.EditorBindings {
		m.list("node", c.EditorBindings[i].SourceNodeIDs)
	}
	for i := range c.Criteria {
		m.criterion(&c.Criteria[i], payloads)
	}
}
