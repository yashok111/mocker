package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
)

// relationalHistory answers the two narrow questions relational validation
// asks of an import session's ancestor revisions: does this pinned node exist
// as a relational subject of the session's repository, and does any ancestor
// own one of these external keys.
//
// Review 2026-10-06, F79/F80: both used to load every needed ancestor's full
// RevisionState (nodes, edges and evidence, up to 256 MiB per revision) and
// keep all of them until validation returned, inside the single writer. A
// crafted migration facet with one source_only change was enough to
// materialise the whole history at once on a box that is OOM-killed. Here a
// pin is one primary-key row and a source_only key set is one SQL-filtered
// query per ancestor; nothing outlives the call but each revision's derived
// default owner.
type relationalHistory struct {
	ctx       context.Context
	q         importReader
	s         *ImportSession
	ancestors map[string]bool
	owners    map[string]*AssertionOwnership
}

func (h *relationalHistory) ancestor(rid string) (bool, error) {
	if h.ancestors == nil {
		ancestors, err := sourceAncestors(h.ctx, h.q, h.s.ProjectID, h.s.BaseRevisionID)
		if err != nil {
			return false, err
		}
		h.ancestors = ancestors
	}
	return h.ancestors[rid], nil
}

// ownedBySession applies loadRevisionState's deriveMetadata rule: a stored
// node without ownership inherits the revision's primary source.
func (h *relationalHistory) ownedBySession(rid string, n *Node) (bool, error) {
	owner := n.Ownership
	if owner == nil {
		if h.owners == nil {
			h.owners = map[string]*AssertionOwnership{}
		}
		derived, ok := h.owners[rid]
		if !ok {
			state, err := loadSourceState(h.ctx, h.q, h.s.ProjectID, rid)
			if err != nil {
				return false, err
			}
			var fresh *AssertionFreshness
			deriveMetadata(*state, &derived, &fresh)
			h.owners[rid] = derived
		}
		owner = derived
	}
	return owner != nil && owner.RepositoryID == h.s.RepositoryID, nil
}

// relationalSubjectAt reports whether node id of revision rid is a relational
// subject owned by the session's repository.
func (h *relationalHistory) relationalSubjectAt(rid, id string) (bool, error) {
	var doc string
	err := h.q.QueryRowContext(h.ctx, `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type='node' AND id=?`, h.s.ProjectID, rid, id).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var n Node
	if err := json.Unmarshal([]byte(doc), &n); err != nil {
		return false, err
	}
	if !relationalSubject(n.Kind, n.Attributes, false) {
		return false, nil
	}
	return h.ownedBySession(rid, &n)
}

// ownedKeysInAncestors returns the subset of keys that some ancestor revision
// holds as a node owned by the session's repository.
func (h *relationalHistory) ownedKeysInAncestors(keys []string) (map[string]bool, error) {
	if _, err := h.ancestor(""); err != nil {
		return nil, err
	}
	wanted, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for rid := range h.ancestors {
		if err := h.ctx.Err(); err != nil {
			return nil, err
		}
		matches, err := h.nodesWithKeys(rid, string(wanted))
		if err != nil {
			return nil, err
		}
		for i := range matches {
			if owned[matches[i].ExternalKey] {
				continue
			}
			ok, err := h.ownedBySession(rid, &matches[i])
			if err != nil {
				return nil, err
			}
			if ok {
				owned[matches[i].ExternalKey] = true
			}
		}
	}
	return owned, nil
}

// nodesWithKeys decodes only the nodes of rid whose externalKey is in the JSON
// array wanted. The rows are closed before the caller issues further queries
// on the same transaction.
func (h *relationalHistory) nodesWithKeys(rid, wanted string) ([]Node, error) {
	rows, err := h.q.QueryContext(h.ctx, `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type='node' AND json_extract(document,'$.externalKey') IN (SELECT value FROM json_each(?))`, h.s.ProjectID, rid, wanted)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Node
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var n Node
		if err := json.Unmarshal([]byte(doc), &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
