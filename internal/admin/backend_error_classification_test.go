package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"testing"
)

// TestBackendErrorCancellationIsRetryableAndQuiet pins review 2026-10-06,
// F176: a cancelled or timed-out request (r.Context() ends while the call is
// queued behind the single writer, or store.Write wraps it as "begin: context
// canceled") used to be logged at ERROR as a failed backend operation and
// answered as a non-retryable 500 backend_internal.
func TestBackendErrorCancellationIsRetryableAndQuiet(t *testing.T) {
	for name, cause := range map[string]error{
		"canceled": fmt.Errorf("begin: %w", context.Canceled),
		"deadline": context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			s := &Server{log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError}))}
			w := httptest.NewRecorder()
			s.backendError(w, cause)
			if w.Code != 503 {
				t.Errorf("status = %d, want 503", w.Code)
			}
			var body struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "backend_request_cancelled" || !body.Error.Retryable {
				t.Errorf("error = %+v, want retryable backend_request_cancelled", body.Error)
			}
			if logs.Len() != 0 {
				t.Errorf("cancellation logged at ERROR: %s", logs.String())
			}
		})
	}
}
