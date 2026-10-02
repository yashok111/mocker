package apidesign

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/schemamodel"
)

type ArtifactSnapshot struct {
	DesignID         int64
	RevisionID       int64
	DesignName       string
	ContentHash      string
	Document         string
	IdentityDocument string
}
type ArtifactSelector struct {
	ObjectKey   string `json:"objectKey,omitempty"`
	JSONPointer string `json:"jsonPointer,omitempty"`
}
type ArtifactObject struct {
	Label           string
	ObjectHash      string
	Pointer         string
	ConsumerPointer string
	Document        string
}

// ArtifactSnapshot reads raw immutable bytes and metadata from one SQL snapshot.
func (r *Repo) ArtifactSnapshot(ctx context.Context, designID, revisionID int64) (*ArtifactSnapshot, error) {
	if designID <= 0 || revisionID <= 0 {
		return nil, invalidField("", "Укажите положительные ID API и ревизии")
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out := &ArtifactSnapshot{DesignID: designID, RevisionID: revisionID}
	err = tx.QueryRowContext(ctx, `SELECT d.name,r.hash FROM api_designs d JOIN api_design_revisions r ON r.design_id=d.id WHERE d.id=? AND r.id=?`, designID, revisionID).Scan(&out.DesignName, &out.ContentHash)
	if err != nil {
		return nil, notFound(err)
	}
	out.Document, err = r.impactRevisionDocument(ctx, tx, designID, revisionID)
	if err != nil {
		return nil, err
	}
	if out.ContentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(out.Document))) {
		return nil, invalidField("/contentHash", "Хеш сохранённого документа не совпадает")
	}
	root, err := impactDocument(out.Document)
	if err != nil {
		return nil, err
	}
	if err = artifactGuard(ctx, root, 0, new(int)); err != nil {
		return nil, err
	}
	out.IdentityDocument, err = withOperationKeys(out.Document, "", designID)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ArtifactDigestTx verifies immutable ownership and stored digest in the caller's
// transaction. Raw-byte verification belongs to ArtifactSnapshot preparation.
func (r *Repo) ArtifactDigestTx(ctx context.Context, tx *sql.Tx, designID, revisionID int64) (string, error) {
	if designID <= 0 || revisionID <= 0 {
		return "", invalidField("", "Укажите положительные ID API и ревизии")
	}
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT r.hash FROM api_design_revisions r JOIN api_designs d ON d.id=r.design_id WHERE d.id=? AND r.id=?`, designID, revisionID).Scan(&digest)
	if err != nil {
		return "", notFound(err)
	}
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		return "", invalidField("/contentHash", "Некорректный хеш сохранённой ревизии")
	}
	return digest, nil
}
func (r *Repo) ArtifactHead(ctx context.Context, designID int64) (int64, error) {
	if designID <= 0 {
		return 0, invalidField("/designId", "Укажите положительный ID API")
	}
	var id int64
	err := r.db.R.QueryRowContext(ctx, `SELECT draft_revision_id FROM api_designs WHERE id=?`, designID).Scan(&id)
	return id, notFound(err)
}

func ResolveArtifactObject(ctx context.Context, snapshot *ArtifactSnapshot, selector ArtifactSelector) (*ArtifactObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, invalidField("", "Укажите snapshot")
	}
	if (selector.ObjectKey == "") == (selector.JSONPointer == "") {
		return nil, invalidField("/selector", "Укажите ровно один селектор")
	}
	root, err := impactDocument(snapshot.Document)
	if err != nil {
		return nil, err
	}
	if err = artifactGuard(ctx, root, 0, new(int)); err != nil {
		return nil, err
	}
	var value any
	out := &ArtifactObject{}
	if selector.JSONPointer != "" {
		admitted, err := schemamodel.IsSchemaPosition(ctx, root, selector.JSONPointer)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, invalidField("/selector/jsonPointer", err.Error())
		}
		if !admitted {
			return nil, invalidField("/selector/jsonPointer", "Указатель не является позицией схемы")
		}
		value, _, err = schemamodel.ResolveAuthoredPointer(root, selector.JSONPointer)
		if err != nil {
			return nil, err
		}
		out.Pointer = selector.JSONPointer
		out.ConsumerPointer = out.Pointer
		out.Label = out.Pointer
	} else {
		if len(selector.ObjectKey) > 200 || strings.TrimSpace(selector.ObjectKey) == "" {
			return nil, invalidField("/selector/objectKey", "Некорректный ключ операции")
		}
		identity, err := withOperationKeys(snapshot.Document, "", snapshot.DesignID)
		if err != nil {
			return nil, err
		}
		projected, err := impactDocument(identity)
		if err != nil {
			return nil, err
		}
		operations, err := authoredOperations(projected)
		if err != nil {
			return nil, err
		}
		for _, op := range operations {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			key, _ := op.key()
			if key != selector.ObjectKey {
				continue
			}
			itemPointer, method, _ := strings.CutLast(op.pointer, "/")
			item, _, err := schemamodel.ResolveAuthoredPointer(root, itemPointer)
			if err != nil {
				return nil, err
			}
			nodes, diagnostics := schemamodel.PathItems(root, item, itemPointer)
			for _, diagnostic := range diagnostics {
				if diagnostic.Code != "path_item_method_conflict" {
					return nil, invalidField(diagnostic.Pointer, diagnostic.Message)
				}
			}
			for _, resolved := range schemamodel.PathItemOperations(nodes) {
				if resolved.Method == method {
					value = resolved.Value
					out.Pointer = resolved.Pointer
					out.ConsumerPointer = op.pointer
					out.Label = strings.ToUpper(method) + " " + unescapeArtifactPath(itemPointer)
					break
				}
			}
			break
		}
		if value == nil {
			return nil, invalidField("/selector/objectKey", "Операция не найдена")
		}
	}
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	out.Document = string(raw)
	envelope := struct {
		Version string `json:"version"`
		Object  any    `json:"object"`
	}{"api-artifact-object-v1", value}
	canonical, err := jsonx.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	out.ObjectHash = fmt.Sprintf("%x", sha256.Sum256(canonical))
	return out, nil
}
func unescapeArtifactPath(pointer string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(pointer, "/paths/"), "~1", "/"), "~0", "~")
}

// Bound all authored data, including opaque examples, before projection/encoding.
func artifactGuard(ctx context.Context, value any, depth int, nodes *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*nodes++
	if depth > 128 || *nodes > 100000 {
		return invalidField("/document", "Превышен предел обхода документа (128 уровней, 100000 узлов)")
	}
	switch node := value.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(node)) {
			if err := artifactGuard(ctx, node[key], depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := artifactGuard(ctx, child, depth+1, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}
