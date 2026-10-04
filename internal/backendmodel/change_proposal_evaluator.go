package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
)

type changeEvaluation struct {
	readBudget      *changeReadBudget
	referenceGraphs map[string]*SourceGraphSnapshot
	artifactRequest *EditorArtifactRequest
	source          *SourceGraphSnapshot
	revision        ChangeProposalRevision
	records         map[string]ChangeCreatedRecord
	used            map[string]ChangeObjectIdentity
	newIDs          []ChangeObjectIdentity
	changes         []ChangeProposalChange
}

func newChangeEvaluation(source *SourceGraphSnapshot, draft ChangeProposalRevision, used map[string]ChangeObjectIdentity) (*changeEvaluation, error) {
	// The entire draft is detached: preview must never mutate a loaded draft,
	// nested attribute bytes, baseline source or another concurrent request.
	raw, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	var copied ChangeProposalRevision
	if err = json.Unmarshal(raw, &copied); err != nil {
		return nil, err
	}
	e := &changeEvaluation{source: source, revision: copied, records: map[string]ChangeCreatedRecord{}, used: maps.Clone(used), newIDs: []ChangeObjectIdentity{}, changes: []ChangeProposalChange{}}
	add := func(ref ChangeRecordRef, p SourceAssertionPayload) {
		origin := EffectiveOrigin{Kind: "source", BaseRef: &EffectiveBasis{RevisionID: draft.BaseRevisionID, SemanticHash: draft.BaseSemanticHash, RecordType: ref.RecordType, ID: ref.ID}}
		e.records[ref.ID] = ChangeCreatedRecord{ChangeRecordRef: ref, Payload: p, Origin: origin}
		e.used[ref.ID] = ChangeObjectIdentity{ChangeRecordRef: ref, Kind: p.Kind, Origin: origin}
	}
	for _, n := range source.State.Nodes {
		add(ChangeRecordRef{RecordType: "node", ID: n.ID}, sourceNodePayload(n))
	}
	for _, edge := range source.State.Edges {
		add(ChangeRecordRef{RecordType: "edge", ID: edge.ID}, sourceEdgePayload(edge))
	}
	for _, r := range copied.Delta.Created {
		e.records[r.ID] = r
		if _, ok := e.used[r.ID]; !ok {
			e.used[r.ID] = ChangeObjectIdentity{ChangeRecordRef: r.ChangeRecordRef, Kind: r.Payload.Kind, Origin: r.Origin}
		}
	}
	for _, p := range copied.Delta.Properties {
		record, ok := e.records[p.ID]
		if !ok {
			return nil, invalid("delta", "Changed record is missing")
		}
		record.Payload, err = ApplySourceProperty(record.Payload, p.Selector, p.Value)
		if err != nil {
			return nil, err
		}
		e.records[p.ID] = record
	}

	// Removing every semantic group removes the selected facet itself. Retained
	// proof wrappers cannot keep an otherwise deleted facet alive on reload.
	for id, record := range e.records {
		if !relationalSubject(record.Payload.Kind, record.Payload.Attributes, record.RecordType == "edge") {
			continue
		}
		facets, _, err := relationalFacetObject(record.Payload.Kind, record.Payload.Attributes)
		if err != nil {
			return nil, err
		}
		for key, raw := range facets {
			fields, err := relationalObject(raw)
			if err != nil {
				return nil, err
			}
			for _, proof := range []string{"sourceKind", "sourceSnapshotId", "evidenceIds", "evidenceKeys", "freshness"} {
				delete(fields, proof)
			}
			if len(fields) == 0 {
				record.Payload, err = changeWithFacet(record.Payload, key, nil)
				if err != nil {
					return nil, err
				}
			}
		}
		e.records[id] = record
	}
	for _, r := range copied.Delta.Removed {
		delete(e.records, r.ID)
	}
	return e, nil
}

func (e *changeEvaluation) origin(c ChangeProposalCommand, ref ChangeRecordRef) EffectiveOrigin {
	origin := EffectiveOrigin{Kind: "intent", CommandID: c.CommandID, Reason: c.Reason}
	if old, ok := e.used[ref.ID]; ok && old.Origin.Kind == "source" {
		origin.BaseRef = old.Origin.BaseRef
	}
	return origin
}
func (e *changeEvaluation) live(typ, id, kind string) (ChangeCreatedRecord, error) {
	r, ok := e.records[id]
	if !ok || r.RecordType != typ {
		return r, invalid("id", "Target must be a live record of the selected type")
	}
	if kind != "" && r.Payload.Kind != kind {
		return r, invalid("kind", "An existing graph ID cannot change kind")
	}
	return r, nil
}
func (e *changeEvaluation) create(c ChangeProposalCommand, ref ChangeRecordRef, p SourceAssertionPayload) error {
	if _, ok := e.used[ref.ID]; ok {
		return &FaultError{Status: 409, Code: "backend_change_identity_conflict", Message: "Graph ID is already used or permanently reserved"}
	}
	origin := e.origin(c, ref)
	r := ChangeCreatedRecord{ChangeRecordRef: ref, Payload: p, Origin: origin}
	e.records[ref.ID] = r
	e.revision.Delta.Created = append(e.revision.Delta.Created, r)
	id := ChangeObjectIdentity{ChangeRecordRef: ref, Kind: p.Kind, Origin: origin}
	e.used[ref.ID] = id
	e.newIDs = append(e.newIDs, id)
	return nil
}
func (e *changeEvaluation) replace(c ChangeProposalCommand, r ChangeCreatedRecord, next SourceAssertionPayload) error {
	selectors, err := sourceSelectors([]SourceAssertionPayload{r.Payload, next})
	if err != nil {
		return err
	}
	for _, selector := range selectors {
		old, err := SelectSourceProperty(r.Payload, selector)
		if err != nil {
			return err
		}
		value, err := SelectSourceProperty(next, selector)
		if err != nil {
			return err
		}
		a, _ := canonicalJSON(old)
		b, _ := canonicalJSON(value)
		if string(a) == string(b) {
			continue
		}
		change := ChangeProperty{ChangeRecordRef: r.ChangeRecordRef, Selector: selector, Value: value, Origin: e.origin(c, r.ChangeRecordRef)}
		pos := slices.IndexFunc(e.revision.Delta.Properties, func(p ChangeProperty) bool {
			return p.ChangeRecordRef == change.ChangeRecordRef && p.Selector == selector
		})
		if pos >= 0 {
			e.revision.Delta.Properties[pos] = change
		} else {
			e.revision.Delta.Properties = append(e.revision.Delta.Properties, change)
		}
	}
	r.Payload = next
	e.records[r.ID] = r
	return nil
}
func (e *changeEvaluation) remove(c ChangeProposalCommand, typ, id string) error {
	r, err := e.live(typ, id, "")
	if err != nil {
		return err
	}
	e.revision.Delta.Removed = append(e.revision.Delta.Removed, ChangeRemoval{ChangeRecordRef: r.ChangeRecordRef, Origin: e.origin(c, r.ChangeRecordRef)})
	delete(e.records, id)
	return nil
}
func (e *changeEvaluation) edge(c ChangeProposalCommand, id, kind, from, to string, attrs map[string]jsontext.Value) error {
	next := SourceAssertionPayload{RecordType: "edge", Kind: kind, From: from, To: to, Attributes: attrs}
	if _, ok := e.records[id]; !ok {
		return e.create(c, ChangeRecordRef{RecordType: "edge", ID: id}, next)
	}
	r, err := e.live("edge", id, kind)
	if err != nil {
		return err
	}
	return e.replace(c, r, next)
}
func (e *changeEvaluation) apply(c ChangeProposalCommand) error {
	if err := c.Validate(); err != nil {
		return err
	}
	actions := map[string]func(ChangeProposalCommand) error{
		"create_node": e.createNode, "update_node": e.updateNode, "rename": e.renameRecord,
		"remove_node": e.removeRecord, "remove_edge": e.removeRecord, "upsert_edge": e.upsertEdge, "edit_branch": e.upsertEdge,
		"alter_column": e.alterColumn, "alter_constraint": e.alterRelational, "alter_index": e.alterRelational,
		"edit_flow_step": e.editFlowStep, "set_field_mapping": e.setFieldMapping, "map_identity": e.mapIdentity,
		"set_criteria":     func(c ChangeProposalCommand) error { e.revision.Criteria = c.Criteria; return nil },
		"set_artifact_pin": func(ChangeProposalCommand) error { return nil }, "remove_artifact_pin": func(ChangeProposalCommand) error { return nil },
	}
	action, ok := actions[c.Type]
	if !ok {
		return invalid("type", "Unknown full proposal command")
	}
	if err := action(c); err != nil {
		return err
	}
	ref := changeCommandObject(c)
	e.changes = append(e.changes, ChangeProposalChange{CommandID: c.CommandID, Type: c.Type, RecordType: ref.RecordType, ID: ref.ID})
	return nil
}
func changeCommandObject(c ChangeProposalCommand) ChangeRecordRef {
	switch c.Type {
	case "create_node", "update_node", "remove_node":
		return ChangeRecordRef{RecordType: "node", ID: c.ID}
	case "upsert_edge", "remove_edge":
		return ChangeRecordRef{RecordType: "edge", ID: c.ID}
	case "rename":
		return ChangeRecordRef{RecordType: c.RecordType, ID: c.ID}
	case "edit_branch":
		return ChangeRecordRef{RecordType: "edge", ID: c.EdgeID}
	case "edit_flow_step":
		return ChangeRecordRef{RecordType: "node", ID: c.StepID}
	case "alter_column":
		return ChangeRecordRef{RecordType: "node", ID: c.ColumnID}
	case "alter_constraint":
		return ChangeRecordRef{RecordType: "node", ID: c.ConstraintID}
	case "alter_index":
		return ChangeRecordRef{RecordType: "node", ID: c.IndexID}
	case "set_field_mapping":
		return ChangeRecordRef{RecordType: "node", ID: c.MappingID}
	case "map_identity":
		return changeIdentityRef(*c.Target)
	}
	return ChangeRecordRef{}
}
func (e *changeEvaluation) createNode(c ChangeProposalCommand) error {
	return e.create(c, ChangeRecordRef{RecordType: "node", ID: c.ID}, SourceAssertionPayload{RecordType: "node", Kind: c.Kind, Name: c.Name, ParentID: c.ParentID, Attributes: c.Attributes})
}
func (e *changeEvaluation) updateNode(c ChangeProposalCommand) error {
	r, err := e.live("node", c.ID, c.Update.Kind)
	if err != nil {
		return err
	}
	next := r.Payload
	if c.Update.Group == "parent" {
		next.ParentID = c.Update.ParentID
	} else {
		next.Attributes = c.Update.Attributes
	}
	return e.replace(c, r, next)
}
func (e *changeEvaluation) renameRecord(c ChangeProposalCommand) error {
	r, err := e.live(c.RecordType, c.ID, "")
	if err != nil {
		return err
	}
	if c.RecordType == "node" {
		next := r.Payload
		next.Name = c.Name
		return e.replace(c, r, next)
	}
	value := ChangeEdgeName{ID: c.ID, Name: c.Name, Origin: e.origin(c, r.ChangeRecordRef)}
	i := slices.IndexFunc(e.revision.Delta.EdgeNames, func(n ChangeEdgeName) bool { return n.ID == c.ID })
	if i < 0 {
		e.revision.Delta.EdgeNames = append(e.revision.Delta.EdgeNames, value)
	} else {
		e.revision.Delta.EdgeNames[i] = value
	}
	return nil
}
func (e *changeEvaluation) removeRecord(c ChangeProposalCommand) error {
	typ := "node"
	if c.Type == "remove_edge" {
		typ = "edge"
	}
	return e.remove(c, typ, c.ID)
}
func (e *changeEvaluation) upsertEdge(c ChangeProposalCommand) error {
	id := c.ID
	if c.Type == "edit_branch" {
		id = c.EdgeID
	}
	return e.edge(c, id, c.Kind, c.From, c.To, c.Attributes)
}
func (e *changeEvaluation) alterColumn(c ChangeProposalCommand) error {
	r, err := e.live("node", c.ColumnID, "column")
	if err != nil {
		return err
	}
	facets, _, err := relationalFacetObject(r.Payload.Kind, r.Payload.Attributes)
	if err != nil {
		return err
	}
	facet, err := relationalObject(facets[c.FacetKey])
	if err != nil {
		return invalid("facetKey", "Selected column facet is absent")
	}
	switch c.Change.Group {
	case "native_type":
		facet["nativeType"], facet["typeFamily"] = c.Change.NativeType, c.Change.TypeFamily
	case "nullable":
		facet["nullable"] = c.Change.Nullable
	case "default":
		facet["defaultExpression"] = c.Change.DefaultExpression
	}
	next, err := changeWithFacet(r.Payload, c.FacetKey, mustChangeJSON(facet))
	if err != nil {
		return err
	}
	return e.replace(c, r, next)
}
func (e *changeEvaluation) editFlowStep(c ChangeProposalCommand) error {
	r, err := e.live("node", c.StepID, "flow_step")
	if err != nil {
		return err
	}
	next := r.Payload
	next.Attributes = c.Attributes
	return e.replace(c, r, next)
}
func (e *changeEvaluation) setFieldMapping(c ChangeProposalCommand) error {
	attrs, err := changeMappingAttributes(c)
	if err != nil {
		return err
	}
	if _, ok := e.records[c.MappingID]; !ok {
		return e.create(c, ChangeRecordRef{RecordType: "node", ID: c.MappingID}, SourceAssertionPayload{RecordType: "node", Kind: "field_mapping", Name: "Field mapping", ParentID: c.ParentID, Attributes: attrs})
	}
	r, err := e.live("node", c.MappingID, "field_mapping")
	if err != nil {
		return err
	}
	next := r.Payload
	next.ParentID, next.Attributes = c.ParentID, attrs
	return e.replace(c, r, next)
}

func changeWithFacet(p SourceAssertionPayload, key string, value jsontext.Value) (SourceAssertionPayload, error) {
	fs, _, err := relationalFacetObject(p.Kind, p.Attributes)
	if err != nil {
		return p, err
	}
	fs = maps.Clone(fs)
	if value == nil {
		delete(fs, key)
	} else {
		fs[key] = value
	}
	p.Attributes = maps.Clone(p.Attributes)
	switch p.Kind {
	case "datastore":
		p.Attributes["relational"] = mustChangeJSON(map[string]any{"facets": fs})
	case "symbol":
		p.Attributes["databaseRoutine"] = mustChangeJSON(map[string]any{"facets": fs})
	default:
		p.Attributes["facets"] = mustChangeJSON(fs)
	}
	return p, nil
}
func (e *changeEvaluation) alterRelational(c ChangeProposalCommand) error {
	id, kind := c.ConstraintID, "constraint"
	if c.Type == "alter_index" {
		id, kind = c.IndexID, "index"
	}
	if c.Action == "remove" {
		return e.removeRelationalFacet(c, id, kind)
	}

	definition, err := relationalObject(c.Definition)
	if err != nil {
		return err
	}
	reference := definition["reference"]
	delete(definition, "reference")
	attrs := map[string]jsontext.Value{"facets": mustChangeJSON(map[string]jsontext.Value{c.FacetKey: mustChangeJSON(definition)})}
	if c.Action == "create" {
		err = e.create(c, ChangeRecordRef{RecordType: "node", ID: id}, SourceAssertionPayload{RecordType: "node", Kind: kind, Name: c.Name, ParentID: new(c.TableID), Attributes: attrs})
	} else {
		r, loadErr := e.live("node", id, kind)
		if loadErr != nil {
			return loadErr
		}
		next, loadErr := changeWithFacet(r.Payload, c.FacetKey, mustChangeJSON(definition))
		if loadErr != nil {
			return loadErr
		}
		err = e.replace(c, r, next)
	}
	if err != nil {
		return err
	}
	if reference == nil {
		return nil
	}
	return e.applyForeignKey(c, id, definition, reference)
}
func (e *changeEvaluation) removeRelationalFacet(c ChangeProposalCommand, id, kind string) error {
	r, err := e.live("node", id, kind)
	if err != nil {
		return err
	}
	fs, _, err := relationalFacetObject(kind, r.Payload.Attributes)
	if err != nil {
		return err
	}
	if _, ok := fs[c.FacetKey]; !ok {
		return invalid("facetKey", "Selected facet is absent")
	}
	if len(fs) == 1 {
		return e.remove(c, "node", id)
	}
	next, err := changeWithFacet(r.Payload, c.FacetKey, nil)
	if err != nil {
		return err
	}
	return e.replace(c, r, next)

}
func (e *changeEvaluation) applyForeignKey(c ChangeProposalCommand, id string, definition map[string]jsontext.Value, reference jsontext.Value) error {
	ref, err := relationalObject(reference)
	if err != nil {
		return err
	}
	var edgeID, target string
	_ = json.Unmarshal(ref["id"], &edgeID)
	_ = json.Unmarshal(ref["targetTableId"], &target)
	facet := map[string]jsontext.Value{}
	for _, key := range []string{"dialect", "analysisStatus", "gaps"} {
		facet[key] = definition[key]
	}
	for _, key := range []string{"columnPairs", "updateAction", "deleteAction", "matchType"} {
		facet[key] = ref[key]
	}
	targetRecord := e.records[target]
	if targetRecord.Payload.Kind == "unresolved_target" {
		facet["targetReason"] = mustChangeJSON("Desired foreign-key target remains unresolved")
	}
	edgeAttrs := map[string]jsontext.Value{"facets": mustChangeJSON(map[string]jsontext.Value{c.FacetKey: mustChangeJSON(facet)})}
	if old, ok := e.records[edgeID]; ok {
		if old.RecordType != "edge" || old.Payload.Kind != "references" {
			return invalid("reference/id", "FK identity cannot change kind")
		}
		next, err := changeWithFacet(old.Payload, c.FacetKey, mustChangeJSON(facet))
		if err != nil {
			return err
		}
		next.From, next.To = id, target
		return e.replace(c, old, next)
	}
	return e.edge(c, edgeID, "references", id, target, edgeAttrs)
}

func (e *changeEvaluation) mapIdentity(c ChangeProposalCommand) error {
	target := *c.Target
	ref := changeIdentityRef(target)
	r, err := e.live(ref.RecordType, ref.ID, "")
	if err != nil {
		return err
	}
	var expected *string
	if target.Kind == "source_identity" {
		if !slices.Contains(e.source.Identities, *target.Source) {
			return invalid("target", "Qualified identity does not match selected draft baseline")
		}
		expected = new(target.Source.ExternalKey)
	} else {
		reserved, ok := e.used[ref.ID]
		if !ok || reserved.Origin.Kind != "intent" {
			return invalid("target", "Intent identity requires a proposal-created record")
		}
	}
	key := changeIdentityKey(target)
	index := slices.IndexFunc(e.revision.Delta.IdentityIntents, func(i ChangeIdentityIntent) bool { return changeIdentityKey(i.Target) == key })
	if index >= 0 {
		expected = e.revision.Delta.IdentityIntents[index].ExternalKey
	}
	same := expected == nil && c.ExpectedExternalKey == nil || expected != nil && c.ExpectedExternalKey != nil && *expected == *c.ExpectedExternalKey
	if !same {
		return &FaultError{Status: 409, Code: "backend_change_identity_conflict", Message: "Expected intended external key changed"}
	}
	value := ChangeIdentityIntent{Target: target, ExternalKey: new(c.NewExternalKey), Origin: e.origin(c, r.ChangeRecordRef)}
	if index < 0 {
		e.revision.Delta.IdentityIntents = append(e.revision.Delta.IdentityIntents, value)
	} else {
		e.revision.Delta.IdentityIntents[index] = value
	}
	return nil
}

func (e *changeEvaluation) snapshot() (*ChangeEvaluationSnapshot, error) {
	rev := e.revision
	out := &ChangeEvaluationSnapshot{
		DocumentVersion:       ChangeProposalDocumentVersion,
		ProposalID:            rev.ProposalID,
		ProposalRevisionID:    rev.ID,
		ProjectID:             e.source.State.Revision.ProjectID,
		BaseRevisionID:        rev.BaseRevisionID,
		BaseSemanticHash:      rev.BaseSemanticHash,
		BaselineSchemaVersion: rev.BaseSchemaVersion,
		SemanticHash:          rev.SemanticHash,
		SchemaVersion:         ComposedSchemaVersion,
		Source:                e.source,
		Nodes:                 []Node{}, Edges: []Edge{}, EdgeNames: []ChangeEdgeName{}, Identities: []ChangeEvaluationIdentity{},
		Criteria: rev.Criteria, SourceVector: rev.SourceVector, SourceSnapshotIDs: rev.SourceSnapshotIDs,
		ArtifactPins: rev.ArtifactPins, ArtifactContext: rev.ArtifactContext,
		Coverage: e.source.State.Revision.Coverage, Origins: []ChangeEvaluationFieldOrigin{}, BaselineEvidence: e.source.State.Evidence,
	}
	for _, id := range slices.Sorted(maps.Keys(e.records)) {
		r := e.records[id]
		p := r.Payload
		if r.RecordType == "node" {
			out.Nodes = append(out.Nodes, Node{ID: id, Kind: p.Kind, Name: p.Name, ParentID: p.ParentID, Attributes: p.Attributes, EvidenceIDs: []string{}})
		} else {
			out.Edges = append(out.Edges, Edge{ID: id, Kind: p.Kind, From: p.From, To: p.To, Attributes: p.Attributes, EvidenceIDs: []string{}})
		}
		selectors, err := sourceSelectors([]SourceAssertionPayload{p})
		if err != nil {
			return nil, err
		}
		for _, selector := range selectors {
			origin := r.Origin
			for _, override := range rev.Delta.Properties {
				if override.ChangeRecordRef == r.ChangeRecordRef && override.Selector == selector {
					origin = override.Origin
					break
				}
			}
			out.Origins = append(out.Origins, ChangeEvaluationFieldOrigin{ChangeRecordRef: r.ChangeRecordRef, Selector: EffectivePropertySelector{Kind: "source", Source: new(selector)}, Origin: origin})
		}
	}
	for _, name := range rev.Delta.EdgeNames {
		if r, ok := e.records[name.ID]; ok && r.RecordType == "edge" {
			out.EdgeNames = append(out.EdgeNames, name)
			out.Origins = append(out.Origins, ChangeEvaluationFieldOrigin{ChangeRecordRef: r.ChangeRecordRef, Selector: EffectivePropertySelector{Kind: "edge_name"}, Origin: name.Origin})
		}
	}
	for _, s := range e.source.Identities {
		if _, ok := e.records[s.ID]; !ok {
			continue
		}
		r := e.records[s.ID]
		out.Identities = append(out.Identities, ChangeEvaluationIdentity{Target: ChangeIdentityTarget{Kind: "source_identity", Source: new(s)}, ExternalKey: new(s.ExternalKey), Origin: EffectiveOrigin{Kind: "source", BaseRef: e.used[r.ID].Origin.BaseRef}})
	}
	for _, r := range rev.Delta.Created {
		if _, ok := e.records[r.ID]; !ok {
			continue
		}
		out.Identities = append(out.Identities, ChangeEvaluationIdentity{Target: ChangeIdentityTarget{Kind: "intent_identity", RecordType: r.RecordType, ID: r.ID}, Origin: r.Origin})
	}
	for i := range out.Identities {
		identity := &out.Identities[i]
		for _, override := range rev.Delta.IdentityIntents {
			if changeIdentityKey(identity.Target) == changeIdentityKey(override.Target) {
				identity.ExternalKey, identity.Origin = override.ExternalKey, override.Origin
			}
		}
	}
	slices.SortFunc(out.Identities, func(a, b ChangeEvaluationIdentity) int {
		return strings.Compare(changeIdentityKey(a.Target), changeIdentityKey(b.Target))
	})
	for _, identity := range out.Identities {
		ref := changeIdentityRef(identity.Target)
		selector := EffectivePropertySelector{Kind: identity.Target.Kind, RecordType: ref.RecordType, ID: ref.ID}
		if identity.Target.Source != nil {
			selector.RepositoryID, selector.ProviderNamespace = identity.Target.Source.RepositoryID, identity.Target.Source.ProviderNamespace
		}
		out.Origins = append(out.Origins, ChangeEvaluationFieldOrigin{ChangeRecordRef: ref, Selector: selector, Origin: identity.Origin})
	}
	return out, nil
}

func validateChangeIdentityKeys(snapshot *ChangeEvaluationSnapshot) error {
	used := map[string]string{}
	for _, identity := range snapshot.Identities {
		if identity.ExternalKey == nil {
			continue
		}
		r := changeIdentityRef(identity.Target)
		key := identity.Target.Kind + "\x00" + r.RecordType + "\x00"
		if identity.Target.Source != nil {
			key += identity.Target.Source.RepositoryID + "\x00" + identity.Target.Source.ProviderNamespace + "\x00"
		}
		key += *identity.ExternalKey
		if id, ok := used[key]; ok && id != r.ID {
			return invalid("identity", "Effective identity key collides within its namespace")
		}
		used[key] = r.ID
	}
	return nil
}

func (e *changeEvaluation) validate(ctx context.Context) (*ChangeEvaluationSnapshot, []ImportDiagnostic, error) {
	snapshot, err := e.snapshot()
	if err != nil {
		return nil, nil, err
	}
	if len(snapshot.Nodes) > MaxRevisionNodes || len(snapshot.Edges) > MaxRevisionEdges {
		return nil, nil, limitFault("Effective graph exceeds record bounds")
	}
	diagnostics := validateChangeBaselineVocabulary(snapshot)
	structuralDiagnostics, err := ValidateSourceStructure(ctx, SourceStructuralGraph{SchemaVersion: ComposedSchemaVersion, Nodes: snapshot.Nodes, Edges: snapshot.Edges})
	if err != nil {
		return nil, nil, err
	}
	diagnostics = append(diagnostics, structuralDiagnostics...)
	if err = validateChangeIdentityKeys(snapshot); err != nil {
		diagnostics = append(diagnostics, ImportDiagnostic{Code: "backend_change_invalid", Path: "identities", Message: err.Error()})
	}
	return snapshot, diagnostics, nil
}
