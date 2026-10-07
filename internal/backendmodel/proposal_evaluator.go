package backendmodel

import (
	"crypto/sha1" //nolint:gosec // RFC 9562 UUIDv5 is defined over SHA-1; it derives an ID and protects nothing
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"uuid"
)

type ProposalDiagnostic struct {
	Code        string   `json:"code"`
	CommandID   string   `json:"commandId"`
	Property    string   `json:"property"`
	AffectedIDs []string `json:"affectedIds"`
	Message     string   `json:"message"`
}

type ProposalChange struct {
	Type         string                    `json:"type"`
	CommandID    string                    `json:"commandId"`
	SubjectID    string                    `json:"subjectId"`
	Before       map[string]jsontext.Value `json:"before"`
	After        map[string]jsontext.Value `json:"after"`
	GeneratedIDs map[string]string         `json:"generatedIds"`
}

type ProposalCandidate struct {
	Overlays      []ProposalOverlay    `json:"overlays"`
	Criteria      []ProposalCriterion  `json:"criteria"`
	Changes       []ProposalChange     `json:"changes"`
	Diagnostics   []ProposalDiagnostic `json:"diagnostics"`
	Limitations   []string             `json:"limitations"`
	CandidateHash *string              `json:"candidateHash"`
	GraphHash     *string              `json:"graphHash"`
}

type proposalEvaluation struct {
	proposal     Proposal
	projection   *databaseProjection
	overlays     map[string]ProposalOverlay
	baseEdges    map[string]Edge
	sourceValues map[string]map[string]jsontext.Value
	result       *ProposalCandidate
}

func proposalDesignedID(proposalID, commandID, role string) string {
	// RFC 9562 UUIDv5. The standard UUID package has no named-hash constructor.
	namespace := uuid.MustParse("7d651c82-44d3-5f20-9c72-2c20d503a8b6")
	h := sha1.New() //nolint:gosec // UUIDv5 is SHA-1 by definition, see the import
	_, _ = h.Write(namespace[:])
	_, _ = h.Write([]byte(proposalID + "\x00" + commandID + "\x00" + role))
	var id uuid.UUID
	copy(id[:], h.Sum(nil))
	id[6] = id[6]&0x0f | 0x50
	id[8] = id[8]&0x3f | 0x80
	return id.String()
}

func evaluateProposal(base *graphCandidate, proposal Proposal, draft ProposalRevision, commands []ProposalCommand) (*ProposalCandidate, error) {
	if err := validateProposalCommands(commands); err != nil {
		return nil, err
	}
	return evaluateProposalGraph(base, proposal, draft, commands)
}

// validateProposalSnapshot reuses owner graph/FK validation without inventing an edit command.
func validateProposalSnapshot(base *graphCandidate, proposal Proposal, draft ProposalRevision) (*ProposalCandidate, error) {
	return evaluateProposalGraph(base, proposal, draft, nil)
}
func evaluateProposalGraph(base *graphCandidate, proposal Proposal, draft ProposalRevision, commands []ProposalCommand) (*ProposalCandidate, error) {
	p := &databaseProjection{in: DatabaseQueryInput{DatastoreID: proposal.DatastoreID, FacetKey: proposal.FacetKey}, nodes: map[string]Node{}, children: map[string][]Node{}, facets: map[string]map[string]*relationalFacet{}, evidence: map[string]Evidence{}, stores: map[string]string{}, page: &DatabasePage{Limitations: []string{}, FacetStatus: "current"}, observed: map[string]bool{}, limitations: map[string]bool{}, uniqueResults: map[string]databaseUniqueness{}}
	for _, n := range base.Nodes {
		p.nodes[n.ID] = n
		if n.ParentID != nil {
			p.children[*n.ParentID] = append(p.children[*n.ParentID], n)
		}
	}
	for _, n := range base.Nodes {
		id, visited := n.ID, map[string]bool{}
		for id != "" && !visited[id] {
			visited[id] = true
			parent := p.nodes[id]
			if parent.Kind == "datastore" {
				p.stores[n.ID] = id
				break
			}
			if parent.ParentID == nil {
				break
			}
			id = *parent.ParentID
		}
	}
	for _, proof := range base.Evidence {
		p.evidence[proof.ID] = proof
	}
	sourceValues := map[string]map[string]jsontext.Value{}
	decode := func(id, kind string, attrs map[string]jsontext.Value) error {
		fs, _, err := relationalFacetObject(kind, attrs)
		if err != nil {
			return err
		}
		if fs[proposal.FacetKey] == nil {
			return nil
		}
		f, err := decodeRelationalFacet(kind, fs[proposal.FacetKey], true)
		if err != nil {
			return err
		}
		p.facets[id] = map[string]*relationalFacet{proposal.FacetKey: f}
		values, err := proposalFacetValues(fs[proposal.FacetKey])
		if err != nil {
			return err
		}
		sourceValues[id] = values
		return nil
	}
	for _, n := range base.Nodes {
		if p.stores[n.ID] == proposal.DatastoreID && relationalSubject(n.Kind, n.Attributes, false) {
			if err := decode(n.ID, n.Kind, n.Attributes); err != nil {
				return nil, err
			}
		}
	}
	e := &proposalEvaluation{proposal: proposal, projection: p, overlays: map[string]ProposalOverlay{}, baseEdges: map[string]Edge{}, sourceValues: sourceValues, result: &ProposalCandidate{Overlays: []ProposalOverlay{}, Criteria: slices.Clone(draft.Criteria), Changes: []ProposalChange{}, Diagnostics: []ProposalDiagnostic{}, Limitations: []string{"Existing data, writer inventory and migration feasibility are unverified; runtime enforcement and impact analysis are unavailable"}}}
	for _, edge := range base.Edges {
		if edge.Kind != "references" || p.stores[edge.From] != proposal.DatastoreID {
			continue
		}
		e.baseEdges[edge.ID] = edge
		p.stores[edge.ID] = p.stores[edge.From]
		if err := decode(edge.ID, edge.Kind, edge.Attributes); err != nil {
			return nil, err
		}
	}
	// Detach cumulative overlay maps from the immutable input revision.
	b, err := json.Marshal(draft.Overlays)
	if err != nil {
		return nil, err
	}
	var old []ProposalOverlay
	if err := json.Unmarshal(b, &old); err != nil {
		return nil, err
	}
	for _, overlay := range old {
		e.overlays[overlay.SubjectID] = overlay
	}
	for _, c := range commands {
		switch c.Type {
		case "alter_column":
			e.column(c)
		case "alter_constraint":
			e.constraint(c)
		case "set_criteria":
			e.criteria(c)
		}
	}
	e.validateFKs()
	for _, id := range slices.Sorted(maps.Keys(e.overlays)) {
		e.result.Overlays = append(e.result.Overlays, e.overlays[id])
	}
	if len(base.Nodes)+e.newCount("node") > MaxRevisionNodes || len(base.Edges)+e.newCount("edge") > MaxRevisionEdges {
		return nil, proposalLimit("Effective proposal graph record limit exceeded")
	}
	if len(e.result.Diagnostics) == 0 {
		hash, err := proposalEffectiveGraphHash(proposal, e.result.Overlays)
		if err != nil {
			return nil, err
		}
		e.result.GraphHash = new(hash)
		digest, err := requestDigest(struct {
			DocumentVersion  string              `json:"documentVersion"`
			ProposalID       string              `json:"proposalId"`
			Version          int64               `json:"version"`
			DraftRevisionID  string              `json:"draftRevisionId"`
			DraftHash        string              `json:"draftHash"`
			BaseRevisionID   string              `json:"baseRevisionId"`
			BaseSemanticHash string              `json:"baseSemanticHash"`
			RepositoryID     string              `json:"repositoryId"`
			DatastoreID      string              `json:"datastoreId"`
			FacetKey         string              `json:"facetKey"`
			Commands         []ProposalCommand   `json:"commands"`
			Overlays         []ProposalOverlay   `json:"overlays"`
			Criteria         []ProposalCriterion `json:"criteria"`
		}{ProposalDocumentVersion, proposal.ID, proposal.Version, draft.ID, draft.SemanticHash, proposal.BaseRevisionID, proposal.BaseSemanticHash, proposal.RepositoryID, proposal.DatastoreID, proposal.FacetKey, commands, e.result.Overlays, e.result.Criteria})
		if err != nil {
			return nil, err
		}
		e.result.CandidateHash = new(digest)
	}
	return e.result, nil
}

func proposalEffectiveGraphHash(p Proposal, overlays []ProposalOverlay) (string, error) {
	return requestDigest(struct {
		Version          string            `json:"documentVersion"`
		ProposalID       string            `json:"proposalId"`
		BaseRevisionID   string            `json:"baseRevisionId"`
		BaseSemanticHash string            `json:"baseSemanticHash"`
		RepositoryID     string            `json:"repositoryId"`
		DatastoreID      string            `json:"datastoreId"`
		FacetKey         string            `json:"facetKey"`
		Overlays         []ProposalOverlay `json:"overlays"`
	}{ProposalDocumentVersion, p.ID, p.BaseRevisionID, p.BaseSemanticHash, p.RepositoryID, p.DatastoreID, p.FacetKey, overlays})
}

func (e *proposalEvaluation) diagnostic(c ProposalCommand, property, message string, ids ...string) {
	e.result.Diagnostics = append(e.result.Diagnostics, ProposalDiagnostic{Code: "backend_proposal_invalid", CommandID: c.CommandID, Property: property, Message: message, AffectedIDs: ids})
}

func (e *proposalEvaluation) selected(c ProposalCommand, id, kind, property string) bool {
	if o, ok := e.overlays[id]; ok && o.Base == nil && o.RecordType == "node" && o.Kind == kind && o.ParentID != nil && e.projection.stores[*o.ParentID] == e.proposal.DatastoreID {
		return true
	}
	n, ok := e.projection.nodes[id]
	if !ok || n.Kind != kind || e.projection.stores[id] != e.proposal.DatastoreID || e.projection.selected(id) == nil {
		e.diagnostic(c, property, "Reference must resolve to a selected source facet of the required kind in this datastore", id)
		return false
	}
	if e.projection.selected(id).Dialect != e.projection.selected(e.proposal.DatastoreID).Dialect {
		e.diagnostic(c, property, "Selected source dialect differs from the datastore", id)
		return false
	}
	return true
}

func proposalFacetValues(raw jsontext.Value) (map[string]jsontext.Value, error) {
	m, err := relationalObject(raw)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"sourceKind", "dialect", "analysisStatus", "gaps", "evidenceKeys", "evidenceIds", "freshness", "sourceSnapshotId"} {
		delete(m, key)
	}
	return m, nil
}

func (e *proposalEvaluation) overlay(id, kind, recordType string) ProposalOverlay {
	if o, ok := e.overlays[id]; ok {
		return o
	}
	f := e.projection.selected(id)
	values := map[string]jsontext.Value{}
	for key, raw := range e.sourceValues[id] {
		values[key] = slices.Clone(raw)
	}
	basis := &ProposalBasis{RevisionID: e.proposal.BaseRevisionID, SemanticHash: e.proposal.BaseSemanticHash, SubjectID: id, FacetKey: e.proposal.FacetKey}
	o := ProposalOverlay{SubjectID: id, Kind: kind, RecordType: recordType, FacetKey: e.proposal.FacetKey, Base: basis, Values: values, PropertyOrigins: map[string]ProposalPropertyOrigin{}}
	for key := range values {
		path := "/" + key
		o.PropertyOrigins[path] = ProposalPropertyOrigin{Kind: "source", Base: basis, Property: path, EvidenceIDs: slices.Clone(f.EvidenceIDs)}
	}
	return o
}

func (e *proposalEvaluation) intent(o *ProposalOverlay, c ProposalCommand, key string, value any) {
	raw, _ := json.Marshal(value)
	o.Values[key] = raw
	o.PropertyOrigins["/"+key] = ProposalPropertyOrigin{Kind: "intent", CommandID: c.CommandID, Reason: c.Reason, EvidenceIDs: []string{}}
	o.CommandID, o.Reason = c.CommandID, c.Reason
}

func (e *proposalEvaluation) required(c ProposalCommand, targets []string, kind, description string) {
	key := "required:" + hashBytes([]byte(c.CommandID)) + ":" + kind
	if slices.ContainsFunc(e.result.Criteria, func(old ProposalCriterion) bool { return old.Key == key }) {
		e.diagnostic(c, "/criteria/key", "Required and supplemental criterion keys must be unique", targets...)
		return
	}
	e.result.Criteria = append(e.result.Criteria, ProposalCriterion{Key: key, Kind: kind, TargetIDs: slices.Clone(targets), Description: description, Origin: "required", Status: "unverified", CommandID: c.CommandID})
}

func (e *proposalEvaluation) column(c ProposalCommand) {
	if !e.selected(c, c.ColumnID, "column", "/columnId") {
		return
	}
	o := e.overlay(c.ColumnID, "column", "node")
	before := map[string]jsontext.Value{"nullable": slices.Clone(o.Values["nullable"])}
	e.intent(&o, c, "nullable", struct {
		Status string `json:"status"`
		Value  bool   `json:"value"`
	}{"known", *c.Nullable})
	e.overlays[o.SubjectID] = o
	e.result.Changes = append(e.result.Changes, ProposalChange{Type: c.Type, CommandID: c.CommandID, SubjectID: o.SubjectID, Before: before, After: map[string]jsontext.Value{"nullable": slices.Clone(o.Values["nullable"])}, GeneratedIDs: map[string]string{}})
	if !*c.Nullable {
		e.required(c, []string{c.ColumnID}, "existing_data", "Check existing nulls and backfill before enforcing NOT NULL")
	}
	e.required(c, []string{c.ColumnID}, "writers", "Identify all writers and verify omission/null handling; writer coverage is unknown")
	e.required(c, []string{c.ColumnID}, "migration_plan", "Review migration, rollout and rollback against the pinned baseline")
	f := e.projection.selected(c.ColumnID)
	if f.Nullable.Status != "known" || !e.projection.proofCurrent(f) {
		e.result.Limitations = append(e.result.Limitations, "Edited nullability has an unknown or stale baseline for "+c.ColumnID)
	}
}

func (e *proposalEvaluation) constraint(c ProposalCommand) {
	table, constraintID, edgeID := c.TableID, c.ConstraintID, ""
	generated := map[string]string{}
	var co, ro ProposalOverlay
	if c.Action == "create" {
		if !e.selected(c, table, "table", "/tableId") {
			return
		}
		constraintID = proposalDesignedID(e.proposal.ID, c.CommandID, "constraint")
		edgeID = proposalDesignedID(e.proposal.ID, c.CommandID, "reference")
		generated = map[string]string{"constraintId": constraintID, "edgeId": edgeID}
		co = ProposalOverlay{SubjectID: constraintID, RecordType: "node", Kind: "constraint", FacetKey: e.proposal.FacetKey, Name: c.Name, ParentID: new(table), Values: map[string]jsontext.Value{}, PropertyOrigins: map[string]ProposalPropertyOrigin{}}
		ro = ProposalOverlay{SubjectID: edgeID, RecordType: "edge", Kind: "references", FacetKey: e.proposal.FacetKey, Values: map[string]jsontext.Value{}, PropertyOrigins: map[string]ProposalPropertyOrigin{}}
	} else {
		if !e.selected(c, constraintID, "constraint", "/constraintId") {
			return
		}
		if designed, ok := e.overlays[constraintID]; ok && designed.Base == nil {
			var kind string
			if json.Unmarshal(designed.Values["constraintKind"], &kind) != nil || kind != "foreign_key" {
				e.diagnostic(c, "/constraintId", "Only designed foreign-key constraints can be updated", constraintID)
				return
			}
			table = *designed.ParentID
			co = designed
			for id, o := range e.overlays {
				if o.Kind == "references" && o.FromID == constraintID {
					if edgeID != "" {
						e.diagnostic(c, "/constraintId", "Designed FK has ambiguous reference edges", constraintID)
						return
					}
					edgeID, ro = id, o
				}
			}
			if edgeID == "" {
				e.diagnostic(c, "/constraintId", "Designed FK reference edge is missing", constraintID)
				return
			}
		} else {
			if e.projection.selected(constraintID).ConstraintKind != "foreign_key" {
				e.diagnostic(c, "/constraintId", "Only selected foreign-key constraints can be updated", constraintID)
				return
			}
			table = *e.projection.nodes[constraintID].ParentID
			for id, edge := range e.baseEdges {
				if edge.From == constraintID && e.projection.selected(id) != nil {
					if edgeID != "" {
						e.diagnostic(c, "/constraintId", "Selected FK has ambiguous reference edges", constraintID)
						return
					}
					edgeID = id
				}
			}
			if edgeID == "" {
				e.diagnostic(c, "/constraintId", "Selected FK reference edge is missing", constraintID)
				return
			}
			co = e.overlay(constraintID, "constraint", "node")
			co.ParentID = new(table)
			ro = e.overlay(edgeID, "references", "edge")
		}
	}
	if !e.selected(c, c.TargetTableID, "table", "/targetTableId") {
		return
	}
	source, target := []string{}, []string{}
	for _, pair := range c.ColumnPairs {
		if !e.selected(c, pair.FromColumnID, "column", "/columnPairs") || !e.selected(c, pair.ToColumnID, "column", "/columnPairs") {
			return
		}
		if !relationalPairParents(e.projection.nodes, table, c.TargetTableID, pair) {
			e.diagnostic(c, "/columnPairs", "Ordered pair columns must belong to the source and target table", pair.FromColumnID, pair.ToColumnID)
			return
		}
		if slices.Contains(source, pair.FromColumnID) || slices.Contains(target, pair.ToColumnID) {
			e.diagnostic(c, "/columnPairs", "Each side of an ordered FK must have unique columns", pair.FromColumnID, pair.ToColumnID)
			return
		}
		source = append(source, pair.FromColumnID)
		target = append(target, pair.ToColumnID)
	}
	if c.MatchType == "partial" || e.projection.selected(e.proposal.DatastoreID).Dialect == "sqlite" && c.MatchType != "simple" {
		e.diagnostic(c, "/matchType", "Designed FK match mode is unsupported by this dialect", constraintID)
		return
	}
	if *c.InitiallyDeferred && !*c.Deferrable {
		e.diagnostic(c, "/initiallyDeferred", "Initially deferred requires deferrable", constraintID)
		return
	}
	if c.Action == "create" {
		for _, n := range e.projection.children[table] {
			if (n.Kind == "constraint" || n.Kind == "index") && e.projection.selected(n.ID) != nil && n.Name == c.Name {
				e.diagnostic(c, "/name", "Selected constraint/index name is already in use", n.ID)
				return
			}
		}
		for _, o := range e.overlays {
			if o.SubjectID != constraintID && o.Name == c.Name && o.ParentID != nil && *o.ParentID == table {
				e.diagnostic(c, "/name", "Designed constraint name is already in use", o.SubjectID)
				return
			}
		}
	}
	unique, complete, _ := e.projection.unique(c.TargetTableID, target, true)
	if !unique && complete {
		e.diagnostic(c, "/columnPairs", "Ordered target columns do not match a selected complete global unique key", c.TargetTableID)
		return
	}
	if !unique {
		e.result.Limitations = append(e.result.Limitations, "Target uniqueness is unknown or incomplete for "+c.TargetTableID)
	}
	before := maps.Clone(ro.Values)
	e.intent(&co, c, "constraintKind", "foreign_key")
	e.intent(&co, c, "columnIds", source)
	e.intent(&co, c, "expression", relationalScalar{Status: "known", Value: jsontext.Value("null")})
	e.intent(&co, c, "deferrable", relationalScalar{Status: "known", Value: jsontext.Value(fmt.Sprint(*c.Deferrable))})
	e.intent(&co, c, "initiallyDeferred", relationalScalar{Status: "known", Value: jsontext.Value(fmt.Sprint(*c.InitiallyDeferred))})
	e.intent(&co, c, "nativeDefinition", nil)
	ro.FromID, ro.ToID = constraintID, c.TargetTableID
	e.intent(&ro, c, "columnPairs", c.ColumnPairs)
	for key, value := range map[string]string{"updateAction": c.UpdateAction, "deleteAction": c.DeleteAction, "matchType": c.MatchType} {
		raw, _ := json.Marshal(value)
		e.intent(&ro, c, key, relationalScalar{Status: "known", Value: raw})
	}
	delete(ro.Values, "targetReason")
	delete(ro.PropertyOrigins, "/targetReason")
	e.overlays[co.SubjectID], e.overlays[ro.SubjectID] = co, ro
	e.result.Changes = append(e.result.Changes, ProposalChange{Type: c.Type, CommandID: c.CommandID, SubjectID: constraintID, Before: before, After: maps.Clone(ro.Values), GeneratedIDs: generated})
	for _, tc := range []struct{ kind, description string }{
		{"referential_integrity", "Check existing orphan rows and ordered pair values before enforcing the FK"},
		{"target_uniqueness", "Verify actual target uniqueness and FK enforcement against the selected baseline"},
		{"writers", "Identify every source/target writer and verify update/delete actions; writer coverage is unknown"},
		{"migration_plan", "Review FK migration, rollout and rollback against the pinned baseline"},
	} {
		e.required(c, []string{constraintID, table, c.TargetTableID}, tc.kind, tc.description)
	}
}

func (e *proposalEvaluation) criteria(c ProposalCommand) {
	for _, criterion := range c.Criteria {
		if slices.ContainsFunc(e.result.Criteria, func(old ProposalCriterion) bool { return old.Origin == "required" && old.Key == criterion.Key }) {
			e.diagnostic(c, "/criteria/key", "Supplemental recommendation key collides with a required criterion")
			return
		}
		for _, id := range criterion.TargetIDs {
			if _, designed := e.overlays[id]; !designed && (e.projection.stores[id] != e.proposal.DatastoreID || e.projection.selected(id) == nil) {
				e.diagnostic(c, "/criteria/targetIds", "Recommendation targets must be selected subjects", id)
				return
			}
		}
	}
	e.result.Criteria = slices.DeleteFunc(e.result.Criteria, func(c ProposalCriterion) bool { return c.Origin == "authored" })
	for _, criterion := range c.Criteria {
		e.result.Criteria = append(e.result.Criteria, ProposalCriterion{Key: criterion.Key, Kind: criterion.Kind, TargetIDs: slices.Clone(criterion.TargetIDs), Description: criterion.Description, Origin: "authored", Status: "unverified", CommandID: c.CommandID})
	}
	e.result.Changes = append(e.result.Changes, ProposalChange{Type: c.Type, CommandID: c.CommandID, Before: map[string]jsontext.Value{}, After: map[string]jsontext.Value{}, GeneratedIDs: map[string]string{}})
}

func (e *proposalEvaluation) validateFKs() {
	// Validate action feasibility after the whole batch, including inherited
	// relationships affected by a column edit. Source-only imported match modes
	// retain their native meaning and are not subjected to designed match rules.
	references := map[string]ProposalOverlay{}
	for id, edge := range e.baseEdges {
		if e.projection.selected(id) == nil {
			continue
		}
		o := e.overlay(id, "references", "edge")
		o.FromID, o.ToID = edge.From, edge.To
		references[id] = o
	}
	for id, o := range e.overlays {
		if o.Kind == "references" {
			references[id] = o
		}
	}
	for _, id := range slices.Sorted(maps.Keys(references)) {
		o := references[id]
		var pairs []DatabaseColumnPair
		if err := json.Unmarshal(o.Values["columnPairs"], &pairs); err != nil {
			continue
		}
		c := ProposalCommand{CommandID: o.CommandID, Reason: o.Reason}
		for _, actionKey := range []string{"updateAction", "deleteAction"} {
			var action relationalScalar
			if json.Unmarshal(o.Values[actionKey], &action) != nil || action.Status != "known" {
				continue
			}
			for _, pair := range pairs {
				f := e.projection.selected(pair.FromColumnID)
				if f == nil {
					continue
				}
				col, changed := e.overlays[pair.FromColumnID]
				if o.CommandID == "" && !changed {
					continue
				}
				property := "/" + actionKey
				cause := c
				if cause.CommandID == "" {
					cause.CommandID, cause.Reason, property = col.CommandID, col.Reason, "/nullable"
				}
				n := f.Nullable
				if changed {
					var value relationalScalar
					if json.Unmarshal(col.Values["nullable"], &value) == nil {
						n = &value
					}
				}
				if rawStringEquals(action.Value, "set_null") {
					if scalarBool(n, false) {
						e.diagnostic(cause, property, "SET NULL conflicts with final selected NOT NULL column", o.FromID, pair.FromColumnID)
					} else if n == nil || n.Status != "known" {
						e.result.Limitations = append(e.result.Limitations, "SET NULL feasibility is unknown for "+pair.FromColumnID)
					}
				}
				if rawStringEquals(action.Value, "set_default") && (f.DefaultExpression == nil || f.DefaultExpression.Status != "known" || string(f.DefaultExpression.Value) == "null") {
					e.result.Limitations = append(e.result.Limitations, "SET DEFAULT feasibility is unverified for "+pair.FromColumnID)
				}
			}
		}
	}
	// Keep repeated feasibility warnings deterministic and bounded.
	slices.Sort(e.result.Limitations)
	e.result.Limitations = slices.Compact(e.result.Limitations)
}

func (e *proposalEvaluation) newCount(recordType string) int {
	n := 0
	for _, o := range e.overlays {
		if o.Base == nil && o.RecordType == recordType {
			n++
		}
	}
	return n
}

func proposalChangeSummary(changes []ProposalChange) string {
	kinds := make([]string, 0, len(changes))
	for _, c := range changes {
		kinds = append(kinds, c.Type)
	}
	return "Database proposal: " + strings.Join(kinds, ", ")
}
