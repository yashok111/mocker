package backendmodel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/backendblob"

	"github.com/yashok111/mocker/internal/store"
)

type Repo struct {
	db                *store.DB
	architectureReads architectureReadCache
	importIdentities  importIdentityCache
}

func NewRepo(db *store.DB) *Repo { return &Repo{db: db} }

func (r *Repo) Create(ctx context.Context, in CreateInput) (*Project, error) {
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	in.Name = name
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	return r.mutate(ctx, "create", in.IdempotencyKey, in, func(tx *sql.Tx) (*Project, error) {
		now := time.Now().UTC()
		p := &Project{ID: uuid.NewV7().String(), Name: name, Version: 1, CurrentRevisionID: uuid.NewV7().String(), Repositories: []Repository{}, Capabilities: Features(), CreatedAt: now, UpdatedAt: now}
		rev := initialRevision(p, now)
		doc, err := json.Marshal(rev)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_projects (id,name,version,current_revision_id,created_at,updated_at) VALUES (?,?,?,?,?,?)`, p.ID, p.Name, p.Version, p.CurrentRevisionID, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return nil, err
		}
		_, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_revisions (id,project_id,document) VALUES (?,?,?)`, rev.ID, p.ID, string(doc))
		return p, err
	})
}

func initialRevision(p *Project, now time.Time) Revision {
	coverage := Coverage{Status: "partial", Gaps: []string{"Source inventory has not been imported; total object count is unknown."}}
	// This pre-import semantic document deliberately excludes identity and time.
	semantic := struct {
		SchemaVersion     string        `json:"schemaVersion"`
		Nodes             []string      `json:"nodes"`
		Edges             []string      `json:"edges"`
		Evidence          []string      `json:"evidence"`
		SourceSnapshotIDs []string      `json:"sourceSnapshotIds"`
		ArtifactPins      []ArtifactPin `json:"artifactPins"`
		Coverage          Coverage      `json:"coverage"`
	}{SchemaVersion: SchemaVersion, Coverage: coverage}
	doc, _ := json.Marshal(semantic)
	return Revision{ID: p.CurrentRevisionID, ProjectID: p.ID, SchemaVersion: SchemaVersion,
		SemanticHash: hashBytes(doc), SourceSnapshotIDs: []string{}, ArtifactPins: []ArtifactPin{},
		Coverage: coverage, Author: "system", Summary: "Initial empty model; source inventory pending", CreatedAt: now}
}

func (r *Repo) Apply(ctx context.Context, id string, in CommandsInput) (*Project, error) {
	return r.ApplyAs(ctx, id, in, "system")
}

// ApplyAs attributes annotation edits to the authenticated caller, outside the request digest.
func (r *Repo) ApplyAs(ctx context.Context, id string, in CommandsInput, actor string) (*Project, error) {
	if !ValidID(id) {
		return nil, notFound()
	}
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	if in.ExpectedVersion <= 0 {
		return nil, invalid("expectedVersion", "expectedVersion must be positive")
	}
	commands, err := normalizeProjectCommands(in.Commands)
	if err != nil {
		return nil, err
	}
	in.Commands = commands
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxProjectCommandBytes {
		return nil, invalid("body", "Project commands exceed 1 MiB")
	}
	return r.mutate(ctx, "project:"+id, in.IdempotencyKey, in, func(tx *sql.Tx) (*Project, error) {
		p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, id))
		if err != nil {
			return nil, err
		}
		if p.Version != in.ExpectedVersion {
			return nil, &FaultError{Status: 409, Code: "backend_version_conflict", Message: "Project changed; read and reconcile before retrying", CurrentVersion: p.Version, Details: map[string]any{"conflictingIds": []string{id}}}
		}
		if p.Version == math.MaxInt64 {
			return nil, &FaultError{Status: 409, Code: "backend_version_exhausted", Message: "Project version cannot be incremented", CurrentVersion: p.Version}
		}
		now := time.Now().UTC()
		annotationsChanged := false
		for _, c := range commands {
			if c.Type == "set_start_view" || c.Type == "clear_start_view" {
				p.StartView = c.StartView
				if err := writeProjectStartView(ctx, tx, p); err != nil {
					return nil, err
				}
				continue
			}
			if c.Type == "rename_project" {
				p.Name = c.Name
				continue
			}
			if err := applyAnnotationCommand(ctx, tx, p, c, actor, now); err != nil {
				return nil, err
			}
			annotationsChanged = true
		}
		if annotationsChanged {
			if err := checkAnnotationQuota(ctx, tx, id); err != nil {
				return nil, err
			}
		}
		p.Version++
		p.UpdatedAt = now
		_, err = tx.ExecContext(ctx, `UPDATE backend_projects SET name=?,version=?,updated_at=? WHERE id=?`, p.Name, p.Version, now.Format(time.RFC3339Nano), id)
		return p, err
	})
}

// mutate reads receipts before CAS, then atomically writes data and the replay result.
func (r *Repo) mutate(ctx context.Context, scope, key string, request any, apply func(*sql.Tx) (*Project, error)) (*Project, error) {
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	requestHash := hashBytes(requestJSON)
	var result *Project
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		var previousHash, response string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&previousHash, &response)
		if err == nil {
			if previousHash != requestHash {
				return &FaultError{Status: 409, Code: "backend_idempotency_conflict", Message: "Idempotency key was already used with a different request"}
			}
			return json.Unmarshal([]byte(response), &result)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result, err = apply(tx)
		if err != nil {
			return err
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_command_receipts (scope,key,request_hash,response) VALUES (?,?,?,?)`, scope, key, requestHash, string(data))
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

const projectColumns = "id,name,version,current_revision_id,created_at,updated_at,(SELECT COALESCE(json_group_array(json_object('id',id,'logicalName',logical_name)),'[]') FROM backend_repositories WHERE project_id=backend_projects.id),start_view_json"

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	p := &Project{Repositories: []Repository{}, Capabilities: Features()}
	var created, updated, repositories string
	var start sql.NullString
	err := row.Scan(&p.ID, &p.Name, &p.Version, &p.CurrentRevisionID, &created, &updated, &repositories, &start)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(repositories), &p.Repositories); err != nil {
		return nil, err
	}
	if start.Valid {
		if err := json.Unmarshal([]byte(start.String), &p.StartView); err != nil {
			return nil, err
		}
	}
	p.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, fmt.Errorf("read project creation time: %w", err)
	}
	p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return p, err
}

func (r *Repo) Get(ctx context.Context, id string) (*Project, error) {
	if !ValidID(id) {
		return nil, notFound()
	}
	return scanProject(r.db.R.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, id))
}

func (r *Repo) Revision(ctx context.Context, projectID, revisionID string) (*Revision, error) {
	if !ValidID(projectID) || !ValidID(revisionID) {
		return nil, notFound()
	}
	var doc string
	err := r.db.R.QueryRowContext(ctx, `SELECT document FROM backend_revisions_documents WHERE project_id=? AND id=?`, projectID, revisionID).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	var rev Revision
	if err := json.Unmarshal([]byte(doc), &rev); err != nil {
		return nil, err
	}
	return &rev, nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
