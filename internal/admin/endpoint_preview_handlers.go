// endpoint_preview_handlers.go is P6b's one new route (decisions.md
// mocker-p6b-sse-mock D13): POST /api/workspaces/{id}/endpoints/preview, the
// first frames a stream DRAFT would send, laid out on one time axis, with no
// row written, no revision bumped and no auto checkpoint taken (the route
// joins the "never touches a layer" group). It takes the draft in the
// create-request shape and runs the SAME write-time validation
// customep.Repo.Create runs — one owner — so a draft that previews is a
// draft that saves. Agent-only: preview_endpoint (internal/mcp) is its one
// caller and web/src/api/coverage.test.ts's EXEMPT map says so.
package admin

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/workspaces"
)

// StreamPreviewer is the mock plane as this route needs it — a second
// method beside [Previewer]'s, on the same *mockplane.Plane instance
// SetPreviewer already wires, so a stream draft is expanded by the plane
// that would serve it, with the workspace's own settings and seed.
type StreamPreviewer interface {
	PreviewStream(ctx context.Context, ws *workspaces.Workspace, draft *customep.Row) (domain.StreamPreview, error)
}

// endpointPreviewRequest is the POST body: createEndpointRequest's own
// kind/stream half plus the method and path the validator needs (an sse
// row requires GET). status/body/mediaType are absent on purpose — an http
// draft has nothing to lay out on a time axis and is refused below.
type endpointPreviewRequest struct {
	Method string           `json:"method"`
	Path   string           `json:"path"`
	Kind   string           `json:"kind"`
	Stream jsonx.RawMessage `json:"stream,omitempty"`
}

// streamPreviewFrameView is one frame of the answer.
type streamPreviewFrameView struct {
	AtMs  int              `json:"atMs"`
	Event string           `json:"event,omitempty"`
	Data  jsonx.RawMessage `json:"data"`
	// NotRun is A18 D10.1: the frame's PLACE is real, its body was not
	// produced because the preview's aggregate Lua budget ran out. Data is
	// null on such a frame — a caller that renders the body must read this
	// flag, and the omitempty is deliberate so a schema-tick preview's
	// document does not grow a field that is false on every frame.
	NotRun bool `json:"notRun,omitempty"`
}

// streamPreviewView is D13's response document.
type streamPreviewView struct {
	Kind           string                   `json:"kind"`
	Frames         []streamPreviewFrameView `json:"frames"`
	Truncated      bool                     `json:"truncated"`
	MaxBytesPerSec int64                    `json:"maxBytesPerSec"`
	// Rules and Echo are P6d's (decisions.md mocker-p6d-websocket D12): a
	// ws draft's inbound behaviours cannot be laid out on a time axis (there
	// is no inbound to react to), so the preview says how many rules it has
	// and whether it echoes, beside the frames its timeline/tick would send.
	Rules int  `json:"rules"`
	Echo  bool `json:"echo"`
	// NominalRate is A18 D10.1's label on maxBytesPerSec: with a `tick.lua`
	// producer the number is a SAMPLE of what ran, never a bound, because
	// the next firing may return anything. It is the one thing that stops
	// the amplifier estimate §30.12 wants shown from being read as a
	// guarantee it is not.
	NominalRate bool `json:"nominalRate"`
}

func newStreamPreviewView(p domain.StreamPreview) streamPreviewView {
	frames := make([]streamPreviewFrameView, 0, len(p.Frames))
	for _, f := range p.Frames {
		frames = append(frames, streamPreviewFrameView{AtMs: f.AtMs, Event: f.Event, Data: jsonx.RawMessage(f.Data), NotRun: f.NotRun})
	}
	return streamPreviewView{Kind: p.Kind, Frames: frames, Truncated: p.Truncated, MaxBytesPerSec: p.MaxBytesPerSec,
		Rules: p.Rules, Echo: p.Echo, NominalRate: p.NominalRate}
}

// handlePreviewEndpoint answers POST /api/workspaces/{id}/endpoints/preview.
func (s *Server) handlePreviewEndpoint(w http.ResponseWriter, r *http.Request) {
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
	previewer, ok := s.previewer.(StreamPreviewer)
	if s.previewer == nil || !ok {
		httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable, "no stream previewer is wired in this deployment")
		return
	}

	var body endpointPreviewRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	row, err := endpointRowFromDraft(body.Method, body.Path, body.Kind, body.Stream, s.customepRepo.MaxFrameBytes)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, refusalCode(err), err.Error())
		return
	}
	if row.Kind != customep.KindSSE && row.Kind != customep.KindWS {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "only a stream draft (kind \"sse\" or \"ws\") has frames to preview")
		return
	}

	result, err := previewer.PreviewStream(r.Context(), ws, row)
	if err != nil {
		s.log.Error("preview endpoint", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to preview the stream")
		return
	}
	httpx.JSON(w, http.StatusOK, newStreamPreviewView(result))
}

// endpointRowFromDraft builds and VALIDATES a customep.Row from the wire
// kind/stream pair — the shared step of create, update and preview, so the
// three cannot disagree about what a legal stream document is. stream is
// decoded through jsonx (unknown fields refused, like every other body on
// this plane); a nil/absent stream with kind "sse" is refused by
// customep.ValidateDraft, and a stream with kind "http" likewise.
func endpointRowFromDraft(method, path, kind string, stream jsonx.RawMessage, maxFrameBytes int64) (*customep.Row, error) {
	row := &customep.Row{Method: method, Path: path, Kind: kind}
	st, err := decodeStreamDoc(stream)
	if err != nil {
		return nil, err
	}
	row.Stream = st
	if err := customep.ValidateDraft(row, maxFrameBytes); err != nil {
		return nil, err
	}
	return row, nil
}

// decodeStreamDoc decodes the wire `stream` field: absent or null is nil
// (an http row), anything else must be a stream document.
func decodeStreamDoc(raw jsonx.RawMessage) (*customep.Stream, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var st customep.Stream
	if err := decodeStrict(bytes.NewReader(raw), &st); err != nil {
		return nil, errors.New("stream: " + err.Error())
	}
	return &st, nil
}
