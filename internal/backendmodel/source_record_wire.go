package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Source sidecars mark the composed read projection. Keep the ordinary source
// record encoder for historical revisions and stored documents without sidecars.
func (n Node) MarshalJSON() ([]byte, error) {
	return jsonx.Marshal(sourceNodeWire(n))
}

func (n Node) MarshalJSONTo(out *jsontext.Encoder) error {
	return json.MarshalEncode(out, sourceNodeWire(n))
}

func sourceNodeWire(n Node) any {
	type record Node
	if n.Source == nil {
		return record(n)
	}
	return struct {
		record
		ExternalKey *string             `json:"externalKey,omitempty"`
		Ownership   *AssertionOwnership `json:"ownership,omitzero"`
		Freshness   *AssertionFreshness `json:"freshness,omitzero"`
	}{record: record(n)}
}

func (e Edge) MarshalJSON() ([]byte, error) {
	return jsonx.Marshal(sourceEdgeWire(e))
}

func (e Edge) MarshalJSONTo(out *jsontext.Encoder) error {
	return json.MarshalEncode(out, sourceEdgeWire(e))
}

func sourceEdgeWire(e Edge) any {
	type record Edge
	if e.Source == nil {
		return record(e)
	}
	return struct {
		record
		ExternalKey *string             `json:"externalKey,omitempty"`
		Ownership   *AssertionOwnership `json:"ownership,omitzero"`
		Freshness   *AssertionFreshness `json:"freshness,omitzero"`
	}{record: record(e)}
}
