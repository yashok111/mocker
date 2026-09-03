package livestate

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestTarget_JSONRoundTrip covers the union MarshalJSON/UnmarshalJSON must
// speak: the bare string "*" for All, and {"method","path"} otherwise. Both
// planes (mock and admin) decode straight into Target, so a mismatch here
// is a mismatch on the wire for both of them at once.
func TestTarget_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   string
	}{
		{"all", Target{All: true}, `"*"`},
		{"all ignores method and path", Target{All: true, Method: "GET", Path: "/x"}, `"*"`},
		{"method and path", Target{Method: "POST", Path: "/auth/login"}, `{"method":"POST","path":"/auth/login"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.target)
			if err != nil {
				t.Fatalf("Marshal(%+v): %v", tt.target, err)
			}
			if string(got) != tt.want {
				t.Fatalf("Marshal(%+v) = %s, want %s", tt.target, got, tt.want)
			}

			var back Target
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("Unmarshal(%s): %v", got, err)
			}
			want := tt.target
			if want.All {
				want.Method, want.Path = "", "" // MarshalJSON drops these for All; round trip can't recover them
			}
			if back != want {
				t.Fatalf("round trip = %+v, want %+v", back, want)
			}
		})
	}
}

// TestTarget_UnmarshalJSON_Invalid covers the shapes UnmarshalJSON must
// reject: any string other than "*", and anything that is neither a string
// nor an object.
func TestTarget_UnmarshalJSON_Invalid(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"wrong string", `"all"`},
		{"empty string", `""`},
		{"number", `42`},
		{"array", `["*"]`},
		{"null", `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var target Target
			err := json.Unmarshal([]byte(tt.data), &target)
			if !errors.Is(err, ErrInvalidDirective) {
				t.Fatalf("Unmarshal(%s) error = %v, want ErrInvalidDirective", tt.data, err)
			}
		})
	}
}

// TestDirective_UnmarshalJSON_WireShape decodes exactly the example DESIGN
// §14 and the task both give, into the struct both HTTP handlers decode
// into. If this ever needs a rename or a different default, every caller
// of this package needs to know.
func TestDirective_UnmarshalJSON_WireShape(t *testing.T) {
	const wire = `{"target":{"method":"POST","path":"/auth/login"},
		"action":"status","status":503,"once":false,"n":0,
		"setAt":"2026-08-18T12:00:00Z"}`

	var d Directive
	if err := json.Unmarshal([]byte(wire), &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := Directive{
		Target: Target{Method: "POST", Path: "/auth/login"},
		Action: ActionStatus,
		Status: 503,
		SetAt:  time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}
	if d.Target != want.Target || d.Action != want.Action || d.Status != want.Status ||
		d.Once != want.Once || d.N != want.N || !d.SetAt.Equal(want.SetAt) {
		t.Fatalf("Unmarshal(%s) = %+v, want %+v", wire, d, want)
	}
}

// TestDirective_HasScenario is the case the task calls out by name: a
// literal `"scenario":null` must NOT read as "a scenario was requested",
// only real content should. A bare len(Scenario)>0 check gets this wrong
// because json.RawMessage stores the four bytes "null" for an explicit
// null, same as it would for any other present value.
func TestDirective_HasScenario(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want bool
	}{
		{"absent", `{"action":"status","status":200}`, false},
		{"explicit null", `{"action":"status","status":200,"scenario":null}`, false},
		{"object", `{"action":"status","status":200,"scenario":{"name":"empty-list"}}`, true},
		{"string", `{"action":"status","status":200,"scenario":"empty-list"}`, true},
		{"whitespace only null", `{"action":"status","status":200,"scenario":  null  }`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var d Directive
			if err := json.Unmarshal([]byte(tt.wire), &d); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tt.wire, err)
			}
			if got := d.HasScenario(); got != tt.want {
				t.Fatalf("HasScenario() on %s = %v, want %v (Scenario=%q)", tt.wire, got, tt.want, d.Scenario)
			}
		})
	}
}

// TestStore_Set_Invalid covers every ErrInvalidDirective case the task
// enumerates. Set is the only exported entry point into normalize's
// validation, so these run against a real Store.
func TestStore_Set_Invalid(t *testing.T) {
	tests := []struct {
		name string
		d    Directive
	}{
		{"unknown action", Directive{Target: Target{All: true}, Action: "wait", Status: 200}},
		{"unknown action empty", Directive{Target: Target{All: true}, Action: "", Status: 200}},
		{"status too low", Directive{Target: Target{All: true}, Action: ActionStatus, Status: 99}},
		{"status too high", Directive{Target: Target{All: true}, Action: ActionStatus, Status: 600}},

		// The ACTION-SCOPED field rules, both directions for all four
		// actions. Every one of these is a field the wrong action would
		// otherwise carry silently: a delay that also names a status reads
		// like it forces one, and a pause that carries ms reads like it
		// ends on its own.
		{"status with ms", Directive{Target: Target{All: true}, Action: ActionStatus, Status: 200, Ms: 5}},
		{"fail with ms", Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, N: 1, Ms: 5}},
		{"delay with status", Directive{Target: Target{All: true}, Action: ActionDelay, Status: 200, Ms: 300}},
		{"delay without ms", Directive{Target: Target{All: true}, Action: ActionDelay}},
		{"delay ms negative", Directive{Target: Target{All: true}, Action: ActionDelay, Ms: -1}},
		{"delay ms over the bound", Directive{Target: Target{All: true}, Action: ActionDelay, Ms: maxDelayMs + 1}},
		{"pause with status", Directive{Target: Target{All: true}, Action: ActionPause, Status: 200}},
		{"pause with ms", Directive{Target: Target{All: true}, Action: ActionPause, Ms: 5}},
		{"fail n<=0 not once", Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, N: 0, Once: false}},
		{"fail negative n not once", Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, N: -1, Once: false}},
		{"non-all empty method", Directive{Target: Target{Path: "/x"}, Action: ActionStatus, Status: 200}},
		{"non-all path missing slash", Directive{Target: Target{Method: "GET", Path: "x"}, Action: ActionStatus, Status: 200}},
		{"non-all empty path", Directive{Target: Target{Method: "GET", Path: ""}, Action: ActionStatus, Status: 200}},
		{"target method over the length bound", Directive{Target: Target{Method: strings.Repeat("G", maxTargetMethodLen+1), Path: "/x"}, Action: ActionStatus, Status: 200}},
		{"target path over the length bound", Directive{Target: Target{Method: "GET", Path: "/" + strings.Repeat("a", maxTargetPathLen)}, Action: ActionStatus, Status: 200}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := NewStore(0, nil)
			err := s.Set(1, tt.d)
			if !errors.Is(err, ErrInvalidDirective) {
				t.Fatalf("Set(%+v) error = %v, want ErrInvalidDirective", tt.d, err)
			}
		})
	}
}

// TestStore_Set_Valid_NotRejected is the mirror of the invalid table: every
// shape validation must accept, so a future tightening of normalize has a
// case pinned against it.
func TestStore_Set_Valid_NotRejected(t *testing.T) {
	tests := []struct {
		name string
		d    Directive
	}{
		{"status all", Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503}},
		{"status boundary low", Directive{Target: Target{All: true}, Action: ActionStatus, Status: 100}},
		{"status boundary high", Directive{Target: Target{All: true}, Action: ActionStatus, Status: 599}},
		{"fail with n", Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, N: 3}},
		{"fail once with n=0", Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, Once: true}},
		{"exact target", Directive{Target: Target{Method: "post", Path: "/auth/login"}, Action: ActionStatus, Status: 409}},
		{"delay all", Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 300}},
		{"delay ms boundary low", Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 1}},
		{"delay ms boundary high", Directive{Target: Target{All: true}, Action: ActionDelay, Ms: maxDelayMs}},
		{"delay exact target", Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionDelay, Ms: 300}},
		{"pause all", Directive{Target: Target{All: true}, Action: ActionPause}},
		{"pause exact target", Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionPause}},
		{"target method exactly at the length bound", Directive{Target: Target{Method: strings.Repeat("G", maxTargetMethodLen), Path: "/x"}, Action: ActionStatus, Status: 200}},
		{"target path exactly at the length bound", Directive{Target: Target{Method: "GET", Path: "/" + strings.Repeat("a", maxTargetPathLen-1)}, Action: ActionStatus, Status: 200}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := NewStore(0, nil)
			if err := s.Set(1, tt.d); err != nil {
				t.Fatalf("Set(%+v): unexpected error %v", tt.d, err)
			}
		})
	}
}

// TestStore_Set_NormalizesMethodCase proves the "Method string // upper
// case" invariant is actually enforced, not just documented: Apply matches
// against r.Method, and a stored lower-case method would silently never
// match a real request.
func TestStore_Set_NormalizesMethodCase(t *testing.T) {
	s := NewStore(0, nil)
	if err := s.Set(1, Directive{Target: Target{Method: "post", Path: "/x"}, Action: ActionStatus, Status: 503}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := s.List(1)
	if len(got) != 1 || got[0].Target.Method != http.MethodPost {
		t.Fatalf("List = %+v, want a single directive with Target.Method == POST", got)
	}
}

// TestStore_Set_StatusIgnoresOnceAndN proves a status directive's Once/N
// are normalized away: those fields belong to ActionFail's counter, and a
// caller sending stray values (e.g. a form that always posts every field)
// must not leave a confusing "n:5" sitting next to an action that has no
// notion of a counter.
func TestStore_Set_StatusIgnoresOnceAndN(t *testing.T) {
	s := NewStore(0, nil)
	err := s.Set(1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503, Once: true, N: 5})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := s.List(1)
	if len(got) != 1 {
		t.Fatalf("List = %+v, want exactly one directive", got)
	}
	if got[0].Once || got[0].N != 0 {
		t.Fatalf("List[0] = %+v, want Once=false N=0", got[0])
	}
}

// TestStore_Set_OnceForcesN1 proves "Once implies 1": Apply's decrement
// logic has no special case for Once at all, it only ever looks at N, so
// this normalization is the only place that guarantee is enforced.
func TestStore_Set_OnceForcesN1(t *testing.T) {
	s := NewStore(0, nil)
	err := s.Set(1, Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, Once: true, N: 99})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := s.List(1)
	if len(got) != 1 || got[0].N != 1 {
		t.Fatalf("List = %+v, want a single directive with N == 1", got)
	}
}

// TestDirective_WireShape_DelayAndPause pins the two new actions' shape on
// the wire, in both directions, because two HTTP handlers and a generated
// TypeScript client all speak it.
//
// The omitempty half is the load-bearing one: the OpenAPI document the
// frontend client is generated from declares minimum:100 for status, so a
// delay or pause rendered with "status":0 by GET .../session would be the
// server contradicting its own schema. Same for "ms" on the actions that
// have no delay at all.
func TestDirective_WireShape_DelayAndPause(t *testing.T) {
	t.Parallel()

	const wire = `{"target":{"method":"GET","path":"/widgets"},"action":"delay","ms":300}`
	var d Directive
	if err := json.Unmarshal([]byte(wire), &d); err != nil {
		t.Fatalf("Unmarshal(%s): %v", wire, err)
	}
	if d.Action != ActionDelay || d.Ms != 300 || d.Status != 0 {
		t.Fatalf("Unmarshal(%s) = %+v, want ActionDelay ms=300 status=0", wire, d)
	}

	tests := []struct {
		name        string
		d           Directive
		wantIn      []string
		wantMissing []string
	}{
		{
			name:        "delay renders ms and no status",
			d:           Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 300},
			wantIn:      []string{`"action":"delay"`, `"ms":300`},
			wantMissing: []string{`"status"`},
		},
		{
			name:        "pause renders neither",
			d:           Directive{Target: Target{All: true}, Action: ActionPause},
			wantIn:      []string{`"action":"pause"`},
			wantMissing: []string{`"status"`, `"ms"`},
		},
		{
			name:        "status still renders its status",
			d:           Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503},
			wantIn:      []string{`"action":"status"`, `"status":503`},
			wantMissing: []string{`"ms"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(tt.d)
			if err != nil {
				t.Fatalf("Marshal(%+v): %v", tt.d, err)
			}
			for _, want := range tt.wantIn {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("Marshal(%+v) = %s, want it to contain %s", tt.d, raw, want)
				}
			}
			for _, unwanted := range tt.wantMissing {
				if strings.Contains(string(raw), unwanted) {
					t.Fatalf("Marshal(%+v) = %s, want no %s (omitempty)", tt.d, raw, unwanted)
				}
			}
		})
	}
}

// TestStore_Set_DelayAndPauseIgnoreOnceAndN mirrors
// TestStore_Set_StatusIgnoresOnceAndN for the two new actions: Once and N
// belong to the fail counter alone, so a client that always posts every
// field it knows about must not leave "n:5" sitting next to an action with
// no notion of a counter. Ms and Status are REJECTED on the wrong action
// (see TestStore_Set_Invalid) — these two are merely normalized away,
// exactly as they already were for status.
func TestStore_Set_DelayAndPauseIgnoreOnceAndN(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 300, Once: true, N: 5})
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/x"}, Action: ActionPause, Once: true, N: 5})

	got := s.List(1)
	if len(got) != 2 {
		t.Fatalf("List = %+v, want two directives", got)
	}
	for _, d := range got {
		if d.Once || d.N != 0 {
			t.Fatalf("List entry %+v carries Once/N, want both zeroed", d)
		}
	}
	// List must hand the delay's ms back out: the admin UI reads the value
	// in force from exactly this call, and a delay whose ms never came back
	// would render as a delay of nothing.
	var delayMs int
	for _, d := range got {
		if d.Action == ActionDelay {
			delayMs = d.Ms
		}
	}
	if delayMs != 300 {
		t.Fatalf("List's delay directive has Ms=%d, want 300", delayMs)
	}
}
