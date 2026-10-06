package backendanalysis

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type StartCommon struct {
	Kind            string           `json:"kind"`
	Limits          Limits           `json:"limits"`
	ObservationMode string           `json:"observationMode"`
	ObservationPins []jsontext.Value `json:"observationPins"`
	IdempotencyKey  string           `json:"idempotencyKey"`
}
type StartPackageInput struct {
	StartCommon
	ChangeProposal backendmodel.ProposalReadTarget `json:"changeProposal"`
}
type IdentityMapEntry struct {
	ProposalNodeID string `json:"proposalNodeId"`
	SourceNodeID   string `json:"sourceNodeId"`
	Reason         string `json:"reason"`
}
type CriterionAttachment struct {
	CriterionKey string                         `json:"criterionKey"`
	Attachment   backendmodel.TestAttachmentRef `json:"attachment"`
}
type StartConformanceInput struct {
	StartCommon
	ChangeProposal   backendmodel.ProposalReadTarget `json:"changeProposal"`
	ResultRevisionID string                          `json:"resultRevisionId"`
	IdentityMap      []IdentityMapEntry              `json:"identityMap"`
	TestAttachments  []CriterionAttachment           `json:"testAttachments"`
}
type StartEndpointReviewInput struct {
	StartCommon
	FromRevisionID   string                           `json:"fromRevisionId"`
	ToRevisionID     string                           `json:"toRevisionId"`
	BeforeEndpointID string                           `json:"beforeEndpointId"`
	AfterEndpointID  *string                          `json:"afterEndpointId"`
	ChangeProposal   *backendmodel.ProposalReadTarget `json:"changeProposal,omitzero"`
}

func b43Kind(kind string) bool {
	return slices.Contains([]string{"change_package", "conformance", "endpoint_review"}, kind)
}
func (in StartInput) MarshalJSON() ([]byte, error) {
	if in.Measurement != nil {
		return json.Marshal(in.Measurement)
	}
	switch in.Kind {
	case "change_package":
		if in.Package != nil {
			return json.Marshal(in.Package)
		}
	case "conformance":
		if in.Conformance != nil {
			return json.Marshal(in.Conformance)
		}
	case "endpoint_review":
		if in.EndpointReview != nil {
			return json.Marshal(in.EndpointReview)
		}
	}
	type plain StartInput
	return json.Marshal(plain(in))
}
func (in *StartInput) unmarshalB43(raw []byte, kind string) error {
	required := []string{"kind", "limits", "observationMode", "idempotencyKey"}
	optional := []string{"observationPins"}
	check := raw
	switch kind {
	case "change_package":
		required = append(required, "changeProposal")
	case "conformance":
		required = append(required, "changeProposal", "resultRevisionId", "identityMap", "testAttachments")
	case "endpoint_review":
		required = append(required, "fromRevisionId", "toRevisionId", "beforeEndpointId", "afterEndpointId")
		optional = append(optional, "changeProposal")
		// This arm's removal discriminator is explicitly nullable, unlike all other members.
		var m map[string]jsontext.Value
		if err := json.Unmarshal(raw, &m); err != nil {
			return malformed(err.Error())
		}
		if bytes.Equal(bytes.TrimSpace(m["afterEndpointId"]), []byte("null")) {
			m["afterEndpointId"] = jsontext.Value(`""`)
		}
		var err error
		check, err = json.Marshal(m)
		if err != nil {
			return err
		}
	}
	if _, err := closed(check, required, optional); err != nil {
		return err
	}
	var common StartCommon
	if err := json.Unmarshal(raw, &common); err != nil {
		return malformed(err.Error())
	}
	if common.ObservationMode != "none" || len(common.ObservationPins) > 0 {
		return fault(422, "unsupported", "Observations are not supported")
	}
	if !validKey(common.IdempotencyKey) {
		return malformed("Invalid idempotency key")
	}
	common.ObservationPins = []jsontext.Value{}
	next := StartInput{Kind: kind, Limits: common.Limits, ObservationMode: common.ObservationMode, ObservationPins: common.ObservationPins, IdempotencyKey: common.IdempotencyKey}
	switch kind {
	case "change_package":
		var p StartPackageInput
		if err := decode(raw, &p); err != nil {
			return err
		}
		p.StartCommon = common
		next.Package = &p
	case "conformance":
		var p StartConformanceInput
		if err := decode(raw, &p); err != nil {
			return err
		}
		p.StartCommon = common
		if err := normalizeConformanceStart(&p); err != nil {
			return err
		}
		next.Conformance = &p
	case "endpoint_review":
		var err error
		next.EndpointReview, err = decodeEndpointStart(raw, common)
		if err != nil {
			return err
		}

	}
	*in = next
	return nil
}
func (m *IdentityMapEntry) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, []string{"proposalNodeId", "sourceNodeId", "reason"}, nil); err != nil {
		return err
	}
	type plain IdentityMapEntry
	var p plain
	if err := decode(raw, &p); err != nil {
		return err
	}
	if !backendmodel.ValidID(p.ProposalNodeID) || !backendmodel.ValidID(p.SourceNodeID) || !b43Text(p.Reason) {
		return malformed("Invalid identity mapping")
	}
	*m = IdentityMapEntry(p)
	return nil
}
func (a *CriterionAttachment) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, []string{"criterionKey", "attachment"}, nil); err != nil {
		return err
	}
	type plain CriterionAttachment
	var p plain
	if err := decode(raw, &p); err != nil {
		return err
	}
	if !utf8.ValidString(p.CriterionKey) || utf8.RuneCountInString(p.CriterionKey) < 1 || utf8.RuneCountInString(p.CriterionKey) > backendmodel.MaxExternalKeyLength || strings.ContainsFunc(p.CriterionKey, unicode.IsControl) {
		return malformed("Invalid criterion key")
	}
	*a = CriterionAttachment(p)
	return nil
}
func b43Text(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && len(s) <= 4096
}

func normalizeConformanceStart(p *StartConformanceInput) error {
	if !backendmodel.ValidID(p.ResultRevisionID) || len(p.IdentityMap) > 10000 || len(p.TestAttachments) > 100 {
		return malformed("Invalid conformance associations")
	}
	proposals, sources, keys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, m := range p.IdentityMap {
		if proposals[m.ProposalNodeID] || sources[m.SourceNodeID] {
			return malformed("Identity map must be one-to-one")
		}
		proposals[m.ProposalNodeID] = true
		sources[m.SourceNodeID] = true
	}
	for _, a := range p.TestAttachments {
		if keys[a.CriterionKey] {
			return malformed("Duplicate criterion attachment")
		}
		keys[a.CriterionKey] = true
	}
	slices.SortFunc(p.IdentityMap, func(a, b IdentityMapEntry) int { return strings.Compare(a.ProposalNodeID, b.ProposalNodeID) })
	slices.SortFunc(p.TestAttachments, func(a, b CriterionAttachment) int { return strings.Compare(a.CriterionKey, b.CriterionKey) })
	return nil
}

func decodeEndpointStart(raw []byte, common StartCommon) (*StartEndpointReviewInput, error) {
	var p StartEndpointReviewInput
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	p.StartCommon = common
	if !backendmodel.ValidID(p.FromRevisionID) || !backendmodel.ValidID(p.ToRevisionID) || !backendmodel.ValidID(p.BeforeEndpointID) || (p.AfterEndpointID != nil && !backendmodel.ValidID(*p.AfterEndpointID)) {
		return nil, malformed("Invalid endpoint revision or identity")
	}
	return &p, nil
}
