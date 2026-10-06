package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
)

func sourceVectorAt(ctx context.Context, q importReader, state *RevisionState) (*SourceVector, error) {
	if state.Revision.SchemaVersion == ComposedSchemaVersion {
		var doc string
		if err := q.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources_documents WHERE revision_id=?`, state.Revision.ID).Scan(&doc); err != nil {
			return nil, err
		}
		var coverage SourceRevisionContext
		if err := json.Unmarshal([]byte(doc), &coverage, json.RejectUnknownMembers(true)); err != nil {
			return nil, err
		}
		if coverage.ViewSchemaVersion != ComposedSchemaVersion || coverage.SourceVector.DocumentVersion != "source-vector-v1" {
			return nil, semantic("sourceVector", "Invalid composed source context")
		}
		return &coverage.SourceVector, nil
	}
	vector := &SourceVector{DocumentVersion: "source-vector-v1", Partitions: []SourcePartition{}, Snapshots: slices.Clone(state.Sources)}
	if len(state.Sources) == 0 {
		return vector, nil
	}
	if state.Revision.SchemaVersion != EventsSchemaVersion {
		return nil, reconciliationFault("backend_unsupported_scope", "Composed imports require an empty project or source5/source6 base")
	}
	primary := primarySource(*state)
	if primary == nil {
		return nil, semantic("sourceVector", "Source5 base has no primary source")
	}
	status := SourceScopeStatus{Status: "partial", Gaps: []string{"Legacy source scope retained"}}
	if state.Revision.Coverage.Status == "complete" {
		status = SourceScopeStatus{Status: "complete", Gaps: []string{}}
	}
	vector.Partitions = append(vector.Partitions, SourcePartition{RepositoryID: primary.RepositoryID, ProviderNamespace: primary.Provider.Namespace, SnapshotID: primary.ID, Provider: primary.Provider, Inventory: state.Inventory, ScopeStatus: status})
	for i := range vector.Snapshots {
		vector.Snapshots[i].Role = "retained_provenance"
		if vector.Snapshots[i].ID == primary.ID {
			vector.Snapshots[i].Role = "active_source"
		}
	}
	return vector, nil
}

type sourceScopePartitions struct {
	selected     *SourcePartition
	from         *SourcePartition
	repositories int
	providers    int
}

func requireComposedBase(ctx context.Context, q importReader, s *ImportSession) error {
	if err := validateComposedMode(BeginImportInput{
		Mode: s.Mode, Profile: s.Profile, ProfileExtension: s.ProfileExtension,
		SourceScope: s.SourceScope, ScopeStatus: s.ScopeStatus, SyncPolicy: s.SyncPolicy, Manifest: s.Manifest, ChangeManifest: s.ChangeManifest,
	}); err != nil {
		return err
	}
	state, err := loadSourceState(ctx, q, s.ProjectID, s.BaseRevisionID)
	if err != nil {
		return err
	}
	vector, err := sourceVectorAt(ctx, q, state)
	if err != nil {
		return err
	}
	vectorHash, err := requestDigest(vector)
	if err != nil {
		return err
	}
	if s.BaseVectorHash != "" && s.BaseVectorHash != vectorHash {
		return importConflict("backend_import_base_conflict", "Pinned source vector changed", s.Version)
	}
	s.BaseVectorHash = vectorHash
	partitions := inspectSourceScopePartitions(vector, s)
	needsExtension := sourceScopeNeedsExtension(state, vector, s, partitions.selected)
	if needsExtension != (s.ProfileExtension != nil) {
		return reconciliationFault("backend_unsupported_scope", "Selected source transition requires exactly one explicit adjacent extension")
	}
	if err := validateComposedRepository(ctx, q, s, partitions.repositories); err != nil {
		return err
	}
	switch s.SourceScope.Kind {
	case "reconcile":
		err = validateComposedReconcile(s, partitions.selected, needsExtension)
	case "add_provider", "add_repository", "migrate_provider":
		err = validateComposedPartitionAddition(s, partitions)
	}
	if err != nil {
		return err
	}
	s.BasePartition = nil
	if s.SourceScope.Kind == "reconcile" {
		s.BasePartition = partitions.selected
	}
	if s.SourceScope.Kind == "migrate_provider" {
		s.BasePartition = partitions.from
	}
	if s.SyncPolicy == IncrementalSourcePolicy {
		base := &SourceGraphSnapshot{SourceVector: vector}
		before, err := incrementalBaseSnapshot(base, s)
		if err != nil {
			return err
		}
		if err := ValidateChangeManifest(before, s.Manifest.Snapshot, *s.ChangeManifest); err != nil {
			return err
		}
	}
	s.SelectedPartition = new(selectedSourcePartition(s))
	return nil
}

func selectedSourcePartition(s *ImportSession) SourcePartition {
	return SourcePartition{RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace, SnapshotID: s.SnapshotID, Provider: s.Manifest.Provider, Inventory: s.Inventory, ScopeStatus: *s.ScopeStatus}
}

func inspectSourceScopePartitions(vector *SourceVector, s *ImportSession) sourceScopePartitions {
	out := sourceScopePartitions{}
	repositories := map[string]bool{}
	for i := range vector.Partitions {
		partition := &vector.Partitions[i]
		repositories[partition.RepositoryID] = true
		if partition.RepositoryID != s.RepositoryID {
			continue
		}
		out.providers++
		if partition.ProviderNamespace == s.Manifest.Provider.Namespace {
			out.selected = partition
		}
		if partition.ProviderNamespace == s.SourceScope.FromProviderNamespace {
			out.from = partition
		}
	}
	out.repositories = len(repositories)
	return out
}

func sourceScopeNeedsExtension(
	state *RevisionState,
	vector *SourceVector,
	s *ImportSession,
	selected *SourcePartition,
) bool {
	if len(vector.Partitions) > 0 && state.Revision.SchemaVersion == EventsSchemaVersion {
		return true
	}
	if s.SourceScope.Kind != "reconcile" || selected == nil {
		return false
	}
	return sourceProfilesMatch(EventsSchemaVersion, selected.Provider.Profiles)
}

func validateComposedRepository(ctx context.Context, q importReader, s *ImportSession, repositories int) error {
	if s.SourceScope.Kind == "add_repository" {
		if repositories >= MaxSourceRepositories {
			return limitFault("Project repository limit exceeded")
		}
		var duplicate int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM backend_repositories WHERE project_id=? AND logical_name=?`, s.ProjectID, s.Manifest.RepositoryName).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate != 0 {
			return semantic("manifest.repositoryName", "Repository already exists; select its explicit scope")
		}
		return nil
	}
	var name string
	if err := q.QueryRowContext(ctx, `SELECT logical_name FROM backend_repositories WHERE project_id=? AND id=?`, s.ProjectID, s.RepositoryID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return notFound()
		}
		return err
	}
	if name != s.Manifest.RepositoryName {
		return semantic("manifest.repositoryName", "Repository name must match selected repository")
	}
	return nil
}

func validateComposedReconcile(s *ImportSession, selected *SourcePartition, needsExtension bool) error {
	if selected == nil || s.SourceScope.ProviderNamespace != s.Manifest.Provider.Namespace {
		return semantic("sourceScope", "Selected provider partition does not exist in base")
	}
	prior, incoming := selected.Provider, s.Manifest.Provider
	profiles := slices.Clone(prior.Profiles)
	if needsExtension {
		profiles = append(profiles, ComposedProfile)
	}
	sameIdentity := prior.Name == incoming.Name && prior.Version == incoming.Version && prior.Namespace == incoming.Namespace
	sameMethod := prior.Method == incoming.Method
	sameProfiles := slices.Equal(profileSet(profiles), profileSet(incoming.Profiles))
	if !sameIdentity || !sameMethod || !sameProfiles {
		return reconciliationFault("backend_incompatible_provider", "Provider identity, version, method and profiles must match the selected partition")
	}
	return nil
}

func validateComposedPartitionAddition(s *ImportSession, partitions sourceScopePartitions) error {
	if partitions.selected != nil {
		return semantic("sourceScope", "Incoming provider namespace must be absent from base")
	}
	if partitions.providers >= MaxSourceProviders {
		return limitFault("Repository provider limit exceeded")
	}
	if s.SourceScope.Kind != "migrate_provider" {
		return nil
	}
	from := partitions.from
	if from == nil || from.SnapshotID != s.SourceScope.FromSnapshotID || from.ProviderNamespace == s.Manifest.Provider.Namespace {
		return semantic("sourceScope", "Migration requires an exact active source partition and a new namespace")
	}
	return nil
}

func sourceWholeInventoryDiagnostics(s *ImportSession, assertions []ProviderAssertion) []ImportDiagnostic {
	counts := map[string]int64{"files": int64(len(s.Manifest.Snapshot.Files)), "endpoints": 0, "datastores": 0}
	for _, a := range assertions {
		if a.RecordType != "node" || a.Owner.RepositoryID != s.RepositoryID || a.Owner.ProviderNamespace != s.Manifest.Provider.Namespace {
			continue
		}
		switch a.Payload.Kind {
		case "http_operation":
			counts["endpoints"]++
		case "datastore":
			counts["datastores"]++
		}
	}
	diagnostics := []ImportDiagnostic{}
	for _, item := range s.Inventory {
		expected, known := counts[item.Category]
		if known && item.Status == "complete" && (item.KnownCount != expected || item.Denominator == nil || *item.Denominator != expected) {
			diagnostics = append(diagnostics, ImportDiagnostic{Code: "backend_inventory_incomplete", Path: "inventory/" + item.Category, Message: "Complete inventory must count the final selected partition membership"})
		}
	}
	return diagnostics
}
