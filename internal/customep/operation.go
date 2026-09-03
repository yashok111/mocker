package customep

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// Operation is the OpenAPI operation object's own fields a custom endpoint
// carries for the export (P7a, DESIGN §34.3) — what a mock never needed and
// a contract cannot do without. Stored as one JSON document in
// custom_endpoints.operation (migration 0008), nil for every row that
// predates it. None of it is read by the serving path: `parameters` is
// declared, never validated against a request (that is P2), and
// `deprecated` marks the exported operation, never the route.
type Operation struct {
	Summary     string      `json:"summary,omitempty"`
	Description string      `json:"description,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	OperationID string      `json:"operationId,omitempty"`
	Deprecated  bool        `json:"deprecated,omitempty"`
	Parameters  []Parameter `json:"parameters,omitempty"`
}

// Parameter is one entry of Operation.Parameters: a query, path or header
// parameter with an optional inline schema (the same shape rules a
// response schema has, ValidateSchemaShape). A path parameter must name a
// `{name}` segment of the row's own path; a `{}` segment the operator did
// not declare is derived at export time as a required string.
type Parameter struct {
	Name        string           `json:"name"`
	In          string           `json:"in"`
	Required    bool             `json:"required,omitempty"`
	Description string           `json:"description,omitempty"`
	Schema      jsonx.RawMessage `json:"schema,omitempty"`
}

// The ceilings below bound what one row's operation document may carry.
// They are generous for anything written by hand and small enough that a
// stored row cannot become an export the panel chokes on.
const (
	maxOperationSummary     = 256
	maxOperationDescription = 4096
	maxOperationTags        = 32
	maxOperationTagLen      = 64
	maxOperationParameters  = 64
	maxOperationIDLen       = 128
)

// operationIDRe is what an operationId may look like: the identifier
// alphabet code generators accept without renaming.
var operationIDRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// validParameterIn is the closed set of parameter locations this row can
// declare; "cookie" is deliberately absent — nothing in the product reads
// one and an exported cookie parameter would promise what the mock does
// not do.
var validParameterIn = map[string]bool{"query": true, "path": true, "header": true}

// validateOperation checks row.Operation's shape against row.Path. A nil
// Operation is "no operation fields" and passes.
func validateOperation(row *Row) error {
	op := row.Operation
	if op == nil {
		return nil
	}
	if len(op.Summary) > maxOperationSummary {
		return fmt.Errorf("%w: operation.summary is %d bytes, over the %d-byte limit", ErrInvalidRow, len(op.Summary), maxOperationSummary)
	}
	if len(op.Description) > maxOperationDescription {
		return fmt.Errorf("%w: operation.description is %d bytes, over the %d-byte limit", ErrInvalidRow, len(op.Description), maxOperationDescription)
	}
	if len(op.Tags) > maxOperationTags {
		return fmt.Errorf("%w: operation.tags holds %d entries, over the %d-entry limit", ErrInvalidRow, len(op.Tags), maxOperationTags)
	}
	for i, tag := range op.Tags {
		if strings.TrimSpace(tag) == "" || len(tag) > maxOperationTagLen {
			return fmt.Errorf("%w: operation.tags[%d] must be 1..%d non-blank bytes", ErrInvalidRow, i, maxOperationTagLen)
		}
	}
	if op.OperationID != "" && !operationIDRe.MatchString(op.OperationID) {
		return fmt.Errorf("%w: operation.operationId %q must match [A-Za-z0-9_.-]{1,%d}", ErrInvalidRow, op.OperationID, maxOperationIDLen)
	}
	if len(op.Parameters) > maxOperationParameters {
		return fmt.Errorf("%w: operation.parameters holds %d entries, over the %d-entry limit", ErrInvalidRow, len(op.Parameters), maxOperationParameters)
	}
	pathParams := pathParamNames(row.Path)
	seen := map[string]bool{}
	for i, prm := range op.Parameters {
		if strings.TrimSpace(prm.Name) == "" {
			return fmt.Errorf("%w: operation.parameters[%d].name is empty", ErrInvalidRow, i)
		}
		if !validParameterIn[prm.In] {
			return fmt.Errorf("%w: operation.parameters[%d].in %q is not one of query, path, header", ErrInvalidRow, i, prm.In)
		}
		key := prm.In + " " + prm.Name
		if seen[key] {
			return fmt.Errorf("%w: operation.parameters[%d] repeats %s parameter %q", ErrInvalidRow, i, prm.In, prm.Name)
		}
		seen[key] = true
		if prm.In == "path" && !pathParams[prm.Name] {
			return fmt.Errorf("%w: operation.parameters[%d]: path parameter %q names no {%s} segment of %s", ErrInvalidRow, i, prm.Name, prm.Name, row.Path)
		}
		if err := overrides.ValidateSchemaShape(prm.Schema); err != nil {
			return fmt.Errorf("%w: operation.parameters[%d].schema: %w", ErrInvalidRow, i, err)
		}
	}
	return nil
}

// pathParamNames returns the set of `{name}` segments of a path — the
// names an export derives path parameters for and a declared path
// parameter must be one of. A brace that does not wrap a whole segment is
// not a parameter (router.CanonicalPath's own rule).
func pathParamNames(path string) map[string]bool {
	out := map[string]bool{}
	for _, seg := range strings.Split(path, "/") {
		if len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
			out[seg[1:len(seg)-1]] = true
		}
	}
	return out
}

// PathParamNames is pathParamNames for the export composer
// (internal/design), which derives a required string parameter for every
// `{name}` segment the row's own Operation does not declare.
func PathParamNames(path string) map[string]bool { return pathParamNames(path) }

// RefResolver is what ValidateRefs needs of a bound spec: the one method
// *openapi.Resolver already has. The interface lives here so this package
// needs no import of internal/openapi and a test can hand in a stub.
type RefResolver interface {
	Resolve(pointer string) (any, error)
}

// ErrRefUnresolved wraps every `$ref` a row carries that the bound spec —
// or the absence of one — cannot resolve (P7a D2/D6: "never stored
// dangling"). The message names the pointer and where it sits.
var ErrRefUnresolved = errors.New("customep: schema $ref does not resolve")

// ValidateRefs walks every schema document the row carries — each
// response's schema, reqSchema, each parameter's schema — and resolves
// every `$ref` in them through res. A nil res means no spec is bound, and
// then ANY `$ref` is refused: there is nothing to resolve against. Only a
// local pointer (`#/...`) is ever accepted; an external or relative
// reference is refused by name, because the export can carry neither.
//
// Called by the admin plane on the two writers of a row and on every
// verb that changes which document a row resolves into (a rebind, an
// import, a rollback) — never by this package's own Create/Update, which
// hold no spec. The serving path tolerates what slips past (a hand-run
// UPDATE, a spec deleted underneath): an unresolvable node generates as
// `{}` after one log line at build.
func ValidateRefs(row *Row, res RefResolver) error {
	if row == nil {
		return nil
	}
	for status, v := range row.Responses {
		if err := validateRefsIn(v.Schema, res); err != nil {
			return fmt.Errorf("%w: responses[%s].schema: %w", ErrRefUnresolved, status, err)
		}
	}
	if err := validateRefsIn(row.ReqSchema, res); err != nil {
		return fmt.Errorf("%w: reqSchema: %w", ErrRefUnresolved, err)
	}
	if row.Operation != nil {
		for i, prm := range row.Operation.Parameters {
			if err := validateRefsIn(prm.Schema, res); err != nil {
				return fmt.Errorf("%w: operation.parameters[%d].schema: %w", ErrRefUnresolved, i, err)
			}
		}
	}
	return nil
}

func validateRefsIn(raw jsonx.RawMessage, res RefResolver) error {
	if len(raw) == 0 {
		return nil
	}
	var doc any
	if err := jsonx.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	refs, err := SchemaRefs(doc)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if res == nil {
			return fmt.Errorf("%q: no spec is bound to resolve it against", ref)
		}
		if _, rerr := res.Resolve(ref); rerr != nil {
			return fmt.Errorf("%q: %w", ref, rerr)
		}
	}
	return nil
}

// SchemaRefs lists every `$ref` string in a decoded schema document,
// depth-first, refusing one that is not a string or not a local pointer.
// Exported for the export composer, which resolves the same set.
func SchemaRefs(doc any) ([]string, error) {
	var out []string
	var walk func(node any) error
	walk = func(node any) error {
		switch n := node.(type) {
		case map[string]any:
			if raw, ok := n["$ref"]; ok {
				ref, isString := raw.(string)
				if !isString {
					return errors.New("$ref must be a string")
				}
				if !strings.HasPrefix(ref, "#/") {
					return fmt.Errorf("$ref %q is not a local pointer (#/...)", ref)
				}
				out = append(out, ref)
			}
			for _, child := range n {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range n {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(doc); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrOperationIDTaken wraps the refusal decisions.md D3 asks for: an
// operationId is UNIQUE across the workspace's custom rows AND the bound
// spec's own operations, because the export writes both into one document
// and a code generator keys on the id. Checked inside the write
// transaction, like the two UNIQUE indexes' own conflicts, and never on a
// restore or an import (those rows were checked when they were written,
// and the snapshot is the record of what the operator had).
var ErrOperationIDTaken = errors.New("customep: operationId is already taken")

// operationIDHolderTx names what already holds opID in the workspace's
// scope — "custom endpoint GET /x" or "spec operation GET /y" — or ""
// when nothing does. excludeID is the row being updated (0 on create).
// The custom half reads the operation column with json_extract, which is
// what keeps the id out of a column of its own (D3: one JSON column, and
// nothing queries it — this is the one exception, on a write, once).
func operationIDHolderTx(ctx context.Context, tx *sql.Tx, workspaceID, excludeID int64, opID string) (string, error) {
	var method, path string
	err := tx.QueryRowContext(ctx, `
		SELECT method, path FROM custom_endpoints
		WHERE workspace_id = ? AND id <> ? AND json_extract(operation, '$.operationId') = ?
		LIMIT 1`, workspaceID, excludeID, opID).Scan(&method, &path)
	switch {
	case err == nil:
		return "custom endpoint " + method + " " + path, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("look up operationId %q among custom endpoints: %w", opID, err)
	}
	err = tx.QueryRowContext(ctx, `
		SELECT o.method, o.path FROM operations o
		JOIN workspaces w ON w.spec_id = o.spec_id
		WHERE w.id = ? AND o.operation_id = ?
		LIMIT 1`, workspaceID, opID).Scan(&method, &path)
	switch {
	case err == nil:
		return "spec operation " + method + " " + path, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("look up operationId %q among spec operations: %w", opID, err)
	}
	return "", nil
}

// refuseTakenOperationIDTx is operationIDHolderTx as the one-line guard
// Create and UpdateExpecting call after normalizeAndValidate.
func refuseTakenOperationIDTx(ctx context.Context, tx *sql.Tx, row *Row, excludeID int64) error {
	if row.Operation == nil || row.Operation.OperationID == "" {
		return nil
	}
	holder, err := operationIDHolderTx(ctx, tx, row.WorkspaceID, excludeID, row.Operation.OperationID)
	if err != nil {
		return err
	}
	if holder != "" {
		return fmt.Errorf("%w: operationId %q is held by %s", ErrOperationIDTaken, row.Operation.OperationID, holder)
	}
	return nil
}
