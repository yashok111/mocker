// Package jsonx is this module's single JSON boundary. Everything in cmd/ and
// internal/ encodes and decodes through it; no other production file imports
// encoding/json, and boundary_test.go fails the build if one does.
//
// # Why a boundary at all, when the backend is the standard library
//
// Because it is the seam that makes the backend a decision instead of a
// rewrite. Swapping it is one line — [api] — and the tests beside this file
// are what make that swap safe rather than hopeful.
//
// It was measured, not assumed. bytedance/sonic v1.15.2, wired here in full
// and run against the real spec:
//
//	marshal microbenchmark      4771 -> 2692 ns/op (29 -> 8 allocs)
//	unmarshal microbenchmark   10261 -> 4320 ns/op
//	generating all 419 bodies   0.524 -> 0.452 s   (6 runs each, ~14%)
//
// The microbenchmark's 1.8x collapses to 14% end to end, because generation is
// dominated by the schema walk, the PRNG and validation rather than by
// marshalling. Against that: five more modules including a JIT that writes
// executable memory at runtime, which is an odd thing to add to a product
// whose whole delivery story is one static CGO-free binary loaded into a
// closed contour; `go test -race` on internal/gen going 28 s to 86 s; and a
// hard coupling to sonic keeping up with Go releases, on a module that tracks
// them closely. Declined on that balance, not on quality — and the wrapper
// stays so the decision can be revisited with one line and one benchmark.
//
// # The two traps that swap exposed, and why they live here
//
// They are the reason this package is worth its own file even today.
//
// First, output stability. sonic's DEFAULT configuration does not sort map
// keys and does not escape HTML; only sonic.ConfigStd matches encoding/json.
// Go randomises map iteration per run, and this project's entire promise is
// that one seed and one spec produce byte-identical bodies (internal/gen's
// golden pins 419 hashes). A backend chosen for speed without checking that
// property would break it intermittently — the worst possible failure shape.
// TestMarshal_isStableAcrossRuns is what actually holds the line.
//
// Second, error taxonomy — see [Malformed].
//
// # What is deliberately NOT routed through here
//
// Tests. A test that builds its expectation with encoding/json and compares it
// to what this package produced is a continuous cross-check of whatever
// backend is configured, and jsonx_test.go is exactly that. Uniformity would
// cost more than it buys.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// api names the backend. It is a var, and the only line that has to change to
// swap one in: everything below delegates to it, and the tests in this package
// assert the properties any replacement must keep.
//
// If you do change it, read [Malformed] first — it is the one function that
// knows something backend-specific, and the test for it will tell you so.
var api backend = stdlib{}

// backend is the surface this package needs from a JSON implementation.
type backend interface {
	Marshal(v any) ([]byte, error)
	MarshalIndent(v any, prefix, indent string) ([]byte, error)
	Unmarshal(data []byte, v any) error
	Valid(data []byte) bool
	Compact(dst *bytes.Buffer, src []byte) error
	NewDecoder(r io.Reader) Decoder
	NewEncoder(w io.Writer) Encoder
}

// stdlib is encoding/json behind [backend].
type stdlib struct{}

func (stdlib) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (stdlib) MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

func (stdlib) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func (stdlib) Valid(data []byte) bool                      { return json.Valid(data) }
func (stdlib) Compact(dst *bytes.Buffer, src []byte) error { return json.Compact(dst, src) }

func (stdlib) NewDecoder(r io.Reader) Decoder { return json.NewDecoder(r) }

func (stdlib) NewEncoder(w io.Writer) Encoder { return json.NewEncoder(w) }

// RawMessage, Number, Marshaler and Unmarshaler are the stdlib types, aliased
// so a caller needs only this package. Every serious backend honours all four.
type (
	RawMessage  = json.RawMessage
	Number      = json.Number
	Marshaler   = json.Marshaler
	Unmarshaler = json.Unmarshaler
)

// SyntaxError and UnmarshalTypeError are aliased for completeness. Prefer
// [Malformed] over asserting on them — see its doc comment for why.
type (
	SyntaxError        = json.SyntaxError
	UnmarshalTypeError = json.UnmarshalTypeError
)

// Decoder is the streaming-decoder surface this module uses, as an interface
// so a caller never names the backend's concrete type.
type Decoder interface {
	Decode(v any) error
	DisallowUnknownFields()
	UseNumber()
	More() bool
}

// Encoder is the streaming-encoder surface this module uses.
type Encoder interface {
	Encode(v any) error
	SetIndent(prefix, indent string)
	SetEscapeHTML(on bool)
}

// Marshal encodes v.
func Marshal(v any) ([]byte, error) { return api.Marshal(v) }

// MarshalIndent encodes v with indentation.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return api.MarshalIndent(v, prefix, indent)
}

// Unmarshal decodes data into v.
func Unmarshal(data []byte, v any) error { return api.Unmarshal(data, v) }

// Valid reports whether data is well-formed JSON.
func Valid(data []byte) bool { return api.Valid(data) }

// Compact appends the compacted form of src to dst — the bytes as written,
// only whitespace removed. Used where a caller must re-emit a document it
// validated WITHOUT decoding it: a decode into any loses integer precision
// past 2^53 and re-escapes HTML, and neither is the payload the caller
// accepted.
func Compact(dst *bytes.Buffer, src []byte) error { return api.Compact(dst, src) }

// NewDecoder returns a streaming decoder over r.
func NewDecoder(r io.Reader) Decoder { return api.NewDecoder(r) }

// NewEncoder returns a streaming encoder over w.
func NewEncoder(w io.Writer) Encoder { return api.NewEncoder(w) }

// Malformed reports whether err is a decoding failure caused by the INPUT —
// bad syntax, a value of the wrong type, a truncated body, an unknown field —
// rather than a failure of whatever was reading it. A handler uses it to
// answer 400 instead of 500.
//
// It exists because that question has no backend-independent answer, and
// getting it wrong is silent. encoding/json reports a type mismatch as
// *json.UnmarshalTypeError; sonic reports the same condition as
// *decoder.MismatchTypeError. An errors.As against the stdlib type therefore
// keeps COMPILING after a backend swap and simply stops matching, and a
// malformed login body starts being answered 500 with nothing in the type
// system to notice. Measured, not hypothesised — it is what the sonic trial
// turned up.
//
// So: a new backend must extend this function. TestMalformed_recognisesTheBackendsOwnErrors
// decodes through [NewDecoder] and asserts through here, so it fails against
// whatever backend is configured — that test, not this comment, is what makes
// the requirement impossible to skip.
func Malformed(err error) bool {
	if err == nil {
		return false
	}

	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &syntaxErr), errors.As(err, &typeErr):
		return true
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return true
	}

	// DisallowUnknownFields has no distinct error type in any backend seen so
	// far — the message is the only signal there is.
	return strings.Contains(err.Error(), "unknown field")
}
