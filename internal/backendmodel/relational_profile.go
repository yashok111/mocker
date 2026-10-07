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
	case ComposedProfile:
		return append(SupportedNodeKindsForProfile(EventsProfile), "domain_entity", "dto", "api_schema", "representation_field")
	case GraphProfile:
		return SupportedNodeKinds()
	case RelationalProfile:
		return append(SupportedNodeKinds(), "db_schema", "table", "column", "constraint", "index", "view", "migration")
	case EventsProfile:
		return append(SupportedNodeKindsForProfile(LineageProfile), "channel", "message", "consumer", "job", "event_field")
	case LineageProfile:
		return append(SupportedNodeKindsForProfile(RuntimeProfile), "api_field", "field_mapping")
	case RuntimeProfile:
		return append(SupportedNodeKindsForProfile(RelationalProfile), "flow", "flow_step", "query", "transaction")
	default:
		return nil
	}
}

func SupportedEdgeKindsForProfile(profile string) []string {
	switch profile {
	case ComposedProfile:
		return SupportedEdgeKindsForProfile(EventsProfile)
	case GraphProfile:
		return SupportedEdgeKinds()
	case RelationalProfile:
		return append(SupportedEdgeKinds(), "references")
	case EventsProfile:
		return append(SupportedEdgeKindsForProfile(LineageProfile), "emits", "delivered_to", "retries", "dead_letters")
	case LineageProfile:
		return SupportedEdgeKindsForProfile(RuntimeProfile)
	case RuntimeProfile:
		return append(SupportedEdgeKindsForProfile(RelationalProfile), "next", "branch", "error", "returns", "reads", "writes", "deletes", "begins", "commits", "rolls_back")
	default:
		return nil
	}
}

func SupportedModelSchemaVersions() []string {
	return []string{SchemaVersion, RelationalSchemaVersion, RuntimeSchemaVersion, LineageSchemaVersion, EventsSchemaVersion, ComposedSchemaVersion}
}

func selectedProfile(profile string) string {
	if profile == "" {
		return GraphProfile
	}
	return profile
}

func modelSchemaVersion(profile string) string {
	if profile == ComposedProfile {
		return ComposedSchemaVersion
	}
	if profile == EventsProfile {
		return EventsSchemaVersion
	}
	if profile == LineageProfile {
		return LineageSchemaVersion
	}
	if profile == RuntimeProfile {
		return RuntimeSchemaVersion
	}
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
	if profile != GraphProfile && !hasRelationalProfile(profile) {
		return reconciliationFault("backend_unsupported_scope", "Unsupported import profile")
	}
	if in.ProfileExtension != nil {
		x := in.ProfileExtension
		valid := profile == RelationalProfile && x.FromProfile == GraphProfile && x.ToProfile == RelationalProfile || profile == RuntimeProfile && x.FromProfile == RelationalProfile && x.ToProfile == RuntimeProfile || profile == LineageProfile && x.FromProfile == RuntimeProfile && x.ToProfile == LineageProfile || profile == EventsProfile && x.FromProfile == LineageProfile && x.ToProfile == EventsProfile
		if in.Mode != "reconcile" || !valid {
			return reconciliationFault("backend_unsupported_scope", "Profile extension requires the explicit adjacent source profile transition")
		}
	}
	// A foundation initial import declares exactly the foundation profile.
	// validateManifest checks only that it is present, so an extra profile
	// was committed into an immutable revision and then refused the
	// documented foundation-to-relational extension ("Profile extension
	// requires a foundation-only base") with no legacy way to correct it
	// (review 2026-10-06, F88). A reconcile already must match its prior
	// declared profiles (requireProviderProfile).
	if in.Mode != "reconcile" && profile == GraphProfile && !slices.Equal(profileSet(in.Manifest.Provider.Profiles), []string{GraphProfile}) {
		return reconciliationFault("backend_incompatible_provider", "Foundation initial imports require exactly the foundation profile")
	}
	if in.Mode != "reconcile" && profile == RelationalProfile && (len(in.Manifest.Provider.Profiles) != 2 || !slices.Equal(profileSet(in.Manifest.Provider.Profiles), profileSet([]string{GraphProfile, RelationalProfile}))) {
		return reconciliationFault("backend_incompatible_provider", "Relational initial imports require exactly the foundation and relational profiles")
	}
	if in.Mode != "reconcile" && profile == RuntimeProfile && (len(in.Manifest.Provider.Profiles) != 3 || !slices.Equal(profileSet(in.Manifest.Provider.Profiles), profileSet([]string{GraphProfile, RelationalProfile, RuntimeProfile}))) {
		return reconciliationFault("backend_incompatible_provider", "Runtime initial imports require exactly foundation, relational and runtime profiles")
	}
	if in.Mode != "reconcile" && profile == LineageProfile && !sourceProfilesMatch(LineageSchemaVersion, in.Manifest.Provider.Profiles) {
		return reconciliationFault("backend_incompatible_provider", "Lineage initial imports require exactly foundation, relational, runtime and lineage profiles")
	}
	if in.Mode != "reconcile" && profile == EventsProfile && !sourceProfilesMatch(EventsSchemaVersion, in.Manifest.Provider.Profiles) {
		return reconciliationFault("backend_incompatible_provider", "Events initial imports require exactly five provider profiles")
	}
	return nil
}

func requireProviderProfile(state *RevisionState, s *ImportSession, prior SourceProvider) error {
	profile := selectedProfile(s.Profile)
	if state.Revision.SchemaVersion == EventsSchemaVersion && profile != EventsProfile {
		return reconciliationFault("backend_unsupported_scope", "An events source base requires the events profile")
	}
	if profile == EventsProfile {
		if !sourceProfilesMatch(EventsSchemaVersion, s.Manifest.Provider.Profiles) {
			return reconciliationFault("backend_incompatible_provider", "Events requires exactly five provider profiles")
		}
		if s.ProfileExtension == nil && state.Revision.SchemaVersion != EventsSchemaVersion {
			return reconciliationFault("backend_unsupported_scope", "Events requires an explicit lineage profile extension")
		}
		if s.ProfileExtension != nil && (state.Revision.SchemaVersion != LineageSchemaVersion || slices.Contains(prior.Profiles, EventsProfile)) {
			return reconciliationFault("backend_unsupported_scope", "Events extension requires a lineage source base")
		}
	}

	if state.Revision.SchemaVersion == LineageSchemaVersion && !hasLineageProfile(profile) {
		return reconciliationFault("backend_unsupported_scope", "A lineage source base requires the lineage profile")
	}
	if profile == LineageProfile {
		if !sourceProfilesMatch(LineageSchemaVersion, s.Manifest.Provider.Profiles) {
			return reconciliationFault("backend_incompatible_provider", "Lineage requires exactly four provider profiles")
		}
		if s.ProfileExtension == nil && state.Revision.SchemaVersion != LineageSchemaVersion {
			return reconciliationFault("backend_unsupported_scope", "Lineage requires an explicit runtime profile extension")
		}
		if s.ProfileExtension != nil && (state.Revision.SchemaVersion != RuntimeSchemaVersion || slices.Contains(prior.Profiles, LineageProfile)) {
			return reconciliationFault("backend_unsupported_scope", "Lineage extension requires a runtime source base")
		}
	}
	if state.Revision.SchemaVersion == RuntimeSchemaVersion && !hasRuntimeProfile(profile) {
		return reconciliationFault("backend_unsupported_scope", "A runtime source base requires the runtime profile")
	}
	if state.Revision.SchemaVersion == RelationalSchemaVersion && !hasRelationalProfile(profile) {
		return reconciliationFault("backend_unsupported_scope", "A relational base requires the relational profile")
	}
	if profile == RuntimeProfile {
		if s.ProfileExtension == nil && state.Revision.SchemaVersion != RuntimeSchemaVersion {
			return reconciliationFault("backend_unsupported_scope", "A relational base requires an explicit runtime profile extension")
		}
		if s.ProfileExtension != nil && (state.Revision.SchemaVersion != RelationalSchemaVersion || slices.Contains(prior.Profiles, RuntimeProfile)) {
			return reconciliationFault("backend_unsupported_scope", "Runtime extension requires a relational source base")
		}
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
		profiles = append(slices.Clone(profiles), s.ProfileExtension.ToProfile)
	}
	if prior.Name != current.Name || prior.Version != current.Version || prior.Namespace != current.Namespace || prior.Method != current.Method || !slices.Equal(profileSet(profiles), profileSet(current.Profiles)) {
		return reconciliationFault("backend_incompatible_provider", "Provider identity, version, method and declared profiles must match the selected transition")
	}
	return nil
}
