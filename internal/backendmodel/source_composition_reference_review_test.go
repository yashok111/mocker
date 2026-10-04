package backendmodel

import (
	"encoding/json/jsontext"
	"maps"
	"testing"
	"uuid"
)

type sourceReviewFixture struct {
	repo       *Repo
	project    *Project
	repository string
	a, b       BaseAssertionRef
	kind       string
}

func sourceReviewCommit(t *testing.T, r *Repo, p *Project, s *ImportSession, version int64, selectProvider string) *ImportCommitResult {
	t.Helper()
	for attempt := range 5 {
		v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: version, BaseRevisionID: p.CurrentRevisionID})
		if err != nil {
			t.Fatal(err)
		}
		if v.State == "ready" {
			out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"})
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		page, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "assertion_conflict"})
		if err != nil || len(page.Items) == 0 {
			t.Fatalf("unresolvable preview: %+v %v", v, err)
		}
		commands := make([]ImportCommand, 0, len(page.Items))
		for _, item := range page.Items {
			c := item.AssertionConflict
			chosen := c.Contenders[0]
			for _, x := range c.Contenders {
				if selectProvider == "present" && x.Value.Present || x.Owner.ProviderNamespace == selectProvider {
					chosen = x
					break
				}
			}
			commands = append(commands, ImportCommand{Op: "resolve_assertion", Resolution: &SourceAssertionResolution{DecisionID: uuid.NewV7().String(), RecordType: c.RecordType, ID: c.ID, Property: c.Property, ConflictHash: c.ConflictHash, Select: SourceAssertionSelection{RepositoryID: chosen.Owner.RepositoryID, ProviderNamespace: chosen.Owner.ProviderNamespace, AssertionHash: chosen.AssertionHash}, Reason: "fixture exact selection"}})
		}
		batch := sendCommands(t, r, p, s, v.Version, "selection-"+string(rune('a'+attempt)), commands...)
		version = batch.AcceptedVersion
	}
	t.Fatal("selection did not stabilize")
	return nil
}

func sourceReviewSharedValue(t *testing.T, kind, variant string) *sourceReviewFixture {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "exact-values")
	in := source6Input(t, p)
	if kind == "column" {
		for i := range in.Inventory {
			if in.Inventory[i].Category == "datastores" {
				in.Inventory[i].KnownCount = 1
				in.Inventory[i].Denominator = new(int64(1))
			}
		}
	}
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var commands []ImportCommand
	if kind == "port" {
		commands = runtimeSource6Commands(t, s, true)
		for _, c := range commands {
			if n := c.Node; n != nil && n.Kind == "flow_step" {
				n.Attributes["inputs"] = relationalRaw(t, []runtimePort{{Key: "input", Name: "input", NativeType: relationalScalar{Status: "known", Value: jsontext.Value(`"string"`)}}})
				n.Attributes["outputs"] = relationalRaw(t, []runtimePort{{Key: "a", Name: "a", NativeType: relationalScalar{Status: "known", Value: jsontext.Value(`"string"`)}}})
			}
		}
	} else {
		g := structuralTestGraph(t, "relational")
		nodes := []Node{}
		keep := map[string]bool{}
		for _, n := range g.Nodes {
			if n.ID == structuralID("db") || n.ID == structuralID("schema") || n.ID == structuralID("table") || n.ID == structuralID("column") {
				if n.Kind == "column" && variant == "borrow" {
					facets, _, _ := relationalFacetObject(n.Kind, n.Attributes)
					n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, map[string]jsontext.Value{"orm": facets["sql"]})
				}
				nodes = append(nodes, n)
				keep[n.ID] = true
			}
		}
		edges := []Edge{}
		for _, e := range g.Edges {
			if e.Kind == "contains" && keep[e.From] && keep[e.To] {
				edges = append(edges, e)
			}
		}
		g.Nodes, g.Edges = nodes, edges
		commands = representationChainCommands(t, s, g)
	}
	batch := sendCommands(t, r, p, s, 1, "base", commands...)
	first := sourceReviewCommit(t, r, p, s, batch.AcceptedVersion, "present")
	p = &first.Project
	base, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var a, parent ProviderAssertion
	for _, claim := range base.Assertions {
		if kind == "column" && claim.Payload.Kind == "column" || kind == "port" && claim.Payload.Kind == "flow_step" {
			a = claim
		}
	}
	for _, claim := range base.Assertions {
		if a.Payload.ParentID != nil && claim.RecordID == *a.Payload.ParentID {
			parent = claim
		}
	}
	next := source6Input(t, p)
	next.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: s.RepositoryID}
	next.Manifest.Provider.Namespace = "provider-b"
	next.IdempotencyKey = "provider-b"
	other, err := r.BeginImport(t.Context(), p.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	attrs := maps.Clone(a.Payload.Attributes)
	if kind == "port" {
		attrs["outputs"] = relationalRaw(t, []runtimePort{{Key: "b", Name: "b", NativeType: relationalScalar{Status: "known", Value: jsontext.Value(`"string"`)}}})
	} else {
		facets, _, _ := relationalFacetObject("column", attrs)
		var raw jsontext.Value
		if variant == "borrow" {
			raw = facets["orm"]
		} else {
			raw = facets["sql"]
		}
		facet, _ := relationalObject(raw)
		delete(facet, "sourceSnapshotId")
		delete(facet, "freshness")
		delete(facet, "evidenceIds")
		facet["evidenceKeys"] = relationalRaw(t, []string{"b-proof"})
		if variant == "different" {
			facet["nativeType"] = relationalRaw(t, known("text"))
			facet["typeFamily"] = relationalRaw(t, known("string"))
		}
		attrs = replaceRelationalFacets("column", attrs, map[string]jsontext.Value{"sql": relationalRaw(t, facet)})
	}
	ref := sourceAssertionRef(parent)
	proof := *fixtureCommands(other)[1].Evidence
	proof.ExternalKey = "b-proof"
	proof.SubjectKey = "target-b"
	proof.Source.StartLine = new(int64(1))
	proof.Source.EndLine = new(int64(2))
	claim := ImportCommand{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "target-b", Target: sourceAssertionRef(a), Reason: "same stable value owner", EvidenceKeys: []string{"b-proof"}}}
	node := &ImportNode{ExternalKey: "target-b", Kind: a.Payload.Kind, Name: a.Payload.Name, ParentRef: &ImportRecordRef{Base: &ref}, Attributes: attrs, EvidenceKeys: []string{"b-proof"}}
	if variant == "equal" {
		node.Name += " other label"
	}
	batch = sendCommands(t, r, p, other, 1, "claim", claim, ImportCommand{Op: "upsert_node", Node: node}, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	favor := "present"
	if kind == "port" {
		favor = "provider-b"
	}
	if variant != "borrow" {
		favor = a.Owner.ProviderNamespace
	}
	second := sourceReviewCommit(t, r, p, other, batch.AcceptedVersion, favor)
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, second.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := &sourceReviewFixture{repo: r, project: &second.Project, repository: s.RepositoryID, a: sourceAssertionRef(a), kind: kind}
	for _, claim := range graph.Assertions {
		if claim.Owner.ProviderNamespace == "provider-b" && claim.RecordID == a.RecordID {
			result.b = sourceAssertionRef(claim)
		}
	}
	return result
}

func (f *sourceReviewFixture) mapping(t *testing.T, target BaseAssertionRef) (*ImportSession, *BatchReceipt) {
	t.Helper()
	in := source6Input(t, f.project)
	in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: f.repository}
	in.Manifest.Provider.Namespace = "provider-c"
	in.IdempotencyKey = "provider-c"
	s, err := f.repo.BeginImport(t.Context(), f.project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := representationCommands(s)
	source := map[string]any{"kind": f.kind, "nodeRef": ImportRecordRef{Base: &target}}
	if f.kind == "column" {
		source["facetKey"] = "sql"
	} else {
		source["collection"] = "outputs"
		source["portKey"] = "b"
	}
	node := &ImportNode{ExternalKey: "mapping", Kind: "field_mapping", Name: "mapping", ParentRef: &ImportRecordRef{LocalKey: "domain_entity"}, Attributes: runtimeAttrs(t, map[string]any{"sources": []any{source}, "destination": map[string]any{"kind": "representation_field", "nodeRef": ImportRecordRef{LocalKey: "representation_field"}}, "transform": LineageTransform{Kind: "copy", Description: "declared copy"}, "analysisStatus": "complete", "gaps": []string{}}), EvidenceKeys: []string{"mapping-proof"}}
	proof := *fixtureCommands(s)[1].Evidence
	proof.ExternalKey = "mapping-proof"
	proof.SubjectKey = "mapping"
	proof.Source.StartLine = new(int64(1))
	proof.Source.EndLine = new(int64(2))
	edgeProof := proof
	edgeProof.ExternalKey = "edge-proof"
	edgeProof.SubjectType = "edge"
	edgeProof.SubjectKey = "mapping-parent"
	commands = append(commands, ImportCommand{Op: "upsert_node", Node: node}, ImportCommand{Op: "upsert_evidence", Evidence: &proof}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "mapping-parent", Kind: "contains", FromRef: &ImportRecordRef{LocalKey: "domain_entity"}, ToRef: &ImportRecordRef{LocalKey: "mapping"}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"edge-proof"}}}, ImportCommand{Op: "upsert_evidence", Evidence: &edgeProof})
	return s, sendCommands(t, f.repo, f.project, s, 1, "mapping", commands...)
}

func TestSource6ExactClaimSubaddressAdmission(t *testing.T) {
	for _, kind := range []string{"column", "port"} {
		for _, owner := range []string{"missing", "present"} {
			t.Run(kind+"/"+owner, func(t *testing.T) {
				f := sourceReviewSharedValue(t, kind, "borrow")
				target := f.a
				if owner == "present" {
					target = f.b
				}
				s, b := f.mapping(t, target)
				v, err := f.repo.PreviewImport(t.Context(), f.project.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: f.project.CurrentRevisionID})
				if owner == "missing" {
					if err == nil && v.State == "ready" {
						t.Fatal("scoped claim borrowed selected value from another provider")
					}
					return
				}
				if err != nil || v.State != "ready" {
					t.Fatalf("exact owning claim rejected: %+v %v", v, err)
				}
			})
		}
	}
}
