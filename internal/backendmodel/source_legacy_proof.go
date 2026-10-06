package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
)

func sourcePropertyForPointer(p SourceAssertionPayload, path string) *TypedSourcePropertySelector {
	selectors, err := sourceSelectors([]SourceAssertionPayload{p})
	if err != nil {
		return nil
	}
	for _, selector := range selectors {
		members, err := sourcePropertyPath(p, selector)
		if err != nil || len(members) == 0 {
			continue
		}
		parts := make([]string, len(members))
		for i, v := range members {
			parts[i] = escapeRelationalPointer(v)
		}
		prefix := "/" + strings.Join(parts, "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return &selector
		}
	}
	if p.RecordType == "edge" && (path == "/from" || path == "/to") {
		return &TypedSourcePropertySelector{Kind: "edge_endpoints"}
	}
	return nil
}

type legacyProofCache struct {
	documents map[string]jsontext.Value
	hashes    map[string]string
	bytes     int
}

func newLegacyProofCache() *legacyProofCache {
	return &legacyProofCache{documents: map[string]jsontext.Value{}, hashes: map[string]string{}}
}

func (cache *legacyProofCache) raw(ctx context.Context, q importReader, pid, rid, typ, id, eid string) (*LegacyProofRawBasis, error) {
	result := new(LegacyProofRawBasis)
	for _, row := range []struct {
		key    string
		query  string
		args   []any
		target *jsontext.Value
	}{
		{pid + rid + "revision", `SELECT document FROM backend_revisions_documents WHERE project_id=? AND id=?`, []any{pid, rid}, &result.RevisionDocument},
		{pid + rid + "source", `SELECT s.document FROM backend_revision_sources_documents s JOIN backend_revisions_documents r ON r.id=s.revision_id WHERE r.project_id=? AND r.id=?`, []any{pid, rid}, &result.SourceDocument},
		{pid + rid + typ + id, `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type=? AND id=?`, []any{pid, rid, typ, id}, &result.SubjectDocument},
		{pid + rid + "evidence" + eid, `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type='evidence' AND id=? AND subject_id=?`, []any{pid, rid, eid, id}, &result.EvidenceDocument},
	} {
		if raw, ok := cache.documents[row.key]; ok {
			*row.target = raw
			continue
		}
		var document string
		if err := q.QueryRowContext(ctx, row.query, row.args...).Scan(&document); err != nil {
			return nil, err
		}
		cache.bytes += len(document)
		if cache.bytes > MaxRevisionBytes {
			return nil, limitFault("Legacy proof raw basis exceeds revision limit")
		}
		raw := jsontext.Value(document)
		cache.documents[row.key] = raw
		cache.hashes[row.key] = hashBytes(raw)
		*row.target = raw
	}
	return result, nil
}

func deriveLegacyProofBasis(ctx context.Context, q importReader, pid, rid, typ, id, eid string) (LegacyProofBasis, error) {
	return newLegacyProofCache().derive(ctx, q, pid, rid, typ, id, eid)
}

func (cache *legacyProofCache) derive(ctx context.Context, q importReader, pid, rid, typ, id, eid string) (LegacyProofBasis, error) {
	b := LegacyProofBasis{DocumentVersion: "legacy-proof-basis-v1", SourceSchemaVersion: EventsSchemaVersion, ProjectID: pid, SourceRevisionID: rid, RecordType: typ, RecordID: id, EvidenceID: eid}
	raw, err := cache.raw(ctx, q, pid, rid, typ, id, eid)
	if err != nil {
		return b, err
	}
	var rev Revision
	if err := json.Unmarshal(raw.RevisionDocument, &rev); err != nil {
		return b, err
	}
	if rev.SchemaVersion != EventsSchemaVersion {
		return b, semantic("legacyProofBasis", "Original proof must come from source5")
	}
	b.SourceSemanticHash = rev.SemanticHash
	var evidence Evidence
	if err := json.Unmarshal(raw.EvidenceDocument, &evidence); err != nil {
		return b, err
	}
	var payload SourceAssertionPayload
	var ids []string
	switch typ {
	case "node":
		var n Node
		if err := json.Unmarshal(raw.SubjectDocument, &n); err != nil {
			return b, err
		}
		payload = sourceNodePayload(n)
		ids = n.EvidenceIDs
	case "edge":
		var e Edge
		if err := json.Unmarshal(raw.SubjectDocument, &e); err != nil {
			return b, err
		}
		payload = sourceEdgePayload(e)
		ids = e.EvidenceIDs
	default:
		return b, semantic("legacyProofBasis", "Invalid original subject type")
	}
	if !slices.Contains(ids, eid) || evidence.SubjectID != id {
		return b, semantic("legacyProofBasis", "Original evidence does not belong to subject")
	}
	b.Support = "legacy_record"
	if evidence.PropertyPath != nil {
		if !pointerExists(raw.SubjectDocument, *evidence.PropertyPath) {
			return b, semantic("legacyProofBasis", "Original property pointer is invalid")
		}
		b.Support = "historical_metadata"
		b.Property = sourcePropertyForPointer(payload, *evidence.PropertyPath)
		if b.Property != nil && pointerExists(payload, *evidence.PropertyPath) {
			b.Support = "legacy_semantic"
		} else {
			b.Property = nil
		}
	}
	b.RevisionDocumentHash = cache.hashes[pid+rid+"revision"]
	b.SourceDocumentHash = cache.hashes[pid+rid+"source"]
	b.SubjectDocumentHash = cache.hashes[pid+rid+typ+id]
	b.EvidenceDocumentHash = cache.hashes[pid+rid+"evidence"+eid]
	b.BasisHash, err = sourceDomainHash("backend-legacy-proof-basis-v1", b)
	return b, err
}

func loadLegacyProofBasis(ctx context.Context, q importReader, b LegacyProofBasis) (*LegacyProofRawBasis, error) {
	return newLegacyProofCache().load(ctx, q, b)
}

func (cache *legacyProofCache) load(ctx context.Context, q importReader, b LegacyProofBasis) (*LegacyProofRawBasis, error) {
	expected, err := cache.derive(ctx, q, b.ProjectID, b.SourceRevisionID, b.RecordType, b.RecordID, b.EvidenceID)
	if err != nil {
		return nil, err
	}
	a, err := canonicalJSON(expected)
	if err != nil {
		return nil, err
	}
	stored, err := canonicalJSON(b)
	if err != nil {
		return nil, err
	}
	if string(a) != string(stored) {
		return nil, semantic("legacyProofBasis", "Original proof basis hashes or membership changed")
	}
	return cache.raw(ctx, q, b.ProjectID, b.SourceRevisionID, b.RecordType, b.RecordID, b.EvidenceID)
}
