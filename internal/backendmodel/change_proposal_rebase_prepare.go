package backendmodel

// rebaseDraftFootprint reads byte metadata and exact locators through SQL. A
// potentially large draft must not be decoded into a generic JSON tree before
// the combined B/O/N reservation exists.
func rebaseDraftFootprint(b *analysisFootprintBuilder, proposalID, revisionID string) error {
	var base string
	var size int64
	if err := b.q.QueryRowContext(b.ctx, `SELECT base_revision_id,length(CAST(document AS BLOB)) FROM backend_change_proposal_revisions WHERE project_id=? AND proposal_id=? AND id=?`, b.pid, proposalID, revisionID).Scan(&base, &size); err != nil {
		return err
	}
	if err := b.add("change:"+revisionID, size); err != nil {
		return err
	}
	if err := b.source(base); err != nil {
		return err
	}
	ledger, err := changeIdentityInputBytes(b.ctx, b.q, b.pid, proposalID)
	if err != nil {
		return err
	}
	if err = b.add("identities:"+revisionID, ledger); err != nil {
		return err
	}
	if err = b.pins(`SELECT value FROM backend_change_proposal_revisions r,json_each(r.document,'$.artifactPins') WHERE r.project_id=? AND r.proposal_id=? AND r.id=?`, b.pid, proposalID, revisionID); err != nil {
		return err
	}
	rows, err := b.q.QueryContext(b.ctx, `SELECT DISTINCT j.value FROM backend_change_proposal_revisions r,json_tree(r.document) j WHERE r.project_id=? AND r.proposal_id=? AND r.id=? AND j.key='historicalRevisionId' AND j.type='text'
 UNION SELECT json_extract(j.value,'$.basis.revisionId') FROM backend_change_proposal_revisions r,json_each(r.document,'$.delta.carriedIdentities') j WHERE r.project_id=? AND r.proposal_id=? AND r.id=?
 UNION SELECT json_extract(j.value,'$.revisionId') FROM backend_change_proposal_revisions r,json_tree(r.document) j WHERE r.project_id=? AND r.proposal_id=? AND r.id=? AND j.type='object' AND json_extract(j.value,'$.kind')='source' AND json_type(j.value,'$.revisionId')='text' AND json_extract(j.value,'$.revisionId')<>''`, b.pid, proposalID, revisionID, b.pid, proposalID, revisionID, b.pid, proposalID, revisionID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	refs := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		refs = append(refs, id)
	}
	scanErr := rows.Err()
	closeErr := rows.Close()
	if scanErr != nil {
		return scanErr
	}
	if closeErr != nil {
		return closeErr
	}
	for _, id := range refs {
		if err = b.source(id); err != nil {
			return err
		}
	}
	return nil
}
