package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
)

func representationCommands(s *ImportSession) []ImportCommand {
	g := representationStructure()
	keys := map[string]string{}
	for _, n := range g.Nodes {
		keys[n.ID] = n.Kind
	}
	commands := make([]ImportCommand, 0, 2*len(g.Nodes)+2*len(g.Edges))
	proof := func(typ, key string) ImportCommand {
		return ImportCommand{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: "proof-" + key, SubjectType: typ, SubjectKey: key, Method: "ast", Status: "explicit", Source: EvidenceSource{RepositoryID: s.RepositoryID, SnapshotID: s.SnapshotID, File: "main.go", ContentHash: fixtureHash, StartLine: new(int64(1)), EndLine: new(int64(2))}, Explanation: "Declared structure"}}
	}
	for _, n := range g.Nodes {
		node := &ImportNode{ExternalKey: keys[n.ID], Kind: n.Kind, Name: n.Name, Attributes: n.Attributes, EvidenceKeys: []string{"proof-" + keys[n.ID]}}
		if n.ParentID != nil {
			node.ParentRef = &ImportRecordRef{LocalKey: keys[*n.ParentID]}
		}
		commands = append(commands, ImportCommand{Op: "upsert_node", Node: node}, proof("node", node.ExternalKey))
	}
	for _, e := range g.Edges {
		key := "contains-" + keys[e.To]
		commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: key, Kind: "contains", FromRef: &ImportRecordRef{LocalKey: keys[e.From]}, ToRef: &ImportRecordRef{LocalKey: keys[e.To]}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof-" + key}}}, proof("edge", key))
	}
	return commands
}

func TestRepresentationSource6Import(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "representations")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	commands := representationCommands(s)
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "representations", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "ready" {
		t.Fatalf("preview: %+v", v)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.State.Nodes) != 3 || len(snapshot.Assertions) != 5 {
		t.Fatalf("unexpected graph: %d nodes %d claims", len(snapshot.State.Nodes), len(snapshot.Assertions))
	}
	for _, a := range snapshot.Assertions {
		if a.Payload.Kind == "domain_entity" || a.Payload.Kind == "representation_field" {
			if a.Owner.Profile != ComposedProfile {
				t.Fatalf("representation owner profile: %+v", a.Owner)
			}
			if err := validateSourceOwnProof(s, a, snapshot, false, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, n := range snapshot.State.Nodes {
		if n.Ownership != nil || n.Freshness != nil || n.ExternalKey != "" {
			t.Fatalf("fictional singular metadata: %+v", n)
		}
	}
}

func TestRepresentationSource5RejectsKindsAndRefs(t *testing.T) {
	t.Parallel()
	s := &ImportSession{Profile: EventsProfile}
	for _, kind := range []string{"domain_entity", "dto", "api_schema", "representation_field"} {
		a := representationOwnerAttrs()
		if kind == "representation_field" {
			a = representationFieldAttrs()
		}
		err := validateCommand(ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: kind, Kind: kind, Name: kind, Attributes: a, EvidenceKeys: []string{"proof"}}}, s)
		if err == nil {
			t.Fatalf("source5 accepted %s", kind)
		}
	}
	ref := jsontext.Value(`{"kind":"representation_field","nodeId":"00000000-0000-4000-8000-000000000003"}`)
	if err := validateEventsLineageRef(ref, true); err == nil {
		t.Fatal("source5 accepted representation ref")
	}
}

func TestRepresentationSourceProofRequired(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"evidence", "line bounds"} {
		t.Run(missing, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "proof")
			s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
			if err != nil {
				t.Fatal(err)
			}
			commands := representationCommands(s)
			for i := range commands {
				if n := commands[i].Node; n != nil && n.Kind == "representation_field" && missing == "evidence" {
					n.EvidenceKeys = []string{}
				}
				if e := commands[i].Evidence; e != nil && e.SubjectKey == "representation_field" && missing == "line bounds" {
					e.Source.StartLine = nil
					e.Source.EndLine = nil
				}
			}
			hash, _ := ImportBatchHash(commands)
			b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "proof", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands})
			if err != nil {
				t.Fatal(err)
			}
			v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if err == nil && v.State == "ready" {
				raw, _ := json.Marshal(v)
				t.Fatalf("unsupported proof admitted: %s", raw)
			}
			if err != nil && !strings.Contains(err.Error(), "evidence") && !strings.Contains(err.Error(), "proof") {
				t.Fatalf("unrelated preview rejection: %v", err)
			}
		})
	}
}

func TestRepresentationMappingSource6Import(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "mapping-chain")
	in := source6Input(t, p)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" || in.Inventory[i].Category == "datastores" {
			in.Inventory[i].KnownCount, in.Inventory[i].Denominator = 1, new(int64(1))
		}
	}
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	g := representationChain(t)
	// A representation parent alone selects the composed profile even when the
	// mapping's value references use only the older API field vocabulary.
	g.Nodes = append(g.Nodes, Node{ID: "00000000-0000-4000-8000-000000000054", Kind: "field_mapping", Name: "constant operation value", ParentID: new("00000000-0000-4000-8000-000000000020"), Attributes: sourceContractAttrs(t, `{"sources":[],"destination":{"kind":"api_field","nodeId":"00000000-0000-4000-8000-000000000031"},"transform":{"kind":"constant","description":"Default response","redacted":false},"analysisStatus":"complete","gaps":[]}`)})
	g.Edges = append(g.Edges, Edge{ID: "a0000000-0000-4000-8000-000000000054", Kind: "contains", From: "00000000-0000-4000-8000-000000000020", To: "00000000-0000-4000-8000-000000000054", Attributes: map[string]jsontext.Value{}})
	commands := representationChainCommands(t, s, g)
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "chain", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "ready" {
		t.Fatalf("chain preview state=%s diagnostics=%+v", v.State, v.Diagnostics)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, a := range snapshot.Assertions {
		ids[a.ExternalKey] = a.RecordID
	}
	mappings := 0
	for _, a := range snapshot.Assertions {
		if a.Payload.Kind != "field_mapping" {
			continue
		}
		mappings++
		if a.Owner.Profile != ComposedProfile {
			t.Fatalf("mapping %s profile=%s", a.ExternalKey, a.Owner.Profile)
		}
		attrs, err := decodeLineageMappingForSchema(a.Payload.Attributes, ComposedSchemaVersion)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateSourceBindings(a); err != nil {
			t.Fatal(err)
		}
		if err := validateSourceOwnProof(s, a, snapshot, false, nil); err != nil {
			t.Fatal(err)
		}
		if a.ExternalKey == "00000000-0000-4000-8000-000000000050" {
			want := []LineageValueRef{{Kind: "column", NodeID: ids["00000000-0000-4000-8000-000000000043"], FacetKey: "sql"}, {Kind: "column", NodeID: ids["00000000-0000-4000-8000-000000000044"], FacetKey: "sql"}}
			if !slices.Equal(attrs.Sources, want) {
				t.Fatalf("ordered source refs changed: %+v", attrs.Sources)
			}
		}
	}
	if mappings != 5 {
		t.Fatalf("mapping count=%d", mappings)
	}
}

func representationChainCommands(t *testing.T, s *ImportSession, g SourceStructuralGraph) []ImportCommand {
	t.Helper()
	commands := make([]ImportCommand, 0, 2*len(g.Nodes)+2*len(g.Edges))
	proof := func(typ, key string) ImportCommand {
		return ImportCommand{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: "proof:" + key, SubjectType: typ, SubjectKey: key, Method: "ast", Status: "explicit", Source: EvidenceSource{RepositoryID: s.RepositoryID, SnapshotID: s.SnapshotID, File: "main.go", ContentHash: fixtureHash, StartLine: new(int64(1)), EndLine: new(int64(2))}, Explanation: "Declared mapping"}}
	}
	for _, n := range g.Nodes {
		a := n.Attributes
		if relationalSubject(n.Kind, a, false) {
			facets, _, err := relationalFacetObject(n.Kind, a)
			if err != nil {
				t.Fatal(err)
			}
			for key, raw := range facets {
				f, err := relationalObject(raw)
				if err != nil {
					t.Fatal(err)
				}
				f["sourceKind"] = jsontext.Value(`"sql"`)
				f["evidenceKeys"], _ = json.Marshal([]string{"proof:" + n.ID})
				facets[key], _ = json.Marshal(f)
			}
			a = replaceRelationalFacets(n.Kind, a, facets)
		}
		if n.Kind == "field_mapping" {
			var sources []map[string]jsontext.Value
			if err := json.Unmarshal(a["sources"], &sources); err != nil {
				t.Fatal(err)
			}
			var destination map[string]jsontext.Value
			if err := json.Unmarshal(a["destination"], &destination); err != nil {
				t.Fatal(err)
			}
			for _, ref := range append(sources, destination) {
				ref["nodeRef"], _ = json.Marshal(ImportRecordRef{LocalKey: runtimeString(ref["nodeId"])})
				delete(ref, "nodeId")
			}
			a["sources"], _ = json.Marshal(sources)
			a["destination"], _ = json.Marshal(destination)
		}
		node := &ImportNode{ExternalKey: n.ID, Kind: n.Kind, Name: n.Name, Attributes: a, EvidenceKeys: []string{"proof:" + n.ID}}
		if n.ParentID != nil {
			node.ParentRef = &ImportRecordRef{LocalKey: *n.ParentID}
		}
		commands = append(commands, ImportCommand{Op: "upsert_node", Node: node}, proof("node", n.ID))
	}
	for _, e := range g.Edges {
		commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: e.ID, Kind: e.Kind, FromRef: &ImportRecordRef{LocalKey: e.From}, ToRef: &ImportRecordRef{LocalKey: e.To}, Attributes: e.Attributes, EvidenceKeys: []string{"proof:" + e.ID}}}, proof("edge", e.ID))
	}
	return commands
}
