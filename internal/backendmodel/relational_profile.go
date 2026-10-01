package backendmodel

import "slices"

const (
	RelationalProfile       = "relational-graph-v1"
	RelationalSchemaVersion = "2"
)

type ImportProfileExtension struct {
	FromProfile string `json:"fromProfile"`
	ToProfile   string `json:"toProfile"`
}

func SupportedNodeKindsForProfile(profile string) []string {
	switch profile {
	case GraphProfile:
		return SupportedNodeKinds()
	case RelationalProfile:
		return append(SupportedNodeKinds(), "db_schema", "table", "column", "constraint", "index", "view", "migration")
	default:
		return nil
	}
}

func SupportedEdgeKindsForProfile(profile string) []string {
	switch profile {
	case GraphProfile:
		return SupportedEdgeKinds()
	case RelationalProfile:
		return append(SupportedEdgeKinds(), "references")
	default:
		return nil
	}
}

func SupportedModelSchemaVersions() []string { return []string{SchemaVersion, RelationalSchemaVersion} }

func selectedProfile(profile string) string {
	if profile == "" {
		return GraphProfile
	}
	return profile
}

func modelSchemaVersion(profile string) string {
	if profile == RelationalProfile {
		return RelationalSchemaVersion
	}
	return SchemaVersion
}

func profileSet(profiles []string) []string {
	set := slices.Clone(profiles)
	slices.Sort(set)
	return slices.Compact(set)
}

func validateImportProfile(in BeginImportInput) error {
	profile := selectedProfile(in.Profile)
	if profile != GraphProfile && profile != RelationalProfile {
		return reconciliationFault("backend_unsupported_scope", "Unsupported import profile")
	}
	if in.ProfileExtension != nil && (in.Mode != "reconcile" || profile != RelationalProfile || in.ProfileExtension.FromProfile != GraphProfile || in.ProfileExtension.ToProfile != RelationalProfile) {
		return reconciliationFault("backend_unsupported_scope", "Only an explicit foundation to relational reconciliation extension is supported")
	}
	if in.Mode != "reconcile" && profile == RelationalProfile && (len(in.Manifest.Provider.Profiles) != 2 || !slices.Equal(profileSet(in.Manifest.Provider.Profiles), profileSet([]string{GraphProfile, RelationalProfile}))) {
		return reconciliationFault("backend_incompatible_provider", "Relational initial imports require exactly the foundation and relational profiles")
	}
	return nil
}

func requireProviderProfile(state *RevisionState, s *ImportSession, prior SourceProvider) error {
	profile := selectedProfile(s.Profile)
	if state.Revision.SchemaVersion == RelationalSchemaVersion && profile != RelationalProfile {
		return reconciliationFault("backend_unsupported_scope", "A relational base requires the relational profile")
	}
	if profile == RelationalProfile {
		if s.ProfileExtension == nil && state.Revision.SchemaVersion != RelationalSchemaVersion {
			return reconciliationFault("backend_unsupported_scope", "A foundation base requires an explicit relational profile extension")
		}
		if s.ProfileExtension != nil && (state.Revision.SchemaVersion != SchemaVersion || slices.Contains(prior.Profiles, RelationalProfile)) {
			return reconciliationFault("backend_unsupported_scope", "Profile extension requires a foundation-only base")
		}
	}
	current := s.Manifest.Provider
	profiles := prior.Profiles
	if s.ProfileExtension != nil {
		profiles = append(slices.Clone(profiles), RelationalProfile)
	}
	if prior.Name != current.Name || prior.Version != current.Version || prior.Namespace != current.Namespace || prior.Method != current.Method || !slices.Equal(profileSet(profiles), profileSet(current.Profiles)) {
		return reconciliationFault("backend_incompatible_provider", "Provider identity, version, method and declared profiles must match the selected transition")
	}
	return nil
}
