package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
)

const changeRebaseProtocol = "backend-change-rebase-v1"

type ChangeRebaseIdentityResolution struct {
	OldSourceID string `json:"oldSourceId"`
	NewSourceID string `json:"newSourceId"`
	Reason      string `json:"reason"`
}
type ChangeRebaseValue struct {
	Presence string         `json:"presence"`
	Value    jsontext.Value `json:"value,omitzero"`
}
type ChangeRebaseResolution struct {
	ConflictID string         `json:"conflictId"`
	Choice     string         `json:"choice"`
	Reason     string         `json:"reason"`
	Value      jsontext.Value `json:"value,omitzero"`
}
type ChangeRebaseSelector struct {
	Kind         string                     `json:"kind"`
	Property     *EffectivePropertySelector `json:"property,omitzero"`
	Identity     *ChangeIdentityTarget      `json:"identity,omitzero"`
	Artifact     *ArtifactKey               `json:"artifact,omitzero"`
	CriterionKey string                     `json:"criterionKey,omitempty"`
}
type PreviewChangeProposalRebaseInput struct {
	ExpectedVersion     int64                            `json:"expectedVersion"`
	ProposalRevisionID  string                           `json:"proposalRevisionId"`
	NewBaseRevisionID   string                           `json:"newBaseRevisionId"`
	IdentityResolutions []ChangeRebaseIdentityResolution `json:"identityResolutions"`
	Resolutions         []ChangeRebaseResolution         `json:"resolutions"`
	RepairCommands      []ChangeProposalCommand          `json:"repairCommands"`
}
type ApplyChangeProposalRebaseInput struct {
	PreviewChangeProposalRebaseInput
	CandidateHash  string `json:"candidateHash"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type ChangeRebaseConflict struct {
	ID        string               `json:"id"`
	Object    ChangeRecordRef      `json:"object"`
	Selector  ChangeRebaseSelector `json:"selector"`
	Base      ChangeRebaseValue    `json:"base"`
	Ours      ChangeRebaseValue    `json:"ours"`
	NewSource ChangeRebaseValue    `json:"newSource"`
}
type ChangeProposalRebaseCandidate struct {
	ProposalID          string                 `json:"proposalId"`
	ProposalRevisionID  string                 `json:"proposalRevisionId"`
	ExpectedVersion     int64                  `json:"expectedVersion"`
	OldBaseRevisionID   string                 `json:"oldBaseRevisionId"`
	OldBaseSemanticHash string                 `json:"oldBaseSemanticHash"`
	NewBaseRevisionID   string                 `json:"newBaseRevisionId"`
	NewBaseSemanticHash string                 `json:"newBaseSemanticHash"`
	Conflicts           []ChangeRebaseConflict `json:"conflicts"`
	Diagnostics         []ImportDiagnostic     `json:"diagnostics"`
	SemanticHash        *string                `json:"semanticHash"`
	CandidateHash       *string                `json:"candidateHash"`
	SourcePins          AnalysisSourcePins     `json:"sourcePins"`
	ArtifactPins        []ArtifactPin          `json:"artifactPins"`
}

type RebaseResolutionOrigin struct {
	ProposalRevisionID string `json:"proposalRevisionId"`
	ResolutionID       string `json:"resolutionId"`
	Reason             string `json:"reason"`
}

func (in *PreviewChangeProposalRebaseInput) UnmarshalJSON(raw []byte) error {
	type plain PreviewChangeProposalRebaseInput
	return decodeChangeOperation(raw, []string{"expectedVersion", "proposalRevisionId", "newBaseRevisionId", "identityResolutions", "resolutions", "repairCommands"}, (*plain)(in))
}
func (in *ApplyChangeProposalRebaseInput) UnmarshalJSON(raw []byte) error {
	// An embedded custom decoder would consume the complete apply object.
	var v struct {
		ExpectedVersion     int64                            `json:"expectedVersion"`
		ProposalRevisionID  string                           `json:"proposalRevisionId"`
		NewBaseRevisionID   string                           `json:"newBaseRevisionId"`
		IdentityResolutions []ChangeRebaseIdentityResolution `json:"identityResolutions"`
		Resolutions         []ChangeRebaseResolution         `json:"resolutions"`
		RepairCommands      []ChangeProposalCommand          `json:"repairCommands"`
		CandidateHash       string                           `json:"candidateHash"`
		IdempotencyKey      string                           `json:"idempotencyKey"`
	}
	if err := decodeChangeOperation(raw, []string{"expectedVersion", "proposalRevisionId", "newBaseRevisionId", "identityResolutions", "resolutions", "repairCommands", "candidateHash", "idempotencyKey"}, &v); err != nil {
		return err
	}
	*in = ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: PreviewChangeProposalRebaseInput{ExpectedVersion: v.ExpectedVersion, ProposalRevisionID: v.ProposalRevisionID, NewBaseRevisionID: v.NewBaseRevisionID, IdentityResolutions: v.IdentityResolutions, Resolutions: v.Resolutions, RepairCommands: v.RepairCommands}, CandidateHash: v.CandidateHash, IdempotencyKey: v.IdempotencyKey}
	return nil
}
func (r *ChangeRebaseResolution) UnmarshalJSON(raw []byte) error {
	type plain ChangeRebaseResolution
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	fields := []string{"conflictId", "choice", "reason"}
	if value.Choice == "replace" {
		fields = append(fields, "value")
	}
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err = relationalFields(m, fields, nil); err != nil {
		return err
	}
	if !validHash(value.ConflictID) || !slices.Contains([]string{"take_source", "keep_proposal", "replace"}, value.Choice) || !validAPIText(value.Reason, 1, 4096) {
		return invalid("resolution", "Exact conflict, choice and bounded reason required")
	}
	*r = ChangeRebaseResolution(value)
	return nil
}

type ChangeRebaseAction struct {
	Protocol      string                           `json:"protocol"`
	Input         PreviewChangeProposalRebaseInput `json:"input"`
	CandidateHash string                           `json:"candidateHash"`
}

func (in ApplyChangeProposalRebaseInput) MarshalJSON() ([]byte, error) {
	type preview PreviewChangeProposalRebaseInput
	return json.Marshal(struct {
		preview
		CandidateHash  string `json:"candidateHash"`
		IdempotencyKey string `json:"idempotencyKey"`
	}{preview: preview(in.PreviewChangeProposalRebaseInput), CandidateHash: in.CandidateHash, IdempotencyKey: in.IdempotencyKey})
}

func validateRebaseOrigin(kind, commandID, reason string, resolution *RebaseResolutionOrigin) error {
	if resolution == nil {
		return nil
	}
	if kind != "intent" || commandID != "" || reason != "" || !ValidID(resolution.ProposalRevisionID) || !validHash(resolution.ResolutionID) || !validAPIText(resolution.Reason, 1, 4096) {
		return invalid("origin", "Rebase resolution authorship is exclusive to intent and cannot include command authorship")
	}
	return nil
}
func (o *EffectiveOrigin) UnmarshalJSON(raw []byte) error {
	type plain EffectiveOrigin
	var v plain
	if err := json.Unmarshal(raw, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if err := validateRebaseOrigin(v.Kind, v.CommandID, v.Reason, v.RebaseResolution); err != nil {
		return err
	}
	*o = EffectiveOrigin(v)
	return nil
}
func (o *EffectiveFieldOrigin) UnmarshalJSON(raw []byte) error {
	type plain EffectiveFieldOrigin
	var v plain
	if err := json.Unmarshal(raw, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if err := validateRebaseOrigin(v.Kind, v.CommandID, v.Reason, v.RebaseResolution); err != nil {
		return err
	}
	*o = EffectiveFieldOrigin(v)
	return nil
}

func validateChangeRebaseReplacement(selector ChangeRebaseSelector, raw jsontext.Value) error {
	switch selector.Kind {
	case "property":
		return nil // Validated against the exact payload/property at merge.
	case "identity":
		var key string
		if err := json.Unmarshal(raw, &key); err != nil || !externalKey(key) {
			return invalid("resolution/value", "Identity replacement requires a bounded nonempty key")
		}
	case "edge_name":
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return err
		}
		_, err := normalizeName(name)
		return err
	case "artifact":
		var unit changeRebaseArtifact
		if err := json.Unmarshal(raw, &unit, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		if selector.Artifact == nil || artifactKey(unit.Pin) != *selector.Artifact {
			return invalid("resolution/value", "Artifact replacement must address the exact conflicting owner")
		}
		return ValidateArtifactVector([]ArtifactPin{unit.Pin}, unit.API, unit.Editor)
	case "criterion":
		var criterion ChangeCriterion
		if err := json.Unmarshal(raw, &criterion); err != nil {
			return err
		}
		if criterion.Key != selector.CriterionKey {
			return invalid("resolution/value", "Criterion replacement must retain its key")
		}
	default:
		return invalid("resolution/value", "Whole-record replacements require real repair commands")
	}
	return nil
}
func (s *ChangeRebaseSelector) UnmarshalJSON(raw []byte) error {
	type plain ChangeRebaseSelector
	var v plain
	if err := json.Unmarshal(raw, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	fields := []string{"kind"}
	switch v.Kind {
	case "record", "edge_name":
	case "property":
		fields = append(fields, "property")
		if v.Property == nil {
			return invalid("selector", "Exact property required")
		}
	case "identity":
		fields = append(fields, "identity")
		if v.Identity == nil {
			return invalid("selector", "Exact identity required")
		}
	case "artifact":
		fields = append(fields, "artifact")
		if v.Artifact == nil {
			return invalid("selector", "Exact artifact required")
		}
		if err := v.Artifact.Validate(); err != nil {
			return err
		}
	case "criterion":
		fields = append(fields, "criterionKey")
		if !externalKey(v.CriterionKey) {
			return invalid("selector", "Criterion key required")
		}
	default:
		return invalid("selector", "Unknown rebase selector")
	}
	members, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err = relationalFields(members, fields, nil); err != nil {
		return err
	}
	*s = ChangeRebaseSelector(v)
	return nil
}
