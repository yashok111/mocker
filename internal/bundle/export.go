package bundle

import (
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Export (P4b, 2026-09-02) is the document GET /api/workspaces/{id}/export
// answers and POST /api/workspaces/import reads: the v4 [Bundle] a
// checkpoint's config_snap already holds, with the workspace's entity rows
// beside it under one extra key, `data`, as the SAME [DataBundle] a
// checkpoint's data_snap holds.
//
// The rows travel under `data` and NOT under the Bundle's own `entities`
// field, which DESIGN §17 draws as "only in a checkpoint's data snapshot".
// P3d (D3) put entity rows into a separate document type with its own
// version and its own Validate, and [Validate] refuses a non-null
// `entities` for exactly that reason; an export keeps that refusal intact
// (the embedded Bundle is validated by the same function) and carries the
// rows the way the checkpoint table does, as a second document. A reader
// holding a checkpoint's two columns and a reader holding this document
// therefore decode the same two types.
//
// Bundle is EMBEDDED so its fields promote to the top level: an export is
// byte-for-byte a v4 bundle with one more key, not a wrapper around one —
// a document written by hand for a scenario or lifted out of a checkpoint
// imports unchanged, and an export with `data` omitted IS a plain bundle.
type Export struct {
	Bundle
	// Data is nil when the export was asked without entity rows and when
	// the workspace confirmed no family; omitted from the wire in both
	// cases, so a config-only export is indistinguishable from a bundle.
	Data *DataBundle `json:"data,omitempty"`
}

// EncodeExport canonicalises both halves exactly as [Encode] and
// [EncodeData] do — the same sorted, stable bytes, so that an exported
// document diffs cleanly in git (DESIGN §17's "stable key sorting") — and
// validates them first.
func EncodeExport(e Export) ([]byte, error) {
	if err := Validate(e.Bundle); err != nil {
		return nil, err
	}
	canon, err := canonicalize(e.Bundle)
	if err != nil {
		return nil, fmt.Errorf("bundle: encode export: %w", err)
	}
	out := Export{Bundle: canon}
	if e.Data != nil {
		if err := ValidateData(*e.Data); err != nil {
			return nil, err
		}
		d := canonicalizeData(*e.Data)
		out.Data = &d
	}
	b, err := jsonx.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("bundle: encode export: marshal: %w", err)
	}
	return b, nil
}

// DecodeExport is [Decode] over the export shape: the Bundle half through
// [Validate], the optional Data half through [ValidateData]. A document
// that is a plain v4 bundle (no `data` key) decodes with Data nil.
func DecodeExport(raw []byte) (Export, error) {
	var e Export
	if err := jsonx.Unmarshal(raw, &e); err != nil {
		return Export{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := Validate(e.Bundle); err != nil {
		return Export{}, err
	}
	if e.Data != nil {
		if err := ValidateData(*e.Data); err != nil {
			return Export{}, err
		}
	}
	return e, nil
}
