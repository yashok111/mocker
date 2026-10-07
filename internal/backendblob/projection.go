package backendblob

import (
	"encoding/json/v2"
	"fmt"
)

// validateProjection verifies the graph query columns against the immutable raw
// record. Decoding inspects only graph identity fields and never re-encodes bytes
// (in particular, unknown numeric lexemes are not converted to float64).
func validateProjection(o Owner, values []any, raw []byte) error {
	if o.Table != "backend_graph_records" {
		return nil
	}
	var record struct {
		ID        string  `json:"id"`
		Kind      string  `json:"kind"`
		Name      string  `json:"name"`
		ParentID  *string `json:"parentId"`
		From      string  `json:"from"`
		To        string  `json:"to"`
		SubjectID string  `json:"subjectId"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("%w: graph payload: %w", ErrCanonical, err)
	}
	m := rowMap(o, values)
	matches := m["id"] == record.ID
	switch m["record_type"] {
	case "node":
		parent := ""
		if record.ParentID != nil {
			parent = *record.ParentID
		}
		matches = matches && m["kind"] == record.Kind && m["name"] == record.Name && m["parent_id"] == parent
	case "edge":
		matches = matches && m["kind"] == record.Kind && m["from_id"] == record.From && m["to_id"] == record.To
	case "evidence":
		matches = matches && m["subject_id"] == record.SubjectID
	default:
		matches = false
	}
	if !matches {
		return fmt.Errorf("%w: graph payload/projection identity mismatch", ErrCanonical)
	}
	return nil
}
