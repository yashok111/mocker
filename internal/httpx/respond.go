// Package httpx holds the HTTP helpers both planes share: JSON responses, a
// single error shape, middleware plumbing and peer-address resolution.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ErrorBody is the one error shape mocker's own API ever returns. Mocked
// responses are whatever the user configured; this is only for mocker itself.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable machine code and a message meant for a human
// reading it in the UI.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Error codes returned by mocker's own API.
const (
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeTooLarge     = "too_large"
	CodeInternal     = "internal"
)

// JSON writes v as JSON with the given status.
//
// The body is marshalled BEFORE the header is written: a failure halfway
// through a streaming encode would leave a 200 with truncated JSON, which the
// caller cannot tell from a successful response.
func JSON(w http.ResponseWriter, status int, v any) {
	body, err := jsonx.Marshal(v)
	if err != nil {
		slog.Error("marshal response", "err", err)
		writeRaw(w, http.StatusInternalServerError, mustMarshal(ErrorBody{
			Error: ErrorDetail{Code: CodeInternal, Message: "failed to encode response"},
		}))
		return
	}
	writeRaw(w, status, body)
}

// Err writes a JSON error body with the given status and code.
func Err(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// ErrDetails writes a JSON error body carrying structured details, used for
// per-field validation failures.
func ErrDetails(w http.ResponseWriter, status int, code, message string, details any) {
	JSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message, Details: details}})
}

// NoContent answers 204 with no body.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		slog.Debug("write response", "err", err)
	}
}

func mustMarshal(v any) []byte {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return []byte(`{"error":{"code":"internal","message":"encode failure"}}`)
	}
	return b
}
