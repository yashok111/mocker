package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
)

func changeCommandFields(c ChangeProposalCommand) ([]string, []string, error) {
	fields, ok := map[string]string{
		"create_node": "id kind name parentId attributes", "update_node": "id update", "rename": "recordType id name", "remove_node": "id", "remove_edge": "id",
		"upsert_edge": "id kind from to attributes", "alter_column": "columnId facetKey change", "edit_flow_step": "stepId attributes", "edit_branch": "edgeId kind from to attributes",
		"set_field_mapping": "mappingId parentId sources destination transform analysisStatus gaps", "remove_artifact_pin": "artifact", "map_identity": "target expectedExternalKey newExternalKey", "set_criteria": "criteria",
	}[c.Type]
	optional := []string{}
	if !ok {
		var err error
		fields, err = changeVariableCommandFields(c)
		if err != nil {
			return nil, nil, err
		}
	}
	if c.Type == "set_field_mapping" {
		optional = []string{"transport", "description"}
	}
	required := make([]string, 3, 16)
	copy(required, []string{"type", "commandId", "reason"})
	return append(required, strings.Fields(fields)...), optional, nil
}
func changeVariableCommandFields(c ChangeProposalCommand) (string, error) {
	switch c.Type {
	case "alter_constraint", "alter_index":
		fields := "action facetKey"
		if c.Type == "alter_constraint" {
			fields += " constraintId"
		} else {
			fields += " indexId"
		}
		switch c.Action {
		case "create":
			fields += " name tableId definition"
		case "update":
			fields += " definition"
		case "remove":
		default:
			return "", invalid("action", "Expected create, update or remove")
		}
		return fields, nil
	case "set_artifact_pin":
		fields := "artifact revisionId editorBindings"
		if c.Artifact != nil && c.Artifact.Kind == "api_design" {
			fields += " apiBindings"
		}
		return fields, nil
	default:
		return "", invalid("type", "Unknown full proposal command")
	}
}

func (c ChangeProposalCommand) MarshalJSON() ([]byte, error) {
	required, optional, err := changeCommandFields(c)
	if err != nil {
		return nil, err
	}
	type plain ChangeProposalCommand
	raw, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	var m map[string]jsontext.Value
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	// Empty complete groups are meaningful, unlike omitted groups.
	for k, v := range map[string]any{"attributes": c.Attributes, "sources": c.Sources, "gaps": c.Gaps, "criteria": c.Criteria, "editorBindings": c.EditorBindings, "apiBindings": c.APIBindings} {
		if slices.Contains(required, k) {
			m[k], err = json.Marshal(v)
			if err != nil {
				return nil, err
			}
		}
	}
	for k := range m {
		if !slices.Contains(required, k) && !slices.Contains(optional, k) {
			delete(m, k)
		}
	}
	return json.Marshal(m)
}

func (c *ChangeProposalCommand) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("command", err.Error())
	}
	type plain ChangeProposalCommand
	var v plain
	if err = json.Unmarshal(b, &v, json.RejectUnknownMembers(true)); err != nil {
		return invalid("command", err.Error())
	}
	decoded := ChangeProposalCommand(v)
	required, optional, err := changeCommandFields(decoded)
	if err != nil {
		return err
	}
	if err = relationalFields(m, required, optional); err != nil {
		return invalid("command", err.Error())
	}
	for k, raw := range m {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && k != "parentId" && k != "expectedExternalKey" {
			return invalid(k, "Null is not accepted")
		}
	}
	if err = decoded.Validate(); err != nil {
		return err
	}
	*c = decoded
	return nil
}

func (c ChangeProposalCommand) Validate() error {
	if !ValidID(c.CommandID) || !validAPIText(c.Reason, 1, 4096) {
		return invalid("command", "Canonical commandId and 1–4096-byte reason are required")
	}
	validators := map[string]func() error{
		"create_node": c.validateCreateNode, "update_node": c.validateUpdateNode, "rename": c.validateRename,
		"remove_node": func() error { return validateChangeIDs(c.ID) }, "remove_edge": func() error { return validateChangeIDs(c.ID) },
		"upsert_edge": c.validateEdgeCommand, "edit_branch": c.validateEdgeCommand, "alter_column": c.validateColumnCommand,
		"alter_constraint": c.validateRelationalCommand, "alter_index": c.validateRelationalCommand, "edit_flow_step": c.validateFlowStepCommand,
		"set_field_mapping": c.validateMappingCommand, "set_artifact_pin": c.validateArtifactCommand, "remove_artifact_pin": c.validateArtifactCommand,
		"map_identity": c.validateIdentityCommand, "set_criteria": func() error { return validateChangeCriteria(c.Criteria) },
	}
	validate, ok := validators[c.Type]
	if !ok {
		return invalid("type", "Unknown full proposal command")
	}
	return validate()
}
func validateChangeIDs(ids ...string) error {
	for _, id := range ids {
		if !ValidID(id) {
			return invalid("id", "Expected canonical nonzero UUID")
		}
	}
	return nil
}
func validateChangeParent(id *string) error {
	if id == nil {
		return nil
	}
	return validateChangeIDs(*id)
}
func (c ChangeProposalCommand) validateCreateNode() error {
	if !slices.Contains(SupportedNodeKindsForProfile(ComposedProfile), c.Kind) {
		return invalid("kind", "Unsupported desired node kind")
	}
	if err := validateChangeIDs(c.ID); err != nil {
		return err
	}
	if err := validateChangeParent(c.ParentID); err != nil {
		return err
	}
	if _, err := normalizeName(c.Name); err != nil {
		return err
	}
	return validateChangeAttributes(c.Kind, c.Attributes, false)
}
func (c ChangeProposalCommand) validateUpdateNode() error {
	if err := validateChangeIDs(c.ID); err != nil {
		return err
	}
	if c.Update == nil {
		return invalid("update", "Missing typed update")
	}
	if !slices.Contains(SupportedNodeKindsForProfile(ComposedProfile), c.Update.Kind) {
		return invalid("kind", "Unsupported desired node kind")
	}
	switch c.Update.Group {
	case "attributes":
		return validateChangeAttributes(c.Update.Kind, c.Update.Attributes, false)
	case "parent":
		return validateChangeParent(c.Update.ParentID)
	default:
		return invalid("update/group", "Unknown node update group")
	}
}
func (c ChangeProposalCommand) validateRename() error {
	if err := validateChangeIDs(c.ID); err != nil {
		return err
	}
	if c.RecordType != "node" && c.RecordType != "edge" {
		return invalid("recordType", "Expected node or edge")
	}
	_, err := normalizeName(c.Name)
	return err
}
func (c ChangeProposalCommand) validateEdgeCommand() error {
	if !slices.Contains(SupportedEdgeKindsForProfile(ComposedProfile), c.Kind) {
		return invalid("kind", "Unsupported desired edge kind")
	}
	id := c.ID
	if c.Type == "edit_branch" {
		id = c.EdgeID
		if !slices.Contains([]string{"next", "branch", "error", "returns"}, c.Kind) {
			return invalid("kind", "Expected a control-flow edge")
		}
	}
	if err := validateChangeIDs(id, c.From, c.To); err != nil {
		return err
	}
	return validateChangeAttributes(c.Kind, c.Attributes, true)
}
func (c ChangeProposalCommand) validateColumnCommand() error {
	if err := validateChangeIDs(c.ColumnID); err != nil {
		return err
	}
	if !externalKey(c.FacetKey) || c.Change == nil {
		return invalid("change", "Explicit facet and column group are required")
	}
	return nil
}
func (c ChangeProposalCommand) validateRelationalCommand() error {
	id, kind := c.ConstraintID, "constraint"
	if c.Type == "alter_index" {
		id, kind = c.IndexID, "index"
	}
	if err := validateChangeIDs(id); err != nil {
		return err
	}
	if !externalKey(c.FacetKey) {
		return invalid("facetKey", "An explicit facet is required")
	}
	if c.Action == "create" {
		if err := validateChangeIDs(c.TableID); err != nil {
			return err
		}
		if _, err := normalizeName(c.Name); err != nil {
			return err
		}
	}
	if c.Action != "remove" {
		return validateChangeDefinition(kind, c.Definition)
	}
	return nil
}
func (c ChangeProposalCommand) validateFlowStepCommand() error {
	if err := validateChangeIDs(c.StepID); err != nil {
		return err
	}
	return validateChangeAttributes("flow_step", c.Attributes, false)
}
func (c ChangeProposalCommand) validateMappingCommand() error {
	if err := validateChangeIDs(c.MappingID); err != nil {
		return err
	}
	if c.ParentID == nil {
		return invalid("parentId", "A mapping requires an explicit parent")
	}
	if err := validateChangeParent(c.ParentID); err != nil {
		return err
	}
	missing := c.Destination == nil || c.Transform == nil || c.Gaps == nil
	if c.Sources == nil || len(c.Sources) > 64 || missing {
		return invalid("mapping", "Complete bounded mapping values are required")
	}
	attrs, err := changeMappingAttributes(c)
	if err != nil {
		return err
	}
	return validateChangeAttributes("field_mapping", attrs, false)
}
func (c ChangeProposalCommand) validateArtifactCommand() error {
	if c.Artifact == nil {
		return invalid("artifact", "Artifact is required")
	}
	return c.artifactCommand().Validate()
}
func (c ChangeProposalCommand) validateIdentityCommand() error {
	if c.Target == nil || !externalKey(c.NewExternalKey) {
		return invalid("target", "An exact identity target and bounded new key are required")
	}
	if err := c.Target.Validate(); err != nil {
		return err
	}
	if c.ExpectedExternalKey != nil && !externalKey(*c.ExpectedExternalKey) {
		return invalid("expectedExternalKey", "Expected a bounded existing key or explicit null")
	}
	if c.Target.Kind == "source_identity" && c.ExpectedExternalKey == nil {
		return invalid("expectedExternalKey", "Source identity requires an exact expected key")
	}
	return nil
}

func validateChangeAttributes(kind string, attrs map[string]jsontext.Value, edge bool) error {
	if attrs == nil {
		return invalid("attributes", "Complete attributes object is required")
	}
	// Proof wrappers are not editable groups. The source schema itself remains
	// untouched and structural validation may inspect retained proof wrappers.
	var walk func(jsontext.Value) error
	walk = func(raw jsontext.Value) error {
		var m map[string]jsontext.Value
		if len(raw) == 0 || bytes.TrimSpace(raw)[0] != '{' {
			return nil
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		for k, v := range m {
			if slices.Contains([]string{"evidenceIds", "evidenceKeys", "sourceKind", "sourceSnapshotId", "ownership", "freshness", "facetComparison"}, k) {
				return invalid(k, "Source provenance is not desired input")
			}
			if err := walk(v); err != nil {
				return err
			}
		}
		return nil
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	if err = walk(raw); err != nil {
		return err
	}
	if err = validateSourceStructuralAttributes(ComposedProfile, kind, attrs, edge); err != nil {
		return invalid("attributes", err.Error())
	}
	return nil
}

func (u ChangeNodeUpdate) MarshalJSON() ([]byte, error) {
	m := map[string]any{"kind": u.Kind, "group": u.Group}
	if u.Group == "attributes" {
		m["attributes"] = u.Attributes
	} else {
		m["parentId"] = u.ParentID
	}
	return json.Marshal(m)
}
func (u *ChangeNodeUpdate) UnmarshalJSON(b []byte) error {
	type plain ChangeNodeUpdate
	var v plain
	if err := json.Unmarshal(b, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	fields := []string{"kind", "group"}
	switch v.Group {
	case "attributes":
		fields = append(fields, "attributes")
	case "parent":
		fields = append(fields, "parentId")
	default:
		return invalid("group", "Unknown update group")
	}
	m, err := relationalObject(b)
	if err != nil {
		return err
	}
	if err = relationalFields(m, fields, nil); err != nil {
		return err
	}
	*u = ChangeNodeUpdate(v)
	return nil
}
func (c *ChangeColumnGroup) UnmarshalJSON(b []byte) error {
	type plain ChangeColumnGroup
	var v plain
	if err := json.Unmarshal(b, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	fields := []string{"group"}
	switch v.Group {
	case "native_type":
		fields = append(fields, "nativeType", "typeFamily")
	case "nullable":
		fields = append(fields, "nullable")
	case "default":
		fields = append(fields, "defaultExpression")
	default:
		return invalid("change/group", "Unknown column group")
	}
	m, err := relationalObject(b)
	if err != nil {
		return err
	}
	if err = relationalFields(m, fields, nil); err != nil {
		return err
	}
	for _, f := range fields[1:] {
		typ := "string"
		nullable := f == "defaultExpression"
		if f == "nullable" {
			typ = "bool"
		}
		if err = relationalScalarValue(m[f], typ, nullable); err != nil {
			return err
		}
	}
	*c = ChangeColumnGroup(v)
	return nil
}
func (c ChangeProposalCommand) artifactCommand() ArtifactPinCommand {
	return ArtifactPinCommand{Type: c.Type, Artifact: *c.Artifact, RevisionID: c.RevisionID, APIBindings: c.APIBindings, EditorBindings: c.EditorBindings, Reason: c.Reason}
}
func changeMappingAttributes(c ChangeProposalCommand) (map[string]jsontext.Value, error) {
	raw, err := json.Marshal(LineageMappingAttributes{Sources: c.Sources, Destination: *c.Destination, Transform: *c.Transform, AnalysisStatus: c.AnalysisStatus, Gaps: c.Gaps, Transport: c.Transport, Description: c.Description})
	if err != nil {
		return nil, err
	}
	return relationalObject(raw)
}
func validateChangeCommands(commands []ChangeProposalCommand) error {
	if len(commands) < 1 || len(commands) > MaxChangeProposalCommands {
		return invalid("commands", "Expected 1–100 commands")
	}
	raw, err := json.Marshal(commands)
	if err != nil {
		return err
	}
	if len(raw) > MaxChangeProposalCommandBytes {
		return limitFault("Change command batch exceeds 1 MiB")
	}
	var checked []ChangeProposalCommand
	if err = json.Unmarshal(raw, &checked); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, c := range checked {
		if ids[c.CommandID] {
			return invalid("commandId", "Command IDs must be distinct")
		}
		ids[c.CommandID] = true
	}
	return nil
}

func validateChangeDefinition(kind string, raw jsontext.Value) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	reference := m["reference"]
	delete(m, "reference")
	if err = validateChangeAttributes(kind, map[string]jsontext.Value{"facets": mustChangeJSON(map[string]jsontext.Value{"selected": mustChangeJSON(m)})}, false); err != nil {
		return err
	}
	var constraintKind string
	_ = json.Unmarshal(m["constraintKind"], &constraintKind)
	if kind != "constraint" || constraintKind != "foreign_key" {
		if reference != nil {
			return invalid("reference", "Only a foreign key accepts reference")
		}
		return nil
	}
	return validateChangeFKReference(reference)
}
func validateChangeFKReference(reference jsontext.Value) error {
	ref, err := relationalObject(reference)
	if err != nil {
		return err
	}
	if err = relationalFields(ref, []string{"id", "targetTableId", "columnPairs", "updateAction", "deleteAction", "matchType"}, nil); err != nil {
		return err
	}
	for _, k := range []string{"id", "targetTableId"} {
		var id string
		if json.Unmarshal(ref[k], &id) != nil || !ValidID(id) {
			return invalid(k, "Expected canonical UUID")
		}
	}
	pairs, err := relationalArray(ref["columnPairs"], 64)
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		return invalid("columnPairs", "FK requires ordered column pairs")
	}
	for _, pair := range pairs {
		p, err := relationalObject(pair)
		if err != nil {
			return err
		}
		if err = relationalFields(p, []string{"fromColumnId", "toColumnId"}, nil); err != nil {
			return err
		}
		for _, v := range p {
			var id string
			if json.Unmarshal(v, &id) != nil || !ValidID(id) {
				return invalid("columnPairs", "Expected canonical column UUID")
			}
		}
	}
	for _, k := range []string{"updateAction", "deleteAction"} {
		if err = relationalScalarValue(ref[k], "string", false, "no_action", "restrict", "cascade", "set_null", "set_default"); err != nil {
			return err
		}
	}
	return relationalScalarValue(ref["matchType"], "string", false, "simple", "full", "partial")
}
func mustChangeJSON(v any) jsontext.Value {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
