package backendmodel

import (
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
)

const apiTestID = "018f2396-cf02-7000-8000-000000000001"

func apiTestVector() ([]ArtifactPin, []APIArtifactBinding) {
	h := strings.Repeat("a", 64)
	p := []ArtifactPin{{Kind: "api_design", ID: "1", RevisionID: "2", ContentHash: h}}
	b := []APIArtifactBinding{{SourceNodeID: apiTestID, SourceKind: "http_operation", SourceLastKnownLabel: "source", Origin: "manual", Reason: "review", Ref: ArtifactRef{Kind: "api_design", ArtifactID: "1", RevisionID: "2", ContentHash: h, Selector: APIArtifactSelector{ObjectKey: "op"}, ObjectHash: strings.Repeat("b", 64), LastKnownLabel: "API", ResolvedPointer: "/paths/~1a/get"}}}
	return p, b
}
func TestAPIArtifactSourceHashProjection(t *testing.T) {
	state := RevisionState{Nodes: []Node{{ID: "b", EvidenceIDs: []string{"2", "1"}, Attributes: map[string]jsontext.Value{"sources": []byte(`["first","second"]`)}}, {ID: "a"}}}
	coverage := RevisionCoverage{Coverage: Coverage{Status: "partial", Gaps: []string{"b", "a"}}, ReconciliationGaps: []string{"gap"}, StaleCounts: StaleCounts{Nodes: 1}}
	before, _ := json.Marshal(state)
	covBefore, _ := json.Marshal(coverage)
	a, err := APIArtifactSourceContentHash(state, coverage)
	if err != nil {
		t.Fatal(err)
	}
	state.Revision.ID = "metadata"
	state.Revision.SemanticHash = "cas"
	state.Nodes[0], state.Nodes[1] = state.Nodes[1], state.Nodes[0]
	coverage.Coverage.Gaps[0], coverage.Coverage.Gaps[1] = coverage.Coverage.Gaps[1], coverage.Coverage.Gaps[0]
	same, err := APIArtifactSourceContentHash(state, coverage)
	if err != nil || same != a {
		t.Fatalf("order/metadata %s %s %v", a, same, err)
	}
	state.Nodes[0], state.Nodes[1] = state.Nodes[1], state.Nodes[0]
	state.Revision = Revision{}
	coverage.Coverage.Gaps[0], coverage.Coverage.Gaps[1] = coverage.Coverage.Gaps[1], coverage.Coverage.Gaps[0]
	after, _ := json.Marshal(state)
	covAfter, _ := json.Marshal(coverage)
	if string(before) != string(after) || string(covBefore) != string(covAfter) {
		t.Fatal("caller data mutated")
	}
	for _, name := range []string{"gap", "stale", "source order"} {
		t.Run(name, func(t *testing.T) {
			s := state
			c := coverage
			switch name {
			case "gap":
				c.ReconciliationGaps = []string{"other"}
			case "stale":
				c.StaleCounts.Nodes++
			case "source order":
				s.Nodes = append([]Node(nil), s.Nodes...)
				s.Nodes[0].Attributes = map[string]jsontext.Value{"sources": []byte(`["second","first"]`)}
			}
			h, err := APIArtifactSourceContentHash(s, c)
			if err != nil || h == a {
				t.Fatalf("change lost: %v", err)
			}
		})
	}
}
func TestAPIArtifactSourceHashGolden(t *testing.T) {
	h, err := APIArtifactSourceContentHash(RevisionState{}, RevisionCoverage{})
	if err != nil {
		t.Fatal(err)
	}
	golden := `{"coverage":{"denominator":null,"gaps":[],"knownObjects":0,"status":""},"domain":"backend-api-pins-source-content-v1","edges":[],"evidence":[],"inventory":[],"nodes":[],"reconciliationGaps":[],"snapshots":[],"sourceSnapshotIds":[],"sources":[],"staleCounts":{"edges":0,"evidence":0,"nodes":0}}`
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(golden)))
	if h != want {
		t.Fatalf("got %s want %s", h, want)
	}
}
func TestAPIArtifactSemanticAndCandidateHashes(t *testing.T) {
	p, b := apiTestVector()
	source := strings.Repeat("c", 64)
	anchor := strings.Repeat("d", 64)
	a, err := APIArtifactSemanticHash(source, anchor, p, b)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]APIArtifactBinding(nil), b...)
	changed[0].Reason = "other"
	changed[0].SourceLastKnownLabel = "renamed"
	changed[0].Ref.LastKnownLabel = "renamed API"
	same, err := APIArtifactSemanticHash(source, anchor, p, changed)
	if err != nil || same != a {
		t.Fatal("labels/reason entered semantics", err)
	}
	q := append([]ArtifactPin(nil), p...)
	q[0].ContentHash = strings.Repeat("e", 64)
	changed[0].Ref.ContentHash = q[0].ContentHash
	other, err := APIArtifactSemanticHash(source, anchor, q, changed)
	if err != nil || other == a {
		t.Fatal("whole artifact context lost", err)
	}
	back, err := APIArtifactSemanticHash(source, anchor, p, b)
	if err != nil || back != a {
		t.Fatal("A B A failed", err)
	}
	clear, err := APIArtifactSemanticHash(source, anchor, nil, nil)
	if err != nil || clear != anchor {
		t.Fatal("clear did not restore source anchor", err)
	}
	in := PreviewAPIPinsInput{BaseRevisionID: apiTestID, ExpectedVersion: 1, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: "1", Reason: "review"}}}
	candidate := APIPinsPreview{Pins: p, Bindings: b}
	cas, err := APIArtifactCandidateHash(in, candidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"label", "reason", "version", "base"} {
		t.Run(name, func(t *testing.T) {
			input := in
			input.Commands = append([]APIPinCommand(nil), in.Commands...)
			c := candidate
			c.Bindings = append([]APIArtifactBinding(nil), b...)
			switch name {
			case "label":
				c.Bindings[0].Ref.LastKnownLabel = "new"
			case "reason":
				input.Commands[0].Reason = "other"
			case "version":
				input.ExpectedVersion++
			case "base":
				input.BaseRevisionID = "018f2396-cf02-7000-8000-000000000002"
			}
			h, err := APIArtifactCandidateHash(input, c)
			if err != nil || h == cas {
				t.Fatal("CAS context lost", err)
			}
		})
	}
}
func TestAPIArtifactSemanticGoldenAndOrdering(t *testing.T) {
	p, b := apiTestVector()
	source := strings.Repeat("c", 64)
	anchor := strings.Repeat("d", 64)
	golden := `{"bindings":[{"ref":{"artifactId":"1","contentHash":"` + strings.Repeat("a", 64) + `","kind":"api_design","objectHash":"` + strings.Repeat("b", 64) + `","resolvedPointer":"/paths/~1a/get","revisionId":"2","selector":{"objectKey":"op"}},"sourceKind":"http_operation","sourceNodeId":"` + apiTestID + `"}],"domain":"backend-api-pins-semantic-v1","pins":[{"contentHash":"` + strings.Repeat("a", 64) + `","id":"1","kind":"api_design","revisionId":"2"}],"sourceContentHash":"` + source + `"}`
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(golden)))
	got, err := APIArtifactSemanticHash(source, anchor, p, b)
	if err != nil || got != want {
		t.Fatalf("semantic golden %s %s %v", got, want, err)
	}
	p = append(p, ArtifactPin{Kind: "api_design", ID: "3", RevisionID: "4", ContentHash: p[0].ContentHash})
	second := b[0]
	second.SourceNodeID = "018f2396-cf02-7000-8000-000000000002"
	second.Ref.ArtifactID = "3"
	second.Ref.RevisionID = "4"
	b = append(b, second)
	first, err := APIArtifactSemanticHash(source, anchor, p, b)
	if err != nil {
		t.Fatal(err)
	}
	p[0], p[1] = p[1], p[0]
	b[0], b[1] = b[1], b[0]
	before, _ := json.Marshal(b)
	same, err := APIArtifactSemanticHash(source, anchor, p, b)
	after, _ := json.Marshal(b)
	if err != nil || same != first || string(before) != string(after) {
		t.Fatal("ordering/input mutation", err)
	}
}

func TestAPIArtifactFullVectorSemanticBranch(t *testing.T) {
	source, anchor := strings.Repeat("c", 64), strings.Repeat("d", 64)
	retained := ArtifactPin{Kind: "other_artifact", ID: "legacy", RevisionID: "r1"}
	a, err := APIArtifactSemanticHash(source, anchor, []ArtifactPin{retained}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a == anchor {
		t.Error("nonempty retained vector incorrectly restores empty-vector anchor")
	}
	for _, name := range []string{"revision", "contentHash", "sourceContent"} {
		t.Run(name, func(t *testing.T) {
			p := retained
			s := source
			switch name {
			case "revision":
				p.RevisionID = "r2"
			case "contentHash":
				p.ContentHash = strings.Repeat("e", 64)
			case "sourceContent":
				s = strings.Repeat("f", 64)
			}
			h, err := APIArtifactSemanticHash(s, anchor, []ArtifactPin{p}, nil)
			if err != nil || h == a {
				t.Fatalf("retained-vector change lost: %s %v", name, err)
			}
		})
	}
	api, bindings := apiTestVector()
	combined := append([]ArtifactPin{retained}, api...)
	attached, err := APIArtifactSemanticHash(source, anchor, combined, bindings)
	if err != nil {
		t.Fatal(err)
	}
	detached, err := APIArtifactSemanticHash(source, anchor, []ArtifactPin{retained}, nil)
	if err != nil || detached == anchor || detached == attached {
		t.Error("removing last API pin must retain other artifact semantics", err)
	}
	cleared, err := APIArtifactSemanticHash(source, anchor, nil, nil)
	if err != nil || cleared != anchor {
		t.Error("truly empty vector must restore original anchor", err)
	}
}

func TestAPIArtifactSourcePopulatedGolden(t *testing.T) {
	source := func(id string) SourceSnapshot {
		return SourceSnapshot{ID: id, Role: "primary", RepositoryID: "repo", ManifestHash: "manifest-" + id, Provider: SourceProvider{Name: "analyzer", Version: "v1", Namespace: "ns", Method: "static", Profiles: []string{"z", "a"}, Limitations: []string{"l2", "l1"}}, SnapshotManifest: SnapshotManifest{Consistency: "consistent", Files: []ManifestFile{{Path: "z.go", ContentHash: "hz", FileType: "go", AnalysisStatus: "partial", Reason: "unsupported"}, {Path: "a.go", ContentHash: "ha", FileType: "go", AnalysisStatus: "complete"}}}}
	}
	snapshots := []SourceSnapshot{source("s2"), source("s1")}
	state := RevisionState{Revision: Revision{SourceSnapshotIDs: []string{"s2", "s1"}, ID: "excluded-cas-revision", Author: "excluded-author"}, Sources: snapshots, Nodes: []Node{{ID: "node", ExternalKey: "key", Kind: "handler", Name: "Handle", Attributes: map[string]jsontext.Value{"sources": []byte(`["first","second"]`)}, EvidenceIDs: []string{"e2", "e1"}}}, Edges: []Edge{{ID: "edge", ExternalKey: "edge-key", Kind: "calls", From: "node", To: "target", Attributes: map[string]jsontext.Value{"call": []byte(`"direct"`)}, EvidenceIDs: []string{"e2", "e1"}}}, Evidence: []Evidence{{ID: "e1", ExternalKey: "ev-key", SubjectID: "node", Method: "static", Status: "confirmed", Source: EvidenceSource{RepositoryID: "repo", SnapshotID: "s1", File: "a.go", ContentHash: "ha"}, Explanation: "source assertion"}}}
	coverage := RevisionCoverage{Snapshots: snapshots, Inventory: []InventoryItem{{Category: "handlers", Status: "partial", KnownCount: 1, Denominator: new(int64(2)), DiscoverySource: "static", Gaps: []string{"z", "a"}, Reason: "bounded"}}, Coverage: Coverage{Status: "partial", Denominator: new(int64(2)), KnownObjects: 1, Gaps: []string{"z", "a"}}, ReconciliationGaps: []string{"g2", "g1"}, StaleCounts: StaleCounts{Nodes: 1, Edges: 2, Evidence: 3}}
	before, _ := json.Marshal(state)
	coverageBefore, _ := json.Marshal(coverage)
	const golden = `{"coverage":{"denominator":2,"gaps":["a","z"],"knownObjects":1,"status":"partial"},"domain":"backend-api-pins-source-content-v1","edges":[{"attributes":{"call":"direct"},"evidenceIds":["e1","e2"],"externalKey":"edge-key","from":"node","id":"edge","kind":"calls","to":"target"}],"evidence":[{"explanation":"source assertion","externalKey":"ev-key","id":"e1","method":"static","source":{"contentHash":"ha","file":"a.go","repositoryId":"repo","snapshotId":"s1"},"status":"confirmed","subjectId":"node"}],"inventory":[{"category":"handlers","denominator":2,"discoverySource":"static","gaps":["a","z"],"knownCount":1,"reason":"bounded","status":"partial"}],"nodes":[{"attributes":{"sources":["first","second"]},"evidenceIds":["e1","e2"],"externalKey":"key","id":"node","kind":"handler","name":"Handle","parentId":null}],"reconciliationGaps":["g1","g2"],"snapshots":[{"capturedAt":"0001-01-01T00:00:00Z","consistency":"consistent","dirty":false,"files":[{"analysisStatus":"complete","contentHash":"ha","fileType":"go","path":"a.go"},{"analysisStatus":"partial","contentHash":"hz","fileType":"go","path":"z.go","reason":"unsupported"}],"id":"s1","manifestHash":"manifest-s1","provider":{"limitations":["l1","l2"],"method":"static","name":"analyzer","namespace":"ns","profiles":["a","z"],"version":"v1"},"repositoryId":"repo","role":"primary"},{"capturedAt":"0001-01-01T00:00:00Z","consistency":"consistent","dirty":false,"files":[{"analysisStatus":"complete","contentHash":"ha","fileType":"go","path":"a.go"},{"analysisStatus":"partial","contentHash":"hz","fileType":"go","path":"z.go","reason":"unsupported"}],"id":"s2","manifestHash":"manifest-s2","provider":{"limitations":["l1","l2"],"method":"static","name":"analyzer","namespace":"ns","profiles":["a","z"],"version":"v1"},"repositoryId":"repo","role":"primary"}],"sourceSnapshotIds":["s1","s2"],"sources":[{"capturedAt":"0001-01-01T00:00:00Z","consistency":"consistent","dirty":false,"files":[{"analysisStatus":"complete","contentHash":"ha","fileType":"go","path":"a.go"},{"analysisStatus":"partial","contentHash":"hz","fileType":"go","path":"z.go","reason":"unsupported"}],"id":"s1","manifestHash":"manifest-s1","provider":{"limitations":["l1","l2"],"method":"static","name":"analyzer","namespace":"ns","profiles":["a","z"],"version":"v1"},"repositoryId":"repo","role":"primary"},{"capturedAt":"0001-01-01T00:00:00Z","consistency":"consistent","dirty":false,"files":[{"analysisStatus":"complete","contentHash":"ha","fileType":"go","path":"a.go"},{"analysisStatus":"partial","contentHash":"hz","fileType":"go","path":"z.go","reason":"unsupported"}],"id":"s2","manifestHash":"manifest-s2","provider":{"limitations":["l1","l2"],"method":"static","name":"analyzer","namespace":"ns","profiles":["a","z"],"version":"v1"},"repositoryId":"repo","role":"primary"}],"staleCounts":{"edges":2,"evidence":3,"nodes":1}}`
	projection, err := BuildAPIArtifactSourceProjection(state, coverage)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := canonicalAPIJSON(projection)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != golden {
		t.Fatalf("populated projection differs\ngot %s\nwant %s", actual, golden)
	}
	hash, err := APIArtifactSourceContentHash(state, coverage)
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(golden)))
	if err != nil || hash != want {
		t.Fatalf("populated golden hash %s %s %v", hash, want, err)
	}
	after, _ := json.Marshal(state)
	coverageAfter, _ := json.Marshal(coverage)
	if string(before) != string(after) || string(coverageBefore) != string(coverageAfter) {
		t.Fatal("populated source inputs mutated")
	}
}
