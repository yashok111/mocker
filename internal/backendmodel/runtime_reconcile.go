package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

func runtimeReferences(kind string, attrs map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	refs := []relationalReference{}
	if !runtimeSubject(kind, edge) {
		return refs, nil
	}
	if err := validateRuntimeAttributes(kind, attrs, edge, persisted); err != nil {
		return nil, err
	}
	add := func(path, typ, value string) {
		r := relationalReference{Path: "/attributes/" + path, Kind: typ, Key: value}
		if persisted {
			r.Key, r.ID = "", value
		}
		refs = append(refs, r)
	}
	switch kind {
	case "flow":
		key := runtimeReferenceName("entryStepKey", persisted)
		add(key, "flow_step", runtimeString(attrs[key]))
		key = map[bool]string{false: "exitStepKeys", true: "exitStepIds"}[persisted]
		values, _ := relationalStrings(attrs[key], MaxRuntimeExitReferences, persisted)
		for i, value := range values {
			add(fmt.Sprintf("%s/%d", key, i), "flow_step", value)
		}
	case "flow_step":
		m, _ := relationalObject(attrs["transactionContext"])
		if runtimeString(m["status"]) == "known" {
			key := runtimeReferenceName("transactionKey", persisted)
			add("transactionContext/"+key, "transaction", runtimeString(m[key]))
		}
	case "transaction", "reads", "writes", "deletes":
		key := runtimeReferenceName("datastoreKey", persisted)
		add(key, "datastore", runtimeString(attrs[key]))
	}
	return refs, nil
}

func sourceAttributeReferences(kind string, attrs map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	if representationSubject(kind, edge) || representationMapping(kind, attrs, edge) {
		return representationReferences(kind, attrs, edge, persisted)
	}
	if eventsSubject(kind, edge) || eventsEmit(kind, attrs, edge) {
		return eventsReferences(kind, attrs, edge, persisted)
	}
	if contextualLineageMapping(kind, attrs, edge) {
		return eventsLineageReferences(kind, attrs, edge, persisted)
	}
	if lineageSubject(kind, edge) {
		return lineageReferences(kind, attrs, edge, persisted)
	}
	if runtimeSubject(kind, edge) {
		return runtimeReferences(kind, attrs, edge, persisted)
	}
	return relationalReferences(kind, attrs, edge, persisted)
}

func sourceActiveReferenceTo(kind string, attrs map[string]jsontext.Value, edge bool, id string) bool {
	return sourceActiveRecordReferenceTo(kind, attrs, edge, "node", id)
}
func sourceActiveRecordReferenceTo(kind string, attrs map[string]jsontext.Value, edge bool, recordType, id string) bool {
	refs, err := sourceAttributeReferences(kind, attrs, edge, true)
	if err != nil {
		return eventsSubject(kind, edge) || eventsEmit(kind, attrs, edge) || lineageSubject(kind, edge) || runtimeSubject(kind, edge) || relationalSubject(kind, attrs, edge)
	}
	return activeRecordReferenceTo(refs, recordType, id)
}
func activeRecordReferenceTo(refs []relationalReference, recordType, id string) bool {
	return slices.ContainsFunc(refs, func(ref relationalReference) bool {
		typ := ref.RecordType
		if typ == "" {
			typ = "node"
		}
		return ref.Kind != "evidence" && ref.HistoricalRevisionID == "" && typ == recordType && ref.ID == id
	})
}

func resolveRuntimeAttributes(kind string, attrs map[string]jsontext.Value, edge bool, resolve func(string, string, string) string) (map[string]jsontext.Value, error) {
	if !runtimeSubject(kind, edge) {
		return attrs, nil
	}
	refs, err := runtimeReferences(kind, attrs, edge, false)
	if err != nil {
		return nil, err
	}
	values := map[string]jsontext.Value{}
	for _, ref := range refs {
		values[ref.Path], err = json.Marshal(resolve("node", ref.Key, ref.Path))
		if err != nil {
			return nil, err
		}
	}
	var walk func(jsontext.Value, string) (jsontext.Value, error)
	walk = func(raw jsontext.Value, path string) (jsontext.Value, error) {
		if value, ok := values[path]; ok {
			return value, nil
		}
		switch bytesPrefix := strings.TrimSpace(string(raw)); {
		case strings.HasPrefix(bytesPrefix, "{"):
			m, err := relationalObject(raw)
			if err != nil {
				return nil, err
			}
			out := map[string]jsontext.Value{}
			for key, value := range m {
				converted, err := walk(value, path+"/"+key)
				if err != nil {
					return nil, err
				}
				target := key
				if slices.Contains([]string{"entryStepKey", "exitStepKeys", "datastoreKey", "transactionKey"}, key) {
					if strings.HasSuffix(key, "Keys") {
						target = strings.TrimSuffix(key, "Keys") + "Ids"
					} else {
						target = runtimeReferenceName(key, true)
					}
				}
				out[target] = converted
			}
			return json.Marshal(out)
		case strings.HasPrefix(bytesPrefix, "["):
			var a []jsontext.Value
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			for i := range a {
				a[i], err = walk(a[i], fmt.Sprintf("%s/%d", path, i))
				if err != nil {
					return nil, err
				}
			}
			return json.Marshal(a)
		default:
			return raw, nil
		}
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	raw, err = walk(raw, "/attributes")
	if err != nil {
		return nil, err
	}
	return relationalObject(raw)
}

func runtimeContains(from, to Node) (bool, bool) {
	switch to.Kind {
	case "flow":
		return from.Kind == "handler" || from.Kind == "symbol", true
	case "flow_step", "transaction":
		return from.Kind == "flow", true
	case "query":
		return slices.Contains([]string{"module", "handler", "symbol"}, from.Kind), true
	default:
		return false, false
	}
}

func runtimeEndpoints(e Edge, from, to Node) (bool, bool) {
	switch e.Kind {
	case "contains":
		return runtimeContains(from, to)
	case "calls":
		if from.Kind == "flow_step" {
			step := runtimeString(from.Attributes["stepKind"])
			return (step == "call" || step == "query") && slices.Contains([]string{"symbol", "handler", "external_system", "query", "unresolved_target"}, to.Kind), true
		}
	case "next", "branch", "error", "returns":
		return from.Kind == "flow_step" && to.Kind == "flow_step", true
	case "reads", "writes", "deletes":
		return from.Kind == "query" && slices.Contains([]string{"table", "column", "view", "unresolved_target"}, to.Kind), true
	case "begins", "commits", "rolls_back":
		return from.Kind == "flow_step" && to.Kind == "transaction", true
	}
	return false, false
}

func decodeRuntimeAttributes(attrs map[string]jsontext.Value) runtimeAttributes {
	var out runtimeAttributes
	raw, _ := json.Marshal(attrs)
	_ = json.Unmarshal(raw, &out)
	return out
}

func validateRuntimeGraph(ctx context.Context, _ importReader, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	return validateRuntimeGraphRules(ctx, selectedProfile(s.Profile), s, g, diagnostics)
}

// A nil admission session selects the shared structural rules only.
func validateRuntimeGraphRules(ctx context.Context, profile string, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	add := func(path, message string) {
		*diagnostics = append(*diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
	}
	nodes := map[string]Node{}
	attributes := map[string]runtimeAttributes{}
	proofs := map[string]Evidence{}
	children := map[string][]string{}
	outgoing := map[string][]Edge{}
	incoming := map[string][]Edge{}
	contains := map[string][]Edge{}
	owners := map[string]string{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		attributes[n.ID] = decodeRuntimeAttributes(n.Attributes)
		if n.ParentID != nil {
			children[*n.ParentID] = append(children[*n.ParentID], n.ID)
		}
	}
	for _, e := range g.Evidence {
		proofs[e.ID] = e
	}
	for _, e := range g.Edges {
		outgoing[e.From] = append(outgoing[e.From], e)
		incoming[e.To] = append(incoming[e.To], e)
		if e.Kind == "contains" {
			contains[e.To] = append(contains[e.To], e)
		}
	}
	datastore := func(id string) string {
		seen := map[string]bool{}
		for !seen[id] {
			seen[id] = true
			n, ok := nodes[id]
			if !ok {
				return ""
			}
			if n.Kind == "datastore" {
				return id
			}
			if n.ParentID == nil {
				return ""
			}
			id = *n.ParentID
		}
		return ""
	}
	boundedProof := func(id string, ids []string, fresh *AssertionFreshness) {
		if s == nil {
			return
		}
		found := false
		for _, eid := range ids {
			e, ok := proofs[eid]
			if !ok || e.SubjectID != id || e.Source.RepositoryID != s.RepositoryID || e.Source.StartLine == nil || e.Source.EndLine == nil || *e.Source.StartLine <= 0 || *e.Source.EndLine < *e.Source.StartLine {
				continue
			}
			if fresh != nil && fresh.Status == "current" && e.Source.SnapshotID != s.SnapshotID {
				continue
			}
			for _, snapshot := range g.Sources {
				if snapshot.ID != e.Source.SnapshotID || snapshot.RepositoryID != e.Source.RepositoryID {
					continue
				}
				for _, file := range snapshot.SnapshotManifest.Files {
					if file.Path == e.Source.File && file.ContentHash == e.Source.ContentHash && file.AnalysisStatus == "analyzed" {
						found = true
					}
				}
			}
		}
		if !found {
			add("subjects/"+id+"/evidenceIds", "Runtime source assertions require a member proof in an analyzed snapshot file with physical line bounds")
		}
	}
	check := func(id, kind string, attrs map[string]jsontext.Value, edge bool, ids []string, fresh *AssertionFreshness) {
		if !runtimeSubject(kind, edge) {
			return
		}
		path := "nodes/" + id
		if edge {
			path = "edges/" + id
		}
		validator := validateRuntimeAttributes
		if profile == EventsProfile {
			validator = validateEventsAttributes
		}
		if err := validator(kind, attrs, edge, true); err != nil {
			add(path, err.Error())
			return
		}
		boundedProof(id, ids, fresh)
		refs, err := sourceAttributeReferences(kind, attrs, edge, true)
		if err != nil {
			add(path, err.Error())
			return
		}
		for _, ref := range refs {
			target, ok := nodes[ref.ID]
			if !ok || target.Kind != ref.Kind || s != nil && (target.Ownership == nil || target.Ownership.RepositoryID != s.RepositoryID) {
				add(path+ref.Path, "Nested reference must survive with its declared kind in the same repository")
			}
		}
	}
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		check(n.ID, n.Kind, n.Attributes, false, n.EvidenceIDs, n.Freshness)
		if !runtimeSubject(n.Kind, false) {
			continue
		}
		a := attributes[n.ID]
		path := "nodes/" + n.ID
		if n.ParentID == nil || len(contains[n.ID]) != 1 {
			add(path+"/parentId", "Runtime nodes require exactly one parent and contains edge")
			continue
		}
		parent := nodes[*n.ParentID]
		if valid, _ := runtimeContains(parent, n); !valid {
			add(path+"/parentId", "Runtime parent kind is invalid")
		}
		switch n.Kind {
		case "flow":
			if prior := owners[*n.ParentID]; prior != "" && prior != n.ID {
				add(path, "An owner has at most one source flow")
			}
			owners[*n.ParentID] = n.ID
			entry := nodes[a.EntryStepID]
			if entry.Kind != "flow_step" || entry.ParentID == nil || *entry.ParentID != n.ID {
				add(path+"/attributes/entryStepId", "Entry must be a direct step in this flow")
			}
			exits := map[string]bool{}
			for _, id := range a.ExitStepIDs {
				exits[id] = true
				step := nodes[id]
				if step.Kind != "flow_step" || step.ParentID == nil || *step.ParentID != n.ID {
					add(path+"/attributes/exitStepIds", "Exits must be direct steps in this flow")
				}
				if slices.ContainsFunc(outgoing[id], func(e Edge) bool { return runtimeControlKind(e.Kind) }) {
					add(path+"/attributes/exitStepIds", "Every declared exit must be terminal")
				}
			}
			for _, id := range children[n.ID] {
				step := nodes[id]
				if step.Kind != "flow_step" {
					continue
				}
				terminal := !slices.ContainsFunc(outgoing[id], func(e Edge) bool { return runtimeControlKind(e.Kind) })
				if a.ExitStatus == "complete" && terminal && !exits[id] {
					add(path+"/attributes/exitStepIds", "Complete exits must list every terminal step")
				}
				if a.AnalysisStatus == "complete" {
					if attributes[id].AnalysisStatus != "complete" {
						add(path, "Complete flow requires complete direct steps")
					}
					for _, e := range outgoing[id] {
						if e.Kind == "calls" && nodes[e.To].Kind == "unresolved_target" {
							add(path, "Complete flow cannot hide unresolved direct call/query remainder")
						}
					}
				}
			}
			if a.AnalysisStatus == "complete" && a.ExitStatus != "complete" {
				add(path, "Complete flow requires complete exit inventory")
			}
		case "flow_step":
			calls := []Edge{}
			boundaries := []Edge{}
			for _, e := range outgoing[n.ID] {
				if e.Kind == "calls" {
					calls = append(calls, e)
				}
				if runtimeBoundaryKind(e.Kind) {
					boundaries = append(boundaries, e)
				}
			}
			if a.TransactionContext.Status == "known" {
				tx := nodes[a.TransactionContext.TransactionID]
				if tx.Kind != "transaction" || tx.ParentID == nil || *tx.ParentID != *n.ParentID {
					add(path+"/attributes/transactionContext", "Known transaction context must reference a transaction in this flow")
				}
			}
			if a.StepKind == "query" {
				if len(calls) != 1 || !slices.Contains([]string{"query", "unresolved_target"}, nodes[callsFirst(calls)].Kind) {
					add(path, "Query steps require exactly one query or unresolved call target")
				}
			}
			if a.StepKind == "call" {
				if len(calls) == 0 || len(calls) > MaxRuntimeCallCandidates {
					add(path, "Call steps require one to 500 candidates")
				}
				unresolved := slices.ContainsFunc(calls, func(e Edge) bool { return nodes[e.To].Kind == "unresolved_target" })
				if a.DispatchStatus != "complete" && !unresolved || a.DispatchStatus == "complete" && unresolved {
					add(path, "Dispatch status must disclose an explicit unresolved remainder")
				}
			}
			expected := map[string]string{"transaction_begin": "begins", "transaction_commit": "commits", "transaction_rollback": "rolls_back"}[a.StepKind]
			if expected != "" {
				if len(boundaries) != 1 || boundaries[0].Kind != expected || a.TransactionContext.Status != "known" || boundaries[0].To != a.TransactionContext.TransactionID {
					add(path, "Transaction boundary steps require one matching edge and agreeing known context")
				}
			} else if len(boundaries) > 0 {
				add(path, "Only matching transaction boundary step kinds may carry boundary edges")
			}
		case "transaction":
			if a.BoundaryStatus == "complete" {
				begin, end := false, false
				for _, e := range incoming[n.ID] {
					if e.To == n.ID {
						begin = begin || e.Kind == "begins"
						end = end || e.Kind == "commits" || e.Kind == "rolls_back"
					}
				}
				if !begin || !end {
					add(path, "Complete transaction boundaries require at least one begin and one commit/rollback")
				}
			}
		case "query":
			if a.ColumnScope == "complete" {
				for _, e := range outgoing[n.ID] {
					if runtimeAccessEdgeKind(e.Kind) && nodes[e.To].Kind != "column" || e.Kind == "calls" && nodes[e.To].Kind == "unresolved_target" {
						add(path, "Complete query column inventory requires known columns and no unresolved remainder")
					}
				}
			}
		}
	}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		check(e.ID, e.Kind, e.Attributes, true, e.EvidenceIDs, e.Freshness)
		from, to := nodes[e.From], nodes[e.To]
		a := decodeRuntimeAttributes(e.Attributes)
		path := "edges/" + e.ID
		if !runtimeSubject(e.Kind, true) && (runtimeSubject(from.Kind, false) || runtimeSubject(to.Kind, false)) {
			boundedProof(e.ID, e.EvidenceIDs, e.Freshness)
		}
		if e.Kind == "calls" && from.Kind == "flow_step" {
			if to.Kind == "unresolved_target" {
				expected := runtimeString(to.Attributes["expectedKind"])
				valid := slices.Contains([]string{"symbol", "handler", "external_system", "query"}, expected) || profile == EventsProfile && expected == "http_operation"
				if attributes[from.ID].StepKind == "query" {
					valid = expected == "query"
				}
				if !valid {
					add(path, "Unresolved call remainder must declare a compatible expected kind")
				}
			}
		}
		if runtimeControlKind(e.Kind) || runtimeBoundaryKind(e.Kind) {
			if from.ParentID == nil || to.ParentID == nil || *from.ParentID != *to.ParentID {
				add(path, "Control and transaction boundaries must stay in one flow")
			}
		}
		if runtimeAccessEdgeKind(e.Kind) {
			if to.Kind == "unresolved_target" && !slices.Contains([]string{"table", "column", "view"}, runtimeString(to.Attributes["expectedKind"])) {
				add(path, "Unresolved access must declare a table, column or view target")
			}
			ds := nodes[a.DatastoreID]
			fs, _, err := relationalFacetObject("datastore", ds.Attributes)
			if ds.Kind != "datastore" || err != nil || fs[a.FacetKey] == nil {
				add(path, "Access must select an existing relational datastore facet")
			}
			if to.Kind == "column" {
				if a.ColumnScope != "listed" {
					add(path, "Concrete column access uses listed scope")
				}
			} else if a.ColumnScope != "unknown" {
				add(path, "Table, view and unresolved access requires unknown column scope")
			}
			if to.Kind != "unresolved_target" {
				targetFacets, _, err := relationalFacetObject(to.Kind, to.Attributes)
				if datastore(to.ID) != a.DatastoreID || err != nil || targetFacets[a.FacetKey] == nil {
					add(path, "Access target must belong to selected datastore and facet")
				}
			}
		}
	}
	return nil
}

func runtimeControlKind(kind string) bool {
	return slices.Contains([]string{"next", "branch", "error", "returns"}, kind)
}
func runtimeAccessEdgeKind(kind string) bool {
	return slices.Contains([]string{"reads", "writes", "deletes"}, kind)
}
func runtimeBoundaryKind(kind string) bool {
	return slices.Contains([]string{"begins", "commits", "rolls_back"}, kind)
}
func callsFirst(calls []Edge) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].To
}
