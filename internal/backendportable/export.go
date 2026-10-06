package backendportable

import (
	"context"
	"database/sql"
	"errors"
)

// FreezeExport stores already domain-validated records supplied by the owner
// exporter. Envelope checks here are not a substitute for source/closure validation.
func (s *Staging) FreezeExport(ctx context.Context, m Manifest, chunks [][]byte, key string) (*Session, error) {
	hashes := make([]string, len(chunks))
	for i, b := range chunks {
		if len(b) > MaxChunkBytes {
			return nil, fault(413, "Chunk byte quota")
		}
		hashes[i] = bytesHash(b)
	}
	return s.mutation(ctx, "installation", "export", key, struct {
		Manifest Manifest
		Hashes   []string
	}{m, hashes}, func(tx *sql.Tx) (*Session, error) {
		if err := m.Validate(); err != nil {
			return nil, err
		}
		if len(chunks) != len(m.Chunks) {
			return nil, fault(422, "Missing export chunks")
		}
		out, err := insertSession(ctx, tx, "export", m)
		if err != nil {
			return nil, err
		}
		for i, b := range chunks {
			if err = putChunk(ctx, tx, out.ID, m, i, b); err != nil {
				return nil, err
			}
		}
		out.State = "ready"
		if err = advanceSession(ctx, tx, out); err != nil {
			return nil, err
		}
		return out, nil
	})
}
func (s *Staging) ExportChunk(ctx context.Context, id, manifestHash string, index int) ([]byte, error) {
	if index < 0 || !validHash(manifestHash) {
		return nil, fault(422, "Exact manifest hash and chunk index required")
	}
	var body []byte
	err := s.db.R.QueryRowContext(ctx, `SELECT c.body FROM backend_portable_chunks c JOIN backend_portable_sessions s ON s.id=c.session_id WHERE s.id=? AND s.direction='export' AND s.state='ready' AND s.manifest_hash=? AND c.chunk_index=?`, id, manifestHash, index).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fault(404, "Frozen export chunk not found")
	}
	return body, err
}
