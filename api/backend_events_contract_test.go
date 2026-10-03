package api

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

const eventsContractID = "00000000-0000-4000-8000-000000000001"

func TestBackendEventsQueryStrictViews(t *testing.T) {
	validate := lineageSchemaValidator(t, "QueryBackendEventsRequest")
	for _, view := range []string{"routes", "jobs", "service_calls"} {
		input := `{"revisionId":"` + eventsContractID + `","view":"` + view + `"`
		selector := "serviceId"
		wrong := "seedNodeId"
		if view == "routes" {
			selector, wrong = wrong, selector
		}
		for _, suffix := range []string{`}`, `,"` + selector + `":"` + eventsContractID + `"}`, `,"limit":100,"cursor":""}`} {
			if err := validate(input + suffix); err != nil {
				t.Fatal(view, err)
			}
		}
		for _, suffix := range []string{`,"` + wrong + `":"` + eventsContractID + `"}`, `,"` + selector + `":null}`, `,"` + selector + `":""}`, `,"proposal":{}}`, `,"unknown":true}`, `,"limit":null}`, `,"limit":0}`, `,"limit":101}`, `,"cursor":null}`} {
			if validate(input+suffix) == nil {
				t.Fatal("invalid query accepted", view, suffix)
			}
		}
	}
}

func TestBackendEventsStrictAttributesAndReferenceModes(t *testing.T) {
	const known = `{"status":"known","value":"native"}`
	const unknown = `{"status":"unknown","reason":"unavailable in source"}`
	const complete = `"analysisStatus":"complete","gaps":[]`
	cases := []struct{ name, wire string }{
		{"Channel", `{` + complete + `,"protocol":` + known + `,"address":` + unknown + `,"scope":` + known + `}`},
		{"Message", `{` + complete + `,"fieldInventory":"complete"}`},
		{"Consumer", `{"analysisStatus":"partial","gaps":["unresolved remainder"],"dispatchStatus":"unknown","dispatchReason":"missing dispatcher"}`},
		{"Job", `{` + complete + `,"dispatchStatus":"complete","trigger":{"kind":"cron","expression":` + known + `,"timezone":` + unknown + `}}`},
		{"EventField", `{` + complete + `,"section":"payload","path":[{"property":"order"},{"items":true}],"nativeType":` + known + `}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := lineageSchemaValidator(t, "BackendEvents"+tc.name+"Attributes")
			if err := v(tc.wire); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{strings.TrimSuffix(tc.wire, `}`) + `,"extra":true}`, strings.Replace(tc.wire, `"analysisStatus":"complete"`, `"analysisStatus":"unknown"`, 1), strings.Replace(tc.wire, `"dispatchStatus":"unknown"`, `"dispatchStatus":"complete"`, 1)} {
				if bad != tc.wire && v(bad) == nil {
					t.Fatal("invalid attrs accepted", bad)
				}
			}
		})
	}
	for _, persisted := range []bool{false, true} {
		suffix, mode, value := "Input", "Key", "ref"
		if persisted {
			suffix, mode, value = "", "Id", eventsContractID
		}
		for _, tc := range []struct{ name, wire string }{
			{"Emits", `{"channel` + mode + `":"` + value + `","deliveryStatus":"declared"}`},
			{"DeliveredTo", `{"message` + mode + `":"` + value + `","condition":` + known + `,"group":` + unknown + `,"deliveryStatus":"unknown","deliveryReason":"not resolved"}`},
			{"Retries", `{"message` + mode + `":"` + value + `","reason":"configured","delay":` + known + `,"maxAttempts":` + known + `}`},
			{"DeadLetters", `{"message` + mode + `":"` + value + `","reason":"configured"}`},
		} {
			v := lineageSchemaValidator(t, "BackendEvents"+tc.name+"Attributes"+suffix)
			if err := v(tc.wire); err != nil {
				t.Fatal(err)
			}
			other := "Id"
			if persisted {
				other = "Key"
			}
			for _, bad := range []string{strings.Replace(tc.wire, mode+`"`, other+`"`, 1), strings.TrimSuffix(tc.wire, `}`) + `,"extra":null}`, strings.Replace(tc.wire, `"deliveryStatus":"declared"`, `"deliveryStatus":"unknown"`, 1), strings.Replace(tc.wire, `"deliveryStatus":"unknown"`, `"deliveryStatus":"declared"`, 1)} {
				if bad != tc.wire && v(bad) == nil {
					t.Fatal("invalid ref mode accepted", bad)
				}
			}
		}
	}
	scalar := lineageSchemaValidator(t, "BackendEventsScalar")
	for _, good := range []string{known, unknown} {
		if err := scalar(good); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{`null`, `{"status":"known","value":null}`, `{"status":"known","value":3}`, `{"status":"known","value":"x","reason":"mixed"}`, `{"status":"unknown","value":"x","reason":"mixed"}`} {
		if scalar(bad) == nil {
			t.Fatal("invalid scalar accepted", bad)
		}
	}
	trigger := lineageSchemaValidator(t, "BackendEventsTrigger")
	for _, good := range []string{`{"kind":"cron","expression":` + known + `,"timezone":` + known + `}`, `{"kind":"interval","duration":` + unknown + `}`, `{"kind":"manual"}`, `{"kind":"unknown","reason":"missing source"}`} {
		if err := trigger(good); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{`{"kind":"cron","expression":` + known + `}`, `{"kind":"manual","duration":` + known + `}`, `{"kind":"unknown","reason":null}`, `{"kind":"interval","duration":` + known + `,"timezone":` + known + `}`} {
		if trigger(bad) == nil {
			t.Fatal("invalid trigger accepted", bad)
		}
	}
}

func TestBackendEventsContextualLineageStrictModes(t *testing.T) {
	for _, imported := range []bool{false, true} {
		prefix, mode, value := "BackendLineage", "Id", eventsContractID
		if imported {
			prefix, mode, value = "BackendImportLineage", "Key", "event"
		}
		address := `{"kind":"event_field","node` + mode + `":"` + value + `","endpoint` + mode + `":"` + value + `","route` + mode + `":"` + value + `"}`
		validate := lineageSchemaValidator(t, prefix+"ValueRef")
		if err := validate(address); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{strings.Replace(address, `,"route`+mode+`":"`+value+`"`, "", 1), strings.TrimSuffix(address, `}`) + `,"facetKey":"sql"}`, strings.Replace(address, `"endpoint`+mode+`":"`+value+`"`, `"endpoint`+mode+`":null`, 1)} {
			if validate(bad) == nil {
				t.Fatal("invalid contextual address accepted", bad)
			}
		}
		transport := `{"emitsEdge` + mode + `":"` + value + `","deliveryEdge` + mode + `":"` + value + `"}`
		mapping := `{"analysisStatus":"complete","gaps":[],"sources":[` + address + `],"destination":` + address + `,"transform":{"kind":"copy","description":"explicit transport","redacted":false},"transport":` + transport + `}`
		validate = lineageSchemaValidator(t, prefix+"MappingAttributes")
		if err := validate(mapping); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{strings.Replace(mapping, transport, `null`, 1), strings.Replace(mapping, transport, strings.TrimSuffix(transport, `}`)+`,"extra":true}`, 1)} {
			if validate(bad) == nil {
				t.Fatal("invalid transport accepted", bad)
			}
		}
	}
}

func TestBackendEventsProfileExtensionAndExactProviders(t *testing.T) {
	validate := lineageSchemaValidator(t, "BeginBackendImportRequest")
	items := make([]any, 0, 9)
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		items = append(items, map[string]any{"category": category, "status": "unsupported", "knownCount": 0, "denominator": nil, "discoverySource": "collector", "gaps": []string{}, "reason": "outside scope"})
	}
	profiles := []string{"foundation-graph-v1", "relational-graph-v1", "runtime-flow-v1", "field-lineage-v1", "events-service-v1"}
	provider := map[string]any{"name": "collector", "version": "1", "namespace": "test", "method": "agent", "profiles": profiles, "limitations": []string{}}
	input := map[string]any{"expectedVersion": 1, "baseRevisionId": eventsContractID, "idempotencyKey": "events-contract", "profile": "events-service-v1", "inventory": items, "manifest": map[string]any{"repositoryName": "orders", "provider": provider, "snapshot": map[string]any{"dirty": false, "consistency": "verified", "capturedAt": "2026-10-03T10:00:00Z", "files": []any{}}}}
	check := func(want bool) {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		err = validate(string(raw))
		if (err == nil) != want {
			t.Fatalf("valid=%t err=%v %s", want, err, raw)
		}
	}
	check(true)
	for _, bad := range [][]string{profiles[:4], append(append([]string{}, profiles...), "extra"), {profiles[0], profiles[1], profiles[2], profiles[3], profiles[3]}} {
		provider["profiles"] = bad
		check(false)
	}
	provider["profiles"] = profiles
	input["mode"] = "reconcile"
	input["repositoryId"] = eventsContractID
	input["graphScope"] = map[string]any{"profile": "events-service-v1", "status": "partial", "gaps": []string{"bounded scope"}}
	check(true)
	extension := map[string]any{"fromProfile": "field-lineage-v1", "toProfile": "events-service-v1"}
	input["profileExtension"] = extension
	check(true)
	for _, from := range profiles {
		if from == "field-lineage-v1" {
			continue
		}
		extension["fromProfile"] = from
		check(false)
	}
	extension["fromProfile"] = "field-lineage-v1"
	input["profile"] = "field-lineage-v1"
	check(false)
}

// Bounded ownership traversal can emit ownership_depth for otherwise legal
// acyclic ancestry. All three view contracts must preserve that incomplete-read
// diagnostic, including when selected through the public page union.
func TestBackendEventsPageOwnershipDepthDiagnostic(t *testing.T) {
	const page = `{"projectId":"` + eventsContractID + `","revisionId":"` + eventsContractID + `","semanticHash":"` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `","policy":"source-events-projection-v1","view":"@view@","items":[],"nextCursor":"","coverage":{"coverage":{"status":"partial","denominator":null,"knownObjects":0,"gaps":["bounded owning-service inspection"]},"inventory":[],"snapshots":[]},"limits":{"maxExaminedEdges":20000,"maxItems":5000,"maxAuxiliaryRecords":20000,"maxWitnessRecords":256,"defaultPageSize":50,"maxPageSize":100,"scanPolicy":"complete-scan-admission"},"complete":false,"truncated":true,"truncationReasons":["@reason@"],"limitations":["Owning service was not established within the ancestry bound"],"totalEdgeCount":260,"examinedEdgeCount":260,"constructedItemCount":0,"auxiliaryRecordCount":0}`
	for _, tc := range []struct{ view, schema string }{{"routes", "BackendEventsRoutesPage"}, {"jobs", "BackendEventsJobsPage"}, {"service_calls", "BackendEventsServiceCallsPage"}} {
		for _, schema := range []string{tc.schema, "BackendEventsPage"} {
			t.Run(tc.view+"/"+schema, func(t *testing.T) {
				validate := lineageSchemaValidator(t, schema)
				wire := strings.ReplaceAll(page, "@view@", tc.view)
				for _, reason := range []string{"edge_limit", "item_limit", "auxiliary_limit", "witness_limit", "ownership_depth"} {
					if err := validate(strings.ReplaceAll(wire, "@reason@", reason)); err != nil {
						t.Errorf("diagnostic %s rejected: %v", reason, err)
					}
				}
				if validate(strings.ReplaceAll(wire, "@reason@", "unrecognized_diagnostic")) == nil {
					t.Fatal("unknown diagnostic accepted")
				}
			})
		}
	}
}
