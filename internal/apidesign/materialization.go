package apidesign

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
)

// PrepareMaterializationTx uses the save pipeline without importing a spec or
// projecting a mock. The caller supplies a read-only transaction during preview.
func (r *Repo) PrepareMaterializationTx(ctx context.Context, tx *sql.Tx, id, expected int64, document string) (string, error) {
	root, err := decodeDocument(document)
	if err != nil {
		return "", err
	}
	operations, err := authoredOperations(root)
	if err != nil {
		return "", err
	}
	for _, operation := range operations {
		if _, exists := operation.key(); !exists {
			return "", invalidField(operation.keyPointer(), "Materialization requires an explicit stable operation key")
		}
	}
	previous := ""
	if id != 0 {
		d, err := getDesign(ctx, tx, id)
		if err != nil {
			return "", err
		}
		if err := checkVersion(d, expected); err != nil {
			return "", err
		}
		revision, err := getRevision(ctx, tx, id, d.DraftRevisionID)
		if err != nil {
			return "", err
		}
		previous = revision.Document
	}
	keyed, err := withOperationKeys(document, previous, 0)
	if err != nil {
		return "", err
	}
	p, err := r.prepare(keyed)
	if err != nil {
		return "", err
	}
	return p.document, nil
}

func (r *Repo) DraftTx(ctx context.Context, tx *sql.Tx, id int64) (Design, Revision, error) {
	d, err := getDesign(ctx, tx, id)
	if err != nil {
		return d, Revision{}, err
	}
	v, err := r.VerifiedRevisionTx(ctx, tx, id, d.DraftRevisionID)
	return d, v, err
}

// VerifiedRevisionTx validates the actual immutable bytes, not only metadata.
func (r *Repo) VerifiedRevisionTx(ctx context.Context, tx *sql.Tx, id, revisionID int64) (Revision, error) {
	var raw, stored string
	if err := tx.QueryRowContext(ctx, `SELECT document,hash FROM api_design_revisions WHERE design_id=? AND id=?`, id, revisionID).Scan(&raw, &stored); err != nil {
		return Revision{}, notFound(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) != stored {
		return Revision{}, invalidField("/contentHash", "Stored API bytes differ from digest")
	}
	return getRevision(ctx, tx, id, revisionID)
}

func (r *Repo) ArtifactSnapshotTx(ctx context.Context, tx *sql.Tx, id, revisionID int64) (*ArtifactSnapshot, error) {
	v, err := r.VerifiedRevisionTx(ctx, tx, id, revisionID)
	if err != nil {
		return nil, err
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT document FROM api_design_revisions WHERE design_id=? AND id=?`, id, revisionID).Scan(&raw); err != nil {
		return nil, notFound(err)
	}
	d, err := getDesign(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return &ArtifactSnapshot{DesignID: id, RevisionID: revisionID, Version: v.Version, DesignName: d.Name, ContentHash: v.Hash, Document: raw, IdentityDocument: v.Document}, nil
}
