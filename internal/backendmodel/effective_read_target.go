package backendmodel

import "encoding/json/v2"

type ImportCandidateReadTarget struct {
	ImportID      string `json:"importId"`
	ImportVersion int64  `json:"importVersion"`
	CandidateHash string `json:"candidateHash"`
}

func (in ImportCandidateReadTarget) Validate() error {
	if !ValidID(in.ImportID) {
		return invalid("importId", "Expected an exact import UUID")
	}
	if in.ImportVersion < 1 {
		return invalid("importVersion", "Expected a positive exact JSON integer")
	}
	if !validHash(in.CandidateHash) {
		return invalid("candidateHash", "Expected an exact lowercase SHA-256 hash")
	}
	return nil
}

func (in *ImportCandidateReadTarget) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("importCandidate", "Expected an exact READY candidate target")
	}
	if err := relationalFields(m, []string{"importId", "importVersion", "candidateHash"}, nil); err != nil {
		return invalid("importCandidate", err.Error())
	}
	type plain ImportCandidateReadTarget
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return invalid("importCandidate", "Invalid candidate target")
	}
	if err := ImportCandidateReadTarget(value).Validate(); err != nil {
		return err
	}
	*in = ImportCandidateReadTarget(value)
	return nil
}

func (in BackendReadTarget) Validate() error {
	count := 0
	if in.RevisionID != "" {
		count++
		if !ValidID(in.RevisionID) {
			return invalid("revisionId", "Expected an exact revision UUID")
		}
	}
	for _, target := range []*ProposalReadTarget{in.Proposal, in.ChangeProposal} {
		if target != nil {
			count++
			if !ValidID(target.ProposalID) || !ValidID(target.ProposalRevisionID) {
				return invalid("proposal", "Expected exact proposal and draft UUIDs")
			}
		}
	}
	if in.ImportCandidate != nil {
		count++
		if err := in.ImportCandidate.Validate(); err != nil {
			return err
		}
	}
	if count != 1 {
		return invalid("target", "Select exactly one pinned graph target")
	}
	return nil
}

func (in *BackendReadTarget) UnmarshalJSON(raw []byte) error {
	type plain BackendReadTarget
	var value plain
	if err := decodeReadQuery(raw, nil, nil, &value); err != nil {
		return err
	}
	if err := BackendReadTarget(value).Validate(); err != nil {
		return err
	}
	*in = BackendReadTarget(value)
	return nil
}
