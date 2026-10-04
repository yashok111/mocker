package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestSource6RecordWireOmitsSingularIdentity(t *testing.T) {
	for _, record := range []any{
		Node{ID: "node", Kind: "service", Source: &SourceReadContext{}},
		Edge{ID: "edge", Kind: "calls", Source: &SourceReadContext{}},
	} {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"externalKey", "ownership", "freshness"} {
			if _, present := object[field]; present {
				t.Fatalf("source6 structural projection invented %s: %s", field, raw)
			}
		}
	}
}

func TestLegacyRecordWirePreservesBothEncoders(t *testing.T) {
	node := Node{ExternalKey: "<legacy>&", Attributes: map[string]jsontext.Value{
		"z": []byte(`{"n":9007199254740993}`), "a": []byte(`"<value>&"`),
	}}
	type historicalNode Node
	want, err := jsonx.Marshal(historicalNode(node))
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonx.Marshal(node)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("HTTP encoder legacy bytes changed: %s vs %s (%v)", got, want, err)
	}
	options := json.JoinOptions(json.Deterministic(true), json.FormatNilSliceAsNull(true))
	want, err = json.Marshal(historicalNode(node), options)
	if err != nil {
		t.Fatal(err)
	}
	got, err = json.Marshal(node, options)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("domain encoder options lost: %s vs %s (%v)", got, want, err)
	}
}

func TestLegacyRecordWireKeepsExternalKey(t *testing.T) {
	for _, record := range []any{Node{ExternalKey: "original"}, Edge{ExternalKey: "original"}} {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		if object["externalKey"] != "original" {
			t.Fatalf("legacy identity wire changed: %s", raw)
		}
	}
}
