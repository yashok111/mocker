package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"uuid"
)

func reconciliationFault(code, message string) error {
	return &FaultError{Status: 422, Code: code, Message: message}
}

func validateImportMode(in BeginImportInput) error {
	if in.Mode == "composed" || in.Profile == ComposedProfile {
		return validateComposedMode(in)
	}
	if in.SourceScope != nil || in.ScopeStatus != nil || in.SyncPolicy != "" || in.ChangeManifest != nil {
		return semantic("sourceScope", "Source composition fields require composed mode")
	}
	return validateLegacyImportMode(in)
}

func validateLegacyImportMode(in BeginImportInput) error {
	if in.Mode != "" && in.Mode != "initial" && in.Mode != "reconcile" {
		return semantic("mode", "Mode must be initial or reconcile")
	}
	if err := validateImportProfile(in); err != nil {
		return err
	}
	if in.Mode != "reconcile" {
		if in.RepositoryID != nil || in.GraphScope != nil {
			return reconciliationFault("backend_unsupported_scope", "Initial mode cannot specify reconciliation fields")
		}
		return nil
	}
	if in.RepositoryID == nil || !ValidID(*in.RepositoryID) || in.GraphScope == nil {
		return reconciliationFault("backend_unsupported_scope", "Reconcile requires repositoryId and graphScope")
	}
	g := in.GraphScope
	if g.Profile != selectedProfile(in.Profile) || g.Status != "complete" && g.Status != "partial" || g.Status == "complete" && len(g.Gaps) != 0 || g.Status == "partial" && len(g.Gaps) == 0 {
		return reconciliationFault("backend_unsupported_scope", "Unsupported graph scope or inconsistent gaps")
	}
	for _, gap := range g.Gaps {
		if !nonblank(gap) {
			return reconciliationFault("backend_unsupported_scope", "Scope gaps must be nonblank")
		}
	}
	return nil
}

func requireImportBase(ctx context.Context, q importReader, s *ImportSession) error {
	if s.Mode == "composed" {
		return requireComposedBase(ctx, q, s)
	}
	if s.Mode != "reconcile" {
		return requireEmptyBase(ctx, q, s.ProjectID, s.BaseRevisionID)
	}
	state, err := loadSourceState(ctx, q, s.ProjectID, s.BaseRevisionID)
	if err != nil {
		return err
	}
	primary := primarySource(*state)
	if primary == nil || primary.RepositoryID != s.RepositoryID {
		return reconciliationFault("backend_unsupported_scope", "Base must belong to the selected repository")
	}
	for _, src := range state.Sources {
		if src.RepositoryID != s.RepositoryID || src.Provider.Namespace != primary.Provider.Namespace {
			return reconciliationFault("backend_unsupported_scope", "Multiple source partitions are unsupported")
		}
	}
	var name string
	if err := q.QueryRowContext(ctx, `SELECT logical_name FROM backend_repositories WHERE project_id=? AND id=?`, s.ProjectID, s.RepositoryID).Scan(&name); err != nil {
		return err
	}
	if name != s.Manifest.RepositoryName {
		return reconciliationFault("backend_unsupported_scope", "Repository name must match the selected repository")
	}
	return requireProviderProfile(state, s, primary.Provider)
}

type identityBinding struct{ ID, State string }

func binding(ctx context.Context, q importReader, s *ImportSession, typ, key string) (*identityBinding, error) {
	var b identityBinding
	err := q.QueryRowContext(ctx, `SELECT id,state FROM backend_identity_bindings WHERE project_id=? AND repository_id=? AND provider_namespace=? AND record_type=? AND external_key=?`, s.ProjectID, s.RepositoryID, s.Manifest.Provider.Namespace, typ, key).Scan(&b.ID, &b.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &b, err
}
func identityConflict(message string) error {
	return importConflict("backend_identity_conflict", message, 0)
}
func loadDecisions(ctx context.Context, q importReader, sid string) ([]ImportCommand, error) {
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_import_decisions WHERE session_id=? ORDER BY record_type,external_key`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportCommand{}
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var c ImportCommand
		if err := json.Unmarshal([]byte(doc), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func reserveIdentity(ctx context.Context, tx *sql.Tx, s *ImportSession, c ImportCommand, typ, key string, base *RevisionState) (string, error) {
	decisions, err := loadDecisions(ctx, tx, s.ID)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM backend_import_identities WHERE session_id=? AND record_type=? AND external_key=?`, s.ID, typ, key).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	reserved := err == nil
	if c.Op == "remove" {
		if !reserved {
			b, e := binding(ctx, tx, s, typ, key)
			if e != nil {
				return "", e
			}
			if b != nil {
				id = b.ID
			}
		}
	} else if c.Identity != nil || c.Deletion != nil {
		if s.Mode != "reconcile" {
			return "", reconciliationFault("backend_unsupported_scope", "Identity and deletion decisions require reconcile mode")
		}
		sourceKey, expected := key, ""
		if c.Identity != nil {
			sourceKey = c.Identity.FromExternalKey
			expected = c.Identity.ExpectedID
		} else {
			expected = c.Deletion.ExpectedID
		}
		sourceID, _ := stateIdentity(*base, typ, sourceKey)
		if sourceID == "" || sourceID != expected {
			return "", identityConflict("Decision expectedId must match the active key in the base")
		}
		if reserved && id != sourceID {
			return "", identityConflict("Acknowledged target reservation has a different UUID")
		}
		if c.Identity != nil {
			target, e := binding(ctx, tx, s, typ, key)
			if e != nil {
				return "", e
			}
			if target != nil && (target.ID != sourceID || target.State != "retired") {
				return "", identityConflict("Mapping target must be unused or a retired alias of the same UUID")
			}
			if reserved {
				var source string
				err := tx.QueryRowContext(ctx, `SELECT source_key FROM backend_import_aliases WHERE session_id=? AND record_type=? AND external_key=? AND id=?`, s.ID, typ, key, sourceID).Scan(&source)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return "", err
				}
				if source != sourceKey {
					return "", identityConflict("Mapping must precede target allocation; acknowledged mapping cannot change source")
				}
			}
			var staged string
			err = tx.QueryRowContext(ctx, `SELECT document FROM backend_import_records WHERE session_id=? AND record_type=? AND external_key IN (?,?) LIMIT 1`, s.ID, typ, key, sourceKey).Scan(&staged)
			if err == nil {
				return "", identityConflict("Remove staged source/target before submitting a mapping")
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO backend_import_aliases(session_id,record_type,external_key,source_key,id) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, s.ID, typ, key, sourceKey, sourceID); err != nil {
				return "", err
			}
		} else {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_import_records WHERE session_id=? AND record_type=? AND external_key=?`, s.ID, typ, key).Scan(&n); err != nil {
				return "", err
			}
			if n > 0 {
				return "", identityConflict("Cannot delete and upsert the same record")
			}
		}
		for _, d := range decisions {
			dt, dk, _ := commandAddress(d)
			if dt != typ {
				continue
			}
			if dk == key {
				old, _ := requestDigest(d)
				next, _ := requestDigest(c)
				if old == next {
					continue
				}
				return "", identityConflict("Remove the existing decision before replacing it")
			}
			oldID := ""
			if d.Identity != nil {
				oldID = d.Identity.ExpectedID
				if d.Identity.FromExternalKey == key || d.Identity.ToExternalKey == sourceKey {
					return "", identityConflict("Identity chains and cycles are unsupported")
				}
			} else if d.Deletion != nil {
				oldID = d.Deletion.ExpectedID
			}
			if oldID == sourceID {
				return "", identityConflict("Only one identity or deletion decision is allowed per UUID")
			}
		}
		id = sourceID
	} else {
		mapped := false
		for _, d := range decisions {
			dt, dk, _ := commandAddress(d)
			if dt != typ {
				continue
			}
			if d.Identity != nil && d.Identity.FromExternalKey == key {
				return "", identityConflict("Mapped source cannot also be upserted")
			}
			if dk == key {
				if d.Deletion != nil {
					return "", identityConflict("Deleted subject cannot be upserted")
				}
				mapped = true
			}
		}
		b, err := binding(ctx, tx, s, typ, key)
		if err != nil {
			return "", err
		}
		if b != nil {
			if b.State == "deleted" {
				return "", importConflict("backend_identity_deleted", "Deleted keys cannot be reused", s.Version)
			}
			if b.State != "active" && b.State != "reserved" && !mapped {
				return "", identityConflict("Reserved or retired key needs an explicit mapping")
			}
			if reserved && id != b.ID {
				return "", identityConflict("Registry changed after this UUID was acknowledged")
			}
			id = b.ID
		}
		if s.Mode == "reconcile" {
			_, kind := stateIdentity(*base, typ, key)
			if mapped {
				for _, d := range decisions {
					if d.Identity != nil && d.Identity.RecordType == typ && d.Identity.ToExternalKey == key {
						_, kind = stateIdentity(*base, typ, d.Identity.FromExternalKey)
					}
				}
			}
			if kind != "" && (c.Node != nil && c.Node.Kind != kind || c.Edge != nil && c.Edge.Kind != kind) {
				return "", identityConflict("An existing identity cannot change semantic kind")
			}
		}
	}
	if id == "" {
		id = uuid.NewV7().String()
	}
	if !reserved {
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_identities(session_id,record_type,external_key,id) VALUES(?,?,?,?)`, s.ID, typ, key, id)
		if err != nil {
			return "", err
		}
	}
	return id, nil
}
func stateIdentity(s RevisionState, typ, key string) (string, string) {
	switch typ {
	case "node":
		for _, x := range s.Nodes {
			if x.ExternalKey == key {
				return x.ID, x.Kind
			}
		}
	case "edge":
		for _, x := range s.Edges {
			if x.ExternalKey == key {
				return x.ID, x.Kind
			}
		}
	case "evidence":
		for _, x := range s.Evidence {
			if x.ExternalKey == key {
				return x.ID, ""
			}
		}
	}
	return "", ""
}

func publishBindings(ctx context.Context, tx *sql.Tx, s *ImportSession, g *graphCandidate, rid string) error {
	if g.Composed != nil {
		selected := &graphCandidate{Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}}
		for _, a := range g.Composed.Source.Assertions {
			if a.Owner.RepositoryID != s.RepositoryID || a.Owner.ProviderNamespace != s.Manifest.Provider.Namespace {
				continue
			}
			if a.RecordType == "node" {
				selected.Nodes = append(selected.Nodes, Node{ID: a.RecordID, ExternalKey: a.ExternalKey})
			} else {
				selected.Edges = append(selected.Edges, Edge{ID: a.RecordID, ExternalKey: a.ExternalKey})
			}
		}
		for _, e := range g.Evidence {
			if e.Ownership != nil && e.Ownership.RepositoryID == s.RepositoryID && e.Ownership.ProviderNamespace == s.Manifest.Provider.Namespace {
				selected.Evidence = append(selected.Evidence, e)
			}
		}
		return publishBindings(ctx, tx, s, selected, rid)
	}
	// Retire first so a mapped active alias can be inserted under the unique index.
	if _, err := tx.ExecContext(ctx, `UPDATE backend_identity_bindings SET state='retired',revision_id=? WHERE project_id=? AND repository_id=? AND provider_namespace=? AND state='active'`, rid, s.ProjectID, s.RepositoryID, s.Manifest.Provider.Namespace); err != nil {
		return err
	}
	write := func(typ, key, id, state string) error {
		b, err := binding(ctx, tx, s, typ, key)
		if err != nil {
			return err
		}
		if b != nil && b.ID != id {
			return identityConflict("A key was bound to another UUID")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_identity_bindings(project_id,repository_id,provider_namespace,record_type,external_key,id,state,revision_id) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(project_id,repository_id,provider_namespace,record_type,external_key) DO UPDATE SET state=excluded.state,revision_id=excluded.revision_id`, s.ProjectID, s.RepositoryID, s.Manifest.Provider.Namespace, typ, key, id, state, rid)
		return err
	}
	for _, n := range g.Nodes {
		if err := write("node", n.ExternalKey, n.ID, "active"); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		if err := write("edge", e.ExternalKey, e.ID, "active"); err != nil {
			return err
		}
	}
	for _, e := range g.Evidence {
		if err := write("evidence", e.ExternalKey, e.ID, "active"); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT record_type,external_key,id FROM backend_import_identities WHERE session_id=?`, s.ID)
	if err != nil {
		return err
	}
	allocations := []RecordIdentity{}
	for rows.Next() {
		var a RecordIdentity
		if err := rows.Scan(&a.RecordType, &a.ExternalKey, &a.ID); err != nil {
			rows.Close()
			return err
		}
		allocations = append(allocations, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range allocations {
		b, err := binding(ctx, tx, s, a.RecordType, a.ExternalKey)
		if err != nil {
			return err
		}
		if b == nil {
			if err := write(a.RecordType, a.ExternalKey, a.ID, "reserved"); err != nil {
				return err
			}
		}
	}
	decisions, err := loadDecisions(ctx, tx, s.ID)
	if err != nil {
		return err
	}
	for _, c := range decisions {
		if c.Deletion != nil {
			d := c.Deletion
			if _, err := tx.ExecContext(ctx, `UPDATE backend_identity_bindings SET state='deleted',revision_id=? WHERE project_id=? AND repository_id=? AND provider_namespace=? AND record_type=? AND id=?`, rid, s.ProjectID, s.RepositoryID, s.Manifest.Provider.Namespace, d.RecordType, d.ExpectedID); err != nil {
				return err
			}
		}
	}
	for _, decision := range g.DeletionDecisions {
		if !decision.Resolved {
			continue
		}
		for _, ref := range decision.OldEvidenceRefs {
			if _, err := tx.ExecContext(ctx, `UPDATE backend_identity_bindings SET state='deleted',revision_id=? WHERE project_id=? AND repository_id=? AND provider_namespace=? AND record_type='evidence' AND id=?`, rid, s.ProjectID, s.RepositoryID, s.Manifest.Provider.Namespace, ref.EvidenceID); err != nil {
				return err
			}
		}
	}
	return nil
}
