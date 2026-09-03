// Package checkpoints owns the checkpoints table (0001_init.sql:216-226,
// already created by migration 0001 — this package writes no migration of
// its own) and the workspace-layer undo DESIGN §12 hangs off it: list, get,
// create a manual checkpoint, roll back to one, and reset the workspace
// layer to the spec.
//
// A checkpoint is a point in a workspace's HISTORY, not a layer composed at
// request time. That is the whole difference from internal/scenarios, which
// stores the identical v3 [bundle.Bundle] document (DESIGN §17,
// :1081-1082 — «Этот же формат — тело сценария и config_snap чекпойнта»):
// activating a scenario overlays a snapshot over the workspace's rows at
// serve time and never writes them; rolling back to a checkpoint WRITES the
// snapshot back over those rows and allocates a new revision. One format,
// two consumers, two entirely different effects.
//
// # Two container encodings for one format
//
// checkpoints.config_snap is GZIPPED (C18); scenarios.snapshot is raw. The
// asymmetry is deliberate rather than an oversight: DESIGN §12:770-772
// argues against per-edit checkpoints by counting «полсотни gzip'ов всего
// слоя на единственном писателе БД», so the design already assumes a
// checkpoint's snapshot is compressed — while P2b shipped scenario rows
// uncompressed, and re-encoding those is a migration this slice does not
// take. A checkpoint blob and a scenario blob therefore decode to the same
// document but differ in their first byte (0x1f 0x8b against '{').
//
// # HARD RULE 5, and the writes this package makes
//
// Like internal/overrides, internal/customep and internal/scenarios beside
// it, this package never imports internal/workspaces:
// workspaces.Repo.Update opens its OWN write transaction, and calling it
// from inside another db.Write callback deadlocks the single-connection
// writer pool (internal/customep/repo.go:20-22, internal/scenarios/repo.go:17-26).
// Every write this package makes to the workspaces table — the wholesale
// settings restore and the revision bump — is hand-written SQL scoped to
// the caller's transaction, and the two writes to op_overrides and
// custom_endpoints go through those packages' own tx-scoped
// [overrides.ReplaceAllTx] and [customep.ReplaceAllTx] (C4), never through
// a repo method that would open a second db.Write.
//
// P3b's restore of `resources` and `resource_decisions` follows the same
// rule from the other side: it is this package's OWN tx-scoped SQL, beside
// writeSettingsTx, and internal/resources is imported nowhere here either.
// It is also the one restore in this package that is UPSERT-ONLY and never
// DELETEs, which is not a style choice: entities.resource_id is
// ON DELETE CASCADE, so the DELETE-then-UPSERT shape the three tables above
// use would turn a configuration rollback into silent DATA DESTRUCTION —
// see upsertResourcesTx.
//
// ONE transaction covers the whole restore, and that is a STATED EXCEPTION
// to DESIGN §18:1101 («крупные операции (наполнение, сброс, импорт) режутся
// на транзакции по N строк»), recorded here because it is exactly the kind
// of thing a later reader reports as a bug (C17):
//
//   - The mock plane is unaffected — it reads through the reader pool, and
//     WAL lets readers run concurrently with the writer.
//   - What a long transaction costs is QUEUEING, not lock contention: a
//     competing writer waits for the single database/sql writer connection
//     BEFORE SQLite ever sees a transaction, so busy_timeout (store.go:45)
//     does not bound that wait. A traffic flush simply queues behind the
//     restore.
//   - Events are lost only if a flush's context is cancelled while it
//     queues (traffic/recorder.go:284-289, reached from
//     admin/traffic_handlers.go:178, which flushes on r.Context()) — a
//     pre-existing hazard this widens in likelihood rather than creates.
//   - The expensive half (read, encode, gzip) happens OUTSIDE the
//     transaction anyway (C5).
//   - Chunking would trade atomic undo — the feature's entire point — for a
//     bound the realistic workload is nowhere near.
//
// # The four duplications this package accepts
//
// A shared snapshot builder over internal/scenarios was proposed and
// rejected (C13): the pre-destructive path cannot use that package's fence
// (it fences differently, see [Repo.Create]), its A10 short-circuit lives
// inside the fenced body, and the blast radius reaches internal/admin's
// error mapping. So, each duplicated rather than imported, with the reason:
//
//  1. The SIX-source read (workspaces row, spec row, overrides, endpoints,
//     and — since P3b — resources and resource_decisions) — extraction
//     rejected for the reasons above. The last two are this package's own
//     SQL on the reader pool, the way readSpecRef already reads the specs
//     table, and not two more repositories on [Repo]: entity rows are NOT
//     among the six, and never will be — since P3d they are captured by a
//     SEVENTH, separate read, [captureEntitiesTx], which runs inside the
//     write transaction rather than on the reader pool (D5.1 of the P3d
//     decision document) and lands in data_snap, not config_snap.
//  2. [bumpRevisionTx] — THREE packages already carry a private copy
//     (overrides/repo.go:283, customep/repo.go:212, scenarios/repo.go:589);
//     this is the fourth, and the house style is a package-local tx helper
//     rather than a cross-package export.
//  3. The hand-written SQL against the workspaces table — HARD RULE 5,
//     above.
//  4. The [bundle.OverrideEntry] → [overrides.Row] rebuild, whose
//     unexported twin is mockplane/scenario.go:237-250, itself documented
//     at :215-219 as living there "because it is this package's need".
package checkpoints

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/bundle"
)

// Kind values this slice writes into checkpoints.kind.
//
// All three of DESIGN §12:770-772's triggers now have a producer — «По
// дебаунсу (не чаще раза в N минут), всегда перед разрушительным действием
// и по кнопке» — with [Repo.Auto] (P2d) the last to land, ahead of ROUTER's
// eight labelled admin routes. The column still has no CHECK constraint: it
// never needed one, since [pruneRetentionTx]'s filter is `kind <> 'manual'`
// rather than an enumeration of the machine-made kinds, so KindAuto joined
// the pruned population the moment this const existed — no second edit
// there was needed to cover it (C7, C6).
const (
	// KindManual is the operator's own button: a named point in history.
	// It does NOT bump workspaces.revision (C12).
	KindManual = "manual"
	// KindPreDestructive is written by [Repo.Rollback] and [Repo.Reset], in
	// the SAME transaction as the destruction it protects, so undo is
	// itself undoable.
	KindPreDestructive = "pre-destructive"
	// KindAuto is written by [Repo.Auto], DESIGN's debounce trigger: at
	// most one row per configured window per workspace. Like KindManual it
	// does NOT bump workspaces.revision — a debounce row is history, not a
	// change to anything served, and ROUTER's handlers call it after their
	// own mutation already decided what revision to serve.
	KindAuto = "auto"
)

// maxLabelRunes caps a manual label. Counted with utf8.RuneCountInString
// and NOT len (C14): the UI is Russian, so a byte cap would refuse a label
// at roughly half the characters an operator can see themselves typing.
// Scenario names are only trimmed and checked non-empty
// (scenarios/repo.go:249-251); this is a new rule, not a copy of that one.
const maxLabelRunes = 200

// Summary is one entry of [Repo.List] and what [Repo.Create] returns:
// everything a history screen needs and NOTHING from the snapshot itself
// (§C). A list endpoint returning N BLOBs is a page-load cost that grows
// with the workspace's history — the same call P2b made for scenarios.
type Summary struct {
	ID   int64
	Kind string
	// Label is a user-visible Russian string: the operator's own text for a
	// manual checkpoint, and a server-generated one for a machine-made row
	// (C14). It is never an identifier and never English.
	Label     string
	CreatedAt time.Time
	// CreatedBy is the session user who caused the checkpoint (C15). It is
	// a pointer because the column is nullable (REFERENCES users(id) with
	// no NOT NULL) — every row this build writes carries a user, but a row
	// written by hand does not have to.
	CreatedBy *int64
	// HasData reports whether data_snap IS NOT NULL — never a decompress-
	// and-decode to find out. [Repo.List] derives it from the column
	// itself; [insertCheckpointTx] sets it in the struct literal it
	// returns, the OTHER producer of this value, because Go zero-fills an
	// omitted field silently and a checkpoint just written is the one a
	// screen renders without a round trip through List. It does not
	// distinguish a degraded capture (D5.2's probe or ceiling refused) from
	// a checkpoint written before P3d: both are data_snap IS NULL, and the
	// operator's question — "can I restore data from this point" — is
	// answered identically either way (D5.2's own "What the operator
	// sees").
	HasData bool
}

// Checkpoint is [Repo.Get]'s result: the summary plus the DECODED snapshot,
// for the one caller that actually needs the document — [Repo.Rollback],
// which has to rebuild rows out of it. No admin route returns this shape;
// §C's list route returns [Summary] only.
type Checkpoint struct {
	Summary
	WorkspaceID int64
	Bundle      bundle.Bundle
	// DataBlob is the RAW data_snap column: nil when the column IS NULL,
	// otherwise the gzipped bytes exactly as stored — NOT a decoded
	// [bundle.DataBundle]. [Repo.Get] runs before any transaction and does
	// not know whether the caller's rollback carries restoreData true or
	// false, so an eager decode here would fail even a restoreData:false
	// rollback on an unreadable blob, which D7 of the P3d decision document
	// promises answers 200. Decoding — gunzip, [bundle.DecodeData],
	// [bundle.ValidateData] — happens in [Repo.rollbackTx], on the
	// restoreData:true path only.
	DataBlob []byte
}

// Outcome is what [Repo.Rollback] and [Repo.Reset] return — the two fields
// §C's response bodies carry, plus the one signal that has nowhere else to
// travel.
//
// Changed exists because [overrides.ReplaceAllTx] and
// [customep.ReplaceAllTx] deliberately return no count (C4's closing
// paragraph: a count is only known AFTER the apply, while C9's no-op
// decision has to be made from a read taken BEFORE the transaction opens),
// so this return value is the only carrier the handler's `changed` field
// has. Rollback always sets it true — it always writes a checkpoint,
// applies a snapshot and bumps — and Reset sets it false for exactly C9's
// no-op, where Revision is the UNCHANGED current revision.
type Outcome struct {
	Revision       int64
	ScenarioActive bool
	Changed        bool
	// DataRestored is [Repo.Rollback]'s own no-op signal for the entity
	// half, next to Changed — and it is NOT an echo of the request's
	// restoreData flag: the nearest wrong implementation is
	// `body.RestoreData`, which needs no field here at all and tells the
	// screen only what it already sent. It is true when
	// [restoreEntitiesTx] actually ran the restore for at least one
	// CARRIED family (resolved a live resources row and replaced its rows)
	// and false when restoreData was false/absent, or every carried family
	// was skipped because none resolved to a live resources row (D6 step
	// 2). A family whose stored and live rows happen to be byte-identical
	// still counts as restored — the signal is "did the restore RUN for
	// this family", not "did any byte change".
	DataRestored bool
}

// ErrWorkspaceNotFound is returned when the target workspace does not
// exist. Distinct from [ErrNotFound] (the workspace exists but the
// checkpoint does not) exactly as internal/overrides and
// internal/scenarios separate the two. Handlers answer 404.
var ErrWorkspaceNotFound = errors.New("checkpoints: workspace not found")

// ErrNotFound is returned when no checkpoint row matches — including,
// deliberately, a checkpoint id that DOES exist but belongs to a DIFFERENT
// workspace. Every lookup here scopes its WHERE clause to the given
// workspaceID, so "exists elsewhere" and "does not exist at all" are
// indistinguishable by construction rather than by a check a later edit
// could skip. Handlers answer 404.
var ErrNotFound = errors.New("checkpoints: checkpoint not found")

// ErrInvalidLabel is returned by [Repo.Create] for a label that is empty
// after trimming or longer than [maxLabelRunes] runes (C14). Handlers
// answer 400 and the message names the field.
var ErrInvalidLabel = errors.New("checkpoints: invalid label")

// ErrConcurrentEdit is C5's fence, exhausted: the workspace's identity
// triple moved between the snapshot read and the transaction on every one
// of [maxAttempts] tries. Handlers answer 409, the same status and the same
// reasoning internal/scenarios' own ErrConcurrentEdit gets
// (admin/scenario_handlers.go:341-353) — nothing is broken, the workspace
// just kept changing underneath.
var ErrConcurrentEdit = errors.New("checkpoints: workspace kept changing while snapshotting it")

// ErrSnapshotTooLarge is C18's ceiling, at BOTH ends: a capture whose
// encoded document exceeds [maxSnapshotBytes] is refused before the row is
// inserted, and a stored blob whose decompressed size exceeds it is refused
// before [bundle.Decode] sees anything. Handlers answer 413, the status
// workspaces.ErrSettingsTooLarge already gets
// (admin/workspace_handlers.go:316-321).
//
// The consequence is stated rather than left to be discovered: a workspace
// whose snapshot exceeds the ceiling loses rollback AND reset entirely,
// because both capture a pre-destructive checkpoint first, until the
// operator shrinks it. That is the honest cost of having any ceiling
// against a total nothing else bounds, and it is why the number is generous
// rather than tight.
var ErrSnapshotTooLarge = errors.New("checkpoints: snapshot is too large")

// ErrCorruptSnapshot is returned when a stored snapshot column is not
// readable — since P3d, EITHER column: a config_snap that is not a valid
// gzip stream, or a data_snap whose decoded [bundle.DataBundle] fails
// [bundle.ValidateData] on a restoreData:true rollback (D7's own table
// reuses this sentinel rather than minting a fourth, because no document
// this build writes can fail that check and a client caused none of it).
// Handlers answer 500: the row is broken, which is not something the
// client did or can fix by retrying.
var ErrCorruptSnapshot = errors.New("checkpoints: stored snapshot is not readable")

// ErrNoDataSnapshot is returned by [Repo.Rollback] when restoreData is true
// but the target checkpoint's data_snap column IS NULL — either it predates
// P3d, or its capture degraded over [maxDataProbeBytes] or
// [maxSnapshotBytes] (D5.2). Handlers answer 409: it is a state conflict
// about what THIS checkpoint carries, not a malformed request body.
var ErrNoDataSnapshot = errors.New("checkpoints: checkpoint carries no entity data")

// ErrDataSnapshotTooLarge is returned by [Repo.Rollback] when restoreData
// is true and the PRE-DESTRUCTIVE checkpoint this rollback is about to
// write — the one that would let the rollback itself be undone — degrades
// over the cap (D5.2). It is a distinct sentinel from [ErrSnapshotTooLarge]
// on purpose: the two need opposite reactions from the operator (the
// config-side ceiling means a workspace cannot be checkpointed at all; this
// one means one capability, data restore, is unavailable while everything
// else still works), and reusing the sentinel would collapse that
// difference into one message. Handlers answer 413.
var ErrDataSnapshotTooLarge = errors.New("checkpoints: entity data is too large to snapshot")

// ErrConfirmSlugRequired and ErrConfirmSlugMismatch are [Repo.Rollback]'s
// own copies of the confirm-slug pair every other row-destroying verb in
// this tree takes (a decline, a reset-data) — declared HERE rather than
// imported because internal/resources' compareConfirmSlug is unexported and
// this package imports internal/resources nowhere (HARD RULE 5). Required
// exactly when restoreData is true: a restoreData:false/absent rollback
// destroys no entity row and needs no slug. Handlers answer 409 for both,
// the same status and the same two wire codes
// (confirm_slug_required/confirm_slug_mismatch) the resource verbs already
// use, so a client keeps one vocabulary across all three.
var ErrConfirmSlugRequired = errors.New("checkpoints: confirmSlug is required")
var ErrConfirmSlugMismatch = errors.New("checkpoints: confirmSlug does not match")

// validateLabel trims, requires non-empty and caps at [maxLabelRunes]
// (C14). It lives in the repo rather than only in the handler for the same
// reason internal/scenarios validates its name here (repo.go:249-251): a
// second entry point added later gets the rule for free instead of
// silently skipping it.
func validateLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", fmt.Errorf("%w: label must not be empty", ErrInvalidLabel)
	}
	if n := utf8.RuneCountInString(label); n > maxLabelRunes {
		return "", fmt.Errorf("%w: label is %d characters (max %d)", ErrInvalidLabel, n, maxLabelRunes)
	}
	return label, nil
}

// rollbackLabel is the machine-made label a rollback's pre-destructive
// checkpoint carries. Russian, and server-generated (C14): every comment
// and identifier in this tree is English, but a label is a user-visible
// string in a Russian UI, so this one is not "corrected" to English.
//
// DEVIATION, stated rather than silent: DESIGN §12:774-775 attaches the
// label «откат к N» to the new REVISION, and no table has a
// revision-label column. There is nowhere to put it; this label carries the
// same information from the other side — the point the workspace came FROM,
// named by the point it went TO.
func rollbackLabel(targetID int64) string {
	return fmt.Sprintf("перед откатом к точке %d", targetID)
}

// resetLabel is the machine-made label a reset's pre-destructive checkpoint
// carries — Russian for the same reason as [rollbackLabel]. It quotes
// screen 10's own button, «сбросить всё к спеке» (DESIGN §14:908).
const resetLabel = "перед сбросом к спеке"
