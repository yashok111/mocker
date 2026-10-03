package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
)

func eventsReferences(kind string, a map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	if eventsEmit(kind, a, edge) {
		cloned := maps.Clone(a)
		cloned["stepKind"] = jsontext.Value(`"transform"`)
		return runtimeReferences(kind, cloned, edge, persisted)
	}
	if !eventsSubject(kind, edge) {
		return nil, nil
	}
	if err := validateEventsAttributes(kind, a, edge, persisted); err != nil {
		return nil, err
	}
	refs := []relationalReference{}
	key, target := "", ""
	if kind == "emits" {
		key, target = "channelKey", "channel"
	} else if edge {
		key, target = "messageKey", "message"
	}
	if key != "" {
		key = runtimeReferenceName(key, persisted)
		r := relationalReference{Path: "/attributes/" + key, Kind: target, Key: runtimeString(a[key])}
		if persisted {
			r.ID, r.Key = r.Key, ""
		}
		refs = append(refs, r)
	}
	return refs, nil
}
func resolveEventsAttributes(kind string, a map[string]jsontext.Value, edge bool, resolve func(string, string, string) string) (map[string]jsontext.Value, error) {
	if eventsEmit(kind, a, edge) {
		cloned := maps.Clone(a)
		cloned["stepKind"] = jsontext.Value(`"transform"`)
		out, err := resolveRuntimeAttributes(kind, cloned, edge, resolve)
		if err != nil {
			return nil, err
		}
		out["stepKind"] = a["stepKind"]
		return out, nil
	}
	if !eventsSubject(kind, edge) {
		if lineageSubject(kind, edge) {
			return resolveEventsLineageAttributes(kind, a, resolve)
		}
		return resolveRuntimeAttributes(kind, a, edge, resolve)
	}
	refs, err := eventsReferences(kind, a, edge, false)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(a)
	for _, r := range refs {
		key := r.Path[len("/attributes/"):]
		out[runtimeReferenceName(key, true)], err = json.Marshal(resolve("node", r.Key, r.Path))
		if err != nil {
			return nil, err
		}
		delete(out, key)
	}
	return out, nil
}
func eventsContains(from, to Node) (bool, bool) {
	switch to.Kind {
	case "field_mapping":
		if contextualLineageMapping(to.Kind, to.Attributes, false) {
			return from.Kind == "consumer" || from.Kind == "flow_step", true
		}
	case "channel":
		return slices.Contains([]string{"service", "module", "external_system"}, from.Kind), true
	case "message", "consumer", "job":
		return from.Kind == "service" || from.Kind == "module", true
	case "event_field":
		return from.Kind == "message", true
	}
	return false, false
}
func eventsEndpoints(e Edge, from, to Node) (bool, bool) {
	switch e.Kind {
	case "contains":
		return eventsContains(from, to)
	case "emits":
		return eventsEmit(from.Kind, from.Attributes, false) && (to.Kind == "message" || eventsUnresolved(to, "message")), true
	case "delivered_to":
		return from.Kind == "channel" && (to.Kind == "consumer" || eventsUnresolved(to, "consumer")), true
	case "retries", "dead_letters":
		return from.Kind == "consumer" && to.Kind == "channel", true
	case "handles":
		if from.Kind == "consumer" || from.Kind == "job" {
			return to.Kind == "handler" || eventsUnresolved(to, "handler"), true
		}
	case "calls":
		if from.Kind == "flow_step" && runtimeString(from.Attributes["stepKind"]) == "call" && (to.Kind == "http_operation" || eventsUnresolved(to, "http_operation")) {
			return true, true
		}
	}
	return false, false
}
func eventsUnresolved(n Node, kind string) bool {
	return n.Kind == "unresolved_target" && runtimeString(n.Attributes["expectedKind"]) == kind
}

type eventsGraphValidator struct {
	session       *ImportSession
	diagnostics   *[]ImportDiagnostic
	nodes         map[string]Node
	proofs        map[string]Evidence
	contains, out map[string][]Edge
	files         map[string]bool
	fields        map[string]int
	selectors     map[string]bool
}

func (v eventsGraphValidator) add(path, message string) {
	*v.diagnostics = append(*v.diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
}
func validateEventsGraph(ctx context.Context, s *ImportSession, g *graphCandidate, d *[]ImportDiagnostic) error {
	v := newEventsGraphValidator(s, g, d)
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := v.node(n); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		v.edge(e)
	}
	// A complete containing flow also discloses unresolved new emission/route assertions.
	for _, n := range g.Nodes {
		v.containingFlow(n)
	}
	return nil
}
func newEventsGraphValidator(s *ImportSession, g *graphCandidate, d *[]ImportDiagnostic) eventsGraphValidator {
	nodes := map[string]Node{}
	proofs := map[string]Evidence{}
	contains := map[string][]Edge{}
	out := map[string][]Edge{}
	files := map[string]bool{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range g.Evidence {
		proofs[e.ID] = e
	}
	for _, e := range g.Edges {
		out[e.From] = append(out[e.From], e)
		if e.Kind == "contains" {
			contains[e.To] = append(contains[e.To], e)
		}
	}
	for _, src := range g.Sources {
		for _, f := range src.Files {
			if f.AnalysisStatus == "analyzed" {
				files[src.RepositoryID+"\x00"+src.ID+"\x00"+f.Path+"\x00"+f.ContentHash] = true
			}
		}
	}
	return eventsGraphValidator{session: s, diagnostics: d, nodes: nodes, proofs: proofs, contains: contains, out: out, files: files, fields: map[string]int{}, selectors: map[string]bool{}}
}
func (v eventsGraphValidator) proof(id string, ids []string, fresh *AssertionFreshness) {
	for _, eid := range ids {
		e, ok := v.proofs[eid]
		if !ok || e.SubjectID != id || e.Source.RepositoryID != v.session.RepositoryID || e.Source.StartLine == nil || e.Source.EndLine == nil || *e.Source.StartLine < 1 || *e.Source.EndLine < *e.Source.StartLine {
			continue
		}
		if fresh != nil && fresh.Status == "current" && e.Source.SnapshotID != v.session.SnapshotID {
			continue
		}
		if v.files[e.Source.RepositoryID+"\x00"+e.Source.SnapshotID+"\x00"+e.Source.File+"\x00"+e.Source.ContentHash] {
			return
		}
	}
	v.add("subjects/"+id+"/evidenceIds", "Event assertions require member evidence in an analyzed manifest file with physical line bounds")
}
func (v eventsGraphValidator) check(id, kind string, a map[string]jsontext.Value, edge bool, ids []string, fresh *AssertionFreshness) {
	if err := validateEventsAttributes(kind, a, edge, true); err != nil {
		v.add("subjects/"+id, err.Error())
		return
	}
	v.proof(id, ids, fresh)
	refs, err := eventsReferences(kind, a, edge, true)
	if err != nil {
		v.add("subjects/"+id, err.Error())
		return
	}
	for _, r := range refs {
		target := v.nodes[r.ID]
		if (target.Kind != r.Kind && !eventsUnresolved(target, r.Kind)) || target.Ownership == nil || target.Ownership.RepositoryID != v.session.RepositoryID {
			v.add("subjects/"+id+r.Path, "Nested event reference must survive with its declared kind in the same repository")
		}
	}
}
func (v eventsGraphValidator) node(n Node) error {
	event := eventsSubject(n.Kind, false)
	emit := eventsEmit(n.Kind, n.Attributes, false)
	if !event && !emit {
		return nil
	}
	v.check(n.ID, n.Kind, n.Attributes, false, n.EvidenceIDs, n.Freshness)
	parent := Node{}
	if n.ParentID != nil {
		parent = v.nodes[*n.ParentID]
	}
	if event {
		valid, _ := eventsContains(parent, n)
		if !valid || len(v.contains[n.ID]) != 1 || v.contains[n.ID][0].From != parent.ID || parent.Ownership == nil || parent.Ownership.RepositoryID != v.session.RepositoryID {
			v.add("nodes/"+n.ID+"/parentId", "Event node requires one agreeing contains edge and a valid parent in the same repository")
		}
	}
	if n.Kind == "event_field" {
		if err := v.field(n, parent); err != nil {
			return err
		}
	}
	if n.Kind == "consumer" || n.Kind == "job" {
		v.dispatch(n)
	}
	if emit && runtimeString(n.Attributes["analysisStatus"]) == "complete" {
		v.emission(n)
	}
	return nil
}
func (v eventsGraphValidator) field(n, parent Node) error {
	v.fields[parent.ID]++
	if v.fields[parent.ID] > MaxEventFieldsPerMessage {
		return limitFault("Event field limit per message exceeded")
	}
	raw, err := canonicalJSON(map[string]jsontext.Value{"section": n.Attributes["section"], "path": n.Attributes["path"]})
	if err != nil {
		return err
	}
	key := parent.ID + "\x00" + string(raw)
	if v.selectors[key] {
		v.add("nodes/"+n.ID+"/attributes/path", "Duplicate event field selector within message")
	}
	v.selectors[key] = true
	return nil
}
func (v eventsGraphValidator) dispatch(n Node) {
	known, remainder := 0, 0
	for _, e := range v.out[n.ID] {
		if e.Kind != "handles" {
			continue
		}
		if v.nodes[e.To].Kind == "handler" {
			known++
		} else if eventsUnresolved(v.nodes[e.To], "handler") {
			remainder++
		}
	}
	complete := runtimeString(n.Attributes["dispatchStatus"]) == "complete"
	if known > MaxEventHandlers || remainder > 1 || complete && (known == 0 || remainder != 0) || !complete && remainder != 1 {
		v.add("nodes/"+n.ID+"/attributes/dispatchStatus", "Dispatch requires at most 50 known handlers and exactly one unresolved remainder when incomplete")
	}
}
func (v eventsGraphValidator) emission(n Node) {
	count := 0
	for _, e := range v.out[n.ID] {
		if e.Kind == "emits" {
			count++
			channel := v.nodes[runtimeString(e.Attributes["channelId"])]
			if v.nodes[e.To].Kind == "unresolved_target" || channel.Kind == "unresolved_target" || runtimeString(e.Attributes["deliveryStatus"]) != "declared" {
				v.add("nodes/"+n.ID, "Complete emit cannot hide an unresolved emission")
			}
		}
	}
	if count == 0 {
		v.add("nodes/"+n.ID, "Emit step requires an explicit emission")
	}

}
func (v eventsGraphValidator) edge(e Edge) {
	from, to := v.nodes[e.From], v.nodes[e.To]
	adjacent := eventsSubject(from.Kind, false) || eventsSubject(to.Kind, false) || eventsEmit(from.Kind, from.Attributes, false) || eventsEmit(to.Kind, to.Attributes, false)
	if eventsSubject(e.Kind, true) {
		v.check(e.ID, e.Kind, e.Attributes, true, e.EvidenceIDs, e.Freshness)
	} else if adjacent || e.Kind == "calls" && to.Kind == "http_operation" {
		v.proof(e.ID, e.EvidenceIDs, e.Freshness)
	}
	if eventsSubject(e.Kind, true) || adjacent || e.Kind == "calls" && to.Kind == "http_operation" {
		if from.Ownership == nil || to.Ownership == nil || from.Ownership.RepositoryID != v.session.RepositoryID || to.Ownership.RepositoryID != v.session.RepositoryID {
			v.add("edges/"+e.ID, "Event relation endpoints must belong to the same repository")
		}
	}
	v.completeChannel(e, from, to)
}
func (v eventsGraphValidator) containingFlow(n Node) {
	if eventsEmit(n.Kind, n.Attributes, false) && n.ParentID != nil && runtimeString(v.nodes[*n.ParentID].Attributes["analysisStatus"]) == "complete" {
		for _, e := range v.out[n.ID] {
			if e.Kind == "emits" && (v.nodes[e.To].Kind == "unresolved_target" || v.nodes[runtimeString(e.Attributes["channelId"])].Kind == "unresolved_target" || runtimeString(e.Attributes["deliveryStatus"]) != "declared") {
				v.add("nodes/"+*n.ParentID, "Complete flow cannot hide an unresolved emission")
			}
		}
	}
}

func (v eventsGraphValidator) completeChannel(e Edge, from, to Node) {
	if e.Kind == "delivered_to" && runtimeString(from.Attributes["analysisStatus"]) == "complete" && (to.Kind == "unresolved_target" || v.nodes[runtimeString(e.Attributes["messageId"])].Kind == "unresolved_target" || runtimeString(e.Attributes["deliveryStatus"]) != "declared") {
		v.add("nodes/"+from.ID, "Complete channel cannot hide an unresolved route")
	}
}

func eventsRelation(kind string, from, to Node) bool {
	return contextualLineageMapping(from.Kind, from.Attributes, false) || contextualLineageMapping(to.Kind, to.Attributes, false) || eventsSubject(kind, true) || eventsSubject(from.Kind, false) || eventsSubject(to.Kind, false) || eventsEmit(from.Kind, from.Attributes, false) || eventsEmit(to.Kind, to.Attributes, false) || kind == "calls" && to.Kind == "http_operation"
}
