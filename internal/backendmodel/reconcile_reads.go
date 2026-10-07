package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
	"strconv"
	"strings"
)

func savePreview(ctx context.Context, tx *sql.Tx, s *ImportSession, p *ImportPreview, g *graphCandidate) error {
	items := []ImportChangeItem{}
	if g.Composed != nil {
		items = append(items, source6ChangeItems(s, g.Composed)...)
	}
	for _, x := range g.SourceChanges {
		items = append(items, ImportChangeItem{RecordType: "source", Source: &x})
	}
	for _, x := range g.IdentityDecisions {
		items = append(items, ImportChangeItem{RecordType: "identity", Identity: &x})
	}
	for _, x := range g.DeletionDecisions {
		items = append(items, ImportChangeItem{RecordType: "deletion", Deletion: &x})
	}
	key := func(x ImportChangeItem) string {
		switch x.RecordType {
		case "assertion_conflict":
			return x.AssertionConflict.RecordType + "/" + x.AssertionConflict.ID + "/" + sourcePropertyKey(x.AssertionConflict.Property)
		case "claim_identity":
			return x.ClaimIdentity.Command.DecisionID
		case "migration":
			return x.Migration.RepositoryID + "/" + x.Migration.Provider.Namespace
		case "source":
			return x.Source.Path
		case "identity":
			return x.Identity.Command.RecordType + "/" + x.Identity.Command.ToExternalKey
		case "deletion":
			return x.Deletion.Command.RecordType + "/" + x.Deletion.Command.ExternalKey
		}
		return ""
	}
	slices.SortFunc(items, func(a, b ImportChangeItem) int {
		if a.RecordType != b.RecordType {
			return strings.Compare(a.RecordType, b.RecordType)
		}
		return strings.Compare(key(a), key(b))
	})
	doc, err := json.Marshal(p)
	if err != nil {
		return err
	}
	details, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_previews(session_id,version,document,details) VALUES(?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET version=excluded.version,document=excluded.document,details=excluded.details`, s.ID, s.Version, string(doc), string(details))
	return err
}
func (r *Repo) ImportChanges(ctx context.Context, pid, sid string, in ImportChangesInput) (*ImportChangesPage, error) {
	session, err := loadSession(ctx, r.db.R, pid, sid)
	if err != nil {
		return nil, err
	}
	if in.PreviewVersion <= 0 {
		return nil, invalid("previewVersion", "A positive saved preview version is required")
	}
	if err := validateImportChangeType(session, in.RecordType); err != nil {
		return nil, err
	}
	var version int64
	var doc, details string
	err = r.db.R.QueryRowContext(ctx, `SELECT version,document,details FROM backend_import_previews WHERE session_id=?`, sid).Scan(&version, &doc, &details)
	if errors.Is(err, sql.ErrNoRows) || err == nil && version != in.PreviewVersion {
		return nil, importConflict("backend_import_preview_conflict", "Saved preview changed or was cleared", version)
	}
	if err != nil {
		return nil, err
	}
	var p ImportPreview
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return nil, err
	}
	scope, err := requestDigest(struct {
		SessionID string
		Version   int64
		Hash      *string
		Type      string
	}{sid, version, p.CandidateHash, in.RecordType})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "import-changes", pid, scope, false)
	if err != nil {
		return nil, err
	}
	offset := 0
	if after != "" {
		offset, err = strconv.Atoi(after)
		if err != nil || offset < 0 {
			return nil, invalid("cursor", "Invalid offset")
		}
	}
	var all []ImportChangeItem
	if err := json.Unmarshal([]byte(details), &all); err != nil {
		return nil, err
	}
	out := &ImportChangesPage{SessionID: sid, PreviewVersion: version, CandidateHash: p.CandidateHash, RecordType: in.RecordType, Items: []ImportChangeItem{}}
	filtered, err := filterImportChanges(ctx, all, in.RecordType)
	if err != nil {
		return nil, err
	}
	if offset > len(filtered) {
		return nil, invalid("cursor", "Invalid offset")
	}
	end := min(offset+limit, len(filtered))
	out.Items = filtered[offset:end]
	if end < len(filtered) {
		out.NextCursor = encodeGraphPage("import-changes", pid, scope, strconv.Itoa(end))
	}
	return out, nil
}

// validateImportChangeType: a composed session's preview also lists claim
// conflicts, claim identities and migrations.
func validateImportChangeType(session *ImportSession, recordType string) error {
	types := []string{"source", "identity", "deletion"}
	if session.Mode == "composed" {
		types = append(types, "assertion_conflict", "claim_identity", "migration")
	}
	if recordType != "" && !slices.Contains(types, recordType) {
		return invalid("recordType", "Select source, identity or deletion")
	}
	return nil
}

func filterImportChanges(ctx context.Context, all []ImportChangeItem, recordType string) ([]ImportChangeItem, error) {
	filtered := []ImportChangeItem{}
	for _, x := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if recordType == "" || recordType == x.RecordType {
			filtered = append(filtered, x)
		}
	}
	return filtered, nil
}

func loadSavedStatus(ctx context.Context, q importReader, out *ImportStatus) error {
	var doc string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_import_previews WHERE session_id=?`, out.Session.ID).Scan(&doc)
	if err == nil {
		out.Preview = new(ImportPreview)
		if err := json.Unmarshal([]byte(doc), out.Preview); err != nil {
			return err
		}
		if out.Preview.ModelSchemaVersion == "" {
			out.Preview.ModelSchemaVersion = modelSchemaVersion(out.Session.Profile)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if out.Session.State == "committed" {
		err := q.QueryRowContext(ctx, `SELECT response FROM backend_command_receipts WHERE scope=? ORDER BY key LIMIT 1`, "import:"+out.Session.ProjectID+":"+out.Session.ID+":commit").Scan(&doc)
		if err != nil {
			return err
		}
		var receipt ImportCommitResult
		if err := json.Unmarshal([]byte(doc), &receipt); err != nil {
			return err
		}
		out.CommittedRevisionID = new(receipt.Revision.ID)
	}
	return nil
}
