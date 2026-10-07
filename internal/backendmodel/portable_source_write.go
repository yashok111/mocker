package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/yashok111/mocker/internal/backendblob"
)

// rehashPortableSource runs after typed ID remapping and before persistent insert.
// Original hashes/documents remain in portable origins, not substituted locally.
func rehashPortableSource(ctx context.Context, tx *sql.Tx, s *PortableSource, hashes map[string]string) error {
	previousSemantic := s.Revision.SemanticHash
	for i := range s.LegacyProofBases {
		b := &s.LegacyProofBases[i]
		old := b.BasisHash
		next, err := deriveLegacyProofBasis(ctx, tx, b.ProjectID, b.SourceRevisionID, b.RecordType, b.RecordID, b.EvidenceID)
		if err != nil {
			return err
		}
		*b = next
		hashes[old] = next.BasisHash
	}
	graph, err := portableSourceGraph(s)
	if err != nil {
		return err
	}
	for i := range graph.Assertions {
		a := &graph.Assertions[i]
		old := a.AssertionHash
		bases := []LegacyProofBasis{}
		for _, b := range graph.LegacyProofBases {
			for _, eid := range a.EvidenceIDs {
				if b.EvidenceID == eid {
					bases = append(bases, b)
				}
			}
		}
		hash, err := sourceIntrinsicHash(*a, graph.RawEvidence, bases)
		if err != nil {
			return err
		}
		a.AssertionHash = hash
		hashes[old] = hash
	}
	replace := func(value *string) {
		if next, ok := hashes[*value]; ok {
			*value = next
		}
	}
	for i := range graph.Assertions {
		for j := range graph.Assertions[i].DependencyClaims {
			replace(&graph.Assertions[i].DependencyClaims[j].Target.AssertionHash)
		}
	}
	for i := range graph.Currentness {
		replace(&graph.Currentness[i].AssertionHash)
	}
	for i := range graph.Selections {
		replace(&graph.Selections[i].Select.AssertionHash)
	}
	graph.Identities = sourceIdentities(graph.Assertions)
	current := map[string]SourceClaimCurrentness{}
	for _, f := range graph.Currentness {
		current[sourceClaimKey(f.RecordType, f.RecordID, f.RepositoryID, f.ProviderNamespace)] = f
	}
	merger := sourceAssertionMerger{base: graph, current: current}
	for i := range graph.Selections {
		r := &graph.Selections[i]
		claims := []ProviderAssertion{}
		for _, a := range graph.Assertions {
			if a.RecordType == r.RecordType && a.RecordID == r.ID {
				claims = append(claims, a)
			}
		}
		conflict, err := merger.conflict(claims, r.Property)
		if err != nil {
			return err
		}
		if conflict == nil {
			return invalid("selection", "Mapped selection has no divergent source values")
		}
		hashes[r.ConflictHash] = conflict.ConflictHash
		r.ConflictHash = conflict.ConflictHash
	}
	s.Assertions, s.Currentness, s.Selections = graph.Assertions, graph.Currentness, graph.Selections
	var content, anchor string
	if s.Revision.SchemaVersion == ComposedSchemaVersion {
		raw, err := source6ContextJSON(graph)
		if err != nil {
			return err
		}
		content = hashBytes(raw)
		anchor, err = sourceDomainHash("backend-source6-semantic-v1", struct {
			Schema  string `json:"schema"`
			Content string `json:"sourceContentHash"`
		}{ComposedSchemaVersion, content})
		if err != nil {
			return err
		}
	} else {
		content, err = APIArtifactSourceContentHash(graph.State, s.Coverage)
		if err != nil {
			return err
		}
		anchor, err = sourceDomainHash("backend-portable-source-anchor-v1", struct{ Schema, Content string }{s.Revision.SchemaVersion, content})
		if err != nil {
			return err
		}
	}
	c := ArtifactContextV3{DocumentVersion: ArtifactContextV3Version, Groups: []ArtifactNamespaceGroup{}}
	if len(s.ArtifactContext) > 0 {
		decoded, err := DecodeVersionedArtifactContext(s.ArtifactContext, s.Revision.ArtifactPins)
		if err != nil {
			return err
		}
		if decoded.V3 == nil {
			return invalid("context", "Portable remap must produce a namespaced context")
		}
		c = *decoded.V3
	}
	c.SourceContentHash, c.SourceSemanticHash = content, anchor
	s.ArtifactContext, err = EncodeArtifactContextV3(c)
	if err != nil {
		return err
	}
	s.SourceContentHash = content
	s.Revision.ArtifactPins = []ArtifactPin{}
	s.Revision.SemanticHash, err = ArtifactContextV3SemanticHash(c)
	if err != nil {
		return err
	}
	hashes[previousSemantic] = s.Revision.SemanticHash
	return ValidatePortableSource(ctx, s)
}

func insertPortableSourceTx(ctx context.Context, tx *sql.Tx, s *PortableSource) error {
	pid, rid := s.Revision.ProjectID, s.Revision.ID
	raw, err := json.Marshal(s.Revision)
	if err != nil {
		return err
	}
	if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, rid, pid, string(raw)); err != nil {
		return err
	}
	for _, n := range s.Nodes {
		raw, err := json.Marshal(n)
		if err != nil {
			return err
		}
		parent := ""
		if n.ParentID != nil {
			parent = *n.ParentID
		}
		if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,parent_id,document) VALUES(?,?,'node',?,?,?,?,?)`, pid, rid, n.ID, n.Kind, n.Name, parent, string(raw)); err != nil {
			return err
		}
	}
	for _, e := range s.Edges {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,from_id,to_id,document) VALUES(?,?,'edge',?,?,?,?,?)`, pid, rid, e.ID, e.Kind, e.From, e.To, string(raw)); err != nil {
			return err
		}
	}
	for _, e := range s.Evidence {
		raw := s.RawEvidence[e.ID]
		if len(raw) == 0 {
			return invalid("evidence", "Mapped raw proof is missing")
		}
		if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,subject_id,document) VALUES(?,?,'evidence',?,?,?)`, pid, rid, e.ID, e.SubjectID, string(raw)); err != nil {
			return err
		}
	}
	var coverage any = s.Coverage
	if s.Revision.SchemaVersion == ComposedSchemaVersion {
		graph, err := portableSourceGraph(s)
		if err != nil {
			return err
		}
		if err := saveComposedContext(ctx, tx, pid, rid, &composedCandidate{Source: graph}); err != nil {
			return err
		}
		coverage = SourceRevisionContext{RevisionCoverage: s.Coverage, ViewSchemaVersion: ComposedSchemaVersion, SourceVector: *s.SourceVector, ClaimCurrentness: s.Currentness, SourceContentHash: s.SourceContentHash}
	}
	raw, err = json.Marshal(coverage)
	if err != nil {
		return err
	}
	if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_revision_sources(revision_id,document) VALUES(?,?)`, rid, string(raw)); err != nil {
		return err
	}
	c, err := DecodeVersionedArtifactContext(s.ArtifactContext, nil)
	if err != nil {
		return err
	}
	if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_revision_api_artifacts(revision_id,source_content_hash,source_semantic_hash,document) VALUES(?,?,?,?)`, rid, c.V3.SourceContentHash, c.V3.SourceSemanticHash, string(s.ArtifactContext)); err != nil {
		return err
	}
	return nil
}

// PortableRevisionDependencies returns only declared immutable history edges.
func PortableRevisionDependencies(s PortableSource) ([]string, error) {
	ids := map[string]bool{}
	if s.Revision.ParentRevisionID != nil {
		ids[*s.Revision.ParentRevisionID] = true
	}
	for _, b := range s.LegacyProofBases {
		ids[b.SourceRevisionID] = true
	}
	for _, a := range s.Assertions {
		for _, b := range a.DependencyClaims {
			if b.BaseRevisionID != "" && b.BaseRevisionID != s.Revision.ID {
				ids[b.BaseRevisionID] = true
			}
		}
	}
	add := func(kind string, attrs map[string]jsontext.Value, edge bool) error {
		refs, err := sourceAttributeReferences(kind, attrs, edge, true)
		if err != nil {
			return err
		}
		for _, r := range refs {
			if r.HistoricalRevisionID != "" {
				ids[r.HistoricalRevisionID] = true
			}
		}
		return nil
	}
	for _, n := range s.Nodes {
		if err := add(n.Kind, n.Attributes, false); err != nil {
			return nil, err
		}
	}
	for _, e := range s.Edges {
		if err := add(e.Kind, e.Attributes, true); err != nil {
			return nil, err
		}
	}
	out := []string{}
	for id := range ids {
		out = append(out, id)
	}
	return out, nil
}
