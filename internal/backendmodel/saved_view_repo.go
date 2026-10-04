package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"uuid"
)

func normalizeSavedState(s SavedViewState) SavedViewState {
	if s.Flow != nil {
		f := *s.Flow
		f.Positions = slices.Clone(f.Positions)
		f.CollapsedGroupIDs = slices.Clone(f.CollapsedGroupIDs)
		s.Flow = &f
		if f.Selection != nil {
			s.Flow.Selection = new(*f.Selection)
		}
	}
	if s.Database != nil {
		d := *s.Database
		d.Positions = slices.Clone(d.Positions)
		d.CollapsedGroupIDs = slices.Clone(d.CollapsedGroupIDs)
		s.Database = &d
		if d.Selection != nil {
			s.Database.Selection = new(*d.Selection)
		}
	}
	var positions []SavedViewPosition
	var groups []string
	if s.Flow != nil {
		positions, groups = s.Flow.Positions, s.Flow.CollapsedGroupIDs
	} else if s.Database != nil {
		positions, groups = s.Database.Positions, s.Database.CollapsedGroupIDs
	}
	slices.SortFunc(positions, func(a, b SavedViewPosition) int { return strings.Compare(a.NodeID, b.NodeID) })
	slices.Sort(groups)
	return s
}
func loadSavedView(ctx context.Context, q importReader, pid, vid string, version int64) (*SavedView, error) {
	if !ValidID(pid) || !ValidID(vid) {
		return nil, notFound()
	}
	if version < 0 {
		return nil, invalid("version", "Use a positive version")
	}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT d.document FROM backend_saved_views v JOIN backend_saved_view_versions d ON d.view_id=v.id AND d.version=CASE WHEN ?=0 THEN v.version ELSE ? END WHERE v.project_id=? AND v.id=?`, version, version, pid, vid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	out := new(SavedView)
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, err
	}
	out.receiptJSON = raw
	return out, nil
}
func (r *Repo) GetSavedView(ctx context.Context, pid, vid string, in GetSavedViewInput) (*SavedView, error) {
	return loadSavedView(ctx, r.db.R, pid, vid, in.Version)
}
func readSavedReceipt(ctx context.Context, q importReader, scope, key, digest string, out *SavedView) (bool, error) {
	found, err := readProposalReceipt(ctx, q, scope, key, digest, out)
	if found && err == nil {
		var raw string
		if err = q.QueryRowContext(ctx, `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&raw); err == nil {
			out.receiptJSON = raw
		}
	}
	return found, err
}
func savedViewQuota() error {
	return &FaultError{Status: 409, Code: "backend_saved_view_quota", Message: "Saved view storage quota exceeded"}
}
func checkSavedViewQuota(ctx context.Context, tx *sql.Tx, pid, vid string, reserved int64) error {
	var views, versions int
	var total int64
	err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM backend_saved_views WHERE project_id=?),(SELECT count(*) FROM backend_saved_view_versions WHERE view_id=?),(SELECT coalesce(sum(length(CAST(d.document AS BLOB))),0) FROM backend_saved_view_versions d JOIN backend_saved_views v ON v.id=d.view_id WHERE v.project_id=?)+(SELECT coalesce(sum(length(CAST(response AS BLOB))),0) FROM backend_command_receipts WHERE scope=? OR scope LIKE ?)`, pid, vid, pid, "saved-view-create:"+pid, "saved-view-save:"+pid+":%").Scan(&views, &versions, &total)
	if err != nil {
		return err
	}
	if vid == "" && views >= MaxSavedViews || vid != "" && versions >= MaxSavedViewVersions || total+reserved > MaxSavedViewBytes {
		return savedViewQuota()
	}
	return nil
}
func writeSavedDocument(ctx context.Context, tx *sql.Tx, out *SavedView, scope, key, digest string) error {
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_saved_view_versions(view_id,version,document) VALUES(?,?,?)`, out.ID, out.Version, string(raw)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,?)`, scope, key, digest, string(raw)); err != nil {
		return err
	}
	out.receiptJSON = string(raw)
	return nil
}
func (r *Repo) CreateSavedView(ctx context.Context, pid string, in CreateSavedViewInput) (*SavedView, error) {
	if !ValidID(pid) {
		return nil, notFound()
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	in.Name = name
	in.State = normalizeSavedState(in.State)
	if err := validateSavedViewState(in.State); err != nil {
		return nil, err
	}
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, invalid("body", err.Error())
	}
	scope := "saved-view-create:" + pid
	out := new(SavedView)
	if found, err := readSavedReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return out, err
	}
	if err := validateSavedViewVersionTarget(in.DocumentVersion, in.Target); err != nil {
		return nil, err
	}
	pins, err := resolveSavedVersionReferences(ctx, r.db.R, pid, in.DocumentVersion, in.Target, in.State)
	if err != nil {
		return nil, err
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if found, err := readSavedReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); err != nil || found {
			return err
		}
		now := time.Now().UTC()
		*out = SavedView{ID: uuid.NewV7().String(), ProjectID: pid, Version: 1, DocumentVersion: selectedSavedViewVersion(in.DocumentVersion), Name: name, Target: in.Target, Pins: *pins, State: in.State, CreatedAt: now, UpdatedAt: now}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := checkSavedViewQuota(ctx, tx, pid, "", int64(2*len(raw))); err != nil {
			return err
		}
		target, err := json.Marshal(in.Target)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_saved_views(id,project_id,version,name,kind,target_json,created_at,updated_at) VALUES(?,?,1,?,?,?,?,?)`, out.ID, pid, name, in.State.kind(), string(target), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		return writeSavedDocument(ctx, tx, out, scope, in.IdempotencyKey, digest)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (r *Repo) SaveSavedView(ctx context.Context, pid, vid string, in SaveSavedViewInput) (*SavedView, error) {
	if !ValidID(pid) || !ValidID(vid) {
		return nil, notFound()
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	in.Name = name
	in.State = normalizeSavedState(in.State)
	if err := validateSavedViewState(in.State); err != nil {
		return nil, err
	}
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, invalid("body", err.Error())
	}
	scope := "saved-view-save:" + pid + ":" + vid
	out := new(SavedView)
	if found, err := readSavedReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return out, err
	}
	if in.ExpectedVersion <= 0 {
		return nil, invalid("expectedVersion", "Use a positive signed int64")
	}
	current, err := loadSavedView(ctx, r.db.R, pid, vid, 0)
	if err != nil {
		return nil, err
	}
	if selectedSavedViewVersion(in.DocumentVersion) != current.DocumentVersion {
		return nil, invalid("documentVersion", "Save tag must match immutable saved view version")
	}
	if current.State.kind() != in.State.kind() {
		return nil, invalid("state.kind", "Saved view kind is immutable")
	}
	if current.Target.Proposal != nil && current.State.Database.Scope != in.State.Database.Scope {
		return nil, invalid("state.scope", "Proposal scope is immutable")
	}
	pins, err := resolveSavedVersionReferences(ctx, r.db.R, pid, current.DocumentVersion, current.Target, in.State)
	if err != nil {
		return nil, err
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if found, err := readSavedReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); err != nil || found {
			return err
		}
		current, err := loadSavedView(ctx, tx, pid, vid, 0)
		if err != nil {
			return err
		}
		if current.Version != in.ExpectedVersion {
			return &FaultError{Status: 409, Code: "backend_version_conflict", Message: "Saved view changed; reload or save as new", CurrentVersion: current.Version}
		}
		if current.Version == math.MaxInt64 {
			return &FaultError{Status: 409, Code: "backend_version_exhausted", Message: "Saved view version cannot be incremented", CurrentVersion: current.Version}
		}
		*out = *current
		out.receiptJSON = ""
		out.Version++
		out.Name = name
		out.State = in.State
		out.Pins = *pins
		out.UpdatedAt = time.Now().UTC()
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := checkSavedViewQuota(ctx, tx, pid, vid, int64(2*len(raw))); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE backend_saved_views SET version=?,name=?,updated_at=? WHERE project_id=? AND id=? AND version=?`, out.Version, name, out.UpdatedAt.Format(time.RFC3339Nano), pid, vid, in.ExpectedVersion)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return &FaultError{Status: 409, Code: "backend_version_conflict", Message: "Saved view changed", CurrentVersion: current.Version}
		}
		return writeSavedDocument(ctx, tx, out, scope, in.IdempotencyKey, digest)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (r *Repo) ListSavedViews(ctx context.Context, pid string, in SavedViewListInput) (*SavedViewPage, error) {
	if _, err := r.Get(ctx, pid); err != nil {
		return nil, err
	}
	if in.Kind != "" && in.Kind != "flow" && in.Kind != "database" {
		return nil, invalid("kind", "Expected flow or database")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, invalid("limit", "Use limit 1–100")
	}
	scope, err := requestDigest(struct {
		Kind  string
		Limit int
	}{in.Kind, limit})
	if err != nil {
		return nil, err
	}
	_, after, err := decodeGraphPage(limit, in.Cursor, "saved-views", pid, scope, true)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.R.QueryContext(ctx, `SELECT id,project_id,version,name,kind,target_json,created_at,updated_at FROM backend_saved_views WHERE project_id=? AND id>? AND (?='' OR kind=?) ORDER BY id LIMIT ?`, pid, after, in.Kind, in.Kind, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &SavedViewPage{Items: []SavedViewSummary{}}
	for rows.Next() {
		var v SavedViewSummary
		var target, created, updated string
		if err := rows.Scan(&v.ID, &v.ProjectID, &v.Version, &v.Name, &v.Kind, &target, &created, &updated); err != nil {
			return nil, err
		}
		if len(out.Items) == limit {
			out.NextCursor = encodeGraphPage("saved-views", pid, scope, after)
			break
		}
		if err := json.Unmarshal([]byte(target), &v.Target); err != nil {
			return nil, err
		}
		v.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		v.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, v)
		after = v.ID
	}
	return out, rows.Err()
}
