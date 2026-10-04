package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"strings"
)

type ChangeIdentityTarget struct {
	Kind       string                   `json:"kind"`
	Source     *QualifiedSourceIdentity `json:"source,omitzero"`
	Basis      *CarriedSourceBasis      `json:"basis,omitzero"`
	RecordType string                   `json:"recordType,omitempty"`
	ID         string                   `json:"id,omitempty"`
	Intent     *IntentIdentitySelector  `json:"-"`
}

func (t ChangeIdentityTarget) MarshalJSON() ([]byte, error) {
	if t.Source != nil && t.Kind != "carried_source_identity" {
		t.Kind = "source_identity"
	}
	if t.Intent != nil {
		t.Kind = "intent_identity"
		t.RecordType, t.ID = t.Intent.RecordType, t.Intent.ID
	}
	type plain ChangeIdentityTarget
	return json.Marshal(plain(t))
}

func (t ChangeIdentityTarget) Validate() error {
	switch t.Kind {
	case "source_identity", "carried_source_identity":
		return t.validateSourceTarget()
	case "intent_identity":
		if t.Source != nil || t.Basis != nil || !slices.Contains([]string{"node", "edge"}, t.RecordType) || !ValidID(t.ID) {
			return invalid("target", "Expected created record identity only")
		}
	default:
		return invalid("target/kind", "Unknown identity target")
	}
	return nil
}
func (t *ChangeIdentityTarget) UnmarshalJSON(b []byte) error {
	type plain ChangeIdentityTarget
	var v plain
	if err := json.Unmarshal(b, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	fields := []string{"kind"}
	if v.Kind == "source_identity" || v.Kind == "carried_source_identity" {
		fields = append(fields, "source")
		if v.Kind == "carried_source_identity" {
			fields = append(fields, "basis")
		}
	} else {
		fields = append(fields, "recordType", "id")
	}
	m, err := relationalObject(b)
	if err != nil {
		return err
	}
	if err = relationalFields(m, fields, nil); err != nil {
		return err
	}
	if v.Source != nil {
		source, err := relationalObject(m["source"])
		if err != nil {
			return err
		}
		if err = relationalFields(source, []string{"recordType", "id", "repositoryId", "providerNamespace", "externalKey", "assertionHash"}, nil); err != nil {
			return err
		}
	}
	target := ChangeIdentityTarget(v)
	if err = target.Validate(); err != nil {
		return err
	}
	*t = target
	if target.Kind == "intent_identity" {
		t.Intent = &IntentIdentitySelector{RecordType: target.RecordType, ID: target.ID}
	}
	return nil
}
func changeIdentityRef(t ChangeIdentityTarget) ChangeRecordRef {
	if t.Source != nil {
		return ChangeRecordRef{RecordType: t.Source.RecordType, ID: t.Source.ID}
	}
	if t.Intent != nil {
		return ChangeRecordRef{RecordType: t.Intent.RecordType, ID: t.Intent.ID}
	}
	return ChangeRecordRef{RecordType: t.RecordType, ID: t.ID}
}
func changeIdentityKey(t ChangeIdentityTarget) string {
	r := changeIdentityRef(t)
	parts := []string{t.Kind, r.RecordType, r.ID}
	if t.Source != nil {
		parts = append(parts, t.Source.RepositoryID, t.Source.ProviderNamespace, t.Source.ExternalKey, t.Source.AssertionHash)
	}
	if t.Basis != nil {
		parts = append(parts, t.Basis.RevisionID, t.Basis.SemanticHash)
	}
	return strings.Join(parts, "\x00")
}

type CarriedSourceBasis struct {
	RevisionID   string `json:"revisionId"`
	SemanticHash string `json:"semanticHash"`
}

type ChangeCarriedSourceIdentity struct {
	Source QualifiedSourceIdentity `json:"source"`
	Basis  CarriedSourceBasis      `json:"basis"`
}

func (t ChangeIdentityTarget) validateSourceTarget() error {
	if t.Source == nil || t.RecordType != "" || t.ID != "" {
		return invalid("target", "Expected exact source identity only")
	}
	s := t.Source
	if !slices.Contains([]string{"node", "edge"}, s.RecordType) || !ValidID(s.ID) || !ValidID(s.RepositoryID) || !externalKey(s.ProviderNamespace) || !externalKey(s.ExternalKey) || !validHash(s.AssertionHash) {
		return invalid("target", "Invalid qualified source identity")
	}
	if t.Kind == "carried_source_identity" {
		if t.Basis == nil || !ValidID(t.Basis.RevisionID) || !validHash(t.Basis.SemanticHash) {
			return invalid("target/basis", "Carried identity requires an exact historical source basis")
		}
	} else if t.Basis != nil {
		return invalid("target/basis", "Current source identity cannot have a historical basis")
	}
	return nil
}
