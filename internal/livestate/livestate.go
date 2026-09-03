// Package livestate implements DESIGN §4's "Session" layer and §12's
// session-слой: the "прямо сейчас" overlay that forces a status, burns down
// a fail counter, holds a response for a fixed delay or parks it on a pause,
// on top of whatever op_overrides and the runtime would otherwise serve.
//
// It lives OUTSIDE the runtime cache (a rebuild must not resurrect a stale
// counter) and OUTSIDE SQLite (DESIGN §12: the alternatives are a
// synchronous INSERT per request — the thing §18 exists to remove — or
// bumping workspaces.revision hundreds of times a second). The remainder of
// a fail counter therefore lives ONLY in the *Store this package defines,
// and Apply is the one call the serving path makes to consume it.
//
// LEAF PACKAGE: stdlib only. Never internal/store, internal/workspaces or
// database/sql — this package holds no database handle, writes nothing to
// disk, and knows nothing about HTTP. Three planes call into it (the mock
// plane's serving path, POST {prefix}/state, and the admin session API);
// none of them may become an import cycle by having this package reach back
// up to any of them.
package livestate

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Action names one of the four directives DESIGN §14 defines. All four are
// live: status and fail since P1c2, delay and pause since P2a.
//
// Pause is not "delay but longer", and the difference is why this package
// grew a channel at all. A delay is a bounded sleep the handler goroutine
// can simply do; a pause has no natural end without a second signal, so
// something must wake every held request when the operator releases it.
// That signal is the per-workspace wake channel Set, Clear, Sweep and Close
// close (see Store), plus the park accounting on Effect.
//
// What this package deliberately does NOT do is wait. It starts no
// goroutine and blocks nobody: it hands the serving path a channel, a
// non-consuming Recheck and an Unpark, and the plane — which is the only
// layer that can see a request's context and its deadline — owns the loop.
// A leaf package that parked goroutines itself would hold them past the
// request that asked for it and past its own Close.
type Action string

const (
	ActionStatus Action = "status" // answer Status instead, until cleared
	ActionFail   Action = "fail"   // answer Status for the next N requests, or exactly once
	ActionDelay  Action = "delay"  // hold the response Ms milliseconds, then serve it normally
	ActionPause  Action = "pause"  // hold the response until it is cleared, or MaxPauseHold
)

// Target addresses one operation, or every operation in the workspace.
//
// Target is comparable (no slices, no maps) on purpose: Store keys its
// directives by Target plus Action, and a comparable struct is a map key
// with no extra plumbing.
type Target struct {
	All    bool   // the "*" target
	Method string // upper case; ignored when All
	Path   string // RELATIVE path, no base path — byte-identical to op_overrides.path; ignored when All
}

// MarshalJSON renders the "*" target as the bare string "*" and every other
// target as {"method","path"} — the union DESIGN §14's session endpoints
// and the admin session API both speak on the wire.
func (t Target) MarshalJSON() ([]byte, error) {
	if t.All {
		return jsonx.Marshal("*")
	}
	return jsonx.Marshal(struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}{t.Method, t.Path})
}

// UnmarshalJSON accepts exactly the two shapes MarshalJSON produces and
// rejects everything else with ErrInvalidDirective — including a string
// that is not "*", which is a mistake worth naming rather than silently
// treating as an object decode failure.
func (t *Target) UnmarshalJSON(data []byte) error {
	var s string
	if err := jsonx.Unmarshal(data, &s); err == nil {
		if s != "*" {
			return fmt.Errorf("%w: target string must be \"*\", got %q", ErrInvalidDirective, s)
		}
		*t = Target{All: true}
		return nil
	}

	var obj struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	if err := jsonx.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("%w: target must be \"*\" or {method,path}: %w", ErrInvalidDirective, err)
	}
	*t = Target{Method: obj.Method, Path: obj.Path}
	return nil
}

// Directive is one stored instruction. N is the REMAINDER, not the
// original: the UI and the router both read the counter from the same
// Store, so a directive fetched back out always shows how many forced
// responses are left, never how many were asked for at Set time.
//
// THE WIRE SHAPE IS THIS PACKAGE'S, NOT THE HANDLERS'. Two handlers accept
// the identical JSON — POST {prefix}/state on the mock plane and POST
// /api/workspaces/{id}/session on the admin plane — and both decode
// straight into a Directive so they cannot drift on a field name, a
// default, or the "*" union:
//
//	{"target":{"method":"POST","path":"/auth/login"} | "*",
//	 "action":"status"|"fail"|"delay"|"pause",
//	 "status":503,        // status|fail ONLY, 100..599 and required there;
//	                      // on delay|pause it must be absent, and a non-zero
//	                      // one is REJECTED rather than ignored
//	 "ms":300,            // delay ONLY, 1..30000 and required there;
//	                      // on every other action a non-zero one is REJECTED
//	 "once":false,"n":0,  // fail only
//	 "setAt":"2026-08-18T12:00:00Z"}
//
// Status and Ms carry omitempty, and that is a contract decision rather
// than tidiness: the OpenAPI document the whole frontend client is
// generated from declares minimum:100 for status, so a delay directive read
// back out of GET .../session without omitempty would render "status":0 and
// the server would be contradicting its own schema.
type Directive struct {
	Target Target    `json:"target"`
	Action Action    `json:"action"`
	Status int       `json:"status,omitempty"` // 100..599; ActionStatus and ActionFail only
	Ms     int       `json:"ms,omitempty"`     // 1..maxDelayMs milliseconds to hold the response; ActionDelay only
	Once   bool      `json:"once"`             // ActionFail only: fire exactly once
	N      int       `json:"n"`                // ActionFail: how many requests are still to fail; Once implies 1
	SetAt  time.Time `json:"setAt"`

	// Scenario is decoded but NEVER stored — Set does not look at it and
	// this package gains no notion of scenarios; they are rows in SQLite
	// (internal/scenarios), this layer is RAM only, and the two never mix.
	// DESIGN §12 line 534's scenario switch is a TOP-LEVEL KEY on this same
	// endpoint, not a third Action. Without this field, encoding/json would
	// drop an unrecognised "scenario" key silently and both planes would
	// answer "400 unknown action" for it — a handler cannot route on a key
	// it never saw.
	//
	// As of P2b, a handler decodes into Directive, checks HasScenario, and
	// BRANCHES rather than refusing: the mock plane (internal/mockplane
	// /livestate.go) hands Scenario to a real activate/deactivate/404
	// switch over the workspaces table, while the admin session route
	// (internal/admin/livestate_handlers.go, A17) answers 400 — DESIGN
	// §14's session body never named this key at all, so the admin plane
	// has no switch of its own to offer here; the scenario has a dedicated
	// route pair instead. Neither surface still answers the 501 this
	// comment used to describe; do not reintroduce it by pattern-matching
	// this comment's old wording rather than the two handlers themselves.
	Scenario jsonx.RawMessage `json:"scenario,omitempty"`
}

// HasScenario reports whether the decoded JSON actually carried a
// "scenario" key with real content, as opposed to the key being absent or
// present as a literal `"scenario":null`.
//
// jsonx.RawMessage cannot tell "absent" and "present but null" apart by
// nilness alone: encoding/json still invokes RawMessage's UnmarshalJSON for
// an explicit null, so {"action":"status","scenario":null} leaves Scenario
// holding the four bytes "null" — non-nil, non-empty. A caller that 501s on
// "len(Scenario) > 0" would therefore refuse an otherwise valid status/fail
// directive from any client that always emits every field it knows about.
// HasScenario trims and compares against that literal so only real content
// counts as a request to switch scenarios.
func (d Directive) HasScenario() bool {
	trimmed := bytes.TrimSpace(d.Scenario)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// Effect is what the serving path must do for ONE request. Every one of the
// four actions reports here, resolved INDEPENDENTLY of the others (see
// [Store.Apply]): a status and a delay and a pause can all be in force at
// once, and none of them hides another.
//
// The last three fields exist because the mock plane reaches this package
// only through its own four-method interface (mockplane.LiveStateSource,
// Apply/Set/List/Clear). A consumer cannot call what its interface does not
// declare, so everything a parked request needs rides out on the Effect
// Apply already returns rather than on a fifth method the plane would have
// to grow first.
//
// Recheck and Unpark belong to the ONE goroutine that received this Effect
// and are safe to call in any order; Unpark after Recheck already released
// the slot is a no-op, which is what makes `defer eff.Unpark()` correct
// next to a wait loop that calls Recheck on every wakeup.
type Effect struct {
	Status  int // 0 means no status or fail directive matched
	DelayMs int // 0 means no delay directive matched

	// Pause is set only when a pause directive matched AND a park slot was
	// reserved for this request; Refused, only when one matched and the
	// workspace already held MaxPausedPerWorkspace parked requests. Refused
	// is served normally (rule 7): a count is the only bound on a plane
	// that is unauthenticated by design and carries no rate limit.
	Pause   bool
	Refused bool

	// Wake is closed when ANY directive of this workspace changes — Set,
	// Clear, Sweep and Close all close it. It is a CHANGE signal, not a
	// "your pause was lifted" signal: on waking, the plane must re-evaluate
	// through Recheck. nil unless Pause.
	Wake <-chan struct{}

	// Recheck re-evaluates the pause after a wakeup WITHOUT consuming
	// anything, releasing this request's park slot and re-reserving one
	// (atomically, so it never holds two) only while a pause still matches.
	// It returns the channel to park on next.
	//
	// It exists because Apply CONSUMES: Apply decrements a fail counter and
	// is documented by the plane as running exactly once per request. A
	// parked request that re-called Apply on every spurious wakeup would
	// burn one unit of an armed `fail n=N` per wakeup for responses nobody
	// ever received — a single unrelated Set would drain a 32-deep counter,
	// and a pause+fail pair would lose its forced status entirely.
	//
	// nil unless Pause.
	Recheck func() (stillPaused bool, wake <-chan struct{})

	// Unpark releases the park slot and ends this request's wait for good:
	// IDEMPOTENT, and a Recheck after it reports not-paused and reserves
	// nothing. nil unless Pause.
	Unpark func()
}

// ErrInvalidDirective is returned by Set when a Directive fails validation:
// an unknown action; a status/fail directive whose status is outside
// [100,599]; a delay/pause directive carrying a status at all; a delay
// whose ms is outside [1,30000]; any other action carrying an ms; a
// fail action with n<=0 and once=false; or a non-All target with an empty
// method or a path that does not start with "/".
//
// The field rules are per action and REJECT rather than ignore, because the
// two things an operator can get wrong here are silent otherwise: a "delay"
// that also carries a status would look like it forces one, and a "pause"
// that carries ms would look like it ends on its own. Both are worth a 400
// naming the field.
var ErrInvalidDirective = errors.New("livestate: invalid directive")

// ErrTooManyDirectives is returned by Set when a workspace already holds
// MaxDirectivesPerWorkspace distinct (Target, Action) directives and the
// call would add a new one rather than replace an existing one.
//
// Pinned here, not left for each caller to invent, because POST
// {prefix}/state is UNAUTHENTICATED by design (DESIGN §12: "переключение из
// тестов", §15: "мок открыт") — an open endpoint that grows a map forever
// is a memory leak with a URL, and both planes answer this the same way:
// 409 Conflict (httpx.CodeConflict on the admin side).
var ErrTooManyDirectives = errors.New("livestate: too many directives for this workspace")

// maxTargetMethodLen and maxTargetPathLen bound a non-All Target's two
// strings. Round-1 review finding 4 (blocker): normalize bounded Status and
// the fail counter but never these — and {prefix}/state is UNAUTHENTICATED
// by design (this file's own header comment), so a caller with no
// credentials at all could Set MaxDirectivesPerWorkspace directives each
// carrying an arbitrarily large Path, pinning that many bytes in RAM per
// workspace for up to TTL+janitor (measured: 63 one-MiB-path directives
// retained ~63.5 MiB, with MOCKER_MAX_BODY as the only real ceiling — tens
// to hundreds of MB depending on how an operator has it dialed), AND every
// later GET {prefix}/state or admin session read echoes the whole thing
// back in one response with no size gate of its own on that path. No
// legitimate route or method DESIGN ever produces comes anywhere close to
// either bound: an HTTP method name is a handful of ASCII letters, and every
// real path in this system is bounded by what SQLite's own op_overrides.path
// column and a spec's own operation paths hold (§13), never anything
// resembling a multi-kilobyte string.
const (
	maxTargetMethodLen = 16
	maxTargetPathLen   = 2 << 10 // 2 KiB
)

// MaxDirectiveBodyBytes is the directive-sized http.MaxBytesReader cap the
// two HTTP handlers that accept a raw Directive body — POST {prefix}/state
// on the mock plane (UNAUTHENTICATED) and POST .../session on the admin
// plane — apply BEFORE decoding, per round-1 review finding 4's own
// suggested fix. It exists on top of maxTargetPathLen/maxTargetMethodLen,
// not instead of them: normalize's bound only ever fires AFTER a full
// jsonx.Decode has already read (and, for Scenario, retained as raw bytes)
// whatever the client sent, so an oversized body would still cost a full
// read-and-allocate before being rejected without a cap at the transport
// boundary too. 32 KiB comfortably covers the worst case a VALID directive
// can produce — a maxTargetPathLen path JSON-escaped byte-for-byte as
// "\u00XX" sequences is at most 6x its raw length, well under half this
// budget, leaving headroom for the rest of the object — while sitting
// nowhere near MOCKER_MAX_BODY's own default (10 MB), the general ceiling
// httpx.MaxBody already applies to every request on both planes and which
// this finding's own measurement showed is not tight enough on its own for
// a directive body specifically.
const MaxDirectiveBodyBytes = 32 << 10

// maxDelayMs bounds an ActionDelay directive's Ms. It matches the mock
// plane's own maxSimulatedDelay (30s), which clamps whatever delay it is
// handed: accepting a bigger one here would only let List hand back a
// number the server has already decided not to honour. 30s is well past any
// frontend loading-state test and still short of a typical client or proxy
// timeout.
const maxDelayMs = 30_000

// normalize validates d and rewrites it into the canonical form Store
// stores and later hands back out of List:
//   - an All target has its Method/Path zeroed, so two "*" directives
//     built from different call sites still hash to the same map key;
//   - a non-All target's Method is upper-cased, so a directive built by
//     hand with a lower-case method still matches Apply's r.Method;
//   - every action but ActionFail carries Once=false, N=0 — those fields
//     are the fail counter's alone, and leaving whatever the caller passed
//     would make List show a counter that means nothing;
//   - once:true always normalizes N to 1, so Apply never needs to treat
//     Once as a second code path: decrementing N to zero is the only rule
//     it has to implement.
//
// The field validation itself lives in validateActionFields because it is
// ACTION-SCOPED: with four actions, "which of status and ms does this one
// require, and which does it forbid" is a table, and inlining it here put
// normalize over the gocyclo bar for no gain in readability.
func normalize(d Directive) (Directive, error) {
	if err := validateActionFields(d); err != nil {
		return Directive{}, err
	}

	if d.Target.All {
		d.Target.Method = ""
		d.Target.Path = ""
	} else {
		if err := validateTarget(d.Target); err != nil {
			return Directive{}, err
		}
		d.Target.Method = strings.ToUpper(d.Target.Method)
	}

	if d.Action == ActionFail {
		if d.Once {
			d.N = 1
		}
	} else {
		d.Once = false
		d.N = 0
	}
	return d, nil
}

// validateActionFields enforces which fields each action requires and which
// it forbids. Forbidden means REJECTED, never quietly zeroed: a "delay"
// carrying a status reads to its author like it forces one, and a "pause"
// carrying ms reads like it ends on its own. Both are worth a 400 that
// names the field over a directive that silently does less than it says.
func validateActionFields(d Directive) error {
	switch d.Action {
	case ActionStatus, ActionFail:
		if d.Status < 100 || d.Status > 599 {
			return fmt.Errorf("%w: status %d is not in [100,599]", ErrInvalidDirective, d.Status)
		}
	case ActionDelay, ActionPause:
		if d.Status != 0 {
			return fmt.Errorf("%w: action %q takes no status, got %d", ErrInvalidDirective, d.Action, d.Status)
		}
	default:
		return fmt.Errorf("%w: unknown action %q", ErrInvalidDirective, d.Action)
	}

	if d.Action == ActionDelay {
		if d.Ms < 1 || d.Ms > maxDelayMs {
			return fmt.Errorf("%w: delay ms %d is not in [1,%d]", ErrInvalidDirective, d.Ms, maxDelayMs)
		}
	} else if d.Ms != 0 {
		return fmt.Errorf("%w: action %q takes no ms, got %d", ErrInvalidDirective, d.Action, d.Ms)
	}

	if d.Action == ActionFail && d.N <= 0 && !d.Once {
		return fmt.Errorf("%w: fail action needs n>0 or once=true", ErrInvalidDirective)
	}
	return nil
}

// validateTarget bounds a non-All target's two strings. Split out of
// normalize alongside validateActionFields; see maxTargetMethodLen for why
// the length bounds exist at all.
func validateTarget(t Target) error {
	if t.Method == "" {
		return fmt.Errorf("%w: target method is empty", ErrInvalidDirective)
	}
	if len(t.Method) > maxTargetMethodLen {
		return fmt.Errorf("%w: target method is %d bytes, over the %d-byte limit",
			ErrInvalidDirective, len(t.Method), maxTargetMethodLen)
	}
	if !strings.HasPrefix(t.Path, "/") {
		return fmt.Errorf("%w: target path %q must start with \"/\"", ErrInvalidDirective, t.Path)
	}
	if len(t.Path) > maxTargetPathLen {
		return fmt.Errorf("%w: target path is %d bytes, over the %d-byte limit",
			ErrInvalidDirective, len(t.Path), maxTargetPathLen)
	}
	return nil
}
