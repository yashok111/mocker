package backendanalysis

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type ConformanceCriterionDetail struct {
	DocumentVersion string                               `json:"documentVersion"`
	Type            string                               `json:"type"`
	CriterionKey    string                               `json:"criterionKey"`
	CriterionKind   string                               `json:"criterionKind"`
	Required        bool                                 `json:"required"`
	Outcome         string                               `json:"outcome"`
	Reason          string                               `json:"reason"`
	Basis           []ProofReference                     `json:"basis"`
	ProposalObject  *ObjectAddress                       `json:"proposalObject"`
	SourceObject    *ObjectAddress                       `json:"sourceObject"`
	DeletionBasis   []backendmodel.AnalysisDeletionProof `json:"deletionBasis"`
}
type ConformanceSummaryDetail struct {
	DocumentVersion    string                          `json:"documentVersion"`
	Type               string                          `json:"type"`
	ChangeProposal     backendmodel.ProposalReadTarget `json:"changeProposal"`
	DraftHash          string                          `json:"draftHash"`
	ResultRevisionID   string                          `json:"resultRevisionId"`
	ResultSemanticHash string                          `json:"resultSemanticHash"`
	CriteriaCount      int                             `json:"criteriaCount"`
	SatisfiedCount     int                             `json:"satisfiedCount"`
	ViolatedCount      int                             `json:"violatedCount"`
	UnverifiedCount    int                             `json:"unverifiedCount"`
	RequiredSatisfied  bool                            `json:"requiredSatisfied"`
	BehaviorStatus     string                          `json:"behaviorStatus"`
}
type OutsideIntentChangeDetail struct {
	DocumentVersion string     `json:"documentVersion"`
	Type            string     `json:"type"`
	Change          DiffChange `json:"change"`
}

func validateConformanceAssociations(p *ConformancePayload, base, draft, result *backendmodel.EffectiveGraphSnapshot) (map[string]string, error) {
	if result.Pins.StructuralSchemaVersion != "5" && result.Pins.StructuralSchemaVersion != "6" {
		return nil, fault(422, "unsupported", "Conformance requires source5 or source6")
	}
	before, desired, actual := map[string]string{}, map[string]string{}, map[string]string{}
	for _, n := range base.State.Nodes {
		before[n.ID] = n.Kind
	}
	for _, n := range draft.State.Nodes {
		desired[n.ID] = n.Kind
	}
	for _, n := range result.State.Nodes {
		actual[n.ID] = n.Kind
	}
	correspondence := map[string]string{}
	used := map[string]string{}
	for id := range before {
		correspondence[id] = id
		if actual[id] != "" {
			used[id] = id
		}
	}
	for _, m := range p.IdentityMap {
		kind := desired[m.ProposalNodeID]
		if kind == "" {
			kind = before[m.ProposalNodeID]
		}
		if kind == "" || actual[m.SourceNodeID] == "" || kind != actual[m.SourceNodeID] {
			return nil, malformed("Identity map requires exact matching node kinds")
		}
		if prior := correspondence[m.ProposalNodeID]; prior != "" && prior != m.SourceNodeID {
			return nil, malformed("Retained source identity cannot be remapped")
		}
		if prior := used[m.SourceNodeID]; prior != "" && prior != m.ProposalNodeID {
			return nil, malformed("Identity map collides with retained identity")
		}
		correspondence[m.ProposalNodeID] = m.SourceNodeID
		used[m.SourceNodeID] = m.ProposalNodeID
	}

	addCreatedEdgeCorrespondence(base, draft, result, correspondence)
	criteria := map[string]backendmodel.ChangeCriterion{}
	for _, c := range draft.Criteria {
		criteria[c.Key] = c
	}
	for _, a := range p.TestAttachments {
		if criteria[a.CriterionKey].Kind != "test_attachment" {
			return nil, malformed("Association must address a saved test_attachment criterion")
		}
	}
	return correspondence, nil
}
func analyzeConformance(ctx context.Context, in *ImmutableInput, p *ConformancePayload, base, draft, result *backendmodel.EffectiveGraphSnapshot, reader backendmodel.AnalysisEvidenceReader, request *backendmodel.EditorArtifactRequest) (*TerminalSnapshot, error) {
	mapping, err := validateConformanceAssociations(p, base, draft, result)
	if err != nil {
		return nil, err
	}
	deletion, err := reader.ReadAnalysisDeletionEvidence(ctx, in.ProjectID, p.BaseRevisionID, p.ResultRevisionID, p.EvidencePins)
	if err != nil {
		return nil, err
	}
	r := newReport(in)
	r.before, r.after = base, result
	r.services = objectServices(base, result)
	summary := ConformanceSummaryDetail{DocumentVersion: b43ResultVersion, Type: "conformance_summary", ChangeProposal: p.ChangeProposal, DraftHash: draft.Pins.EffectiveSemanticHash, ResultRevisionID: p.ResultRevisionID, ResultSemanticHash: result.Pins.BaseSemanticHash, CriteriaCount: len(draft.Criteria), RequiredSatisfied: true, BehaviorStatus: "unverified"}
	// Effective desired hash is the proposal semantic hash for this exact target.
	summary.DraftHash = draft.Pins.EffectiveSemanticHash
	r.progress.Records++
	r.progress.Findings++
	associations := map[string]backendmodel.TestAttachmentRef{}
	for _, a := range p.TestAttachments {
		associations[a.CriterionKey] = a.Attachment
	}
	for _, criterion := range draft.Criteria {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		row, err := evaluateConformanceCriterion(ctx, in.ProjectID, criterion, draft, result, mapping, associations, deletion, reader, request)
		if err != nil {
			return nil, err
		}
		switch row.Outcome {
		case "satisfied":
			summary.SatisfiedCount++
		case "violated":
			summary.ViolatedCount++
			r.incompatible = true
		default:
			summary.UnverifiedCount++
			r.gap("criterion_unverified", ObjectAddress{RecordType: "desired", ID: criterion.Key})
		}
		if criterion.Required && row.Outcome != "satisfied" {
			summary.RequiredSatisfied = false
		}
		certainty := "confirmed"
		if row.Outcome == "unverified" {
			certainty = "unknown"
		}
		if !r.add("checks", ObjectAddress{RecordType: "desired", ID: criterion.Key}, criterion.Kind, certainty, 0, row) && criterion.Required {
			summary.RequiredSatisfied = false
		}
	}
	outside, err := outsideIntentChanges(ctx, base, draft, result, mapping)
	if err != nil {
		return nil, err
	}
	for _, change := range outside {
		if !r.trackChange(change.Object) {
			continue
		}
		r.covered[change.Object] = true
		r.add("changes", change.Object, change.Kind, "confirmed", 0, OutsideIntentChangeDetail{b43ResultVersion, "outside_intent_change", change})
	}
	r.progress.Records--
	r.progress.Findings--
	if len(r.truncations) > 0 {
		summary.RequiredSatisfied = false
	}
	r.add("findings", ObjectAddress{RecordType: "desired", ID: p.ChangeProposal.ProposalRevisionID}, "conformance_summary", "confirmed", 0, summary)
	return r.finish(base, result, nil)
}
func evaluateConformanceCriterion(ctx context.Context, pid string, c backendmodel.ChangeCriterion, draft, result *backendmodel.EffectiveGraphSnapshot, mapping map[string]string, attachments map[string]backendmodel.TestAttachmentRef, deletion *backendmodel.AnalysisDeletionEvidence, reader backendmodel.AnalysisEvidenceReader, request *backendmodel.EditorArtifactRequest) (ConformanceCriterionDetail, error) {
	row := ConformanceCriterionDetail{DocumentVersion: b43ResultVersion, Type: "conformance_criterion", CriterionKey: c.Key, CriterionKind: c.Kind, Required: c.Required, Outcome: "unverified", Reason: "No supported exact source evidence", Basis: []ProofReference{}, DeletionBasis: []backendmodel.AnalysisDeletionProof{}}
	typ := c.RecordType
	if c.Kind == "edge_exists" {
		typ = "edge"
	}
	if c.ID != "" {
		row.ProposalObject = &ObjectAddress{RecordType: typ, ID: c.ID}
		id := mapping[c.ID]
		if id != "" {
			row.SourceObject = &ObjectAddress{RecordType: typ, ID: id}
		}
	}
	switch c.Kind {
	case "runtime_check":
		row.Reason = "Runtime behavior has not been executed or verified"
		return row, nil
	case "test_attachment":
		return evaluateAttachmentCriterion(ctx, pid, row, c, attachments, reader)
	case "artifact_object_matches":
		return evaluateArtifactCriterion(row, c, result, request), nil
	case "artifact_object_matches_v3":
		row.Reason = "Imported namespaced criterion retains exact artifact identity; no implicit local owner lookup"
		return row, nil
	}
	if row.SourceObject == nil {
		row.Reason = "Proposal-created object has no unambiguous source correspondence"
		return row, nil
	}
	object := *row.SourceObject
	if c.Kind == "object_absent" {
		for _, proof := range deletion.Deletions {
			if proof.Record.RecordType == typ && proof.Record.ID == c.ID {
				row.DeletionBasis = append(row.DeletionBasis, proof)
			}
		}
		if len(row.DeletionBasis) > 0 {
			row.conclude(true, "Explicit committed deletion and complete selected owner scope")
			return row, nil
		}
	}
	payload := sourcePayloadFor(result, typ, object.ID)
	if payload == nil {
		return row, nil
	}
	proof := recordProof(result, object)
	if c.Kind == "field_equals" {
		return evaluateFieldCriterion(row, c, draft, result, mapping, *payload), nil
	}

	row.Basis = append(row.Basis, proofReference("after", result, proof))
	if proof.Boundary || proof.Status != "explicit" {
		return row, nil
	}
	return evaluateObjectCriterion(row, c, *payload, mapping), nil
}
func sourcePayloadFor(g *backendmodel.EffectiveGraphSnapshot, typ, id string) *backendmodel.SourceAssertionPayload {
	for _, n := range g.State.Nodes {
		if typ == "node" && n.ID == id {
			return &backendmodel.SourceAssertionPayload{RecordType: "node", Kind: n.Kind, Name: n.Name, ParentID: n.ParentID, Attributes: n.Attributes}
		}
	}
	for _, e := range g.State.Edges {
		if typ == "edge" && e.ID == id {
			return &backendmodel.SourceAssertionPayload{RecordType: "edge", Kind: e.Kind, From: e.From, To: e.To, Attributes: e.Attributes}
		}
	}
	return nil
}
func outsideIntentChanges(ctx context.Context, base, draft, result *backendmodel.EffectiveGraphSnapshot, mapping map[string]string) ([]DiffChange, error) {
	intended, err := structuralChanges(ctx, base, draft)
	if err != nil {
		return nil, err
	}
	actual, err := structuralChanges(ctx, base, result)
	if err != nil {
		return nil, err
	}
	type intentKey struct {
		object ObjectAddress
		facet  string
	}
	intent := map[intentKey][]string{}
	for _, change := range intended {
		object := change.Object
		if (object.RecordType == "node" || object.RecordType == "edge") && mapping[object.ID] != "" {
			object.ID = mapping[object.ID]
		}
		paths := change.Paths
		if change.Operation == "added" {
			paths = propertyLeafPaths(change.After, "")
		}
		intent[intentKey{object, change.Facet}] = append(intent[intentKey{object, change.Facet}], paths...)
	}
	outside := []DiffChange{}
	for _, change := range actual {
		paths := []string{}
		actualPaths := change.Paths
		if change.Operation == "added" {
			actualPaths = propertyLeafPaths(change.After, "")
		}
		for _, path := range actualPaths {
			covered := slices.ContainsFunc(intent[intentKey{change.Object, change.Facet}], func(p string) bool { return p == path || p == "" || strings.HasPrefix(path, p+"/") })
			if !covered {
				paths = append(paths, path)
			}
		}
		if len(paths) > 0 {
			change.Paths = paths
			outside = append(outside, change)
		}
	}
	return outside, nil
}

func propertyLeafPaths(raw jsontext.Value, path string) []string {
	if len(raw) > 0 && raw[0] == '{' {
		var fields map[string]jsontext.Value
		if json.Unmarshal(raw, &fields) != nil {
			return nil
		}
		out := []string{}
		for key, value := range fields {
			out = append(out, propertyLeafPaths(value, path+"/"+pointerPart(key))...)
		}
		slices.Sort(out)
		return out
	}
	return []string{path}
}

func addCreatedEdgeCorrespondence(base, draft, result *backendmodel.EffectiveGraphSnapshot, correspondence map[string]string) {
	retainedEdges := map[string]bool{}
	for _, edge := range base.State.Edges {
		retainedEdges[edge.ID] = true
		correspondence[edge.ID] = edge.ID
	}
	for _, edge := range draft.State.Edges {
		if retainedEdges[edge.ID] {
			continue
		}
		from, to := correspondence[edge.From], correspondence[edge.To]
		if from == "" || to == "" {
			continue
		}
		matches := []backendmodel.Edge{}
		for _, actual := range result.State.Edges {
			if actual.Kind == edge.Kind && actual.From == from && actual.To == to {
				matches = append(matches, actual)
			}
		}
		if len(matches) != 1 {
			continue
		}
		proof := recordProof(result, ObjectAddress{RecordType: "edge", ID: matches[0].ID})
		if proof.Status == "explicit" && !proof.Boundary {
			correspondence[edge.ID] = matches[0].ID
		}
	}
}

func (row *ConformanceCriterionDetail) conclude(pass bool, reason string) {
	row.Outcome = "violated"
	if pass {
		row.Outcome = "satisfied"
	}
	row.Reason = reason
}
func evaluateAttachmentCriterion(ctx context.Context, pid string, row ConformanceCriterionDetail, c backendmodel.ChangeCriterion, attachments map[string]backendmodel.TestAttachmentRef, reader backendmodel.AnalysisEvidenceReader) (ConformanceCriterionDetail, error) {
	supplied, ok := attachments[c.Key]
	if !ok {
		row.Reason = "No attachment association supplied"
		return row, nil
	}
	expected, _ := canonical(c.Attachment)
	actual, _ := canonical(supplied)
	if !bytes.Equal(expected, actual) {
		row.conclude(false, "Supplied locator differs from the saved criterion attachment")
		return row, nil
	}
	evidence, err := reader.ReadAnalysisAttachmentEvidence(ctx, pid, supplied)
	if err != nil {
		return row, err
	}
	row.Reason = evidence.Reason
	if evidence.Verified {
		row.Outcome = "satisfied"
	}
	return row, nil
}
func evaluateArtifactCriterion(row ConformanceCriterionDetail, c backendmodel.ChangeCriterion, result *backendmodel.EffectiveGraphSnapshot, request *backendmodel.EditorArtifactRequest) ConformanceCriterionDetail {
	if c.Artifact == nil || request == nil || !slices.Contains(result.Pins.ArtifactPins, *c.Artifact) {
		row.Reason = "Exact artifact is outside the result context"
		return row
	}
	var hash string
	if c.Artifact.Kind == "api_design" {
		var selector backendmodel.APIArtifactSelector
		if json.Unmarshal(c.Selector, &selector) != nil {
			return row
		}
		object, err := request.ResolveAPIObject(*c.Artifact, selector)
		if err != nil {
			return row
		}
		hash = object.ObjectHash
	} else {
		var selector backendmodel.EditorSelector
		if json.Unmarshal(c.Selector, &selector) != nil {
			return row
		}
		object, err := request.ResolveObject(*c.Artifact, selector)
		if err != nil {
			return row
		}
		hash = object.ObjectHash
	}
	row.conclude(hash == c.ExpectedHash, "Exact pinned artifact object hash compared")
	return row
}
func evaluateFieldCriterion(row ConformanceCriterionDetail, c backendmodel.ChangeCriterion, draft, result *backendmodel.EffectiveGraphSnapshot, mapping map[string]string, payload backendmodel.SourceAssertionPayload) ConformanceCriterionDetail {
	typ := row.SourceObject.RecordType
	object := *row.SourceObject
	var proof backendmodel.EffectiveAnalysisProof
	var selector backendmodel.EffectivePropertySelector
	if err := json.Unmarshal(c.Selector, &selector); err != nil {
		return row
	}
	property, err := backendmodel.EffectivePropertyAnalysisProof(result, backendmodel.ChangeRecordRef{RecordType: typ, ID: object.ID}, selector)
	if err != nil {
		return row
	}
	proof = property
	row.Basis = append(row.Basis, proofReference("after", result, proof))
	if proof.Boundary || proof.Status != "explicit" || selector.Source == nil {
		return row
	}
	value, err := backendmodel.SelectSourceProperty(payload, *selector.Source)
	if err != nil {
		return row
	}
	desired := sourcePayloadFor(draft, typ, c.ID)
	if desired == nil {
		return row
	}
	expected, translated, err := backendmodel.TranslateAnalysisSourceProperty(*desired, *selector.Source, *c.Expected, mapping)
	if err != nil {
		return row
	}
	if !translated {
		row.Reason = "Typed property references lack supported explicit correspondence"
		return row
	}
	a, _ := canonical(value)
	b, _ := canonical(expected)
	row.conclude(bytes.Equal(a, b), "Exact typed source property compared")
	return row
}

func evaluateObjectCriterion(row ConformanceCriterionDetail, c backendmodel.ChangeCriterion, payload backendmodel.SourceAssertionPayload, mapping map[string]string) ConformanceCriterionDetail {
	switch c.Kind {
	case "object_exists":
		row.conclude(payload.Kind == c.ObjectKind, "Exact source object kind compared")
	case "object_absent":
		row.conclude(false, "Supported source object still exists")
	case "edge_exists":
		from, to := mapping[c.From], mapping[c.To]
		if from == "" || to == "" {
			row.Reason = "Edge endpoints lack explicit source correspondence"
			return row
		}
		row.conclude(payload.Kind == c.EdgeKind && payload.From == from && payload.To == to, "Exact stable edge and translated endpoint identities compared")
	}
	return row
}
