package backendmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const eventsOrdersSourceDir = "testdata/events/orders"

// Commands are transcribed from source, independently of expected.json. Reading
// the source bytes supplies hashes and excerpts; no fixture source is executed.
func eventsOrdersInput(t *testing.T, p *Project, resolved bool) BeginImportInput {
	t.Helper()
	in := eventsProfileInput(firstImportFixture(p), false)
	in.IdempotencyKey = fmt.Sprintf("orders-events-begin-%t", resolved)
	in.Manifest.RepositoryName = "orders-events-fixture"
	in.Manifest.Provider = SourceProvider{Name: "orders-events-fixture", Version: "1", Namespace: "orders-events-fixture", Method: "agent", Profiles: []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile}, Limitations: []string{"Inert source only; no runtime or delivery observations", "Dynamic audit remainder and private-ledger source remain unknown"}}
	in.Manifest.Snapshot.Files = []ManifestFile{}
	files := []string{"orders.ts.txt", "wiring.ts.txt", "billing.ts.txt", "jobs.ts.txt"}
	if resolved {
		files = append(files, "fraud-plugin.ts.txt")
	}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(eventsOrdersSourceDir, file))
		if err != nil {
			t.Fatal(err)
		}
		in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, ManifestFile{Path: file, ContentHash: hashBytes(raw), FileType: "typescript", AnalysisStatus: "analyzed"})
	}
	// Initial import cannot specify reconciliation graph scope.
	in.GraphScope = nil
	counts := map[string]int64{"files": int64(len(files)), "endpoints": 1, "datastores": 1, "producers": 3, "consumers": 6, "jobs": 2}
	for i := range in.Inventory {
		x := &in.Inventory[i]
		x.KnownCount = counts[x.Category]
		x.Denominator = new(x.KnownCount)
		x.DiscoverySource = "physical fixture source declarations"
		if slices.Contains([]string{"endpoints", "datastores", "consumers"}, x.Category) {
			x.Status = "partial"
			x.Denominator = nil
			x.Gaps = []string{"Bounded supplied source; unresolved handlers and database schema details"}
		}
	}
	return in
}

type eventsOrdersWitness struct {
	file       string
	start, end int64
}

func ordersWitness(file string, start, end int64) eventsOrdersWitness {
	return eventsOrdersWitness{file, start, end}
}

type eventsOrdersBuilder struct {
	t        *testing.T
	s        *ImportSession
	commands []ImportCommand
	files    map[string]ManifestFile
	lines    map[string][]string
}

func (b *eventsOrdersBuilder) evidence(typ, key string, witnesses []eventsOrdersWitness) []string {
	b.t.Helper()
	keys := make([]string, 0, len(witnesses))
	for i, w := range witnesses {
		f, ok := b.files[w.file]
		lines := b.lines[w.file]
		if !ok || w.start < 1 || w.end < w.start || w.end > int64(len(lines)) {
			b.t.Fatalf("invalid source witness %s: %+v", key, w)
		}
		proof := fmt.Sprintf("proof:%s:%s:%d", typ, key, i)
		keys = append(keys, proof)
		b.commands = append(b.commands, ImportCommand{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: proof, SubjectType: typ, SubjectKey: key, Method: "agent", Status: "explicit", Source: EvidenceSource{RepositoryID: b.s.RepositoryID, SnapshotID: b.s.SnapshotID, File: w.file, ContentHash: f.ContentHash, StartLine: new(w.start), EndLine: new(w.end)}, Snippet: new(strings.Join(lines[w.start-1:w.end], "\n"))}})
	}
	return keys
}
func (b *eventsOrdersBuilder) node(key, kind, name, parent string, a map[string]any, witnesses ...eventsOrdersWitness) {
	b.t.Helper()
	n := &ImportNode{ExternalKey: key, Kind: kind, Name: name, Attributes: runtimeAttrs(b.t, a), EvidenceKeys: b.evidence("node", key, witnesses)}
	if parent != "" {
		n.ParentKey = new(parent)
	}
	b.commands = append(b.commands, ImportCommand{Op: "upsert_node", Node: n})
	if parent != "" {
		b.edge("contains:"+key, "contains", parent, key, map[string]any{}, witnesses...)
	}
}
func (b *eventsOrdersBuilder) edge(key, kind, from, to string, a map[string]any, witnesses ...eventsOrdersWitness) {
	b.t.Helper()
	proofs := b.evidence("edge", key, witnesses)
	b.commands = append(b.commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: key, Kind: kind, FromKey: from, ToKey: to, Attributes: runtimeAttrs(b.t, a), EvidenceKeys: proofs}})
}
func ordersComplete() map[string]any {
	return map[string]any{"analysisStatus": "complete", "gaps": []string{}}
}
func ordersPartial(reason string) map[string]any {
	return map[string]any{"analysisStatus": "partial", "gaps": []string{reason}}
}
func ordersPort(key string) map[string]any {
	return map[string]any{"key": key, "name": key, "nativeType": known("string")}
}
func ordersPortRef(node, collection, key string) ImportLineageValueRef {
	return ImportLineageValueRef{Kind: "port", NodeKey: node, Collection: collection, PortKey: key}
}
func ordersEventRef(field, endpoint, route string) ImportLineageValueRef {
	return ImportLineageValueRef{Kind: "event_field", NodeKey: "field.orders." + field, EndpointKey: endpoint, RouteKey: route}
}
func (b *eventsOrdersBuilder) step(key, parent, kind string, tx bool, inputs, outputs []any, w eventsOrdersWitness) {
	a := ordersComplete()
	a["stepKind"], a["inputs"], a["outputs"] = kind, inputs, outputs
	a["nativeText"] = strings.Join(b.lines[w.file][w.start-1:w.end], "\n")
	a["transactionContext"] = map[string]any{"status": "none", "reason": "Outside the shown local SQL transaction"}
	if tx {
		a["transactionContext"] = map[string]any{"status": "known", "transactionKey": "tx.cancel"}
	}
	if kind == "call" {
		a["dispatchStatus"] = "complete"
	}
	b.node(key, "flow_step", key, parent, a, w)
}
func (b *eventsOrdersBuilder) mapping(key, parent, transform string, sources []any, destination any, transport *ImportLineageTransport, witnesses ...eventsOrdersWitness) {
	a := ordersComplete()
	a["sources"], a["destination"] = sources, destination
	a["transform"] = LineageTransform{Kind: transform, Description: key + ": explicit source dependency", Redacted: false}
	if transport != nil {
		a["transport"] = transport
	}
	b.node(key, "field_mapping", key, parent, a, witnesses...)
}

func eventsOrdersCommands(t *testing.T, s *ImportSession, resolved bool) []ImportCommand {
	t.Helper()
	b := eventsOrdersBuilder{t: t, s: s, commands: []ImportCommand{}, files: map[string]ManifestFile{}, lines: map[string][]string{}}
	for _, f := range s.Manifest.Snapshot.Files {
		if f.AnalysisStatus != "analyzed" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(eventsOrdersSourceDir, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		if hashBytes(raw) != f.ContentHash {
			t.Fatalf("fixture manifest hash changed for %s", f.Path)
		}
		b.files[f.Path], b.lines[f.Path] = f, strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	ow := func(start, end int64) eventsOrdersWitness { return ordersWitness("orders.ts.txt", start, end) }
	ww := func(start, end int64) eventsOrdersWitness { return ordersWitness("wiring.ts.txt", start, end) }
	bw := func(start, end int64) eventsOrdersWitness { return ordersWitness("billing.ts.txt", start, end) }
	jw := func(start, end int64) eventsOrdersWitness { return ordersWitness("jobs.ts.txt", start, end) }
	b.node("svc.orders", "service", "Orders", "", map[string]any{}, ww(23, 23))
	b.node("svc.billing", "service", "Billing", "", map[string]any{}, ww(24, 24))
	for _, c := range []struct {
		key, name, address, scope string
		line                      int64
	}{{"primary", "Order events", "orders.cancelled", "orders", 2}, {"secondary", "Order events", "orders.cancelled.secondary", "orders", 3}, {"returns", "Order events", "returns.cancelled", "returns", 4}, {"retry", "Retry", "orders.retry", "orders", 5}, {"dlq", "DLQ", "orders.dlq", "orders", 6}, {"orphan", "Orphan", "orders.orphan", "orders", 7}} {
		a := ordersComplete()
		a["protocol"], a["address"], a["scope"] = known("kafka"), known(c.address), known(c.scope)
		b.node("ch."+c.key, "channel", c.name, "svc.orders", a, ww(c.line, c.line))
	}
	for _, m := range []struct {
		key        string
		start, end int64
		fields     []string
	}{{"orders", 2, 4, []string{"orderId", "status", "reason"}}, {"returns", 5, 7, []string{"orderId", "status"}}} {
		a := ordersComplete()
		a["fieldInventory"] = "complete"
		b.node("msg."+m.key, "message", "OrderCancelled", "svc.orders", a, ow(m.start, m.end))
		for _, field := range m.fields {
			a := ordersComplete()
			a["section"], a["path"], a["nativeType"] = "payload", []eventsPathSegment{{Property: field}}, known("string")
			b.node("field."+m.key+"."+field, "event_field", field, "msg."+m.key, a, ow(m.start, m.end))
		}
	}
	for _, c := range []struct {
		key, name, status, reason string
		line                      int64
	}{{"cancel", "Cancel order", "complete", "", 8}, {"return", "Cancel order", "complete", "", 11}, {"orphan", "Orphan listener", "complete", "", 15}, {"fraud", "Fraud cancellation", "unknown", "Plugin registration absent from baseline analyzed scope", 17}, {"audit", "Audit", "partial", "runtimeDispatchKey has an unknown remainder", 19}, {"legacy", "Legacy cancellation", "unknown", "private-ledger module outside supplied source", 21}} {
		if c.key == "fraud" && resolved {
			c.status, c.reason = "complete", ""
		}
		a := ordersComplete()
		a["dispatchStatus"] = c.status
		if c.reason != "" {
			a = ordersPartial(c.reason)
			a["dispatchStatus"], a["dispatchReason"] = c.status, c.reason
		}
		b.node("consumer."+c.key, "consumer", c.name, "svc.billing", a, ww(c.line, c.line))
		if c.reason != "" {
			b.node("unknown."+c.key, "unresolved_target", c.name+" unknown handler", "svc.billing", map[string]any{"expectedKind": "handler", "reason": c.reason, "searchScope": "Supplied fixture manifest members only"}, ww(c.line, c.line))
			b.edge("handles.unknown."+c.key, "handles", "consumer."+c.key, "unknown."+c.key, map[string]any{}, ww(c.line, c.line))
		}
	}
	for _, f := range []struct {
		key, service, entry, exit string
		w                         eventsOrdersWitness
	}{{"cancel", "orders", "step.input", "step.return.cancel", ow(9, 23)}, {"return", "orders", "step.emit.returns", "step.emit.returns", ow(25, 28)}, {"billing.cancel", "billing", "step.decode.cancel", "step.decode.cancel", bw(2, 6)}, {"billing.return", "billing", "step.billing.return", "step.billing.return", bw(7, 10)}, {"billing.orphan", "billing", "step.billing.orphan", "step.billing.orphan", bw(11, 11)}, {"billing.audit", "billing", "step.billing.audit", "step.billing.audit", bw(12, 12)}, {"billing.http", "billing", "step.billing.http", "step.billing.http", bw(15, 18)}, {"cron", "orders", "step.cron", "step.cron", jw(5, 7)}, {"manual", "orders", "step.manual", "step.manual", jw(11, 11)}} {
		b.node("handler."+f.key, "handler", f.key, "svc."+f.service, map[string]any{"language": "typescript"}, f.w)
		a := ordersComplete()
		a["entryStepKey"], a["exitStepKeys"], a["exitStatus"] = f.entry, []string{f.exit}, "complete"
		b.node("flow."+f.key, "flow", f.key, "handler."+f.key, a, f.w)
		if strings.HasPrefix(f.key, "billing.") && f.key != "billing.cancel" || f.key == "cron" || f.key == "manual" {
			b.step(f.entry, "flow."+f.key, "transform", false, []any{}, []any{}, f.w)
		}
	}
	b.step("step.input", "flow.cancel", "input", false, []any{ordersPort("operatorReason"), ordersPort("orderId")}, []any{}, ow(9, 9))
	b.step("step.begin", "flow.cancel", "transaction_begin", true, []any{}, []any{}, ow(10, 10))
	b.step("step.query", "flow.cancel", "query", true, []any{}, []any{}, ow(11, 14))
	b.step("step.reason", "flow.cancel", "transform", true, []any{}, []any{ordersPort("reason")}, ow(15, 15))
	b.step("step.emit.primary", "flow.cancel", "emit", true, []any{ordersPort("status"), ordersPort("reason")}, []any{}, ow(16, 17))
	b.step("step.emit.secondary", "flow.cancel", "emit", true, []any{ordersPort("status"), ordersPort("reason")}, []any{}, ow(16, 18))
	b.step("step.commit", "flow.cancel", "transaction_commit", true, []any{}, []any{}, ow(20, 20))
	b.step("step.call.billing", "flow.cancel", "call", false, []any{}, []any{}, ow(21, 21))
	b.step("step.return.cancel", "flow.cancel", "return", false, []any{}, []any{}, ow(22, 22))
	b.step("step.emit.returns", "flow.return", "emit", false, []any{}, []any{}, ow(26, 27))
	b.step("step.decode.cancel", "flow.billing.cancel", "transform", false, []any{}, []any{ordersPort("status")}, bw(3, 4))
	chain := []string{"step.input", "step.begin", "step.query", "step.reason", "step.emit.primary", "step.emit.secondary", "step.commit", "step.call.billing", "step.return.cancel"}
	proofs := []eventsOrdersWitness{ow(9, 10), ow(10, 14), ow(11, 15), ow(15, 17), ow(17, 18), ow(18, 20), ow(20, 21), ow(21, 22)}
	controlKeys := []string{"control.input", "control.begin", "control.reason", "control.primary", "control.secondary", "control.commit", "control.call", "control.return"}
	for i, key := range controlKeys {
		b.edge(key, "next", chain[i], chain[i+1], map[string]any{}, proofs[i])
	}
	a := ordersComplete()
	a["datastoreKey"], a["connectionScope"], a["isolationLevel"], a["boundaryStatus"] = "db.orders", known("tx"), known("read committed"), "complete"
	b.node("tx.cancel", "transaction", "Cancel transaction", "flow.cancel", a, ow(10, 10), ow(20, 20))
	b.edge("begins.cancel", "begins", "step.begin", "tx.cancel", map[string]any{}, ow(10, 10))
	b.edge("commits.cancel", "commits", "step.commit", "tx.cancel", map[string]any{}, ow(20, 20))
	facet := func(key string) map[string]any {
		return map[string]any{"sourceKind": "sql", "dialect": "postgresql", "analysisStatus": "partial", "gaps": []string{"Only the update/returning source is supplied; schema declarations absent"}, "evidenceKeys": []string{"proof:node:" + key + ":0", "proof:node:" + key + ":1"}}
	}
	f := facet("db.orders")
	f["databaseName"], f["qualifiedName"], f["nativeDefinition"] = "orders", "orders", nil
	b.node("db.orders", "datastore", "Orders DB", "svc.orders", map[string]any{"relational": map[string]any{"facets": map[string]any{"sql": f}}}, ow(10, 14), ow(29, 31))
	// Required relational schema carrier; source does not name a SQL schema.
	f = facet("schema.orders")
	f["qualifiedName"], f["nativeDefinition"] = "<unknown schema>", nil
	b.node("schema.orders", "db_schema", "Unknown SQL schema", "db.orders", map[string]any{"facets": map[string]any{"sql": f}}, ow(11, 14), ow(29, 31))
	f = facet("table.orders")
	f["qualifiedName"], f["nativeDefinition"], f["columnsStatus"], f["constraintsStatus"] = "orders", nil, "partial", "partial"
	f["gaps"] = []string{"SQL column definitions and constraints are unknown; schema source is absent"}
	b.node("table.orders", "table", "orders", "schema.orders", map[string]any{"facets": map[string]any{"sql": f}}, ow(11, 14), ow(29, 31))
	f = facet("column.status")
	for _, field := range []string{"nativeType", "typeFamily", "nullable", "defaultExpression", "generatedExpression", "identity", "ordinal"} {
		f[field] = unknown("Column definition absent from supplied source")
	}
	b.node("column.status", "column", "status", "table.orders", map[string]any{"facets": map[string]any{"sql": f}}, ow(11, 14), ow(29, 31))
	a = ordersPartial("Columns other than returned status are not analyzed")
	a["dialect"], a["nativeDefinition"], a["parameters"], a["results"], a["columnScope"] = "postgresql", "UPDATE orders SET status = 'cancelled' WHERE id = $1 RETURNING id, status", []any{ordersPort("orderId")}, []any{ordersPort("status")}, "partial"
	b.node("query.cancel", "query", "Cancel SQL", "handler.cancel", a, ow(11, 14), ow(29, 31))
	b.edge("calls.query", "calls", "step.query", "query.cancel", map[string]any{}, ow(11, 14))
	b.edge("reads.status", "reads", "query.cancel", "column.status", map[string]any{"accessMode": "read", "datastoreKey": "db.orders", "facetKey": "sql", "columnScope": "listed"}, ow(11, 14))
	b.edge("writes.status", "writes", "query.cancel", "column.status", map[string]any{"accessMode": "update", "datastoreKey": "db.orders", "facetKey": "sql", "columnScope": "listed"}, ow(11, 14))
	for _, route := range []struct {
		suffix, msg, ch string
		line            int64
	}{{"primary", "orders", "primary", 17}, {"secondary", "orders", "secondary", 18}, {"returns", "returns", "returns", 27}} {
		a := map[string]any{}
		a["channelKey"], a["deliveryStatus"] = "ch."+route.ch, "declared"
		b.edge("emits."+route.suffix, "emits", "step.emit."+route.suffix, "msg."+route.msg, a, ow(route.line, route.line))
	}
	for _, route := range []struct {
		suffix, ch, msg, consumer, group, condition string
		line                                        int64
	}{{"primary", "primary", "orders", "cancel", "billing", "type=cancelled", 9}, {"secondary", "secondary", "orders", "cancel", "billing", "type=cancelled", 10}, {"returns", "returns", "returns", "return", "billing", "type=cancelled", 12}, {"orphan", "orphan", "orders", "orphan", "billing-orphan", "all", 16}, {"fraud", "primary", "orders", "fraud", "fraud", "risk=high", 18}, {"audit", "primary", "orders", "audit", "audit", "all", 20}, {"legacy", "primary", "orders", "legacy", "legacy", "all", 22}} {
		a := map[string]any{}
		a["messageKey"], a["condition"], a["group"], a["deliveryStatus"] = "msg."+route.msg, known(route.condition), known(route.group), "declared"
		b.edge("delivery."+route.suffix, "delivered_to", "ch."+route.ch, "consumer."+route.consumer, a, ww(route.line, route.line))
	}
	a = map[string]any{}
	a["messageKey"], a["reason"], a["delay"], a["maxAttempts"] = "msg.orders", "transient failure", known("PT30S"), known("3")
	b.edge("retry.cancel", "retries", "consumer.cancel", "ch.retry", a, ww(13, 13))
	a = map[string]any{}
	a["messageKey"], a["reason"] = "msg.orders", "retry attempts exhausted"
	b.edge("dlq.cancel", "dead_letters", "consumer.cancel", "ch.dlq", a, ww(14, 14))
	for _, h := range []struct {
		suffix, target string
		line           int64
		w              eventsOrdersWitness
	}{{"cancel", "billing.cancel", 8, bw(2, 6)}, {"return", "billing.return", 11, bw(7, 10)}, {"orphan", "billing.orphan", 15, bw(11, 11)}, {"audit", "billing.audit", 19, bw(12, 12)}} {
		b.edge("handles."+h.suffix, "handles", "consumer."+h.suffix, "handler."+h.target, map[string]any{}, ww(h.line, h.line), h.w)
	}
	for _, j := range []struct {
		suffix, name string
		trigger      map[string]any
		w            eventsOrdersWitness
	}{{"cron", "Cancellation sweep", map[string]any{"kind": "cron", "expression": known("0 3 * * *"), "timezone": known("UTC")}, jw(2, 4)}, {"manual", "Manual replay", map[string]any{"kind": "manual"}, jw(8, 10)}} {
		a := ordersComplete()
		a["trigger"], a["dispatchStatus"] = j.trigger, "complete"
		b.node("job."+j.suffix, "job", j.name, "svc.orders", a, j.w)
		b.edge("handles.job."+j.suffix, "handles", "job."+j.suffix, "handler."+j.suffix, map[string]any{}, j.w)
	}
	b.node("operation.billing", "http_operation", "Cancel order HTTP", "svc.billing", map[string]any{"method": "POST", "path": "/internal/orders/cancel"}, bw(14, 14))
	b.edge("call.billing", "calls", "step.call.billing", "operation.billing", map[string]any{}, ow(21, 21), bw(14, 14))
	b.edge("handles.http", "handles", "operation.billing", "handler.billing.http", map[string]any{}, bw(14, 18))
	if resolved {
		pw := func(start, end int64) eventsOrdersWitness { return ordersWitness("fraud-plugin.ts.txt", start, end) }
		b.node("handler.fraud", "handler", "onFraudCancelled", "svc.billing", map[string]any{"language": "typescript"}, pw(3, 6))
		a := ordersComplete()
		a["entryStepKey"], a["exitStepKeys"], a["exitStatus"] = "step.fraud", []string{"step.fraud"}, "complete"
		b.node("flow.fraud", "flow", "Fraud handler", "handler.fraud", a, pw(3, 6))
		b.step("step.fraud", "flow.fraud", "transform", false, []any{}, []any{}, pw(4, 5))
		b.edge("handles.fraud", "handles", "consumer.fraud", "handler.fraud", map[string]any{}, ww(17, 17), pw(2, 6))
	}
	b.mapping("map.db.status", "query.cancel", "copy", []any{ImportLineageValueRef{Kind: "column", NodeKey: "column.status", FacetKey: "sql"}}, ordersPortRef("query.cancel", "results", "status"), nil, ow(11, 14))
	b.mapping("map.reason", "step.reason", "compute", []any{ordersPortRef("query.cancel", "results", "status"), ordersPortRef("step.input", "inputs", "operatorReason")}, ordersPortRef("step.reason", "outputs", "reason"), nil, ow(15, 15))
	for _, route := range []struct {
		suffix                 string
		emitLine, deliveryLine int64
	}{{"primary", 17, 9}, {"secondary", 18, 10}} {
		for _, field := range []string{"status", "reason"} {
			source := ordersPortRef("query.cancel", "results", "status")
			if field == "reason" {
				source = ordersPortRef("step.reason", "outputs", "reason")
			}
			// Explicit producer inputs preserve the oracle's semantic source path.
			// This adds an address carrier, with no observed runtime-value claim.
			emitInput := ordersPortRef("step.emit."+route.suffix, "inputs", field)
			b.mapping("map.emit_input."+route.suffix+"."+field, "step.emit."+route.suffix, "copy", []any{source}, emitInput, nil, ow(15, route.emitLine))
			b.mapping("map.serialize."+route.suffix+"."+field, "step.emit."+route.suffix, "copy", []any{emitInput}, ordersEventRef(field, "step.emit."+route.suffix, "emits."+route.suffix), nil, ow(16, route.emitLine))
			b.mapping("map.transport."+route.suffix+"."+field, "consumer.cancel", "copy", []any{ordersEventRef(field, "step.emit."+route.suffix, "emits."+route.suffix)}, ordersEventRef(field, "consumer.cancel", "delivery."+route.suffix), &ImportLineageTransport{EmitsEdgeKey: "emits." + route.suffix, DeliveryEdgeKey: "delivery." + route.suffix}, ow(route.emitLine, route.emitLine), ww(route.deliveryLine, route.deliveryLine))
		}
		b.mapping("map.deserialize."+route.suffix+".status", "step.decode.cancel", "copy", []any{ordersEventRef("status", "consumer.cancel", "delivery."+route.suffix)}, ordersPortRef("step.decode.cancel", "outputs", "status"), nil, bw(3, 4), ww(route.deliveryLine, route.deliveryLine))
	}
	for _, c := range b.commands {
		if err := validateCommand(c, s); err != nil {
			typ, key, _ := commandAddress(c)
			t.Fatalf("fixture command %s/%s: %#v", typ, key, err)
		}
	}
	return b.commands
}

func eventsOrdersCommitted(t *testing.T, resolved bool) (*Repo, *ImportCommitResult, *ImportSession, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, eventsOrdersInput(t, p, resolved))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, eventsOrdersCommands(t, s, resolved), "orders-events")
	if v.State != "ready" {
		t.Fatalf("orders fixture preview: %+v", v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "orders-events-commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, s, ids
}
