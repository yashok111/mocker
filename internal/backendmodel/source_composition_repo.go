package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
)

func sourceNodePayload(n Node) SourceAssertionPayload {
	return SourceAssertionPayload{RecordType: "node", Kind: n.Kind, Name: n.Name, ParentID: n.ParentID, Attributes: n.Attributes}
}
func sourceEdgePayload(e Edge) SourceAssertionPayload {
	return SourceAssertionPayload{RecordType: "edge", Kind: e.Kind, From: e.From, To: e.To, Attributes: e.Attributes}
}

func loadRawSourceEvidence(ctx context.Context, q importReader, pid, rid string) (map[string]jsontext.Value, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,document FROM backend_graph_records WHERE project_id=? AND revision_id=? AND record_type='evidence' ORDER BY id`, pid, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]jsontext.Value{}
	var bytes int
	for rows.Next() {
		var id, doc string
		if err := rows.Scan(&id, &doc); err != nil {
			return nil, err
		}
		bytes += len(doc)
		if bytes > MaxRevisionBytes {
			return nil, limitFault("Source proof limit exceeded")
		}
		out[id] = []byte(doc)
	}
	return out, rows.Err()
}

func bootstrapSource5(ctx context.Context, q importReader, pid, rid string) (*SourceGraphSnapshot, error) {
	state, err := loadRevisionState(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	vector, err := sourceVectorAt(ctx, q, state)
	if err != nil {
		return nil, err
	}
	raw, err := loadRawSourceEvidence(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	graph := &SourceGraphSnapshot{State: *state, SourceVector: vector, Assertions: []ProviderAssertion{}, Selections: []SourceAssertionResolution{}, Currentness: []SourceClaimCurrentness{}, Identities: []QualifiedSourceIdentity{}, LegacyProofBases: []LegacyProofBasis{}, RawEvidence: raw}
	legacyCache := newLegacyProofCache()
	add := func(typ, id, key string, payload SourceAssertionPayload, owner *AssertionOwnership, fresh *AssertionFreshness, proof []string) error {
		if owner == nil || fresh == nil {
			return semantic("source", "Legacy subject has no derivable owner")
		}
		a := ProviderAssertion{RecordType: typ, RecordID: id, Owner: *owner, ExternalKey: key, Payload: payload, EvidenceIDs: slices.Clone(proof), DependencyClaims: []SourceDependencyBinding{}, Freshness: *fresh, FieldCurrentness: []TypedFieldCurrentness{}}
		ownBases := []LegacyProofBasis{}
		for _, eid := range proof {
			basis, err := legacyCache.derive(ctx, q, pid, rid, typ, id, eid)
			if err != nil {
				return err
			}
			graph.LegacyProofBases = append(graph.LegacyProofBases, basis)
			ownBases = append(ownBases, basis)
		}
		hash, err := sourceIntrinsicHash(a, graph.RawEvidence, ownBases)
		if err != nil {
			return err
		}
		a.AssertionHash = hash
		graph.Assertions = append(graph.Assertions, a)
		return nil
	}
	for _, n := range state.Nodes {
		if err := add("node", n.ID, n.ExternalKey, sourceNodePayload(n), n.Ownership, n.Freshness, n.EvidenceIDs); err != nil {
			return nil, err
		}
	}
	for _, e := range state.Edges {
		if err := add("edge", e.ID, e.ExternalKey, sourceEdgePayload(e), e.Ownership, e.Freshness, e.EvidenceIDs); err != nil {
			return nil, err
		}
	}
	if err := bootstrapSourceDependencies(graph, rid); err != nil {
		return nil, err
	}
	graph.Identities = sourceIdentities(graph.Assertions)
	graph.legacyBasisBytes = legacyCache.bytes
	return graph, nil
}

func (r *Repo) ResolveSourceGraph(ctx context.Context, pid, rid string) (*SourceGraphSnapshot, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return loadSourceGraph(ctx, tx, pid, rid)
}

func loadSourceGraph(ctx context.Context, q importReader, pid, rid string) (*SourceGraphSnapshot, error) {
	state, err := loadRevisionState(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	if state.Revision.SchemaVersion == EventsSchemaVersion {
		graph, err := bootstrapSource5(ctx, q, pid, rid)
		if err != nil {
			return nil, err
		}
		graph.SourceVector = nil
		graph.Assertions = nil
		graph.LegacyProofBases = nil
		return graph, nil
	}
	graph := &SourceGraphSnapshot{State: *state, rawAssertions: map[string]jsontext.Value{}, Assertions: []ProviderAssertion{}, Selections: []SourceAssertionResolution{}, Currentness: []SourceClaimCurrentness{}, Identities: []QualifiedSourceIdentity{}, LegacyProofBases: []LegacyProofBasis{}}
	graph.RawEvidence, err = loadRawSourceEvidence(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	if state.Revision.SchemaVersion != ComposedSchemaVersion {
		return graph, nil
	}
	var doc string
	if err := q.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources WHERE revision_id=?`, rid).Scan(&doc); err != nil {
		return nil, err
	}
	var coverage SourceRevisionContext
	if err := json.Unmarshal([]byte(doc), &coverage, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if coverage.ViewSchemaVersion != ComposedSchemaVersion {
		return nil, semantic("source", "Invalid composed context tag")
	}
	graph.SourceVector = &coverage.SourceVector
	graph.Currentness = coverage.ClaimCurrentness
	graph.SourceContentHash = coverage.SourceContentHash
	if err := loadSourceContextRows(ctx, q, pid, rid, graph); err != nil {
		return nil, err
	}
	legacyCache := newLegacyProofCache()
	for _, basis := range graph.LegacyProofBases {
		if _, err := legacyCache.load(ctx, q, basis); err != nil {
			return nil, err
		}
	}
	graph.Identities = sourceIdentities(graph.Assertions)
	graph.legacyBasisBytes = legacyCache.bytes
	return graph, nil
}

func loadSourceContextRows(ctx context.Context, q importReader, pid, rid string, graph *SourceGraphSnapshot) error {
	queries := []struct {
		sql    string
		decode func([]byte) error
	}{
		{`SELECT document FROM backend_revision_assertions WHERE project_id=? AND revision_id=? ORDER BY record_type,record_id,repository_id,provider_namespace`, func(raw []byte) error {
			var a ProviderAssertion
			if err := json.Unmarshal(raw, &a, json.RejectUnknownMembers(true)); err != nil {
				return err
			}
			graph.Assertions = append(graph.Assertions, a)
			graph.rawAssertions[sourceAssertionKey(a)] = append(jsontext.Value(nil), raw...)
			return nil
		}},
		{`SELECT document FROM backend_revision_assertion_resolutions WHERE project_id=? AND revision_id=? ORDER BY record_type,record_id,property_key`, func(raw []byte) error {
			var a SourceAssertionResolution
			if err := json.Unmarshal(raw, &a, json.RejectUnknownMembers(true)); err != nil {
				return err
			}
			graph.Selections = append(graph.Selections, a)
			return nil
		}},
		{`SELECT document FROM backend_revision_legacy_proof_bases WHERE project_id=? AND revision_id=? ORDER BY evidence_id,basis_hash`, func(raw []byte) error {
			var b LegacyProofBasis
			if err := json.Unmarshal(raw, &b, json.RejectUnknownMembers(true)); err != nil {
				return err
			}
			graph.LegacyProofBases = append(graph.LegacyProofBases, b)
			return nil
		}},
	}
	for _, query := range queries {
		if err := readSourceContextRows(ctx, q, pid, rid, query.sql, query.decode); err != nil {
			return err
		}
	}
	return nil
}

func readSourceContextRows(ctx context.Context, q importReader, pid, rid, query string, decode func([]byte) error) error {
	rows, err := q.QueryContext(ctx, query, pid, rid)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return err
		}
		if err := decode([]byte(doc)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func loadComposedBase(ctx context.Context, q importReader, pid, rid string) (*SourceGraphSnapshot, error) {
	graph, err := loadSourceGraph(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	if graph.State.Revision.SchemaVersion == EventsSchemaVersion {
		return bootstrapSource5(ctx, q, pid, rid)
	}
	if graph.SourceVector == nil {
		graph.SourceVector = &SourceVector{DocumentVersion: "source-vector-v1", Partitions: []SourcePartition{}, Snapshots: []SourceSnapshot{}}
	}
	return graph, nil
}

func sourceIdentities(assertions []ProviderAssertion) []QualifiedSourceIdentity {
	out := make([]QualifiedSourceIdentity, 0, len(assertions))
	for _, a := range assertions {
		out = append(out, QualifiedSourceIdentity{RecordType: a.RecordType, ID: a.RecordID, RepositoryID: a.Owner.RepositoryID, ProviderNamespace: a.Owner.ProviderNamespace, ExternalKey: a.ExternalKey, AssertionHash: a.AssertionHash})
	}
	slices.SortFunc(out, func(a, b QualifiedSourceIdentity) int {
		return strings.Compare(strings.Join([]string{a.RecordType, a.ID, a.RepositoryID, a.ProviderNamespace, a.ExternalKey, a.AssertionHash}, "\x00"), strings.Join([]string{b.RecordType, b.ID, b.RepositoryID, b.ProviderNamespace, b.ExternalKey, b.AssertionHash}, "\x00"))
	})
	return out
}
func SourceIdentities(graph *SourceGraphSnapshot, typ, id string) []QualifiedSourceIdentity {
	out := []QualifiedSourceIdentity{}
	for _, identity := range graph.Identities {
		if identity.RecordType == typ && identity.ID == id {
			out = append(out, identity)
		}
	}
	return out
}

func saveComposedContext(ctx context.Context, tx *sql.Tx, pid, rid string, c *composedCandidate) error {
	for _, a := range c.Source.Assertions {
		doc := []byte(c.Source.rawAssertions[sourceAssertionKey(a)])
		if len(doc) == 0 {
			var err error
			doc, err = canonicalJSON(a)
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_revision_assertions(project_id,revision_id,record_type,record_id,repository_id,provider_namespace,external_key,assertion_hash,document) VALUES(?,?,?,?,?,?,?,?,?)`, pid, rid, a.RecordType, a.RecordID, a.Owner.RepositoryID, a.Owner.ProviderNamespace, a.ExternalKey, a.AssertionHash, string(doc)); err != nil {
			return err
		}
	}
	for _, a := range c.Source.Selections {
		doc, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_revision_assertion_resolutions(project_id,revision_id,record_type,record_id,property_key,conflict_hash,document) VALUES(?,?,?,?,?,?,?)`, pid, rid, a.RecordType, a.ID, sourcePropertyKey(a.Property), a.ConflictHash, string(doc)); err != nil {
			return err
		}
	}
	for _, b := range c.Source.LegacyProofBases {
		doc, err := json.Marshal(b)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_revision_legacy_proof_bases(project_id,revision_id,evidence_id,source_revision_id,basis_hash,document) VALUES(?,?,?,?,?,?)`, pid, rid, b.EvidenceID, b.SourceRevisionID, b.BasisHash, string(doc)); err != nil {
			return err
		}
	}
	return nil
}
