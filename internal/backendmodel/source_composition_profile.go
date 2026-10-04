package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
)

func composedProfiles() []string {
	return []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile, ComposedProfile}
}

func validateComposedMode(in BeginImportInput) error {
	if in.Mode != "composed" || in.Profile != ComposedProfile || in.RepositoryID != nil || in.GraphScope != nil {
		return reconciliationFault("backend_unsupported_scope", "Composed imports require composed profile and sourceScope without legacy scope fields")
	}
	if in.SourceScope == nil || in.ScopeStatus == nil {
		return reconciliationFault("backend_unsupported_scope", "Composed imports require sourceScope and scopeStatus")
	}
	if err := validateIncrementalPolicy(in); err != nil {
		return err
	}
	if err := validateSourceScope(*in.SourceScope); err != nil {
		return err
	}
	if err := validateSourceScopeStatus(in.ScopeStatus); err != nil {
		return err
	}
	if len(in.Manifest.Provider.Profiles) != 6 || !slices.Equal(profileSet(in.Manifest.Provider.Profiles), profileSet(composedProfiles())) {
		return reconciliationFault("backend_incompatible_provider", "Composed provider must declare exactly six profiles")
	}
	if x := in.ProfileExtension; x != nil && (x.FromProfile != EventsProfile || x.ToProfile != ComposedProfile) {
		return reconciliationFault("backend_unsupported_scope", "Composed extension must be the adjacent events to composed transition")
	}
	return nil
}

func validateSourceScopeStatus(status *SourceScopeStatus) error {
	g := status
	if g.Gaps == nil {
		return semantic("scopeStatus.gaps", "Gaps must be a non-null array")
	}
	if g.Status != "complete" && g.Status != "partial" || g.Status == "complete" && len(g.Gaps) != 0 || g.Status == "partial" && len(g.Gaps) == 0 {
		return semantic("scopeStatus", "Scope status and gaps are inconsistent")
	}
	for _, gap := range g.Gaps {
		if !nonblank(gap) {
			return semantic("scopeStatus.gaps", "Gap must be nonblank")
		}
	}
	return nil
}

func validateSourceScope(s SourceScope) error {
	expected := SourceScope{Kind: s.Kind}
	switch s.Kind {
	case "add_repository":
	case "add_provider":
		if !ValidID(s.RepositoryID) {
			return semantic("sourceScope", "Repository ID must be valid")
		}
		expected.RepositoryID = s.RepositoryID
	case "reconcile":
		if !ValidID(s.RepositoryID) || !externalKey(s.ProviderNamespace) {
			return semantic("sourceScope", "Reconcile requires repository and provider")
		}
		expected.RepositoryID, expected.ProviderNamespace = s.RepositoryID, s.ProviderNamespace
	case "migrate_provider":
		if !ValidID(s.RepositoryID) || !externalKey(s.FromProviderNamespace) || !ValidID(s.FromSnapshotID) || !nonblank(s.Reason) {
			return semantic("sourceScope", "Migration requires exact source partition, snapshot and reason")
		}
		expected.RepositoryID, expected.FromProviderNamespace, expected.FromSnapshotID, expected.Reason = s.RepositoryID, s.FromProviderNamespace, s.FromSnapshotID, s.Reason
	default:
		return semantic("sourceScope", "Invalid source scope tag")
	}
	if s != expected {
		return semantic("sourceScope", "Scope contains fields for another tag")
	}
	return nil
}

func (s *SourceScope) UnmarshalJSON(b []byte) error {
	type plain SourceScope
	var value plain
	if string(b) == "null" {
		return semantic("sourceScope", "Scope cannot be null")
	}
	if err := json.Unmarshal(b, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	var members map[string]jsontext.Value
	if err := json.Unmarshal(b, &members); err != nil {
		return err
	}
	allowed := []string{"kind"}
	switch value.Kind {
	case "add_provider":
		allowed = append(allowed, "repositoryId")
	case "reconcile":
		allowed = append(allowed, "repositoryId", "providerNamespace")
	case "migrate_provider":
		allowed = append(allowed, "repositoryId", "fromProviderNamespace", "fromSnapshotId", "reason")
	}
	for key, raw := range members {
		if !slices.Contains(allowed, key) || string(raw) == "null" {
			return semantic("sourceScope", "Scope contains an inapplicable or null member")
		}
	}
	if err := validateSourceScope(SourceScope(value)); err != nil {
		return err
	}
	*s = SourceScope(value)
	return nil
}

func validateBaseAssertionRef(ref BaseAssertionRef) error {
	if !ValidID(ref.RepositoryID) || !externalKey(ref.ProviderNamespace) || !slices.Contains([]string{"node", "edge"}, ref.RecordType) || !externalKey(ref.ExternalKey) || !ValidID(ref.ExpectedID) || !validHash(ref.AssertionHash) {
		return semantic("base", "Base reference requires exact repository, provider, record type, key, UUID and hash")
	}
	return nil
}

func (r *ImportRecordRef) UnmarshalJSON(b []byte) error {
	type plain ImportRecordRef
	var value plain
	var members map[string]jsontext.Value
	if err := json.Unmarshal(b, &members); err != nil {
		return err
	}
	if len(members) != 1 {
		return semantic("reference", "Reference requires exactly one non-null tag")
	}
	for _, raw := range members {
		if string(raw) == "null" {
			return semantic("reference", "Reference tag cannot be null")
		}
	}
	if err := json.Unmarshal(b, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if (value.LocalKey != "") == (value.Base != nil) {
		return semantic("reference", "Reference must contain localKey or base exclusively")
	}
	if value.Base != nil {
		if err := validateBaseAssertionRef(*value.Base); err != nil {
			return err
		}
	} else if !externalKey(value.LocalKey) {
		return semantic("reference.localKey", "Invalid local key")
	}
	*r = ImportRecordRef(value)
	return nil
}
