package domain

import (
	"errors"

	"github.com/yashok111/mocker/internal/jsonx"
)

// preview.go holds the types that cross the mockplane/admin boundary for the
// preview route (P2f, DESIGN's "POST .../preview"): what the mock plane's
// response assembly produces (AssembledResponse, consumed by both the real
// serving path and Preview) and what Preview itself takes and returns
// (PreviewRequest/PreviewResult). They live here rather than in
// internal/mockplane or internal/admin because both planes already import
// this package and neither may import the other (A9) — and here, not
// somewhere new, because this file's own package doc comment already claims
// "the types shared by both planes" as its job.

// StatusSource names which rule in the status precedence
// (internal/mockplane/respond.go's own "THE STATUS" comment) actually chose
// the status a response answers with. It is a RETURN value from
// [resolveVariant] in internal/mockplane, never inferred by a caller: when
// the requested status, active_status and a when[] match all resolve to the
// same number the result is identical and only the function that chose knows
// which rule chose it.
type StatusSource string

const (
	// StatusSourceRequested is Preview's own optional Status field (an
	// operator picked a status tab) — the one source with no serving-path
	// counterpart, because serving never lets a request name its own status;
	// only a live-state force does, and Preview never consults that layer.
	StatusSourceRequested StatusSource = "requested"
	// StatusSourceWhen is overrides.SelectWhen matching a when[] condition
	// against an active override row.
	StatusSourceWhen StatusSource = "when"
	// StatusSourceActive is an active override row's own active_status.
	StatusSourceActive StatusSource = "active"
	// StatusSourceDefault is the document's own choice: chooseVariant's
	// DESIGN §7 priority ran with nothing above it.
	StatusSourceDefault StatusSource = "default"
)

// RefusedReason is why the assembly answered no body at all for a request
// that otherwise resolved to a real variant — the typed form of what
// serveGenerated today only logs. Reason is a CLOSED vocabulary
// ("browser_executable_media_type" | "pinned_body_too_large" |
// "pinned_body_undecodable" | "generation_failed"): the admin route and the
// UI panel both switch on it by exact string, so widening it is a contract
// change, not a log-message edit.
type RefusedReason struct {
	Reason string
	Detail string
}

// AssembledResponse is what [Plane.assembleResponse] produces and the two
// callers — serveGenerated's writer half and Plane.Preview — each consume
// their own way: the writer puts Status/MediaType/Headers/Body on the wire
// and sleeps EffectiveDelay before it ever gets here; Preview reports every
// field verbatim as the result document DESIGN's preview route promises.
type AssembledResponse struct {
	Status    int
	MediaType string
	Headers   map[string]string
	Body      []byte

	// WriteMediaType exists because the writer cannot re-derive it from Body
	// alone: an empty body still sets the declared Content-Type when
	// generation FAILED, but writes no Content-Type at all when the variant
	// simply carries no body on this response (a browser-executable refusal
	// or an oversized pinned body, neither of which sets a generation error).
	WriteMediaType bool

	// Refused is nil unless the body-generation switch hit one of
	// RefusedReason's four rows — the same four cases that leave Body empty
	// and WriteMediaType is derived from.
	Refused *RefusedReason

	// AssetMissing is true when a pinned variant's bodyRef named an asset
	// that could not be served (A6): Body is empty, Status is the
	// variant's own. The writer's own lookup already marked the traffic;
	// Preview, which has no lookup, maps this to PreviewResult.Notes.
	AssetMissing bool

	// EffectiveDelay is the delay this operation would pay, computed by
	// [effectiveDelayMs] exactly as the writer already does — but never
	// slept here. The writer sleeps it before calling assembleResponse at
	// all; Preview only ever reports the number.
	EffectiveDelay int

	// SchemaPatchApplied and RecipesBound are known only inside
	// assembleResponse — lookupPatchedSchema and lookupRecipes are called
	// there, and a caller that had to re-run them to learn this would be
	// doing the lookup this whole split exists to avoid.
	SchemaPatchApplied bool
	RecipesBound       int
}

// The five sentinels below are D6/D12's rows 2-5a of the preview taxonomy:
// the cases [mockplane.Plane.Preview] refuses to render ANYTHING for. The
// first three (custom_endpoint_wins, no_spec, operation_not_found) are
// decided BEFORE a variant is even resolved, so no PreviewResult field
// could carry them — every PreviewResult field describes a RENDERED
// response (RefusedReason, just above, is a DIFFERENT thing: a resolved
// variant whose BODY generation failed). The other two —
// ErrPreviewResourceServes and ErrPreviewMissingPathParam — are, since
// P3a's hoist of resolveVariant above both checks (D12), decided AFTER a
// variant is resolved: resolveVariant reads no path parameter, so its
// hoist changes nothing about WHETHER these two still refuse a rendered
// PreviewResult; it only means "before/after a variant is resolved" is no
// longer what separates the five from the four RefusedReason rows — it
// never separated them from the D5/D8 fields (NoBody, RouteOff, DelayMs,
// ...) either, which cover the cases a variant WAS resolved but has
// nothing else worth reporting.
//
// They live here, in internal/domain, rather than in internal/mockplane,
// for the identical reason PreviewRequest/PreviewResult do (this file's own
// top comment): internal/admin's handler must map each to D6/D12's own HTTP
// status (409/400/404/400/409) and cannot import internal/mockplane to do
// that (A9). The WIRE code strings the admin route actually answers with
// are chosen independently, at the handler's own call site — "admin-local
// strings", matching how unsupported_media_type and rate_limited already
// live there — these sentinels exist only so the handler can BRANCH on
// which of the five happened, through the identical errors.Is idiom
// internal/admin already uses for overrides.ErrInvalidOpKey and
// overrides.ErrWorkspaceNotFound at the same kind of call site
// (internal/admin/override_handlers.go's handlePutOperation).
//
// ErrPreviewMissingPathParam is returned wrapped — fmt.Errorf("%w: %s",
// ErrPreviewMissingPathParam, paramName) — so errors.Is still finds it
// while the parameter name DESIGN's own row 5 ("naming the parameter")
// requires travels in the error's own text, the same %w-plus-detail shape
// internal/overrides.ErrInvalidOpKey already uses.
var (
	// ErrPreviewCustomEndpointWins is D6 row 2: the located route is a
	// custom endpoint, or an enabled one occupies the same (method,
	// canonical path) and would outrank the spec operation at match time.
	ErrPreviewCustomEndpointWins = errors.New("mockplane: preview: an enabled custom endpoint outranks this operation")
	// ErrPreviewNoSpec is D6 row 3: the workspace has no spec attached at
	// all, so opKey cannot name any operation.
	ErrPreviewNoSpec = errors.New("mockplane: preview: workspace has no spec attached")
	// ErrPreviewOperationNotFound is D6 row 4: a spec IS attached, but
	// opKey's (method, path) names no operation in it.
	ErrPreviewOperationNotFound = errors.New("mockplane: preview: opKey names no operation in the spec")
	// ErrPreviewMissingPathParam is D6 row 5: the located route declares a
	// path parameter that req.PathParams does not carry, or carries empty.
	ErrPreviewMissingPathParam = errors.New("mockplane: preview: missing path parameter")
	// ErrPreviewResourceServes is D12's row 5a, checked immediately after
	// resolveVariant and BEFORE ErrPreviewMissingPathParam: the located
	// route belongs to a CONFIRMED resource (internal/resources) and names
	// a method P3a takes over at serving time — GET X, GET X/{}, DELETE
	// X/{} when the spec declares it, or POST X when write_form is "bare"
	// — unless the chosen variant is pinned or not 2xx, in which case D12
	// keeps the draft previewing normally so the endpoints editor stays
	// usable for authoring that one pin (D11). Deliberately an
	// OVER-approximation beyond that one exclusion: route_off, a non-JSON
	// or absent media type, a degraded operation and a non-numeric
	// selector on a write verb all still answer this sentinel, because the
	// question being answered is eligibility — "is this route mine to
	// draft for" — not the per-request answer [mockplane.resourceBranch]
	// would give for the same draft saved and actually requested.
	ErrPreviewResourceServes = errors.New("mockplane: preview: route is served from a confirmed resource")
)

// PreviewRequest is the admin-plane POST .../preview body, carried across
// into internal/mockplane. Draft and Body cross as raw JSON — this package
// must not name internal/overrides' Row/Variant types (A10) — and OpKey is
// the same percent-encoded form MergedOperationView.OpKey already is
// (internal/admin's own contract).
type PreviewRequest struct {
	OpKey      string
	Draft      jsonx.RawMessage
	Status     *int
	Query      string
	Headers    map[string]string
	Body       jsonx.RawMessage
	PathParams map[string]string

	// AssetBase is the absolute prefix an asset_url recipe writes a name
	// after (A6 D7), computed by the admin preview handler from ITS request
	// through httpx.WorkspaceURL — the one construction the mock plane
	// also uses — so a previewed body carries the same URL a real request
	// on the same deployment would; "" declines the recipe.
	AssetBase string
}

// PreviewResult is Plane.Preview's whole answer — the document the admin
// route re-encodes as JSON and the UI panel renders verbatim. Presence
// rules: Body is absent (nil) whenever NoBody, RouteOff or Refused is set,
// and those three are mutually exclusive; Encoding is present exactly when
// Body is; Status and StatusSource are always present, RouteOff included — a
// disabled route still has a status the plane would have used had it been
// enabled; ShadowedBy is "" when no scenario is active or the active one
// carries no row for this key.
type PreviewResult struct {
	Status       int
	StatusSource StatusSource
	MediaType    string
	Headers      map[string]string

	// Encoding is "utf8" when Body is valid UTF-8 and "base64" otherwise —
	// F12's base64 pinned bodies and any binary text/* value.
	Encoding string
	Body     []byte

	NoBody   bool
	RouteOff bool
	Refused  *RefusedReason

	SchemaPatchApplied bool
	RecipesBound       int
	DelayMs            int

	// ShadowedBy names the scenario whose snapshot supplied the composed
	// row this result was built from, "" when no scenario supplied one.
	ShadowedBy string

	// Notes carries the traffic-note tokens a real request would have
	// recorded for this response, comma-joined, "" when none — A6 adds it
	// for asset_missing: a previewed bodyRef variant never reads the asset
	// store, so the preview answers NoBody with this note, and the operator
	// learns why the picture would be blank in the same vocabulary the
	// traffic screen uses.
	Notes string
}
