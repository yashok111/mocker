package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/specs"
)

// TestMaterializationOwnerSentinelsUseBackendEnvelope pins review
// 2026-10-06, F175: materializationError (shared by the materialization and
// every portable handler) forwarded apidesign/designscenario sentinels to
// designError/designScenarioError, which write the legacy envelope with no
// `retryable` and a Russian product message, although api/openapi.json
// declares BackendError on these routes.
func TestMaterializationOwnerSentinelsUseBackendEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"api not found", fmt.Errorf("pin: %w", apidesign.ErrNotFound), 404, "backend_materialization_owner_not_found"},
		{"scenario not found", designscenario.ErrNotFound, 404, "backend_materialization_owner_not_found"},
		{"api conflict", &apidesign.ConflictError{Version: 7, DraftRevisionID: 3}, 409, "backend_materialization_owner_conflict"},
		{"linked conflict", &designscenario.LinkedConflictError{ContractID: "c", DesignID: 2, Version: 4, DraftRevisionID: 5}, 409, "backend_materialization_owner_conflict"},
		{"api too large", specs.ErrTooLarge, 413, "backend_too_large"},
		{"scenario too large", designscenario.ErrTooLarge, 413, "backend_too_large"},
		{"api invalid sentinel", apidesign.ErrInvalid, 422, "backend_materialization_owner_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			w := httptest.NewRecorder()
			s.materializationError(w, tc.err)
			var body struct {
				Error map[string]any `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || body.Error["code"] != tc.code {
				t.Fatalf("got %d %v, want %d %s", w.Code, body.Error, tc.status, tc.code)
			}
			if _, ok := body.Error["retryable"]; !ok {
				t.Errorf("envelope without retryable: %v", body.Error)
			}
		})
	}
}

// TestReplayQueueFullCarriesRetryAfter pins review 2026-10-06, F29: only
// backend_analysis_queue_full received Retry-After, so a replay queue refusal
// (now 429, retryable) would reach a client without the wait it documents.
func TestReplayQueueFullCarriesRetryAfter(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := httptest.NewRecorder()
	s.backendError(w, &backendmodel.FaultError{Status: 429, Code: "backend_replay_queue_full", Message: "Replay queue is full", Retryable: true})
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("status %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}
