package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/design"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/workspaces"
)

// codeSchemaRefUnresolved is P7a's refusal (D2/D6) for a `$ref` the bound
// spec cannot resolve — on both endpoint writers and on every verb that
// changes which document a row resolves into (a rebind, an import, a
// rollback), so a dangling reference is never STORED.
const codeSchemaRefUnresolved = "schema_ref_unresolved"

// codeEndpointRefUnresolved is the same refusal seen from the other side:
// the rows are already stored and the DOCUMENT is what is changing.
const codeEndpointRefUnresolved = "endpoint_ref_unresolved"

// resolverForSpec builds the resolver a schema's `$ref`s are checked
// against: the SAME derivation buildRuntime and the auth preset already
// use (the stored bytes are dialect-normalized, so Load here is a re-parse
// and never a second normalization). A nil specID answers a nil resolver
// and no error — "no spec is bound", which customep.ValidateRefs reads as
// "any $ref is refused".
func (s *Server) resolverForSpec(ctx context.Context, specID *int64) (*openapi.Resolver, error) {
	if specID == nil {
		return nil, nil //nolint:nilnil // "no document" is the answer, and every caller reads a nil resolver as exactly that
	}
	normalized, err := s.specsRepo.Normalized(ctx, *specID)
	if err != nil {
		return nil, fmt.Errorf("load normalized document for spec %d: %w", *specID, err)
	}
	doc, _, err := openapi.Load(normalized)
	if err != nil {
		return nil, fmt.Errorf("re-load normalized document for spec %d: %w", *specID, err)
	}
	return openapi.NewResolver(doc, openapi.DefaultRefBudget), nil
}

// refuseUnresolvedRefs is the endpoint writers' half of D6: every schema
// the submitted row carries must resolve against the workspace's currently
// bound spec. Writes the refusal and reports true when the caller has been
// answered.
func (s *Server) refuseUnresolvedRefs(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, row *customep.Row) bool {
	resolver, err := s.resolverForSpec(r.Context(), ws.SpecID)
	if err != nil {
		s.log.Error("build resolver for endpoint schema", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load the bound spec document")
		return true
	}
	if verr := customep.ValidateRefs(row, refResolverOf(resolver)); verr != nil {
		httpx.Err(w, http.StatusBadRequest, codeSchemaRefUnresolved, verr.Error())
		return true
	}
	return false
}

// refResolverOf is the nil-interface guard every caller of
// customep.ValidateRefs needs: a nil *openapi.Resolver stored in the
// RefResolver interface is NOT a nil interface, and ValidateRefs would call
// Resolve on it and panic on the first `$ref` of a no-spec workspace —
// which the first draft did, and a test caught. "No spec bound" must reach
// the validator as a nil INTERFACE.
func refResolverOf(res *openapi.Resolver) customep.RefResolver {
	if res == nil {
		return nil
	}
	return res
}

// endpointRefConflict is one row of the 409 a document change is refused
// with: which endpoint carries the reference that would dangle.
type endpointRefConflict struct {
	// EndpointID is the stored row's id on a rebind or a rollback of
	// rows the workspace holds; omitted on an import and on a rollback's
	// document rows, which have no id yet — (method, path) is the address
	// then.
	EndpointID int64  `json:"endpointId,omitempty"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	// Reason names the pointer and where it sits, in ValidateRefs' words.
	Reason string `json:"reason"`
}

// refuseRowsAgainstSpec is D6's other half: every custom endpoint ALREADY
// stored for the workspace must still resolve against the spec the caller
// is about to bind. Answers 409 with one row per offending endpoint and
// reports true when the caller has been answered. rows may be read for a
// workspace that does not exist yet (an import): pass them in rather than
// letting this read the table.
func (s *Server) refuseRowsAgainstSpec(w http.ResponseWriter, r *http.Request, specID *int64, rows []*customep.Row) bool {
	resolver, err := s.resolverForSpec(r.Context(), specID)
	if err != nil {
		s.log.Error("build resolver for spec rebind", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load the spec document")
		return true
	}
	var conflicts []endpointRefConflict
	res := refResolverOf(resolver)
	for _, row := range rows {
		if verr := customep.ValidateRefs(row, res); verr != nil {
			conflicts = append(conflicts, endpointRefConflict{
				EndpointID: row.ID, Method: row.Method, Path: row.Path, Reason: verr.Error(),
			})
		}
	}
	if len(conflicts) == 0 {
		return false
	}
	httpx.ErrDetails(w, http.StatusConflict, codeEndpointRefUnresolved,
		"a custom endpoint's schema references a component this spec does not have; edit or delete it first",
		conflicts)
	return true
}

// refuseStoredRowsAgainstSpec is refuseRowsAgainstSpec over the rows the
// workspace holds right now — the shape a rebind (PATCH) and a rollback
// need.
func (s *Server) refuseStoredRowsAgainstSpec(w http.ResponseWriter, r *http.Request, workspaceID int64, specID *int64) bool {
	rows, err := s.customepRepo.ForWorkspace(r.Context(), workspaceID)
	if err != nil {
		s.log.Error("list endpoints for a spec change", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list custom endpoints")
		return true
	}
	return s.refuseRowsAgainstSpec(w, r, specID, rows)
}

// handleExportOpenAPI answers GET /api/workspaces/{id}/openapi.json: the
// workspace as ONE OpenAPI 3.1 document (DESIGN §34.4), composed by
// internal/design over the bound spec's normalized document. The RAW
// document is the body — no envelope, because the deliverable is what a
// backend team, a code generator or `import_spec` reads, and an envelope
// would make every one of them unwrap it first.
func (s *Server) handleExportOpenAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	var base []byte
	if ws.SpecID != nil {
		var err error
		base, err = s.specsRepo.Normalized(r.Context(), *ws.SpecID)
		if err != nil {
			s.log.Error("load normalized document for the contract", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load the bound spec document")
			return
		}
	}
	overrideRows, err := s.overridesRepo.ForWorkspace(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("list overrides for the contract", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load the workspace's operations")
		return
	}
	endpointRows, err := s.customepRepo.ForWorkspace(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("list endpoints for the contract", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load the workspace's endpoints")
		return
	}

	doc, err := design.Compose(design.Input{
		WorkspaceName: ws.Name,
		Revision:      ws.Revision,
		Base:          base,
		Overrides:     overrideRows,
		Endpoints:     endpointRows,
	})
	if err != nil {
		// Every reason Compose fails is server-side: a stored document
		// that no longer loads, or a row whose stored JSON is broken. A
		// caller cannot repair either by changing this request.
		s.log.Error("compose the contract", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to compose the contract document")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// No caching at all: the document changes with every edit of the
	// workspace, and a stale contract read as current is the one failure
	// this route must not have.
	w.Header().Set("Cache-Control", "no-store")
	if queryFlag(r, "download") {
		w.Header().Set("Content-Disposition", fmt.Sprintf(
			"attachment; filename=%q", ws.Slug+"-draft-"+strconv.FormatInt(ws.Revision, 10)+".json"))
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(doc)))
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(doc); werr != nil {
		s.log.Debug("write the contract document", "workspace", ws.Slug, "err", werr)
	}
}
