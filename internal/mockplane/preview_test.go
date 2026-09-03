// preview_test.go is a WHITE-BOX test file (package mockplane, not
// mockplane_test): [Plane.Preview], [previewResourceServes] and
// [previewDraftFields] are all unexported, and this file's whole point is
// D13 clause 25 — "preview refuses per ELIGIBLE method" — which needs the
// REAL [Plane.Preview] entry point (buildRuntime, locatePreviewRoute,
// resolveVariant, in that order) rather than a direct unit call on
// previewResourceServes alone: a test that only called the helper could not
// catch a wiring mistake in Preview's own hoist (D12), which is exactly
// what this run's task asks this file to defeat.
//
// It reuses runtime_test.go's fakeRuntimeSource/widgetsWorkspace/
// runtimeTestConfig/runtimeTestLogger and resource_test.go's
// resourceTestRoutes/resourceTestVariants/itemsResource/bareWriteForm —
// same package, same compilation unit — rather than duplicating fixtures
// this file has no reason to own a second copy of.
package mockplane

import (
	"context"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

// previewFakeResourceSource is this file's own [ResourceSource]: a single
// fixed resource (or none), handed back regardless of workspace id — the
// smallest fixture that gets a *resource* into buildRuntime's own step
// (runtime.go) through the real p.resources.ForWorkspace call Preview
// exercises, rather than poking rt.resources by hand the way
// resourceTestRuntime (resource_test.go) does for its own, lower-level,
// tests.
type previewFakeResourceSource struct {
	res *resources.Resource
}

func (s previewFakeResourceSource) ForWorkspace(context.Context, int64) ([]*resources.Resource, error) {
	if s.res == nil {
		return nil, nil
	}
	return []*resources.Resource{s.res}, nil
}

// previewTestPlane wires a *Plane whose SpecSource serves resourceTestRoutes
// over the given variants (blankDoc: every variant here carries no
// SchemaPtr, so the document's own content never matters — respond_test.go's
// own blankDoc doc comment) and whose ResourceSource serves res (nil for
// "no confirmed resource at all").
func previewTestPlane(variants map[int64][]gen.ResponseVariant, res *resources.Resource) (*Plane, *workspaces.Workspace) {
	src := &fakeRuntimeSource{
		normalized: []byte(blankDoc),
		routes:     resourceTestRoutes(),
		variants:   variants,
	}
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())
	p.SetResources(previewFakeResourceSource{res: res})
	return p, widgetsWorkspace(1, domain.DefaultSettings())
}

func previewDraft(t *testing.T, overrideOn bool, responses map[string]overrides.Variant) jsonx.RawMessage {
	t.Helper()
	b, err := jsonx.Marshal(previewDraftFields{OverrideOn: overrideOn, Responses: responses})
	if err != nil {
		t.Fatalf("marshal previewDraftFields: %v", err)
	}
	return jsonx.RawMessage(b)
}

// deleteTextPlainVariants is resourceTestVariants with DELETE /items/{id}'s
// 204 swapped for a 2xx that declares text/plain — the over-approximation
// case clause 25 names by name: the plane would never take this exact
// operation over at request time ([resourceBranch]'s own media-type gate,
// resource.go), but Preview refuses it anyway because eligibility, not the
// per-request outcome, is what this row answers (D12).
func deleteTextPlainVariants() map[int64][]gen.ResponseVariant {
	v := resourceTestVariants()
	v[4] = []gen.ResponseVariant{{OpRowID: 4, Selector: "200", HTTPStatus: 200, MediaType: "text/plain"}}
	return v
}

// TestPreview_ResourceServesEligibility is D13 clause 25: preview refuses
// per ELIGIBLE method, over the five cases the clause names.
func TestPreview_ResourceServesEligibility(t *testing.T) {
	t.Run("GET X refuses", func(t *testing.T) {
		p, ws := previewTestPlane(resourceTestVariants(), itemsResource(nil, "id", specs.Wrapper{}))
		_, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
			OpKey: overrides.OpKey("GET", "/items"),
			Draft: previewDraft(t, false, nil),
		})
		if !errors.Is(err, domain.ErrPreviewResourceServes) {
			t.Fatalf("Preview err = %v, want domain.ErrPreviewResourceServes", err)
		}
	})

	t.Run("POST X on a bare write_form refuses", func(t *testing.T) {
		p, ws := previewTestPlane(resourceTestVariants(), itemsResource(bareWriteForm(), "id", specs.Wrapper{}))
		_, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
			OpKey: overrides.OpKey("POST", "/items"),
			Draft: previewDraft(t, false, nil),
		})
		if !errors.Is(err, domain.ErrPreviewResourceServes) {
			t.Fatalf("Preview err = %v, want domain.ErrPreviewResourceServes", err)
		}
	})

	t.Run("POST X on a nil write_form previews normally", func(t *testing.T) {
		// R14's own table: a family that never takes POST over must not
		// refuse a draft for it either — the exact case a numeric-selector-
		// only or family-only switch (defeated by clause 11) would get
		// wrong in the OPPOSITE direction from every other case here.
		p, ws := previewTestPlane(resourceTestVariants(), itemsResource(nil, "id", specs.Wrapper{}))
		res, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
			OpKey: overrides.OpKey("POST", "/items"),
			Draft: previewDraft(t, false, nil),
		})
		if err != nil {
			t.Fatalf("Preview err = %v, want nil (normal preview)", err)
		}
		if res.Status != 201 {
			t.Errorf("Status = %d, want 201 (the spec's own declared POST response)", res.Status)
		}
	})

	t.Run("DELETE whose 2xx declares text/plain also refuses (over-approximation)", func(t *testing.T) {
		p, ws := previewTestPlane(deleteTextPlainVariants(), itemsResource(nil, "id", specs.Wrapper{}))
		_, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
			OpKey:      overrides.OpKey("DELETE", "/items/{id}"),
			Draft:      previewDraft(t, false, nil),
			PathParams: map[string]string{"id": "3"},
		})
		if !errors.Is(err, domain.ErrPreviewResourceServes) {
			t.Fatalf("Preview err = %v, want domain.ErrPreviewResourceServes (the plane will never take this exact operation over, but eligibility still refuses it)", err)
		}
	})

	t.Run("GET X draft that pins the chosen 2xx previews normally", func(t *testing.T) {
		// D12's one exclusion: D11 keeps the endpoints editor open for a
		// resource-served operation precisely so an operator can author
		// this exact pin — refusing to preview it would make that editor
		// unusable for its only remaining purpose.
		p, ws := previewTestPlane(resourceTestVariants(), itemsResource(nil, "id", specs.Wrapper{}))
		res, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
			OpKey: overrides.OpKey("GET", "/items"),
			Draft: previewDraft(t, true, map[string]overrides.Variant{
				"200": {Mode: "pinned", Body: jsonx.RawMessage(`{"pinned":true}`), MediaType: "application/json"},
			}),
		})
		if err != nil {
			t.Fatalf("Preview err = %v, want nil (a pinned variant previews normally)", err)
		}
		if res.Status != 200 {
			t.Errorf("Status = %d, want 200", res.Status)
		}
	})
}
