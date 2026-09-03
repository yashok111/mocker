// Package bundle owns DESIGN §17's format: the JSON shape a scenario, a
// checkpoint's config_snap and (eventually, P4) an exported/imported
// workspace all share. This slice (P2b) only ever produces and consumes the
// SCENARIO use of it — checkpoints are P2c, export/import is P4 — but the
// shape is the same one document either way, which is exactly why it is its
// own package instead of living inside internal/scenarios.
//
// This package touches NO database and imports internal/store from
// nowhere, directly or transitively through anything it depends on
// (internal/domain, internal/jsonx, internal/overrides and internal/recipes
// are all leaves in the same sense — see each package's own doc comment).
// internal/scenarios (the repo that reads and writes the scenarios table)
// and P2c's applier both depend on this package; this package depends on
// neither. That is deliberate: encoding, decoding and validating one
// document's shape is a fact about the FORMAT, not about SQLite, and a
// package that cannot open a connection cannot accidentally grow a second,
// DB-shaped notion of what a valid bundle is.
package bundle

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// CurrentVersion is the mockerBundle value this package writes and the only
// one [Decode] accepts. DESIGN §17 fixes the top-level field's NAME
// ("mockerBundle") but not that it must always be 3 forever — an earlier
// draft of this comment (A16) expected P2c to need a v4, reasoning that an
// absent overrides key would need to mean two different things (P2b's
// composition overlays a missing key; P2c's restore deletes on it). P2c's
// own decision gate (C2) found that reasoning does not hold: composition
// and restore are two different CONSUMERS reading the identical bytes under
// different rules, which is not a wire-format disagreement — nothing about
// the SHAPE changed, only which code reads it and why. So the version stays
// 3, and a checkpoint's config_snap is this same v3 document, gzipped as a
// container encoding on top (see internal/checkpoints, C18) rather than a
// new version underneath. Gating Decode on exact equality still means a
// genuinely future document (a real v4, whenever one is actually needed)
// fails with a clear "unsupported version" error instead of this package
// silently reinterpreting fields it was never built against.
const CurrentVersion = 5

// minVersion is the OLDEST document [Decode] accepts. P7a moves the format
// to 5 (EndpointEntry.Operation, and Variant.Schema riding inside
// Responses) and — unlike P6b, which refused v3 outright on the owner's
// word that no deployment existed — READS v4: A16 shipped an installer the
// day before this slice, so a v4 checkpoint or scenario on a colleague's
// machine is now plausible, and refusing it would strand their history with
// no migration path. A v4 document decodes with a nil Operation and no
// schema, which is exactly what those rows hold.
const minVersion = 4

// ErrInvalid wraps every reason a Bundle is rejected: an unsupported
// mockerBundle version, a basePath disagreement (A14), a non-null entities
// value — still always null in this build, and deliberately so (see below)
// — or an Overrides or Endpoints entry that fails the same gate its owning
// table's own write path enforces (Method/Path shape, listSize/delayMs
// range, and — via [overrides.ValidateResponses] — mode, base64, recipe and
// when-condition validity).
//
// Resources is NOT in that list anymore (P3b, R20/R21). It was refused for
// being non-empty until this slice, on the same "P3's, nothing writes it"
// warrant Endpoints was refused on before C2 lifted that one; P3b's
// checkpoint capture fills it, so the refusal had to go. Nothing replaced
// it at any level: no writer in this build can produce a SCENARIO snapshot
// carrying resources ([New] hard-codes the field empty and
// internal/scenarios only ever re-encodes what New built), and no reader in
// this build reads the field outside internal/checkpoints' own restore — so
// a scenario-specific guard would have defended a population of zero at the
// cost of reversing P2c's C2 decision and absorbing scanScenario's working
// Endpoints rule into a second exported validator.
//
// The entities refusal is the one this slice positively WANTS. P3b captures
// resource CONFIGURATION and never entity rows, so no document this build
// writes may carry them — and that refusal is what guarantees it rather
// than leaving it to every future capture site to remember.
//
// Endpoints itself is deliberately NOT in the "always empty" list above
// anymore (C2 of P2c's gate): DESIGN §17 always drew a v3 document with
// endpoints populated, and the "a v3 document may never carry one" rule was
// P2b's own local cut, never the format's. A non-empty Endpoints array is
// now a SHAPE this level accepts; the narrower rule that a SCENARIO
// specifically must still never carry one moved to internal/scenarios'
// scanScenario — the one place that already knows it is reading a scenario
// rather than a checkpoint, which this package has no way to know at all.
var ErrInvalid = errors.New("bundle: invalid document")

// Bundle is DESIGN §17's format, decoded. Field order is the struct's own
// declaration order, which is also the WIRE order — encoding/json (and any
// backend behind internal/jsonx that keeps its promise, see that package's
// own doc comment on stability) marshals struct fields in declaration
// order, never alphabetically, so this order pins the envelope's byte
// shape directly: reorder these fields and every already-written snapshot's
// diff against a freshly-encoded one moves for no reason.
type Bundle struct {
	MockerBundle int           `json:"mockerBundle"`
	Workspace    WorkspaceInfo `json:"workspace"`

	// BasePath duplicates Workspace.Settings.BasePath at the top level,
	// exactly as §17 draws it. A14: this field has exactly ONE authority —
	// [New] copies it from settings.basePath on the way in, and [Validate]
	// (which [Decode] always runs) rejects a document where the two values
	// disagree, rather than picking one silently and leaving the other a
	// lie no bar catches.
	BasePath string `json:"basePath"`

	Spec SpecRef `json:"spec"`

	// Overrides encodes ROWS, never merge intent (A13). Two consumers read
	// these same bytes under OPPOSITE rules: P2b's runtime composition
	// overlays them (a key ABSENT from this slice falls through to the
	// workspace's own row) and P2c's restore deletes on them (a key absent
	// here means "delete this operation's override"). Nothing on this type
	// may collapse that distinction:
	//
	//   - there is no "was this key touched" marker anywhere in this
	//     format — membership in the slice IS the only signal a reader
	//     gets, by design;
	//   - OverrideEntry.OverrideOn is never `omitempty`d, and must never
	//     become one: A2 makes a row with OverrideOn=false MEAN something
	//     different from that key being absent altogether (an operation
	//     the scenario deliberately masks back to the spec's own answer,
	//     versus an operation the scenario has no opinion on at all) — a
	//     `false` that vanished into an omitted field would be
	//     indistinguishable from a missing row on the wire, even though
	//     the two produce different served bytes.
	Overrides []OverrideEntry `json:"overrides"`

	// Endpoints is a non-nil array (marshals to "[]", never "null") — never
	// omitted — carrying zero or more [EndpointEntry] rows. Whether a GIVEN
	// v3 document may legitimately have any is not this package's rule to
	// enforce, because this package has no notion of "scenario" versus
	// "checkpoint" at all (see the package doc comment): a SCENARIO's
	// Endpoints must always be empty (DESIGN §12:528 scopes a scenario to
	// "settings + выбранные правки" only, and internal/mockplane's
	// runtime.custom is keyed by a custom_endpoints DB row id a BLOB has no
	// business minting — a scenario cannot carry a custom endpoint at all),
	// while a CHECKPOINT's config_snap (P2c, internal/checkpoints)
	// legitimately carries the workspace's live custom_endpoints rows.
	// Both are the identical format at THIS level (C2: no version bump, no
	// second Validate for the two cases) — the emptiness rule lives one
	// layer up, in internal/scenarios' scanScenario, the one place that
	// already knows which of the two kinds of snapshot it is reading.
	Endpoints []EndpointEntry `json:"endpoints"`

	// Resources is a non-nil array of [ResourceEntry] rows, exactly the
	// shape Endpoints is and for the identical reason: whether a GIVEN v3
	// document may legitimately carry any is not this package's rule (it
	// has no notion of "scenario" versus "checkpoint" at all). A CHECKPOINT
	// carries the workspace's live `resources` rows — that is P3b's whole
	// point, since entities.resource_id is ON DELETE CASCADE and a restore
	// that could not name a resource could only ever delete one. A SCENARIO
	// carries none, and nothing in this build can write one that does:
	// [New] hard-codes the field empty and internal/scenarios never assigns
	// it. Until P3b this was []jsonx.RawMessage, because nothing ever
	// filled it; a restore that must UPSERT by (workspace_id, route_family)
	// needs the columns as FIELDS, not as opaque bytes.
	Resources []ResourceEntry `json:"resources"`

	// Decisions is Resources' other half: the resource_decisions rows,
	// carried in the same document because a confirm writes both and a
	// decline clears both. A snapshot that restored one without the other
	// could put a workspace into a state neither screen nor server agrees
	// on — a decision row saying `declined` beside a live `resources` row,
	// which the confirm path would answer `already_confirmed` for while the
	// screen renders it as declined.
	//
	// It is a SEPARATE array rather than a field on ResourceEntry because a
	// DECLINED family has a decision row and no resource row at all: there
	// is no entry to hang it on.
	Decisions []DecisionEntry `json:"decisions"`

	// Entities is P3's data-snapshot half, and — unlike the three arrays
	// above — §17 draws it as JSON null, not an empty array, "только в
	// data-снимке чекпойнта". It is still always null in every document
	// this build writes, and [Validate] still REFUSES a non-null value.
	// P3d made checkpoints.data_snap real, but NOT by filling this field:
	// entity rows now live in a SEPARATE document type, [DataBundle]
	// (data.go), stored in its own column — see that type's own doc
	// comment (D3 of the P3d decision document) for why entity rows do not
	// belong here. This field's refusal therefore stays exactly what it
	// was: the guarantee that no capture site can quietly start carrying
	// entity rows THROUGH THIS DOCUMENT, on a format whose Validate has no
	// rule for restoring them. [jsonx.RawMessage]'s own zero value (nil) already marshals to
	// the literal "null" (see its MarshalJSON), so leaving this at its zero
	// value is enough — no extra code needed to force the literal, which is
	// itself worth noting: it is easy to accidentally "fix" this into "[]"
	// by treating it the same as the three fields above it, and that would
	// be wrong.
	Entities jsonx.RawMessage `json:"entities"`
}

// ResourceEntry is one entry of Bundle.Resources: the `resources` columns
// that describe a confirmed family's identity and shape, mirror of what
// [OverrideEntry] and [EndpointEntry] do for their own tables.
//
// What it deliberately does NOT carry, each for its own reason:
//
//   - id and workspace_id — a snapshot has no business minting a row id,
//     the same exclusion EndpointEntry makes;
//   - parent_id — NULL by design (P3e D9, re-decided at every depth by
//     P3g D9), not pending a slice: a stored `resources.id` is orphaned by
//     the exact repair this project prescribes for a wrong resource —
//     decline deletes the row, a re-confirm mints a fresh rowid (D9.4) —
//     so parent_id stays NULL on every row this build writes, at every
//     level of a chain, and a restore leaves whatever the live row has
//     standing rather than writing a NULL over it;
//   - confidence — that is a resource_suggestions column, not a resources
//     one (0001_init.sql:92), and an entry carrying it would tempt a
//     restore into writing a column that does not exist.
//
// ParentFamily is the mirror image: it is on the WIRE and maps to no
// `resources` column at all (parent_family belongs to
// resource_suggestions, 0001_init.sql:89). It is here, always null, by the
// same D9 decision — a computed `router.ParentFamily` (D4.2) is what
// addresses a family's IMMEDIATE parent at any depth, never a stored id —
// and a restore must skip it, which
// is why it is called out rather than left to be discovered as a SQL error.
//
// EntitySchema is a JSON POINTER into the spec ("#/components/schemas/X"),
// not a schema document; Wrapper and FilterMap are the raw JSON the
// columns hold.
type ResourceEntry struct {
	RouteFamily  string           `json:"routeFamily"`
	Name         string           `json:"name"`
	IDField      string           `json:"idField"`
	IDStrategy   string           `json:"idStrategy"`
	ScopeParams  []string         `json:"scopeParams"`
	EntitySchema string           `json:"entitySchema"`
	Wrapper      jsonx.RawMessage `json:"wrapper"`
	FilterMap    jsonx.RawMessage `json:"filterMap"`

	// WriteForm is a pointer because NULL means something: the family's
	// POST shape was not recognised and the mock plane keeps answering it
	// from the generator. A restore that turned a NULL into "bare" — or a
	// "bare" into NULL — changes whether a later POST X stores a row at
	// all, and it does so SILENTLY (the takeover simply does not happen
	// and the caller gets a generated 201), which is why this field is a
	// pointer here and asserted by its own acceptance clause.
	WriteForm *string `json:"writeForm"`

	// Seq is the entity-key counter as of the capture. A restore must
	// never write it back verbatim — see internal/checkpoints' restore for
	// the max(current, snapshot) rule and why a rewound counter is a
	// silent lost write rather than an error.
	Seq       int64 `json:"seq"`
	SeedCount int64 `json:"seedCount"`

	// ParentFamily is always null by design (P3e D9, re-decided at every
	// depth by P3g D9), not pending a slice — see the type's own doc
	// comment: it maps to no `resources` column, a family's IMMEDIATE
	// parent is addressed by a computed router.ParentFamily, never a
	// stored id, at any depth of a chain.
	ParentFamily *string `json:"parentFamily"`
}

// DecisionEntry is one entry of Bundle.Decisions: one resource_decisions
// row, both of its columns (workspace_id is the snapshot's own scope, as
// everywhere else in this format). State is "confirmed" or "declined" —
// unvalidated here, exactly as EndpointEntry's own enum-ish fields are: the
// owning table's write path is the authority on the vocabulary, and a
// second copy of it here would be a driftable one.
type DecisionEntry struct {
	RouteFamily string `json:"routeFamily"`
	State       string `json:"state"`
}

// WorkspaceInfo is Bundle.Workspace: the two workspace-level facts a
// snapshot needs to be self-describing. Settings is the FULL
// [domain.Settings] value, not a trimmed subset — DESIGN §12's "scenario
// replaces settings wholesale" (A3's "Replaced wholesale" bullets) means a
// scenario has to carry every field the runtime composition might pick up,
// not a hand-picked list this package would have to keep in sync with
// domain.Settings by hand.
type WorkspaceInfo struct {
	Name     string          `json:"name"`
	Settings domain.Settings `json:"settings"`
}

// SpecRef is Bundle.Spec: the spec identity a snapshot was taken against,
// recorded for PROVENANCE ONLY (A15). Hash is the bare hex sha256
// [specs.Spec.Hash] already stores (no "sha256:" scheme prefix — DESIGN
// §17's own example elides it with "..." and this package has no reason to
// invent a convention the rest of the tree does not already use). Inline
// is always nil (JSON null) in this slice — it exists for P4's portable
// export, where a bundle travels without a server-side spec row to point
// Hash/Name at, and there is nothing for a scenario (which always lives
// beside the spec it was snapshotted from) to put there.
type SpecRef struct {
	Hash   string           `json:"hash"`
	Name   string           `json:"name"`
	Inline jsonx.RawMessage `json:"inline"`
}

// OverrideEntry is one entry of Bundle.Overrides: every column
// [overrides.Row] owns that is not pure DB bookkeeping (ID, WorkspaceID and
// OperationID are a row's identity and a cache of the operations table
// respectively — neither means anything once the row is lifted out of
// op_overrides and into a portable snapshot; UpdatedAt is when the LIVE row
// last changed, not a fact about the snapshot). A13's second half: this
// type reuses [overrides.Variant] and [overrides.ListSize] directly rather
// than re-declaring the same wire shape under new names — there is exactly
// one Go type for "one response variant" in this tree, the same way
// internal/customep reuses them for custom_endpoints.responses instead of
// inventing its own.
type OverrideEntry struct {
	Method string `json:"method"`
	Path   string `json:"path"`

	// OverrideOn is deliberately never `omitempty` — see the field-level
	// comment on Bundle.Overrides for why a `false` here must stay a
	// `false` on the wire, not vanish into an absent key.
	OverrideOn   bool                         `json:"overrideOn"`
	RouteOff     bool                         `json:"routeOff"`
	ActiveStatus *int                         `json:"activeStatus,omitempty"`
	Responses    map[string]overrides.Variant `json:"responses"`
	ListSize     *overrides.ListSize          `json:"listSize,omitempty"`
	DelayMs      *int                         `json:"delayMs,omitempty"`

	// FailDirective is PRESERVED ONLY, exactly as [overrides.Row]'s own
	// field is — this slice's evaluator (session directives are P2c) does
	// not interpret it, and its KEY ORDER is deliberately EXCLUDED from
	// the canonicalisation Encode runs over Body/SchemaPatch/recipe
	// payloads (see canonicalizeOverrideEntry): unlike those, this field is
	// never decoded-and-re-marshalled through [canonicalizeRaw], so
	// whatever key order it arrived in survives. (Whitespace is a
	// different matter: Go's encoding/json always COMPACTS the bytes a
	// json.Marshaler returns before embedding them in a larger document —
	// see RawMessage's own MarshalJSON — so insignificant whitespace is
	// lost here exactly as it is for every other RawMessage field in this
	// type, canonicalised or not; that part of "byte-for-byte" was never
	// achievable once this field lives inside a struct Encode marshals as
	// one document, only ever a property of overrides/repo.go's own
	// upsertTx, which writes FailDirective as a raw SQL TEXT parameter and
	// never calls json.Marshal on it at all.) Re-sorting this field's keys
	// the way Body/SchemaPatch are would make the SAME op_overrides column
	// canonicalise differently depending on whether it reached storage
	// through a scenario snapshot or a direct PUT — key order, not
	// whitespace, is the property worth keeping consistent between the two
	// paths.
	FailDirective jsonx.RawMessage `json:"failDirective,omitempty"`
	ValidateReq   *bool            `json:"validateReq,omitempty"`
}

// NewOverrideEntry converts one op_overrides row into the bundle's wire
// shape. Every field [overrides.Row] owns (other than the DB-only identity
// fields the OverrideEntry doc comment names) is copied — silently
// dropping one here would be a snapshot that quietly loses an operator's
// work the moment this package's caller reads it back.
func NewOverrideEntry(row *overrides.Row) OverrideEntry {
	return OverrideEntry{
		Method:        row.Method,
		Path:          row.Path,
		OverrideOn:    row.OverrideOn,
		RouteOff:      row.RouteOff,
		ActiveStatus:  row.ActiveStatus,
		Responses:     row.Responses,
		ListSize:      row.ListSize,
		DelayMs:       row.DelayMs,
		FailDirective: row.FailDirective,
		ValidateReq:   row.ValidateReq,
	}
}

// EndpointEntry is one entry of Bundle.Endpoints: the twelve columns
// customep.Row owns that describe a custom endpoint's own behaviour, mirror
// of what OverrideEntry above does for overrides.Row. customep.Row itself
// has SEVENTEEN fields, not twelve — the gap is FIVE Row fields excluded
// here plus a sixth thing, ResourceID, that is not one of the seventeen at
// all (it exists only as a column, 0001_init.sql:207, P3's and never on the
// Go struct), which is why the count does not add up to seventeen on its
// own:
//
//   - ID, WorkspaceID, CreatedAt, UpdatedAt — pure DB bookkeeping, the same
//     reason OverrideEntry excludes overrides.Row's ID/WorkspaceID/
//     UpdatedAt: none of the four means anything once a row is lifted out
//     of custom_endpoints and into a portable snapshot. (overrides.Row also
//     excludes OperationID, a cache column customep.Row has no equivalent
//     of.)
//   - CanonicalPath — derived from Path by router.CanonicalPath, the exact
//     authority the UNIQUE (workspace_id, method, canonical_path) index
//     depends on (0001_init.sql:211). A snapshot carrying its own copy
//     would let a restored row disagree with what that function computes
//     from Path, inventing a SECOND authority for the value the index
//     polices — the restore path (internal/customep) recomputes it fresh
//     from Path instead.
//   - ResourceID — P3's, and (as above) not a Row field to begin with.
//
// internal/bundle imports internal/customep for NONE of this: building one
// of these FROM a *customep.Row, and rebuilding a *customep.Row from one of
// these, is internal/checkpoints' job — it already imports both
// internal/bundle and internal/customep for other reasons, and pulling
// internal/customep (which pulls internal/router, for CanonicalPath) into
// this package would be exactly the kind of DB-adjacent dependency the
// package doc comment forbids, just to save one small struct-literal
// conversion function elsewhere.
type EndpointEntry struct {
	Method string `json:"method"`
	Path   string `json:"path"`

	// OverrideOn and RouteOff are never `omitempty`, mirroring
	// OverrideEntry's own fields of the same name: a restore has to write
	// back whatever value the snapshot actually holds, and a `false` that
	// vanished into an absent key would be indistinguishable on the wire
	// from a row that was never snapshotted with an opinion on it at all.
	OverrideOn bool `json:"overrideOn"`
	RouteOff   bool `json:"routeOff"`

	// ActiveStatus is a plain int, never a pointer — unlike
	// OverrideEntry.ActiveStatus, which is *int because nil there means
	// "keep the spec document's own choice". customep.Row's own comment
	// says why that state does not exist here: a custom endpoint has no
	// spec document to fall back on at all, so the column is NOT NULL
	// DEFAULT 200 and there is nothing for a pointer's nil to mean.
	ActiveStatus int `json:"activeStatus"`

	Responses map[string]overrides.Variant `json:"responses"`

	// ReqSchema is PRESERVED ONLY, exactly as customep.Row's own field is:
	// this slice's request-validation evaluator (P2) never interprets it,
	// so — like OverrideEntry.FailDirective below — it is never run through
	// canonicalizeRaw's decode/re-encode pass; whatever bytes and key order
	// it arrived with are what a round trip through this format preserves.
	ReqSchema jsonx.RawMessage `json:"reqSchema,omitempty"`

	ListSize *overrides.ListSize `json:"listSize,omitempty"`
	DelayMs  *int                `json:"delayMs,omitempty"`

	// FailDirective: see OverrideEntry.FailDirective's own comment above —
	// the identical "preserved key order, never re-sorted" rule applies
	// here for the identical reason (the SAME op_overrides/custom_endpoints
	// column, written through either a snapshot or a direct admin PUT, must
	// canonicalise the same way regardless of which path wrote it).
	FailDirective jsonx.RawMessage `json:"failDirective,omitempty"`
	ValidateReq   *bool            `json:"validateReq,omitempty"`

	// SourceOrder is customep.Row's own tie-break for route matching
	// (DESIGN §8 rule 4, router.compareRoutes' final comparison). It is
	// carried, unlike every other DB-bookkeeping field above, because a
	// restore has to write it back VERBATIM rather than reassign it via
	// customep.Repo.Create's max(source_order)+1 rule — reassigning it on
	// restore would silently reorder every custom endpoint relative to each
	// other the moment more than one comes back through the same snapshot.
	SourceOrder int64 `json:"sourceOrder"`

	// Kind and Stream are P6b's (decisions.md mocker-p6b-sse-mock D12) and
	// are what moved this format from 3 to 4: custom_endpoints.kind
	// ("http" | "sse") and the stream document, `null` for an http row —
	// never omitempty, for OverrideOn/RouteOff's own reason above: a
	// restore writes back exactly what the snapshot holds, and the column's
	// CHECK pairs a NULL stream with kind http and nothing else. Stream is
	// canonicalised on the way out like Responses, because — unlike
	// ReqSchema — it is INTERPRETED by the serving path, not preserved.
	Kind   string           `json:"kind"`
	Stream jsonx.RawMessage `json:"stream"`

	// Operation is P7a's (DESIGN §34.3) and is what moved this format
	// from 4 to 5: custom_endpoints.operation, the OpenAPI operation
	// fields the contract export writes. `omitempty` because a v4
	// document has none and a row that declares none must round-trip to
	// the same bytes it arrived as. The response SCHEMA needs no field of
	// its own: it rides inside Responses on the Variant, like every other
	// per-status field.
	Operation jsonx.RawMessage `json:"operation,omitempty"`
}

// New builds a fresh v3 Bundle from a workspace's current state.
//
// entries is sorted by (method, path) before it is stored on the returned
// Bundle — never trusted to already be in a stable order, because its
// caller almost always built it by ranging over
// [overrides.Repo.ForWorkspace]'s map result, and Go randomises map
// iteration per run. Without this sort, two snapshots of the IDENTICAL
// workspace state taken in two different process runs could encode to
// different bytes purely from map order, which would defeat §17's whole
// point ("стабильная сортировка ключей — чтобы git diff читался"). entries
// itself is copied, not sorted in place, so a caller that goes on to reuse
// its slice for something else never sees it silently reordered out from
// under it.
//
// Endpoints, Resources, Decisions and Entities are always set to their
// "nothing here" shape (a non-nil empty slice for the first three, so they
// marshal to "[]"; Entities left at its nil zero value, so it marshals to
// "null") — but for two different reasons, and P3b moved one field across
// the line exactly as C2 of P2c's gate moved Endpoints before it.
//
// Entities stays empty because it is still always empty in this build
// regardless of caller: [Validate] refuses a non-null value outright.
// Endpoints, Resources and Decisions stay empty here for the NARROWER
// reason: New's only caller is internal/scenarios, and a scenario carries
// none of the three (a custom endpoint is keyed by a DB row id a BLOB has
// no business minting; a resource is configuration a scenario overlay has
// no rule for) — not because this FORMAT forbids them anymore.
// internal/checkpoints, which DOES carry all three, calls New unchanged for
// the settings/overrides half and then assigns b.Endpoints, b.Resources and
// b.Decisions on the value New returns, AFTER the call — New keeps this
// exact signature and gains no parameter for any of them (§B).
func New(workspaceName string, settings domain.Settings, spec SpecRef, entries []OverrideEntry) Bundle {
	// make(..., 0, len(entries)), never append([]OverrideEntry(nil), ...):
	// a workspace with zero op_overrides rows is the everyday case for a
	// freshly-imported spec, and that must still encode "overrides":[],
	// consistent with Endpoints/Resources below, not "overrides":null —
	// there is no reason for the one array that sometimes has content to
	// be the one field that looks different from the two that never do.
	sorted := make([]OverrideEntry, 0, len(entries))
	sorted = append(sorted, entries...)
	slices.SortFunc(sorted, func(a, b OverrideEntry) int {
		if c := cmp.Compare(a.Method, b.Method); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})

	return Bundle{
		MockerBundle: CurrentVersion,
		Workspace:    WorkspaceInfo{Name: workspaceName, Settings: settings},
		BasePath:     settings.BasePath, // A14: settings.basePath is the one authority
		Spec:         spec,
		Overrides:    sorted,
		Endpoints:    []EndpointEntry{},
		Resources:    []ResourceEntry{},
		Decisions:    []DecisionEntry{},
		Entities:     nil,
	}
}

// Encode validates b (the same [Validate] Decode runs — Encode must never
// persist a document its own Decode would then refuse to read back) and
// returns its canonical byte form: struct field order (declared above)
// pins the envelope, and canonicalize below re-marshals every nested
// "whatever a client PUT sent" JSON payload through a decode/encode pass
// first (A19), so the output depends on what those payloads MEAN, not on
// which key order and whitespace happened to arrive over the wire that one
// time an operator edited them.
func Encode(b Bundle) ([]byte, error) {
	if err := Validate(b); err != nil {
		return nil, err
	}
	canon, err := canonicalize(b)
	if err != nil {
		return nil, fmt.Errorf("bundle: encode: %w", err)
	}
	out, err := jsonx.Marshal(canon)
	if err != nil {
		return nil, fmt.Errorf("bundle: encode: marshal: %w", err)
	}
	return out, nil
}

// Decode parses data and runs [Validate] over the result before returning
// it — a stored snapshot is re-checked on every read, exactly like
// op_overrides' own scan() re-validates a row on every unauthenticated
// request (repo.go's scan, and its doc comment on why): a document that
// got into storage some other way (a hand-run UPDATE, a future build
// writing a shape this one does not know) fails HERE with a returned
// error, never three calls up the stack as a panic or — worse — as a
// silently wrong served body.
//
// Decode does NOT look at [SpecRef.Hash], and neither does anything this
// package exports: it is provenance only (A15). A snapshot's row for a
// route the CURRENT spec no longer has is inert, never an error, and this
// function has no way to even ask what "current" means — it imports no DB
// code at all, so there is nothing here that COULD consult it.
func Decode(data []byte) (Bundle, error) {
	var b Bundle
	if err := jsonx.Unmarshal(data, &b); err != nil {
		return Bundle{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := Validate(b); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

// Validate checks the structural invariants a v3 Bundle must hold,
// regardless of whether it arrived via [Decode] or was assembled by hand
// before a first [Encode]. It is exported separately from Decode so a
// caller holding an in-memory Bundle it built itself can fail fast, before
// ever writing garbage to storage.
//
// Every OverrideEntry is checked through [overrides.ValidateResponses] —
// the SAME gate the admin API's override handlers write every op_overrides
// row through (A13's second half) — plus the same
// Method/Path/ListSize/DelayMs shape checks [overrides.Row]'s own write
// path enforces (that half is NOT exported by internal/overrides, exactly
// as internal/customep's own normalizeAndValidate re-derives it locally
// rather than importing an unexported function; there is nothing to import
// here, and duplicating four short range checks is not "a second notion of
// a valid override" the way re-implementing ValidateResponses/ValidateVariant
// would be).
func Validate(b Bundle) error {
	if b.MockerBundle < minVersion || b.MockerBundle > CurrentVersion {
		return fmt.Errorf("%w: mockerBundle %d, this build reads versions %d..%d",
			ErrInvalid, b.MockerBundle, minVersion, CurrentVersion)
	}
	if b.BasePath != b.Workspace.Settings.BasePath {
		return fmt.Errorf("%w: basePath %q disagrees with workspace.settings.basePath %q (A14)",
			ErrInvalid, b.BasePath, b.Workspace.Settings.BasePath)
	}
	// Resources has NO emptiness check here — P3b lifted it, and nothing
	// replaced it at any level. [ErrInvalid]'s own doc comment carries the
	// measurement that says why a scenario-specific guard would defend a
	// population of zero.
	if !isJSONNull(b.Entities) {
		return fmt.Errorf("%w: entities must be null in a mockerBundle:%d snapshot (entity rows live in bundle.DataBundle, P3d)", ErrInvalid, CurrentVersion)
	}
	for i, e := range b.Overrides {
		if err := validateOverrideEntry(e); err != nil {
			return fmt.Errorf("%w: overrides[%d] (%s %s): %w", ErrInvalid, i, e.Method, e.Path, err)
		}
	}
	// Endpoints gets the same SHAPE gate Overrides does, and nothing more
	// (C3): a known HTTP method and the "?", "#", "//" path rules that would
	// desync from router.CanonicalPath's segment splitting are customep's
	// own write-path gate, not this format's — this package has no
	// router.CanonicalPath to check against, and duplicating that logic
	// here would be a second, driftable copy of a rule that already lives
	// exactly once, on the table that actually enforces it.
	for i, e := range b.Endpoints {
		if err := validateEndpointEntry(e); err != nil {
			return fmt.Errorf("%w: endpoints[%d] (%s %s): %w", ErrInvalid, i, e.Method, e.Path, err)
		}
	}
	// Resources and Decisions were unchecked while every document came out
	// of this installation's own capture; P4b made the bundle external
	// input, and resources/resource_decisions carry no CHECK of their own
	// (0001_init.sql), so an arbitrary state or an empty family name would
	// otherwise be written verbatim.
	seenRes := make(map[string]bool, len(b.Resources))
	for i, e := range b.Resources {
		if e.RouteFamily == "" {
			return fmt.Errorf("%w: resources[%d]: routeFamily must not be empty", ErrInvalid, i)
		}
		if seenRes[e.RouteFamily] {
			return fmt.Errorf("%w: resources[%d]: duplicate routeFamily %q", ErrInvalid, i, e.RouteFamily)
		}
		seenRes[e.RouteFamily] = true
	}
	seenDec := make(map[string]bool, len(b.Decisions))
	for i, e := range b.Decisions {
		if e.RouteFamily == "" {
			return fmt.Errorf("%w: decisions[%d]: routeFamily must not be empty", ErrInvalid, i)
		}
		if seenDec[e.RouteFamily] {
			return fmt.Errorf("%w: decisions[%d]: duplicate routeFamily %q", ErrInvalid, i, e.RouteFamily)
		}
		seenDec[e.RouteFamily] = true
		if e.State != "confirmed" && e.State != "declined" {
			return fmt.Errorf("%w: decisions[%d] (%s): state %q is not confirmed or declined", ErrInvalid, i, e.RouteFamily, e.State)
		}
	}
	return nil
}

func validateOverrideEntry(e OverrideEntry) error {
	if e.Method == "" {
		return errors.New("method is empty")
	}
	if e.Path == "" || e.Path[0] != '/' {
		return fmt.Errorf("path %q must start with \"/\"", e.Path)
	}
	if e.ListSize != nil {
		if e.ListSize.Min < 0 || e.ListSize.Max < e.ListSize.Min {
			return fmt.Errorf("listSize has an inverted or negative range [%d,%d]", e.ListSize.Min, e.ListSize.Max)
		}
	}
	if e.DelayMs != nil && *e.DelayMs < 0 {
		return fmt.Errorf("delayMs must not be negative, got %d", *e.DelayMs)
	}
	// P7a (D2): `schema` is a CUSTOM endpoint's field; the admin plane
	// refuses it by name on PUT .../operations (schema_on_override), and a
	// document is a second door into op_overrides — a restore or an import
	// must not store what the route refuses. The runtime would ignore it
	// (only a custom row's schema is compiled), so the harm is a stored
	// field nothing serves and the export does not read; refused all the
	// same, so the two doors agree.
	for status, v := range e.Responses {
		if len(v.Schema) > 0 {
			return fmt.Errorf("responses[%s]: schema belongs to a custom endpoint; a spec operation's schema changes through schemaPatch", status)
		}
	}
	// The deep gate: mode/bodyEncoding/recipe/when-condition validity, and
	// pinned-body/recipe-count bounds. Wrapping is unnecessary here —
	// ValidateResponses already wraps overrides.ErrInvalidRow, and the
	// caller (Validate) wraps THIS function's return value in ErrInvalid,
	// so a caller doing errors.Is against either sentinel still matches.
	return overrides.ValidateResponses(e.Responses)
}

// validateEndpointEntry is EndpointEntry's shape gate, field for field the
// same checks validateOverrideEntry runs above (Method/Path shape, the
// listSize/delayMs ranges, then [overrides.ValidateResponses] over
// Responses — the identical function, since EndpointEntry.Responses is the
// same map[string]overrides.Variant type, A13's second half one struct
// over). What this deliberately does NOT check — customep's own path rules
// (a known HTTP method and the "?", "#", "//" shape customep's
// validatePath rejects because it would desync from
// router.CanonicalPath's segment splitting) and any media-type guard — is
// spelled out on the Validate call site above and in C3/C16: that full
// gate stays exactly where it already lives, on customep's write path,
// because this package has no router.CanonicalPath to check a path against
// and no business inventing a second, format-level copy of a rule the
// owning table already enforces once.
func validateEndpointEntry(e EndpointEntry) error {
	if e.Method == "" {
		return errors.New("method is empty")
	}
	if e.Path == "" || e.Path[0] != '/' {
		return fmt.Errorf("path %q must start with \"/\"", e.Path)
	}
	if e.ListSize != nil {
		if e.ListSize.Min < 0 || e.ListSize.Max < e.ListSize.Min {
			return fmt.Errorf("listSize has an inverted or negative range [%d,%d]", e.ListSize.Min, e.ListSize.Max)
		}
	}
	if e.DelayMs != nil && *e.DelayMs < 0 {
		return fmt.Errorf("delayMs must not be negative, got %d", *e.DelayMs)
	}
	// P6b (D12): the kind/stream coupling custom_endpoints' own CHECK
	// states, checked here so a hand-edited v4 document is refused before
	// a restore ever reaches the constraint. The stream document's SHAPE
	// is not validated here — that is internal/customep's job (a package
	// this one deliberately does not import), and the restore's
	// ReplaceAllTx runs it row by row.
	switch e.Kind {
	case "", "http":
		// "" is an entry built in memory by a caller that never heard of
		// kinds (every fixture and every pre-P6b code path); it reads as
		// the column's own DEFAULT and canonicalizeEndpointEntry writes it
		// out as "http" so the encoded document always carries the value.
		if !isJSONNull(e.Stream) {
			return errors.New(`kind "http" must carry stream: null`)
		}
	case "sse", "ws":
		if isJSONNull(e.Stream) {
			return fmt.Errorf("kind %q requires a stream document", e.Kind)
		}
	default:
		return fmt.Errorf("kind %q is not one of http, sse, ws", e.Kind)
	}
	// P7a (D9): reqSchema is a schema now, not a preserved blob — an
	// object under the cap, refused here as invalid_bundle rather than
	// later inside the restore's transaction.
	if err := overrides.ValidateSchemaShape(e.ReqSchema); err != nil {
		return fmt.Errorf("reqSchema: %w", err)
	}
	return overrides.ValidateResponses(e.Responses)
}

// isJSONNull reports whether raw is the JSON literal null, treating an
// empty/nil RawMessage the same way: [jsonx.RawMessage]'s own zero value
// marshals to "null" (see its MarshalJSON), but a value that arrived via
// Decode after actually containing the text "null" is NOT the zero value —
// UnmarshalJSON copies the literal bytes it was given, nil or not — so a
// plain `raw == nil` check would silently accept the zero value while
// rejecting a decoded "null", which is the same value on the wire.
func isJSONNull(raw jsonx.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// canonicalize returns a COPY of b with every nested "whatever a client
// PUT sent" JSON payload re-marshalled through canonicalizeRaw, so Encode
// never mutates the Bundle its caller handed it — a caller that goes on to
// inspect or reuse b after calling Encode must see exactly what it built,
// not a canonicalised copy Encode produced as a side effect.
func canonicalize(b Bundle) (Bundle, error) {
	out := b

	nfb, err := canonicalizeRaw(b.Workspace.Settings.NotFoundBody)
	if err != nil {
		return Bundle{}, fmt.Errorf("workspace.settings.notFoundBody: %w", err)
	}
	out.Workspace.Settings.NotFoundBody = nfb

	inline, err := canonicalizeRaw(b.Spec.Inline)
	if err != nil {
		return Bundle{}, fmt.Errorf("spec.inline: %w", err)
	}
	out.Spec.Inline = inline

	if len(b.Overrides) > 0 {
		entries := make([]OverrideEntry, len(b.Overrides))
		for i, e := range b.Overrides {
			ce, cerr := canonicalizeOverrideEntry(e)
			if cerr != nil {
				return Bundle{}, fmt.Errorf("overrides[%d] (%s %s): %w", i, e.Method, e.Path, cerr)
			}
			entries[i] = ce
		}
		out.Overrides = entries
	}

	if len(b.Endpoints) > 0 {
		entries := make([]EndpointEntry, len(b.Endpoints))
		for i, e := range b.Endpoints {
			ce, cerr := canonicalizeEndpointEntry(e)
			if cerr != nil {
				return Bundle{}, fmt.Errorf("endpoints[%d] (%s %s): %w", i, e.Method, e.Path, cerr)
			}
			entries[i] = ce
		}
		// Sorted HERE, unlike Overrides above (which New already sorted
		// before it ever reached this function) — Endpoints never passes
		// through New at all (C2/C3): internal/checkpoints assigns
		// b.Endpoints directly on the value New returns, in whatever order
		// customep.ForWorkspace handed it back, which is `ORDER BY
		// source_order, id` — a DB ordering, not a (Method, Path) one.
		// Sorting inside canonicalize, the one function every Encode call
		// always runs, is what keeps that DB order from leaking into the
		// document, the same way New's own sort keeps a Go map's randomised
		// iteration order out of Overrides.
		slices.SortFunc(entries, func(a, b EndpointEntry) int {
			if c := cmp.Compare(a.Method, b.Method); c != 0 {
				return c
			}
			return cmp.Compare(a.Path, b.Path)
		})
		out.Endpoints = entries
	}

	// Resources and Decisions get the Endpoints treatment (allocate, then
	// SORT), not the Overrides one (allocate, do not sort): neither passes
	// through New at all — internal/checkpoints assigns both on the value
	// New returns, in whatever order its own SELECTs handed them back —
	// so canonicalize is the only place a stable order can be imposed. They
	// sort by route_family, the natural key a restore UPSERTs on and the
	// only value in either entry that is guaranteed unique and present; an
	// id would be neither, and this document deliberately carries none.
	//
	// The allocation is not a detail either: canonicalize returns `out :=
	// b`, a SHALLOW copy, so sorting b's own backing array in place would
	// reorder the caller's Bundle — exactly what this function's doc
	// comment promises Encode never does.
	if len(b.Resources) > 0 {
		entries := make([]ResourceEntry, len(b.Resources))
		for i, e := range b.Resources {
			ce, cerr := canonicalizeResourceEntry(e)
			if cerr != nil {
				return Bundle{}, fmt.Errorf("resources[%d] (%s): %w", i, e.RouteFamily, cerr)
			}
			entries[i] = ce
		}
		slices.SortFunc(entries, func(a, b ResourceEntry) int {
			return cmp.Compare(a.RouteFamily, b.RouteFamily)
		})
		out.Resources = entries
	}

	if len(b.Decisions) > 0 {
		entries := make([]DecisionEntry, len(b.Decisions))
		copy(entries, b.Decisions)
		slices.SortFunc(entries, func(a, b DecisionEntry) int {
			return cmp.Compare(a.RouteFamily, b.RouteFamily)
		})
		out.Decisions = entries
	}

	return out, nil
}

// canonicalizeResourceEntry re-marshals the two nested JSON payloads a
// resources row carries, Wrapper and FilterMap.
//
// Unlike OverrideEntry.FailDirective and EndpointEntry.ReqSchema — the two
// payloads this package deliberately leaves alone — neither of these is a
// column whose bytes some other write path preserves verbatim: the confirm
// path marshals both from Go values through internal/jsonx, so a
// canonicalising pass here can only ever agree with what that path
// produced. What it buys is that a row written any OTHER way (a hand-run
// INSERT, a future importer) cannot make two otherwise-identical snapshots
// encode to different bytes, which is §17's whole reason for canonicalizing
// anything at all.
func canonicalizeResourceEntry(e ResourceEntry) (ResourceEntry, error) {
	var err error
	if e.Wrapper, err = canonicalizeRaw(e.Wrapper); err != nil {
		return ResourceEntry{}, fmt.Errorf("wrapper: %w", err)
	}
	if e.FilterMap, err = canonicalizeRaw(e.FilterMap); err != nil {
		return ResourceEntry{}, fmt.Errorf("filterMap: %w", err)
	}
	return e, nil
}

// canonicalizeOverrideEntry canonicalises every Variant in e.Responses.
// FailDirective is untouched — see its field comment on OverrideEntry for
// why it is the one nested payload this package deliberately does NOT
// re-marshal.
func canonicalizeOverrideEntry(e OverrideEntry) (OverrideEntry, error) {
	if len(e.Responses) == 0 {
		return e, nil
	}
	responses := make(map[string]overrides.Variant, len(e.Responses))
	for status, v := range e.Responses {
		cv, err := canonicalizeVariant(v)
		if err != nil {
			return OverrideEntry{}, fmt.Errorf("responses[%s]: %w", status, err)
		}
		responses[status] = cv
	}
	e.Responses = responses
	return e, nil
}

// canonicalizeEndpointEntry canonicalises every Variant in e.Responses,
// mirroring canonicalizeOverrideEntry exactly (same map type, same
// per-status canonicalizeVariant call). ReqSchema and FailDirective are
// BOTH left untouched — the identical "preserved, never re-marshalled"
// reason FailDirective is skipped for on OverrideEntry applies to both
// fields here: customep.Row's own doc comment calls ReqSchema "copied
// byte-for-byte... never re-encoded through encoding/json", the same rule
// stated for FailDirective one field below it, so this package has no
// reason to apply a canonicalisation pass the owning table's own write
// path never applies to either one.
func canonicalizeEndpointEntry(e EndpointEntry) (EndpointEntry, error) {
	// The stream document is INTERPRETED, not preserved, so it is
	// canonicalised like a response body: two snapshots of the same row
	// must encode to the same bytes regardless of the key order a client's
	// PUT happened to send.
	if e.Kind == "" {
		e.Kind = "http"
	}
	if len(e.Operation) > 0 && !isJSONNull(e.Operation) {
		canon, cerr := canonicalizeRaw(e.Operation)
		if cerr != nil {
			return EndpointEntry{}, fmt.Errorf("operation: %w", cerr)
		}
		e.Operation = canon
	}
	if !isJSONNull(e.Stream) {
		canon, err := canonicalizeRaw(e.Stream)
		if err != nil {
			return EndpointEntry{}, fmt.Errorf("stream: %w", err)
		}
		e.Stream = canon
	}
	if len(e.Responses) == 0 {
		return e, nil
	}
	responses := make(map[string]overrides.Variant, len(e.Responses))
	for status, v := range e.Responses {
		cv, err := canonicalizeVariant(v)
		if err != nil {
			return EndpointEntry{}, fmt.Errorf("responses[%s]: %w", status, err)
		}
		responses[status] = cv
	}
	e.Responses = responses
	return e, nil
}

// canonicalizeVariant re-marshals v.Body and v.SchemaPatch, plus every
// bound recipe's Data and Claims payloads (recipes.Recipe carries its own
// nested RawMessage fields — the same "arrived however a client's PUT
// happened to send it" problem one level deeper). v.Headers and v.When are
// already structured Go values (map[string]string and []Condition), which
// jsonx.Marshal already renders deterministically on its own — a
// map[string]T is key-sorted and a []T keeps its own slice order, neither
// of which needs a decode/encode pass to become stable.
func canonicalizeVariant(v overrides.Variant) (overrides.Variant, error) {
	out := v
	var err error

	if out.Body, err = canonicalizeRaw(v.Body); err != nil {
		return overrides.Variant{}, fmt.Errorf("body: %w", err)
	}
	if out.SchemaPatch, err = canonicalizeRaw(v.SchemaPatch); err != nil {
		return overrides.Variant{}, fmt.Errorf("schemaPatch: %w", err)
	}
	// P7a: the inline response schema is INTERPRETED (custom_schema.go
	// decodes and generates from it), so it is canonicalized like the two
	// above — two snapshots of one row must not differ by key order.
	if out.Schema, err = canonicalizeRaw(v.Schema); err != nil {
		return overrides.Variant{}, fmt.Errorf("schema: %w", err)
	}

	if len(v.Recipes) == 0 {
		return out, nil
	}
	canonRecipes := make(map[string]recipes.Recipe, len(v.Recipes))
	for pattern, rec := range v.Recipes {
		cr := rec
		if cr.Data, err = canonicalizeRaw(rec.Data); err != nil {
			return overrides.Variant{}, fmt.Errorf("recipe %q: value: %w", pattern, err)
		}
		if cr.Claims, err = canonicalizeRaw(rec.Claims); err != nil {
			return overrides.Variant{}, fmt.Errorf("recipe %q: claims: %w", pattern, err)
		}
		canonRecipes[pattern] = cr
	}
	out.Recipes = canonRecipes
	return out, nil
}

// canonicalizeRaw decodes raw with json.Number preserved (UseNumber — a
// plain decode into `any` would turn every number into float64, silently
// losing precision on an integer ID past 2^53, and this is a canonicalising
// PASS-THROUGH, not a place that should ever change a value's meaning) and
// re-encodes it. jsonx.Marshal key-sorts any map it is given (encoding/json
// does this unconditionally, and [jsonx]'s own stability test pins that any
// configured backend must too), so decoding into `any` and marshalling
// back is what turns "whatever order a client's PUT arrived in" into a
// deterministic order — the actual mechanism A19 asks for.
//
// An empty/nil raw is returned unchanged rather than round-tripped: there
// is no JSON to canonicalise, and running an empty byte slice through
// jsonx.NewDecoder would just fail with an EOF this function would then
// have to special-case right back into "leave it alone" anyway.
func canonicalizeRaw(raw jsonx.RawMessage) (jsonx.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	dec := jsonx.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	out, err := jsonx.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonx.RawMessage(out), nil
}
