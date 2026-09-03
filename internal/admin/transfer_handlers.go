package admin

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/checkpoints"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

// P4b (2026-09-02): the three transfer routes of DESIGN §14's table —
// GET /api/workspaces/{id}/export, POST /api/workspaces/import and
// POST /api/workspaces/{id}/fork — over [checkpoints.Repo]'s Export,
// Import and Fork. The handlers own exactly two things the repository does
// not: the SPEC resolution on import (explicit specId, then the document's
// hash, then its inline copy — this file's [Server.resolveImportSpec]), and
// the inline copy on export (the spec's bytes as uploaded, as one JSON
// string, so the hash survives the round trip).

const (
	// codeInvalidBundle is the 400 for a document bundle.DecodeExport
	// refuses; the message carries the validator's own words because the
	// operator or agent can act on them (which entry, which field).
	codeInvalidBundle = "invalid_bundle"
	// codeSpecNotFound is the 409 for a document naming a spec by hash
	// that this installation does not hold and that the caller neither
	// inlined nor overrode with specId; details carry the hash and name.
	codeSpecNotFound = "spec_not_found"
	// codeExportTooLarge is the 413 for an export asked WITH entity rows
	// whose rows exceed the checkpoint capture's probe budget; the same
	// export without data still answers.
	codeExportTooLarge = "export_too_large"
)

// importLabel and forkLabel name the baseline checkpoint the new
// workspace starts with — operator-facing, so Russian like every other
// label the history tab shows.
const importLabel = "импорт"

func forkLabel(sourceSlug string) string { return "копия воркспейса " + sourceSlug }

// forkNameSuffix is appended to the source's name when the fork request
// names nothing; the slug is derived from it and uniquified as on create.
const forkNameSuffix = " (копия)"

// queryFlag reads a boolean query parameter: "true" or "1" is true,
// anything else (including absence) is false.
func queryFlag(r *http.Request, name string) bool {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name)))
	return v == "true" || v == "1"
}

// handleExportWorkspace answers GET /api/workspaces/{id}/export.
// ?includeData=true adds the entity rows, ?includeSpec=true inlines the
// bound spec's bytes as uploaded. Content-Disposition names the file after
// the slug so a browser download lands as <slug>.mocker.json.
func (s *Server) handleExportWorkspace(w http.ResponseWriter, r *http.Request) {
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

	doc, err := s.checkpointsRepo.Export(r.Context(), ws.ID, queryFlag(r, "includeData"))
	if err != nil {
		switch {
		case errors.Is(err, checkpoints.ErrWorkspaceNotFound):
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
		case errors.Is(err, checkpoints.ErrDataSnapshotTooLarge):
			httpx.Err(w, http.StatusRequestEntityTooLarge, codeExportTooLarge,
				"entity rows exceed the export budget; export without includeData")
		default:
			s.log.Error("export workspace", "workspace", ws.Slug, "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to export workspace")
		}
		return
	}
	if queryFlag(r, "includeSpec") && ws.SpecID != nil {
		raw, rerr := s.specsRepo.Raw(r.Context(), *ws.SpecID)
		if rerr != nil && !errors.Is(rerr, specs.ErrNotFound) {
			s.log.Error("export workspace: read spec", "workspace", ws.Slug, "err", rerr)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to export workspace")
			return
		}
		if rerr == nil {
			// One JSON string, never the document re-serialised: the
			// receiving installation hashes exactly these bytes.
			inline, merr := jsonx.Marshal(string(raw))
			if merr != nil {
				s.log.Error("export workspace: encode spec", "workspace", ws.Slug, "err", merr)
				httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to export workspace")
				return
			}
			doc.Spec.Inline = inline
		}
	}
	body, err := bundle.EncodeExport(doc)
	if err != nil {
		s.log.Error("export workspace: encode", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to export workspace")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ws.Slug+".mocker.json"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// importWorkspaceBody is POST /api/workspaces/import's request.
type importWorkspaceBody struct {
	// Bundle is the export document verbatim — a plain v4 bundle imports
	// too (no `data` key means no entity rows).
	Bundle jsonx.RawMessage `json:"bundle"`
	// Name overrides the document's workspace.name; Slug is optional and
	// uniquified from the name when empty, exactly as on create.
	Name string `json:"name"`
	Slug string `json:"slug"`
	// SpecID, when set, binds the workspace to this spec regardless of what
	// the document names.
	SpecID *int64 `json:"specId"`
}

// importWorkspaceView is the 201 answer: the workspace, which spec it was
// bound to and whether that spec was created by this call (from the
// document's inline copy), and how many families' rows came in.
type importWorkspaceView struct {
	Workspace        workspaceView `json:"workspace"`
	SpecID           *int64        `json:"specId"`
	SpecCreated      bool          `json:"specCreated"`
	EntitiesRestored int           `json:"entitiesRestored"`
}

// handleImportWorkspace answers POST /api/workspaces/import.
func (s *Server) handleImportWorkspace(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body importWorkspaceBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if len(body.Bundle) == 0 || string(body.Bundle) == "null" {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "bundle is required")
		return
	}
	doc, err := bundle.DecodeExport(body.Bundle)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = strings.TrimSpace(doc.Workspace.Name)
	}
	if name == "" {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "name is required (the document names no workspace)")
		return
	}
	// The same two settings checks PATCH runs (D4.3): a base path with an
	// unbalanced brace or a values list of the wrong arity must not reach a
	// confirm through an import any more than through a PATCH.
	doc.Workspace.Settings.Normalize()
	doc.BasePath = doc.Workspace.Settings.BasePath
	if verr := domain.ValidateBasePath(doc.Workspace.Settings.BasePath); verr != nil {
		httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, "workspace.settings.basePath: "+verr.Error())
		return
	}
	if verr := domain.ValidateBasePathValues(doc.Workspace.Settings.BasePath, doc.Workspace.Settings.BasePathValues); verr != nil {
		httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, "workspace.settings.basePathValues: "+verr.Error())
		return
	}

	// The hard rule's "neither stored" half (CLAUDE.md, httpx.
	// BrowserExecutableMediaType): every OTHER writer of a pinned variant —
	// PUT .../operations, the endpoint routes, the preview gate — refuses an
	// executable media type before the row is written, and an imported
	// document is caller-supplied input exactly like those bodies are. The
	// serve path refuses such a variant again, so what an unguarded import
	// stored would never serve — but it would sit in the row, survive every
	// checkpoint, and confuse the operator who pinned it.
	if name, bad := bundleExecutableMediaType(doc.Bundle); bad {
		httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, "a pinned response declares a media type the browser executes: "+name)
		return
	}

	specID, specCreated, ok := s.resolveImportSpec(w, r, user.ID, body.SpecID, doc.Spec)
	if !ok {
		return
	}
	// D4.3's half two, the same check PATCH runs: a base parameter that
	// collides with a route parameter of the spec this import binds.
	if specID != nil && !s.validateBasePathAgainstBoundSpec(w, r, 0, specID, doc.Workspace.Settings.BasePath) {
		return
	}
	// P7a (D6): a document's endpoint rows must resolve every `$ref` against
	// the spec this import binds — the same 409 a rebind answers, before the
	// transaction, so a dangling reference is never stored by this door
	// either. Decoded here ONLY for the check; checkpoints.Repo.Import
	// decodes them again for the write, from the same document.
	endpointRows, err := checkpoints.EndpointRowsFromBundle(doc.Bundle)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, "endpoints: "+err.Error())
		return
	}
	if s.refuseRowsAgainstSpec(w, r, specID, endpointRows) {
		return
	}

	ownerID := user.ID
	out, err := s.checkpointsRepo.Import(r.Context(), checkpoints.ImportInput{
		Name: name, Slug: body.Slug, OwnerID: &ownerID, CreatedBy: user.ID,
		SpecID: specID, Document: doc, Label: importLabel,
	})
	if err != nil {
		s.writeTransferError(w, "import workspace", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, importWorkspaceView{
		Workspace:        s.newWorkspaceView(r, out.Workspace),
		SpecID:           specID,
		SpecCreated:      specCreated,
		EntitiesRestored: out.EntitiesRestored,
	})
}

// resolveImportSpec decides which spec an imported workspace binds to, in
// this order: the request's explicit specId (must exist), the document's
// hash (a spec of the same bytes already here), the document's inline copy
// (imported now, deduplicated by hash like any upload), none (a document
// that names no spec). A document naming a hash that resolves to nothing
// and carries no inline copy answers 409 spec_not_found with the hash and
// name in details, so the caller can import the spec by hand or pass
// specId. Returns ok=false after writing the response.
func (s *Server) resolveImportSpec(w http.ResponseWriter, r *http.Request, userID int64, explicit *int64, ref bundle.SpecRef) (specID *int64, created bool, ok bool) {
	if explicit != nil {
		sp, ok := s.loadSpecForAttach(w, r, *explicit)
		if !ok {
			return nil, false, false
		}
		return &sp.ID, false, true
	}
	if ref.Hash != "" {
		sp, err := s.specsRepo.ByHash(r.Context(), ref.Hash)
		switch {
		case err == nil:
			return &sp.ID, false, true
		case !errors.Is(err, specs.ErrNotFound):
			s.log.Error("import workspace: spec by hash", "hash", ref.Hash, "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to resolve spec")
			return nil, false, false
		}
	}
	var inline string
	if len(ref.Inline) > 0 && string(ref.Inline) != "null" {
		if err := jsonx.Unmarshal(ref.Inline, &inline); err != nil {
			httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, "spec.inline must be the document as one JSON string")
			return nil, false, false
		}
	}
	if strings.TrimSpace(inline) == "" {
		if ref.Hash == "" {
			return nil, false, true
		}
		httpx.ErrDetails(w, http.StatusConflict, codeSpecNotFound,
			"the document names a spec this installation does not hold; import it (or export with includeSpec=true) or pass specId",
			map[string]string{"hash": ref.Hash, "name": ref.Name})
		return nil, false, false
	}
	specName := strings.TrimSpace(ref.Name)
	if specName == "" {
		specName = "imported"
	}
	result, err := s.specsRepo.Import(r.Context(), specs.ImportInput{
		Name: specName, Source: "bundle", Document: []byte(inline), CreatedBy: &userID,
	})
	if errors.Is(err, specs.ErrDuplicate) {
		return &result.Spec.ID, false, true
	}
	if err != nil {
		switch {
		case errors.Is(err, specs.ErrTooLarge), errors.Is(err, specs.ErrTooManyOperations):
			httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "spec.inline: "+err.Error())
		case errors.Is(err, specs.ErrNotADocument), errors.Is(err, specs.ErrUnsupportedFormat):
			httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, "spec.inline: "+err.Error())
		default:
			s.log.Error("import workspace: import inline spec", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to import the inline spec")
		}
		return nil, false, false
	}
	return &result.Spec.ID, true, true
}

// forkWorkspaceBody is POST /api/workspaces/{id}/fork's request. All three
// fields are optional: name defaults to the source's name plus
// forkNameSuffix, slug is uniquified from it, includeData defaults to true.
type forkWorkspaceBody struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	IncludeData *bool  `json:"includeData"`
}

// handleForkWorkspace answers POST /api/workspaces/{id}/fork with the copy
// as a WorkspaceView (forkedFrom set to the source's id).
func (s *Server) handleForkWorkspace(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	src, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	var body forkWorkspaceBody
	if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = src.Name + forkNameSuffix
	}
	includeData := body.IncludeData == nil || *body.IncludeData

	ownerID := user.ID
	ws, err := s.checkpointsRepo.Fork(r.Context(), checkpoints.ForkInput{
		SourceID: src.ID, Name: name, Slug: body.Slug, OwnerID: &ownerID, CreatedBy: user.ID,
		IncludeData: includeData, Label: forkLabel(src.Slug),
	})
	if err != nil {
		s.writeTransferError(w, "fork workspace", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, s.newWorkspaceView(r, ws))
}

// writeTransferError maps the errors Import and Fork share onto the
// statuses create and rollback already use for the same conditions.
func (s *Server) writeTransferError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, workspaces.ErrSlugTaken):
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, "slug already taken")
	case errors.Is(err, workspaces.ErrSlugInvalid):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid slug")
	case errors.Is(err, workspaces.ErrSettingsTooLarge):
		httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "settings too large")
	case errors.Is(err, checkpoints.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, checkpoints.ErrSnapshotTooLarge):
		httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "the configuration exceeds the snapshot ceiling")
	case errors.Is(err, checkpoints.ErrConcurrentEdit):
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, "the source workspace kept changing; try again")
	case errors.Is(err, checkpoints.ErrSpecMissing):
		httpx.Err(w, http.StatusConflict, codeSpecNotFound, err.Error())
	case errors.Is(err, customep.ErrInvalidRow), errors.Is(err, customep.ErrOperationIDTaken):
		// A row of the document that customep refuses by name inside the
		// apply (a shape bundle.Validate does not check, or an
		// operationId the bound spec already holds): the caller's
		// document, answered as such.
		httpx.Err(w, http.StatusBadRequest, codeInvalidBundle, err.Error())
	default:
		s.log.Error(op, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to "+op)
	}
}

// bundleExecutableMediaType scans every pinned variant of an imported
// document for a media type dangerousMediaType refuses and returns the first
// one found, so handleImportWorkspace can name it in the 400.
func bundleExecutableMediaType(b bundle.Bundle) (string, bool) {
	for _, o := range b.Overrides {
		for _, v := range o.Responses {
			if dangerousMediaType(v.MediaType) {
				return v.MediaType, true
			}
		}
	}
	for _, e := range b.Endpoints {
		for _, v := range e.Responses {
			if dangerousMediaType(v.MediaType) {
				return v.MediaType, true
			}
		}
	}
	return "", false
}
