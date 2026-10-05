package backendmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
)

type AnalysisDeletionProof struct {
	RevisionID         string                      `json:"revisionId"`
	BaselineRevisionID string                      `json:"baselineRevisionId"`
	Record             ChangeRecordRef             `json:"record"`
	Owner              AssertionOwnership          `json:"owner"`
	ExternalKey        string                      `json:"externalKey"`
	DecisionPin        AnalysisEvidenceDocumentPin `json:"decisionPin"`
	CoveragePin        AnalysisEvidenceDocumentPin `json:"coveragePin"`
	DecisionPointer    string                      `json:"decisionPointer"`
	CoveragePointer    string                      `json:"coveragePointer"`
	OldSubject         HistoricalSubjectRef        `json:"oldSubject"`
	OldEvidenceRefs    []HistoricalEvidenceRef     `json:"oldEvidenceRefs"`
}
type AnalysisDeletionEvidence struct {
	Pins      []AnalysisEvidenceDocumentPin
	Deletions []AnalysisDeletionProof
}
type AnalysisAttachmentEvidence struct {
	Attachment TestAttachmentRef
	Verified   bool
	Reason     string
	Pins       []AnalysisEvidenceDocumentPin
}
type AnalysisEvidenceReader interface {
	AnalysisEvidenceInputFootprint(context.Context, string, AnalysisEvidenceRequest) (AnalysisInputFootprint, error)
	ReadAnalysisDeletionEvidence(context.Context, string, string, string, []AnalysisEvidenceDocumentPin) (*AnalysisDeletionEvidence, error)
	ReadAnalysisAttachmentEvidence(context.Context, string, TestAttachmentRef) (*AnalysisAttachmentEvidence, error)
}

func analysisPin(raw []byte, rid, kind string) AnalysisEvidenceDocumentPin {
	hash := sha256.Sum256(raw)
	return AnalysisEvidenceDocumentPin{RevisionID: rid, Kind: kind, ContentHash: hex.EncodeToString(hash[:])}
}
func (r *Repo) ReadAnalysisDeletionEvidence(ctx context.Context, pid, baseline, result string, pins []AnalysisEvidenceDocumentPin) (*AnalysisDeletionEvidence, error) {
	if pins == nil {
		return nil, invalid("evidencePins", "Saved immutable pins are required")
	}
	out := &AnalysisDeletionEvidence{Pins: []AnalysisEvidenceDocumentPin{}, Deletions: []AnalysisDeletionProof{}}
	documents, actualPins, err := r.analysisDeletionDocuments(ctx, pid, baseline, result, pins)
	if err != nil {
		return nil, err
	}
	out.Pins = actualPins
	before, err := r.ResolveEffectiveGraph(ctx, pid, BackendReadTarget{RevisionID: baseline})
	if err != nil {
		return nil, err
	}
	after, err := r.ResolveEffectiveGraph(ctx, pid, BackendReadTarget{RevisionID: result})
	if err != nil {
		return nil, err
	}
	decisions, coverage := documents[result+":revision_decisions"], documents[result+":revision_coverage"]
	if decisions == nil || coverage == nil {
		return out, nil
	}
	decisionPin, coveragePin := analysisPin(decisions, result, "revision_decisions"), analysisPin(coverage, result, "revision_coverage")
	builder := analysisDeletionBuilder{before: before, after: after, baseline: baseline, result: result, decisionPin: decisionPin, coveragePin: coveragePin, out: out}
	appendProof := builder.appendProof
	decodeAnalysisDeletions(after.Pins.StructuralSchemaVersion, baseline, decisions, coverage, appendProof)
	return out, nil
}
func analysisRecordExists(g *EffectiveGraphSnapshot, typ, id string) bool {
	if typ == "node" {
		return slices.ContainsFunc(g.State.Nodes, func(n Node) bool { return n.ID == id })
	}
	if typ == "edge" {
		return slices.ContainsFunc(g.State.Edges, func(e Edge) bool { return e.ID == id })
	}
	return true
}
func analysisDeletionBaseline(g *EffectiveGraphSnapshot, c ImportDeletion, scope *SourceScope) (AssertionOwnership, []HistoricalEvidenceRef, bool) {
	if !analysisRecordExists(g, c.RecordType, c.ExpectedID) {
		return AssertionOwnership{}, nil, false
	}
	proof, err := EffectiveRecordAnalysisProof(g, ChangeRecordRef{RecordType: c.RecordType, ID: c.ExpectedID})
	if err != nil || proof.Boundary || proof.Status != "explicit" {
		return AssertionOwnership{}, nil, false
	}
	owners, evidence, ok := analysisDeletionOwners(g, c, scope)
	if !ok {
		return AssertionOwnership{}, nil, false
	}
	if len(owners) != 1 {
		return AssertionOwnership{}, nil, false
	}
	refs := []HistoricalEvidenceRef{}
	for _, id := range evidence {
		refs = append(refs, HistoricalEvidenceRef{RevisionID: g.Pins.BaseRevisionID, EvidenceID: id})
	}
	return owners[0], refs, true
}
func analysisDeletionScope(g *EffectiveGraphSnapshot, owner AssertionOwnership, scope *SourceScope) (string, bool) {
	if g.Source == nil {
		return "", false
	}
	if g.Pins.StructuralSchemaVersion == EventsSchemaVersion {
		return analysisLegacyDeletionScope(g, owner)
	}
	if scope == nil || scope.Kind != "reconcile" || g.Source.SourceVector == nil {
		return "", false
	}
	for i, p := range g.Source.SourceVector.Partitions {
		if p.RepositoryID != owner.RepositoryID || p.ProviderNamespace != owner.ProviderNamespace {
			continue
		}
		if p.ScopeStatus.Status != "complete" || len(p.ScopeStatus.Gaps) > 0 {
			return "", false
		}
		active := slices.ContainsFunc(g.Source.SourceVector.Snapshots, func(s SourceSnapshot) bool {
			return s.ID == p.SnapshotID && s.Role == "active_source" && s.RepositoryID == owner.RepositoryID && s.Provider.Namespace == owner.ProviderNamespace
		})
		if !active {
			return "", false
		}
		for _, item := range p.Inventory {
			if item.Status != "complete" || len(item.Gaps) > 0 {
				return "", false
			}
		}
		return fmt.Sprintf("/sourceVector/partitions/%d/scopeStatus", i), true
	}
	return "", false
}

func (r *Repo) analysisDeletionDocuments(ctx context.Context, pid, baseline, result string, pins []AnalysisEvidenceDocumentPin) (map[string][]byte, []AnalysisEvidenceDocumentPin, error) {
	actualPins := []AnalysisEvidenceDocumentPin{}
	documents := map[string][]byte{}
	for _, rid := range []string{baseline, result} {
		for _, kind := range []string{"revision_decisions", "revision_coverage"} {
			raw, err := r.readAnalysisEvidenceDocument(ctx, pid, rid, kind)
			if err != nil {
				return nil, nil, err
			}
			if raw == nil {
				if slices.ContainsFunc(pins, func(p AnalysisEvidenceDocumentPin) bool { return p.RevisionID == rid && p.Kind == kind }) {
					return nil, nil, &FaultError{Status: 409, Code: "backend_analysis_input_conflict", Message: "Saved evidence document disappeared"}
				}
				continue
			}
			pin := analysisPin(raw, rid, kind)
			if !slices.Contains(pins, pin) {
				return nil, nil, &FaultError{Status: 409, Code: "backend_analysis_input_conflict", Message: "Saved evidence document differs"}
			}
			documents[rid+":"+kind] = raw
			if !slices.Contains(actualPins, pin) {
				actualPins = append(actualPins, pin)
			}
		}
	}
	return documents, actualPins, nil
}

func decodeAnalysisDeletions(schema, baseline string, decisions, coverage []byte, appendProof func(ImportDeletion, HistoricalSubjectRef, []HistoricalEvidenceRef, string, *SourceScope)) {
	switch schema {
	case EventsSchemaVersion:
		var sourceCoverage RevisionCoverage
		if json.Unmarshal(coverage, &sourceCoverage, json.RejectUnknownMembers(true)) != nil || sourceCoverage.Coverage.Status != "complete" || len(sourceCoverage.ReconciliationGaps) > 0 {
			return
		}
		var doc struct {
			Identity []IdentityDecision `json:"identity"`
			Deletion []DeletionDecision `json:"deletion"`
		}
		if json.Unmarshal(decisions, &doc, json.RejectUnknownMembers(true)) != nil {
			return
		}
		for i, d := range doc.Deletion {
			if d.Resolved && d.OldSubject != nil {
				appendProof(d.Command, *d.OldSubject, d.OldEvidenceRefs, fmt.Sprintf("/deletion/%d", i), nil)
			}
		}
	case ComposedSchemaVersion:
		var doc struct {
			DocumentVersion string                   `json:"documentVersion"`
			SourceScope     *SourceScope             `json:"sourceScope"`
			Migration       *SourceMigrationDecision `json:"migration"`
			LegacyDecisions []ImportCommand          `json:"legacyDecisions"`
		}
		if json.Unmarshal(decisions, &doc) != nil || doc.DocumentVersion != "source-decisions-v1" || doc.SourceScope == nil || doc.SourceScope.Kind != "reconcile" || doc.Migration != nil {
			return
		}
		for i, c := range doc.LegacyDecisions {
			if c.Op == "delete_assertion" && c.Deletion != nil {
				appendProof(*c.Deletion, HistoricalSubjectRef{RevisionID: baseline, RecordType: c.Deletion.RecordType, ID: c.Deletion.ExpectedID}, nil, fmt.Sprintf("/legacyDecisions/%d/deletion", i), doc.SourceScope)
			}
		}
	}
}

type analysisDeletionBuilder struct {
	before, after            *EffectiveGraphSnapshot
	baseline, result         string
	decisionPin, coveragePin AnalysisEvidenceDocumentPin
	out                      *AnalysisDeletionEvidence
}

func (b analysisDeletionBuilder) appendProof(command ImportDeletion, subject HistoricalSubjectRef, evidence []HistoricalEvidenceRef, pointer string, scope *SourceScope) {
	if subject.RevisionID != b.baseline || subject.RecordType != command.RecordType || subject.ID != command.ExpectedID {
		return
	}
	owner, refs, ok := analysisDeletionBaseline(b.before, command, scope)
	if !ok || analysisRecordExists(b.after, command.RecordType, command.ExpectedID) {
		return
	}
	for _, a := range b.after.Source.Assertions {
		if a.RecordType == command.RecordType && a.RecordID == command.ExpectedID {
			return
		}
	}
	coveragePointer, ok := analysisDeletionScope(b.after, owner, scope)
	if !ok {
		return
	}
	if evidence == nil {
		evidence = refs
	}
	if evidence == nil {
		evidence = []HistoricalEvidenceRef{}
	}
	b.out.Deletions = append(b.out.Deletions, AnalysisDeletionProof{RevisionID: b.result, BaselineRevisionID: b.baseline, Record: ChangeRecordRef{RecordType: command.RecordType, ID: command.ExpectedID}, Owner: owner, ExternalKey: command.ExternalKey, DecisionPin: b.decisionPin, CoveragePin: b.coveragePin, DecisionPointer: pointer, CoveragePointer: coveragePointer, OldSubject: subject, OldEvidenceRefs: evidence})
}

func analysisDeletionOwners(g *EffectiveGraphSnapshot, c ImportDeletion, scope *SourceScope) ([]AssertionOwnership, []string, bool) {
	owners := []AssertionOwnership{}
	evidence := []string{}
	if g.Pins.StructuralSchemaVersion == ComposedSchemaVersion {
		for _, a := range g.Source.Assertions {
			if a.RecordType == c.RecordType && a.RecordID == c.ExpectedID {
				if a.ExternalKey != c.ExternalKey || scope == nil || a.Owner.RepositoryID != scope.RepositoryID || a.Owner.ProviderNamespace != scope.ProviderNamespace {
					return nil, nil, false
				}
				owners = append(owners, a.Owner)
				evidence = append(evidence, a.EvidenceIDs...)
			}
		}
	} else {
		for _, n := range g.State.Nodes {
			if c.RecordType == "node" && n.ID == c.ExpectedID && n.ExternalKey == c.ExternalKey && n.Ownership != nil {
				owners = append(owners, *n.Ownership)
				evidence = n.EvidenceIDs
			}
		}
		for _, e := range g.State.Edges {
			if c.RecordType == "edge" && e.ID == c.ExpectedID && e.ExternalKey == c.ExternalKey && e.Ownership != nil {
				owners = append(owners, *e.Ownership)
				evidence = e.EvidenceIDs
			}
		}
	}
	return owners, evidence, true
}

func analysisLegacyDeletionScope(g *EffectiveGraphSnapshot, owner AssertionOwnership) (string, bool) {
	src := primarySource(g.State)
	if src == nil || src.RepositoryID != owner.RepositoryID || src.Provider.Namespace != owner.ProviderNamespace || g.State.Revision.Coverage.Status != "complete" {
		return "", false
	}
	for _, item := range g.State.Inventory {
		if item.Status != "complete" || len(item.Gaps) > 0 {
			return "", false
		}
	}
	return "/coverage", true
}

var _ AnalysisEvidenceReader = (*Repo)(nil)
