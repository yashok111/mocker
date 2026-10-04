package backendmodel

import "encoding/json/v2"

type SourceClaimIdentityDecision struct {
	Command      SourceClaimIdentity `json:"command"`
	Owner        AssertionOwnership  `json:"owner"`
	Resolved     bool                `json:"resolved"`
	EvidenceRefs []StagedEvidenceRef `json:"evidenceRefs"`
}

type SourceMigrationDecision struct {
	RepositoryID          string         `json:"repositoryId"`
	Provider              SourceProvider `json:"provider"`
	FromProviderNamespace string         `json:"fromProviderNamespace"`
	FromSnapshotID        string         `json:"fromSnapshotId"`
	Reason                string         `json:"reason"`
	PriorStatus           string         `json:"priorStatus"`
}

func source6ChangeItems(s *ImportSession, c *composedCandidate) []ImportChangeItem {
	items := []ImportChangeItem{}
	for _, conflict := range c.Conflicts {
		items = append(items, ImportChangeItem{RecordType: "assertion_conflict", AssertionConflict: &conflict})
	}
	for _, decision := range c.Decisions {
		if command := decision.Command.ClaimIdentity; command != nil {
			item := SourceClaimIdentityDecision{Command: *command, Owner: AssertionOwnership{RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace, Profile: ComposedProfile}, EvidenceRefs: []StagedEvidenceRef{}}
			for _, a := range c.Source.Assertions {
				if a.RecordType == command.RecordType && a.RecordID == command.Target.ExpectedID && a.Owner.RepositoryID == s.RepositoryID && a.Owner.ProviderNamespace == s.Manifest.Provider.Namespace {
					item.Resolved = true
					item.Owner = a.Owner
				}
			}
			for _, key := range command.EvidenceKeys {
				item.EvidenceRefs = append(item.EvidenceRefs, StagedEvidenceRef{SnapshotID: s.SnapshotID, EvidenceKey: key})
			}
			items = append(items, ImportChangeItem{RecordType: "claim_identity", ClaimIdentity: &item})
		}
	}
	if s.SourceScope.Kind == "migrate_provider" {
		items = append(items, ImportChangeItem{RecordType: "migration", Migration: &SourceMigrationDecision{RepositoryID: s.RepositoryID, Provider: s.Manifest.Provider, FromProviderNamespace: s.SourceScope.FromProviderNamespace, FromSnapshotID: s.SourceScope.FromSnapshotID, Reason: s.SourceScope.Reason, PriorStatus: "active"}})
	}
	return items
}

func source6RevisionDecisions(s *ImportSession, c *composedCandidate) ([]byte, error) {
	var migration *SourceMigrationDecision
	if s.SourceScope.Kind == "migrate_provider" {
		migration = &SourceMigrationDecision{RepositoryID: s.RepositoryID, Provider: s.Manifest.Provider, FromProviderNamespace: s.SourceScope.FromProviderNamespace, FromSnapshotID: s.SourceScope.FromSnapshotID, Reason: s.SourceScope.Reason, PriorStatus: "active"}
	}
	return json.Marshal(struct {
		DocumentVersion string                    `json:"documentVersion"`
		Scope           *SourceScope              `json:"sourceScope"`
		ChangeManifest  *ChangeManifest           `json:"changeManifest,omitzero"`
		AffectedScope   *IncrementalAffectedScope `json:"affectedScope,omitzero"`
		Policy          string                    `json:"syncPolicy"`
		Migration       *SourceMigrationDecision  `json:"migration,omitzero"`
		Decisions       []SourceDecision          `json:"decisions"`
		LegacyDecisions []ImportCommand           `json:"legacyDecisions"`
		Batches         []SourceBatchCommitment   `json:"batches"`
	}{"source-decisions-v1", s.SourceScope, s.ChangeManifest, c.IncrementalScope, s.SyncPolicy, migration, c.Decisions, c.LegacyDecisions, c.BatchCommitments})
}
