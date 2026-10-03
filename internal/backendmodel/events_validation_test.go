package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func eventNodeAttrs(t *testing.T, kind string) map[string]jsontext.Value {
	a := map[string]any{"analysisStatus": "complete", "gaps": []string{}}
	switch kind {
	case "channel":
		a["protocol"], a["address"], a["scope"] = known("kafka"), known("orders"), known("local")
	case "message":
		a["fieldInventory"] = "complete"
	case "consumer":
		a["dispatchStatus"] = "complete"
	case "job":
		a["dispatchStatus"] = "complete"
		a["trigger"] = map[string]any{"kind": "manual"}
	case "event_field":
		a["section"] = "payload"
		a["path"] = []any{map[string]any{"property": "id"}}
		a["nativeType"] = known("string")
	case "flow_step":
		a["stepKind"] = "emit"
		a["inputs"] = []any{}
		a["outputs"] = []any{}
		a["nativeText"] = "send()"
		a["transactionContext"] = map[string]any{"status": "none", "reason": "No transaction"}
	}
	return runtimeAttrs(t, a)
}
func TestEventsStrictShapes(t *testing.T) {
	for _, kind := range []string{"channel", "message", "consumer", "job", "event_field", "flow_step"} {
		t.Run(kind, func(t *testing.T) {
			c := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "subject", Kind: kind, Name: kind, Attributes: eventNodeAttrs(t, kind)}}
			if err := validateCommand(c, &ImportSession{Profile: "events-service-v1"}); err != nil {
				t.Fatal(err)
			}
			if err := validateCommand(c, &ImportSession{Profile: LineageProfile}); err == nil {
				t.Fatal("source4 accepted event shape")
			}
			c.Node.Attributes["extra"] = jsontext.Value(`true`)
			if err := validateCommand(c, &ImportSession{Profile: "events-service-v1"}); err == nil {
				t.Fatal("unknown member accepted")
			}
		})
	}
	for _, tc := range []struct {
		kind  string
		attrs map[string]any
	}{
		{"emits", map[string]any{"channelKey": "channel", "deliveryStatus": "declared"}},
		{"delivered_to", map[string]any{"messageKey": "message", "deliveryStatus": "declared", "condition": known("always"), "group": known("orders")}},
		{"retries", map[string]any{"messageKey": "message", "reason": "Retry config", "delay": known("1s"), "maxAttempts": known("3")}},
		{"dead_letters", map[string]any{"messageKey": "message", "reason": "DLQ config"}},
	} {
		c := ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "edge", Kind: tc.kind, FromKey: "from", ToKey: "to", Attributes: runtimeAttrs(t, tc.attrs)}}
		if err := validateCommand(c, &ImportSession{Profile: "events-service-v1"}); err != nil {
			t.Fatal(tc.kind, err)
		}
		if err := validateCommand(c, &ImportSession{Profile: LineageProfile}); err == nil {
			t.Fatal("source4 accepted event relation")
		}
	}
}
func TestEventsIncompleteContracts(t *testing.T) {
	for _, kind := range []string{"message", "consumer", "job"} {
		a := eventNodeAttrs(t, kind)
		key := "dispatchStatus"
		if kind == "message" {
			key = "fieldInventory"
		}
		a[key] = relationalRaw(t, "unknown")
		if err := validateCommand(ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "n", Kind: kind, Name: "n", Attributes: a}}, &ImportSession{Profile: "events-service-v1"}); err == nil {
			t.Fatal("complete analysis hides unknown", kind)
		}
	}
}
