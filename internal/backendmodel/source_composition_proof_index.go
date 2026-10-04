package backendmodel

import "encoding/json/v2"

type sourceProofIndex struct {
	evidence map[string]Evidence
	legacy   map[string]LegacyProofBasis
	owners   map[string]AssertionOwnership
	files    map[string]bool
}

func sourceProofs(graph *SourceGraphSnapshot) (*sourceProofIndex, error) {
	if graph.proofIndex != nil {
		return graph.proofIndex, nil
	}
	index := &sourceProofIndex{evidence: map[string]Evidence{}, legacy: map[string]LegacyProofBasis{}, owners: map[string]AssertionOwnership{}, files: map[string]bool{}}
	for id, raw := range graph.RawEvidence {
		var e Evidence
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		if e.ID != id {
			return nil, semantic("evidence.id", "Evidence document differs from its stored identity")
		}
		index.evidence[id] = e
	}
	for _, basis := range graph.LegacyProofBases {
		index.legacy[basis.EvidenceID] = basis
	}
	if graph.SourceVector != nil {
		for _, snapshot := range graph.SourceVector.Snapshots {
			index.owners[snapshot.ID] = AssertionOwnership{RepositoryID: snapshot.RepositoryID, ProviderNamespace: snapshot.Provider.Namespace}
			for _, file := range snapshot.Files {
				if file.AnalysisStatus == "analyzed" {
					index.files[sourceProofFileKey(snapshot.ID, file.Path, file.ContentHash)] = true
				}
			}
		}
	}
	graph.proofIndex = index
	return index, nil
}

func sourceProofFileKey(snapshot, file, hash string) string {
	return snapshot + "\x00" + file + "\x00" + hash
}

func sourceMetadataOnly(a ProviderAssertion, index *sourceProofIndex) bool {
	if len(a.EvidenceIDs) == 0 {
		return false
	}
	for _, eid := range a.EvidenceIDs {
		basis, ok := index.legacy[eid]
		if !ok || basis.Support != "historical_metadata" {
			return false
		}
	}
	return true
}

func sourceClaimLegacyBases(a ProviderAssertion, index *sourceProofIndex) []LegacyProofBasis {
	bases := []LegacyProofBasis{}
	for _, eid := range a.EvidenceIDs {
		if basis, ok := index.legacy[eid]; ok {
			bases = append(bases, basis)
		}
	}
	return bases
}

func sourceSemanticCurrentness(graph *SourceGraphSnapshot, current map[string]SourceClaimCurrentness) error {
	index, err := sourceProofs(graph)
	if err != nil {
		return err
	}
	for _, a := range graph.Assertions {
		key := sourceAssertionKey(a)
		f := current[key]
		metadataOnly := sourceMetadataOnly(a, index)
		if metadataOnly {
			f.Own = sourceStaleReason(f.Own, "legacy_metadata_only")
		}
		for i := range f.Fields {
			if !sourceSemanticSupport(graph, a, f.Fields[i].Property) {
				reason := "semantic_support_missing"
				if metadataOnly {
					reason = "legacy_metadata_only"
				}
				f.Fields[i].Own = sourceStaleReason(f.Fields[i].Own, reason)
			}
		}
		current[key] = f
	}
	return nil
}
