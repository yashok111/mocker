// ready_test.go is a BLACK-BOX test file (package mockplane_test), like
// plane_test.go whose testConfig/testLogger/fakeSource it reuses:
// [mockplane.Plane.Ready] is a startup-time question cmd/mocker asks from
// outside this package, and the test that pins it should ask it the same
// way.
package mockplane_test

import (
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/stream"
)

// The Plane's sources, stubbed by EMBEDDING each interface rather than
// implementing it. Ready asks only whether a field is nil, and this file
// calls no handler, so no method here needs a body: the embedded interface
// is nil, so any call would panic loudly instead of quietly answering
// something a later reader could mistake for a fixture. The package's real
// fakes (fakeCustomSource, fakeScenarioSource, ...) deliberately are NOT
// reused for the same reason — a behaviourless stub is the honest shape for
// a test about wiring rather than about serving.
type (
	readySpecs      struct{ mockplane.SpecSource }
	readyOverrides  struct{ mockplane.OverrideSource }
	readyLiveState  struct{ mockplane.LiveStateSource }
	readyTraffic    struct{ mockplane.TrafficSink }
	readyCustom     struct{ mockplane.CustomSource }
	readyScenarios  struct{ mockplane.ScenarioSource }
	readyResources  struct{ mockplane.ResourceSource }
	readyEntities   struct{ mockplane.EntityStore }
	readyAssetStore struct{ mockplane.AssetStore }
)

// readySteps is every source [mockplane.Plane.Ready] calls required, paired
// with the call that supplies it, in Ready's own reporting order. Adding a
// source to the Plane without adding it here leaves the new name reported
// forever and this table's final "nothing missing" assertion red — which is
// the point: the list of things main.go must wire has exactly one place to
// grow.
var readySteps = []struct {
	name string
	wire func(*mockplane.Plane)
}{
	{"SetOverrides", func(p *mockplane.Plane) { p.SetOverrides(readyOverrides{}) }},
	{"SetLiveState", func(p *mockplane.Plane) { p.SetLiveState(readyLiveState{}) }},
	{"SetTraffic", func(p *mockplane.Plane) { p.SetTraffic(readyTraffic{}) }},
	{"SetCustomEndpoints", func(p *mockplane.Plane) { p.SetCustomEndpoints(readyCustom{}) }},
	{"SetScenarios", func(p *mockplane.Plane) { p.SetScenarios(readyScenarios{}) }},
	{"SetResources", func(p *mockplane.Plane) { p.SetResources(readyResources{}) }},
	{"SetEntities", func(p *mockplane.Plane) { p.SetEntities(readyEntities{}) }},
	{"SetAssets", func(p *mockplane.Plane) { p.SetAssets(readyAssetStore{}) }},
	{"SetStreams", func(p *mockplane.Plane) {
		p.SetStreams(stream.NewWorkspaceRegistry(1), mockplane.StreamOptions{})
	}},
}

// TestPlaneReadyReportsEverySetterUntilItRuns is the bar this whole file
// exists for: a bare New reports every required source, and each name
// disappears exactly when its own setter runs. Both halves matter — a Ready
// that reported nothing would pass a "fully wired reports nothing" test on
// its own.
func TestPlaneReadyReportsEverySetterUntilItRuns(t *testing.T) {
	t.Parallel()

	p := mockplane.New(testConfig(), &fakeSource{}, readySpecs{}, testLogger())

	var want []string
	for _, s := range readySteps {
		want = append(want, s.name)
	}
	if got := p.Ready(); !slices.Equal(got, want) {
		t.Fatalf("bare New: Ready() = %v, want %v", got, want)
	}

	for _, s := range readySteps {
		if !slices.Contains(p.Ready(), s.name) {
			t.Fatalf("%s: not reported before its setter ran (Ready() = %v)", s.name, p.Ready())
		}
		s.wire(p)
		if slices.Contains(p.Ready(), s.name) {
			t.Fatalf("%s: still reported after its setter ran (Ready() = %v)", s.name, p.Ready())
		}
	}

	if got := p.Ready(); len(got) != 0 {
		t.Fatalf("every setter ran: Ready() = %v, want nothing missing", got)
	}
}

// TestPlaneReadyReportsNewParameters pins the two sources that arrive
// through New rather than a setter. [mockplane.New]'s own doc comment
// allows a nil specs ("no spec support") and most of this package's tests
// take it up on that — Ready is the one place that says a PRODUCTION plane
// may not.
func TestPlaneReadyReportsNewParameters(t *testing.T) {
	t.Parallel()

	p := mockplane.New(testConfig(), nil, nil, testLogger())
	got := p.Ready()
	for _, name := range []string{"New(src)", "New(specs)"} {
		if !slices.Contains(got, name) {
			t.Errorf("New(cfg, nil, nil, log): Ready() = %v, want it to name %s", got, name)
		}
	}
}
