// Package specs owns the specs / operations / operation_responses tables:
// persisting an imported OpenAPI document, deduplicating repeat imports of
// the same bytes, and serving the rows the runtime route table and the
// admin "Specs" screen are built from.
//
// DESIGN §7's import invariant — "импорт никогда не падает" — is enforced by
// [openapi.Load], not here: Import fails only for input Load itself refuses
// ([ErrNotADocument], [ErrUnsupportedFormat]) or for a document over the
// configured size limit ([ErrTooLarge]). A malformed individual operation is
// never this package's problem to detect; it is recorded by the indexer (a
// later phase, see [Repo.ReplaceOperations]'s doc comment) as
// operations.parse_error, and this package's job is only to store that
// column and surface it again, never to fail an import because of it.
//
// The whole import — spec row plus its (currently empty) operation rows —
// is one [store.DB.Write] transaction, so a reader can never observe a spec
// row with no operations rows for reasons other than "the indexer has not
// run yet" (DESIGN §7: raw+normalized are stored together, atomically).
package specs

import (
	"errors"
	"sync"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/store"
)

// Spec is one row of the specs table (DESIGN §13).
type Spec struct {
	ID      int64
	Name    string
	Version string

	Format    string // oas31 | oas30 (never swagger2: Load refuses it, see ErrUnsupportedFormat)
	Source    string // upload | bundle | url
	SourceRef string
	BasePath  string // a hint only; DESIGN §7 step 3 glues it to a route exactly once, in the router package
	Hash      string // sha256 of the raw document, hex-encoded; the dedup key

	CreatedAt time.Time
	CreatedBy *int64
}

// Operation is one row of the operations table (DESIGN §13). Path is stored
// WITHOUT the spec's base path (DESIGN §7 step 3) — the base path is glued
// on exactly once, by [router.Build], never here and never twice.
type Operation struct {
	ID     int64
	SpecID int64

	Method        string
	Path          string
	CanonicalPath string

	// OperationID is the OpenAPI operationId, a label for humans. Summary and
	// Tag are likewise display-only. None of the three is ever a lookup key —
	// paths change across reimports, ID does not.
	OperationID *string
	Summary     *string
	Tag         *string

	SourceOrder int64
	Pointer     string  // JSON pointer into the spec document this operation was read from
	ParseError  *string // non-nil means this operation is degraded, not that the import failed
}

// Response is one row of the operation_responses table (DESIGN §13): one
// response variant for one operation, keyed by (OperationID, Selector).
type Response struct {
	ID          int64
	OperationID int64

	Selector   string // "200" | "2XX" | "default", as it appeared in the spec
	HTTPStatus int    // what is actually sent: 2XX -> 200, default -> 200
	IsDefault  bool

	MediaType    *string // per status, not per operation
	StatusOrigin string  // numeric | 2XX | default | fallback, for the admin UI
	SchemaPtr    *string
}

// Sentinel errors returned by this package's own methods. Each is declared
// with errors.New so it is a real, matchable value — a bare "var X error"
// declaration is nil and errors.Is would never match it.
var (
	// ErrNotFound is returned when a lookup finds no matching row.
	ErrNotFound = errors.New("specs: not found")
	// ErrDuplicate is returned by Import when Document's raw hash already
	// matches a stored spec (DESIGN §7: "дедупликация по хешу raw"). The
	// pre-existing spec is still returned via ImportResult.Spec.
	ErrDuplicate = errors.New("specs: duplicate import")
	// ErrAttached is returned by Delete when at least one workspace still
	// references the spec. It is a bare sentinel and cannot carry which
	// workspaces — call AttachedWorkspaces for that.
	ErrAttached = errors.New("specs: spec is attached to a workspace")
	// ErrTooLarge is returned by Import for a document over cfg.MaxBody.
	ErrTooLarge = errors.New("specs: document too large")
	// ErrTooManyOperations is returned by Import when Index found more
	// operations in the document than maxIndexedOperations allows. Without
	// this ceiling, a single document inside cfg.MaxBody but packed with
	// trivial path entries can still index into the hundreds of thousands of
	// operations rows; inserting that many holds store.DB.W's one writer
	// connection for tens of seconds, blocking every other write in the
	// process (session login/logout, workspace edits, spec delete) for the
	// duration (finding 4, P1a round-1 review).
	ErrTooManyOperations = errors.New("specs: too many operations")
	// ErrStaleGeneration is returned by [Repo.Rederive] when another writer
	// changed specID's newest resource_suggestions generation between the
	// pre-read (taken before derivation, outside any transaction) and the
	// write transaction's own re-read of it (decisions.md §D4.4, §D13.1) —
	// the same fencing shape internal/resources' own fenceConfirmTx keeps,
	// chosen over letting UNIQUE (spec_id, gen, route_family) fail because
	// a concurrent call minting a HIGHER generation number than either
	// read saw is a case that constraint cannot see at all.
	ErrStaleGeneration = errors.New("specs: suggestion generation changed concurrently")
)

// ErrNotADocument and ErrUnsupportedFormat are deliberate aliases of
// [openapi]'s sentinels, not new values: Import wraps openapi's error with
// %w rather than replacing it, so errors.Is(err, specs.ErrNotADocument) and
// errors.Is(err, specs.ErrUnsupportedFormat) both still match through the
// wrap, exactly as if the caller had checked against openapi's own sentinel.
var (
	ErrNotADocument      = openapi.ErrNotADocument
	ErrUnsupportedFormat = openapi.ErrUnsupportedFormat
)

// Repo is the specs/operations/operation_responses tables' data-access
// layer.
type Repo struct {
	db  *store.DB
	cfg *config.Config

	// reportMu guards reportCache, [Repo.Report]'s per-spec memo (see that
	// method's doc comment for why caching it is safe).
	reportMu    sync.Mutex
	reportCache map[int64]*openapi.Report
}

// NewRepo builds a Repo over db, using cfg for the import size limit.
func NewRepo(db *store.DB, cfg *config.Config) *Repo {
	return &Repo{db: db, cfg: cfg, reportCache: make(map[int64]*openapi.Report)}
}
