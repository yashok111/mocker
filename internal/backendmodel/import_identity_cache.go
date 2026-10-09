package backendmodel

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

// A source5 Put needs identities, not native bodies, evidence snippets or a
// migration proof graph. Retain at most one immutable index. Account for string
// payloads and map entries; this bounds retention, not process RSS.
const MaxImportIdentityCacheBytes = 32 << 20

type importIdentityCache struct {
	mu                    sync.Mutex
	projectID, revisionID string
	index                 map[string][2]string
}

func (r *Repo) loadImportBaseIdentityIndex(ctx context.Context, q importReader, pid, rid string) (map[string][2]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Recheck visibility under the caller's transaction even on a cache hit.
	var visible int
	if err := q.QueryRowContext(ctx, `SELECT 1 FROM backend_revisions WHERE project_id=? AND id=?`, pid, rid).Scan(&visible); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound()
		}
		return nil, err
	}
	c := &r.importIdentities
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.projectID == pid && c.revisionID == rid && c.index != nil {
		return c.index, nil
	}
	c.projectID, c.revisionID, c.index = "", "", nil
	rows, err := q.QueryContext(ctx, `SELECT record_type,id,json_extract(document,'$.externalKey'),coalesce(json_extract(document,'$.kind'),'') FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? ORDER BY record_type,id`, pid, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	index := map[string][2]string{}
	bytes := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var typ, id, key, kind string
		if err := rows.Scan(&typ, &id, &key, &kind); err != nil {
			return nil, err
		}
		if typ != "node" && typ != "edge" && typ != "evidence" {
			continue
		}
		if typ == "evidence" {
			kind = ""
		}
		address := typ + "\x00" + key
		if _, exists := index[address]; !exists {
			index[address] = [2]string{id, kind}
			bytes += len(address) + len(id) + len(kind) + 96
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bytes <= MaxImportIdentityCacheBytes {
		c.projectID, c.revisionID, c.index = pid, rid, index
	}
	return index, nil
}
