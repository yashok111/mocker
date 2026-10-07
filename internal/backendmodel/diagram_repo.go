package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/backendblob"
	"time"
	"uuid"
)

func diagramConflict() error {
	return &FaultError{Status: 409, Code: "backend_diagram_version_conflict", Message: "Diagram changed; reload or fork before saving"}
}
func diagramPinMismatch() error {
	return &FaultError{Status: 409, Code: "backend_diagram_pin_mismatch", Message: "Exact immutable diagram hash does not match"}
}
func diagramQuota() error {
	return &FaultError{Status: 409, Code: "backend_diagram_quota", Message: "Immutable diagram storage quota exceeded"}
}
func readDiagramReceipt(ctx context.Context, q importReader, pid, op, key, digest string) (string, error) {
	var previous, raw string
	err := q.QueryRowContext(ctx, `SELECT request_hash,receipt FROM backend_diagram_receipts WHERE project_id=? AND operation=? AND idempotency_key=?`, pid, op, key).Scan(&previous, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if previous != digest {
		return "", &FaultError{Status: 409, Code: "backend_idempotency_conflict", Message: "Key already used for a different request"}
	}
	return raw, nil
}
func decodeDiagramVersion(raw string) (*DiagramVersion, error) {
	out := new(DiagramVersion)
	if err := json.Unmarshal([]byte(raw), out, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	hash, err := requestDigest(out.Document)
	if err != nil {
		return nil, err
	}
	if hash != out.Pin.ContentHash {
		return nil, diagramPinMismatch()
	}
	hash, err = requestDigest(out.Provenance)
	if err != nil {
		return nil, err
	}
	if hash != out.ProvenanceHash {
		return nil, diagramPinMismatch()
	}
	out.receiptJSON = raw
	return out, nil
}
func loadDiagram(ctx context.Context, q importReader, pid, id string, version int64) (*DiagramVersion, error) {
	if !ValidID(pid) || !ValidID(id) || version <= 0 {
		return nil, notFound()
	}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_diagram_versions_documents WHERE project_id=? AND diagram_id=? AND version=?`, pid, id, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	return decodeDiagramVersion(raw)
}
func (r *Repo) GetDiagram(ctx context.Context, pid string, pin DiagramPin) (*DiagramVersion, error) {
	if err := pin.Validate(); err != nil {
		return nil, err
	}
	out, err := loadDiagram(ctx, r.db.R, pid, pin.ID, pin.Version)
	if err != nil {
		return nil, err
	}
	if out.Pin != pin {
		return nil, diagramPinMismatch()
	}
	return out, nil
}
func diagramHead(ctx context.Context, q importReader, pid, id string) (*DiagramVersion, error) {
	var v int64
	err := q.QueryRowContext(ctx, `SELECT version FROM backend_diagrams WHERE project_id=? AND id=?`, pid, id).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	return loadDiagram(ctx, q, pid, id, v)
}
func advanceDiagramCatalog(ctx context.Context, tx *sql.Tx, pid string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO backend_diagram_catalog(project_id,version) VALUES(?,1) ON CONFLICT(project_id) DO UPDATE SET version=version+1`, pid)
	return err
}
func writeDiagramReceipt(ctx context.Context, tx *sql.Tx, pid, op, key, digest, raw string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO backend_diagram_receipts(project_id,operation,idempotency_key,request_hash,receipt) VALUES(?,?,?,?,?)`, pid, op, key, digest, raw)
	return err
}

type diagramMutation struct {
	pid, id, op, key, digest, reason string
	expected                         int64
	document                         DiagramDocument
	previous                         *DiagramVersion
	graph                            *EffectiveGraphSnapshot
	gaps                             []DiagramGap
}

func (r *Repo) CreateDiagram(ctx context.Context, pid string, in DiagramCreateInput) (*DiagramVersion, error) {
	document, err := normalizeDiagram(in.Document)
	if err != nil {
		return nil, err
	}
	in.Document = document
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	return r.mutateDiagram(ctx, diagramMutation{pid: pid, op: "create", key: in.IdempotencyKey, digest: digest, document: document})
}
func (r *Repo) SaveDiagram(ctx context.Context, pid, id string, in DiagramSaveInput) (*DiagramVersion, error) {
	document, err := normalizeDiagram(in.Document)
	if err != nil {
		return nil, err
	}
	in.Document = document
	digest, err := requestDigest(struct {
		ID    string
		Input DiagramSaveInput
	}{ID: id, Input: in})
	if err != nil {
		return nil, err
	}
	return r.mutateDiagram(ctx, diagramMutation{pid: pid, id: id, op: "save", key: in.IdempotencyKey, digest: digest, expected: in.ExpectedVersion, document: document})
}
func (r *Repo) ForkDiagram(ctx context.Context, pid string, in DiagramForkInput) (*DiagramVersion, error) {
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	if err := in.Source.Validate(); err != nil {
		return nil, err
	}
	if err := validateDiagramTarget(in.Target); err != nil {
		return nil, err
	}
	if !validAPIText(in.Reason, 1, 4096) {
		return nil, invalid("fork", "Architecture fork requires a reason and no dependency pin")
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	raw, err := readDiagramReceipt(ctx, r.db.R, pid, "fork", in.IdempotencyKey, digest)
	if err != nil {
		return nil, err
	}
	if raw != "" {
		return decodeDiagramVersion(raw)
	}
	previous, err := r.GetDiagram(ctx, pid, in.Source)
	if err != nil {
		return nil, err
	}
	doc, err := normalizeDiagram(previous.Document)
	if err != nil {
		return nil, err
	}
	if doc.Interactions == nil && doc.BusinessMap == nil && in.Architecture != nil {
		return nil, invalid("architecture", "Architecture document has no dependency")
	}
	if doc.Interactions != nil {
		a, _ := requestDigest(doc.Target)
		b, _ := requestDigest(in.Target)
		if a != b && doc.Interactions.Architecture != nil && in.Architecture == nil {
			return nil, invalid("architecture", "New-target fork requires an explicit architecture pin")
		}
		if in.Architecture != nil {
			doc.Interactions.Architecture = in.Architecture
		}
	}
	if doc.BusinessMap != nil {
		a, _ := requestDigest(doc.Target)
		b, _ := requestDigest(in.Target)
		if a != b && doc.BusinessMap.Architecture != nil && in.Architecture == nil {
			return nil, invalid("architecture", "New-target fork requires an explicit architecture pin")
		}
		if in.Architecture != nil {
			doc.BusinessMap.Architecture = in.Architecture
		}
	}
	doc.Target = in.Target
	return r.mutateDiagram(ctx, diagramMutation{pid: pid, op: "fork", key: in.IdempotencyKey, digest: digest, document: doc, previous: previous, reason: in.Reason})
}
func (r *Repo) mutateDiagram(ctx context.Context, m diagramMutation) (*DiagramVersion, error) {
	if !ValidID(m.pid) {
		return nil, notFound()
	}
	if err := validateKey(m.key); err != nil {
		return nil, err
	}
	raw, err := readDiagramReceipt(ctx, r.db.R, m.pid, m.op, m.key, m.digest)
	if err != nil {
		return nil, err
	}
	if raw != "" {
		return decodeDiagramVersion(raw)
	}
	if err = m.document.Validate(); err != nil {
		return nil, err
	}
	if m.op == "save" {
		if m.expected <= 0 {
			return nil, invalid("expectedVersion", "Use a positive int64")
		}
		m.previous, err = diagramHead(ctx, r.db.R, m.pid, m.id)
		if err != nil {
			return nil, err
		}
		if err = validateDiagramSave(m.previous, m.document); err != nil {
			return nil, err
		}
	}
	m.graph, err = r.ResolveEffectiveGraph(ctx, m.pid, m.document.Target)
	if err != nil {
		return nil, err
	}
	m.gaps, err = resolveDiagramEvidence(ctx, m.graph, m.document, m.previous)
	if err != nil {
		return nil, err
	}
	dependencyGaps, err := r.resolveInteractionArchitecture(ctx, m.pid, m.graph, m.document, m.previous)
	if err != nil {
		return nil, err
	}
	m.gaps = append(m.gaps, dependencyGaps...)
	businessGaps, err := r.resolveBusinessMapArchitecture(ctx, m.pid, m.graph, m.document, m.previous)
	if err != nil {
		return nil, err
	}
	m.gaps = append(m.gaps, businessGaps...)
	var out *DiagramVersion
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		out, err = r.writeDiagramMutation(ctx, tx, m)
		return err

	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func validateDiagramSave(previous *DiagramVersion, d DiagramDocument) error {
	a, _ := requestDigest(previous.Document.Target)
	b, _ := requestDigest(d.Target)
	if previous.Document.Kind != d.Kind || a != b {
		return invalid("document", "Diagram kind and target are immutable; use fork")
	}
	if d.BusinessMap != nil && !businessMapDependencyEqual(previous.Document.BusinessMap, d.BusinessMap) {
		return invalid("architecture", "Architecture dependency is immutable; use fork")
	}
	if d.Interactions != nil && !interactionDependencyEqual(previous.Document.Interactions, d.Interactions) {
		return invalid("architecture", "Architecture dependency is immutable; use fork")
	}
	return nil
}

// diagramRowArrays are the payload arrays whose members are semantic rows
// (diagramSemanticRows: architecture and business_map elements/links,
// lifecycle states/transitions/rules, interaction participants/steps/
// branches/order). Every kind stores them at $.document.payload.<array>.
const diagramRowArrays = `'elements','links','states','transitions','rules','participants','steps','branches','order'`

// rejectRetiredDiagramIDs refuses a save that reintroduces a row ID an older
// version used and the head no longer has.
//
// Review 2026-10-06, F106: it decoded every stored version of the diagram
// (strict decode, Validate, a re-marshal for the size check, two canonical
// digests) inside the writer on every content-changing save, up to 256 MiB of
// JSON. Only IDs the head does not already have can be a reuse, so a save
// that adds none skips history entirely; otherwise SQLite matches the new IDs
// against the row arrays of the stored versions by JSON path, without
// decoding them in Go.
func rejectRetiredDiagramIDs(ctx context.Context, tx *sql.Tx, pid, id string, current *DiagramVersion, d DiagramDocument) error {
	existing := diagramSemanticRows(current.Document)
	fresh := []string{}
	for rowID := range diagramSemanticRows(d) {
		if _, active := existing[rowID]; !active {
			fresh = append(fresh, rowID)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	wanted, err := json.Marshal(fresh)
	if err != nil {
		return err
	}
	var reused bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM backend_diagram_versions_documents v, json_each(v.document,'$.document.payload') a, json_each(a.value) r
WHERE v.project_id=? AND v.diagram_id=? AND a.type='array' AND a.key IN (`+diagramRowArrays+`)
AND json_extract(r.value,'$.id') IN (SELECT value FROM json_each(?)))`, pid, id, string(wanted)).Scan(&reused)
	if err != nil {
		return err
	}
	if reused {
		return invalid("id", "Retired semantic IDs cannot be reused")
	}
	return nil
}

func persistDiagramVersion(ctx context.Context, tx *sql.Tx, m diagramMutation, out *DiagramVersion) error {
	id, version, hash := out.Pin.ID, out.Pin.Version, out.Pin.ContentHash
	bytes, err := json.Marshal(out)
	if err != nil {
		return err
	}
	raw := string(bytes)
	var documents, versions int
	var total int64
	err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM backend_diagrams WHERE project_id=?),(SELECT count(*) FROM backend_diagram_versions_documents WHERE project_id=? AND diagram_id=?),(SELECT coalesce(sum(length(CAST(document AS BLOB))),0) FROM backend_diagram_versions_documents WHERE project_id=?)`, m.pid, m.pid, id, m.pid).Scan(&documents, &versions, &total)
	if err != nil {
		return err
	}
	if (m.op != "save" && documents >= 1000) || versions >= 1000 || total+int64(len(bytes)) > 256<<20 {
		return diagramQuota()
	}
	if m.op != "save" {
		target, err := json.Marshal(m.document.Target)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO backend_diagrams(project_id,id,kind,version,target_json) VALUES(?,?,?,1,?)`, m.pid, id, m.document.Kind, string(target)); err != nil {
			return err
		}
	}
	provenance, err := json.Marshal(out.Provenance)
	if err != nil {
		return err
	}
	_, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_diagram_versions(project_id,diagram_id,version,content_hash,target_hash,document,author,created_at,provenance,provenance_hash) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.pid, id, version, hash, out.TargetHash, raw, out.Author, out.CreatedAt, string(provenance), out.ProvenanceHash)
	if err != nil {
		return err
	}
	if m.op == "save" {
		if _, err = tx.ExecContext(ctx, `UPDATE backend_diagrams SET version=? WHERE project_id=? AND id=?`, version, m.pid, id); err != nil {
			return err
		}
	}
	if err = advanceDiagramCatalog(ctx, tx, m.pid); err != nil {
		return err
	}
	if err = writeDiagramReceipt(ctx, tx, m.pid, m.op, m.key, m.digest, raw); err != nil {
		return err
	}

	out.receiptJSON = raw
	return nil
}

func (r *Repo) writeDiagramMutation(ctx context.Context, tx *sql.Tx, m diagramMutation) (*DiagramVersion, error) {
	var out *DiagramVersion

	raw, err := readDiagramReceipt(ctx, tx, m.pid, m.op, m.key, m.digest)
	if err != nil {
		return nil, err
	}
	if raw != "" {
		return decodeDiagramVersion(raw)
	}
	graph, err := resolveEffectiveGraph(ctx, tx, m.pid, m.document.Target)
	if err != nil {
		return nil, err
	}
	if graph.Pins.TargetHash != m.graph.Pins.TargetHash {
		return nil, diagramPinMismatch()
	}
	hash, err := requestDigest(m.document)
	if err != nil {
		return nil, err
	}
	if m.op == "save" {
		current, err := diagramHead(ctx, tx, m.pid, m.id)
		if err != nil {
			return nil, err
		}
		if current.Pin.Version != m.expected {
			return nil, diagramConflict()
		}
		if err = validateDiagramSave(current, m.document); err != nil {
			return nil, err
		}
		if current.Pin.ContentHash == hash {
			return current, writeDiagramReceipt(ctx, tx, m.pid, m.op, m.key, m.digest, current.receiptJSON)
		}
		if err = rejectRetiredDiagramIDs(ctx, tx, m.pid, m.id, current, m.document); err != nil {
			return nil, err
		}
		m.previous = current
	}
	version := int64(1)
	id := uuid.NewV7().String()
	if m.op == "save" {
		version = m.expected + 1
		id = m.id
	}
	out = &DiagramVersion{Pin: DiagramPin{ID: id, Version: version, ContentHash: hash}, ProjectID: m.pid, Document: m.document, TargetHash: graph.Pins.TargetHash, Author: diagramActor(ctx), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Gaps: m.gaps}
	diagramProvenance(out, m.previous, m.op, m.reason)
	return out, persistDiagramVersion(ctx, tx, m, out)
}
