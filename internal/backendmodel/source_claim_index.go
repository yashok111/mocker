package backendmodel

// sourceClaimIndex groups a source snapshot's claims, selections, identities,
// legacy proof bases and currentness rows by record once, so per-record and
// per-property reads stop rescanning the whole snapshot. Before it,
// effectiveSourceOrigins ran two linear scans of Selections and Assertions
// plus a Currentness scan for every (record, property) pair of a composed
// revision, every projected record's source context rescanned all five
// slices, and the source claim diff scanned Currentness per claim and per
// selector: O(records × properties × claims) on every read and diff job,
// ~10^9-10^11 comparisons at the admitted revision scale (review 2026-10-06,
// F122, F53).
//
// The index holds slice POSITIONS, not copies, so an in-place edit of a row
// (tests mark a field stale that way) is still seen. A snapshot whose slices
// changed length since the index was built gets a fresh index, and every
// lookup re-checks each returned row's key, so a replaced slice of the same
// length rebuilds instead of answering from the old layout.
type sourceClaimIndex struct {
	lengths    [5]int
	claims     map[sourceRecordKey][]int
	selected   map[sourceRecordKey][]int
	identities map[sourceRecordKey][]int
	bases      map[string][]int
	current    map[sourceClaimRowKey]int
}

type sourceRecordKey struct{ typ, id string }

type sourceClaimRowKey struct {
	typ, id, repository, namespace, hash string
}

func sourceClaimRowKeyOf(a ProviderAssertion) sourceClaimRowKey {
	return sourceClaimRowKey{a.RecordType, a.RecordID, a.Owner.RepositoryID, a.Owner.ProviderNamespace, a.AssertionHash}
}

func sourceCurrentnessRowKey(f SourceClaimCurrentness) sourceClaimRowKey {
	return sourceClaimRowKey{f.RecordType, f.RecordID, f.RepositoryID, f.ProviderNamespace, f.AssertionHash}
}

func (graph *SourceGraphSnapshot) claimIndexLengths() [5]int {
	return [5]int{len(graph.Assertions), len(graph.Selections), len(graph.Identities), len(graph.LegacyProofBases), len(graph.Currentness)}
}

func sourceClaims(graph *SourceGraphSnapshot) *sourceClaimIndex {
	lengths := graph.claimIndexLengths()
	if index := graph.claimIndex; index != nil && index.lengths == lengths {
		return index
	}
	index := &sourceClaimIndex{lengths: lengths, claims: map[sourceRecordKey][]int{}, selected: map[sourceRecordKey][]int{}, identities: map[sourceRecordKey][]int{}, bases: map[string][]int{}, current: map[sourceClaimRowKey]int{}}
	for i, a := range graph.Assertions {
		key := sourceRecordKey{a.RecordType, a.RecordID}
		index.claims[key] = append(index.claims[key], i)
	}
	for i, s := range graph.Selections {
		key := sourceRecordKey{s.RecordType, s.ID}
		index.selected[key] = append(index.selected[key], i)
	}
	for i, identity := range graph.Identities {
		key := sourceRecordKey{identity.RecordType, identity.ID}
		index.identities[key] = append(index.identities[key], i)
	}
	for i, basis := range graph.LegacyProofBases {
		index.bases[basis.RecordID] = append(index.bases[basis.RecordID], i)
	}
	for i, f := range graph.Currentness {
		// The linear scan this replaces returned the FIRST matching row.
		if _, seen := index.current[sourceCurrentnessRowKey(f)]; !seen {
			index.current[sourceCurrentnessRowKey(f)] = i
		}
	}
	graph.claimIndex = index
	return index
}

// sourceIndexedRows returns rows[positions] in snapshot order, or false when a
// row no longer carries the key it was indexed under.
func sourceIndexedRows[T any](rows []T, positions []int, matches func(T) bool) ([]T, bool) {
	out := make([]T, 0, len(positions))
	for _, i := range positions {
		if i >= len(rows) || !matches(rows[i]) {
			return nil, false
		}
		out = append(out, rows[i])
	}
	return out, true
}

// sourceRecordRows looks up one record's rows through the index, rebuilding it
// once on a stale position, and scans as the last resort.
func sourceRecordRows[T any](graph *SourceGraphSnapshot, rows []T, positions func(*sourceClaimIndex) []int, matches func(T) bool) []T {
	for range 2 {
		if out, ok := sourceIndexedRows(rows, positions(sourceClaims(graph)), matches); ok {
			return out
		}
		graph.claimIndex = nil
	}
	out := []T{}
	for _, row := range rows {
		if matches(row) {
			out = append(out, row)
		}
	}
	return out
}

// recordClaims returns the record's claims in snapshot order.
func (graph *SourceGraphSnapshot) recordClaims(typ, id string) []ProviderAssertion {
	key := sourceRecordKey{typ, id}
	return sourceRecordRows(graph, graph.Assertions, func(x *sourceClaimIndex) []int { return x.claims[key] }, func(a ProviderAssertion) bool { return a.RecordType == typ && a.RecordID == id })
}

// recordSelections returns the record's conflict selections in snapshot order.
func (graph *SourceGraphSnapshot) recordSelections(typ, id string) []SourceAssertionResolution {
	key := sourceRecordKey{typ, id}
	return sourceRecordRows(graph, graph.Selections, func(x *sourceClaimIndex) []int { return x.selected[key] }, func(s SourceAssertionResolution) bool { return s.RecordType == typ && s.ID == id })
}

// recordIdentities returns the record's qualified identities in snapshot order.
func (graph *SourceGraphSnapshot) recordIdentities(typ, id string) []QualifiedSourceIdentity {
	key := sourceRecordKey{typ, id}
	return sourceRecordRows(graph, graph.Identities, func(x *sourceClaimIndex) []int { return x.identities[key] }, func(identity QualifiedSourceIdentity) bool {
		return identity.RecordType == typ && identity.ID == id
	})
}

// recordLegacyBases returns the legacy proof bases whose recordId is id, of
// any record type, as the scan it replaces did.
func (graph *SourceGraphSnapshot) recordLegacyBases(id string) []LegacyProofBasis {
	return sourceRecordRows(graph, graph.LegacyProofBases, func(x *sourceClaimIndex) []int { return x.bases[id] }, func(basis LegacyProofBasis) bool { return basis.RecordID == id })
}

// claimCurrentness returns the first currentness row of the exact claim. A
// miss falls back to the scan: the key carries the assertion hash, which the
// portable writer rewrites IN PLACE, and a committed claim without a row is
// itself an anomaly, so the fallback costs nothing on valid data.
func (graph *SourceGraphSnapshot) claimCurrentness(a ProviderAssertion) (SourceClaimCurrentness, bool) {
	key := sourceClaimRowKeyOf(a)
	for range 2 {
		i, ok := sourceClaims(graph).current[key]
		if !ok {
			break
		}
		if i < len(graph.Currentness) && sourceCurrentnessRowKey(graph.Currentness[i]) == key {
			return graph.Currentness[i], true
		}
		graph.claimIndex = nil
	}
	for _, f := range graph.Currentness {
		if sourceCurrentnessRowKey(f) == key {
			return f, true
		}
	}
	return SourceClaimCurrentness{}, false
}

// sourceStateIndex finds a baseline record or evidence row by ID. The legacy
// origin support and the baseline-evidence typing of a full change proposal
// scanned every node, edge and evidence row per origin and per evidence row:
// O(evidence × edges) on every resolve of a full proposal, ~10^10 comparisons
// at 100k rows (review 2026-10-06, F118). Positions, length check and key
// re-check as in sourceClaimIndex; the LAST node or edge with an ID and the
// FIRST evidence row win, as in the scans they replace.
type sourceStateIndex struct {
	lengths  [3]int
	nodes    map[string]int
	edges    map[string]int
	evidence map[string]int
}

func sourceStateRows(graph *SourceGraphSnapshot) *sourceStateIndex {
	lengths := [3]int{len(graph.State.Nodes), len(graph.State.Edges), len(graph.State.Evidence)}
	if index := graph.stateIndex; index != nil && index.lengths == lengths {
		return index
	}
	index := &sourceStateIndex{lengths: lengths, nodes: make(map[string]int, lengths[0]), edges: make(map[string]int, lengths[1]), evidence: make(map[string]int, lengths[2])}
	for i, n := range graph.State.Nodes {
		index.nodes[n.ID] = i
	}
	for i, e := range graph.State.Edges {
		index.edges[e.ID] = i
	}
	for i, e := range graph.State.Evidence {
		// effectiveLegacyPropertySupport's scan stopped at the FIRST row.
		if _, seen := index.evidence[e.ID]; !seen {
			index.evidence[e.ID] = i
		}
	}
	graph.stateIndex = index
	return index
}

func sourceStateRow[T any](graph *SourceGraphSnapshot, rows []T, id string, positions func(*sourceStateIndex) map[string]int, key func(T) string) (T, bool) {
	for range 2 {
		i, ok := positions(sourceStateRows(graph))[id]
		if !ok {
			var zero T
			return zero, false
		}
		if i < len(rows) && key(rows[i]) == id {
			return rows[i], true
		}
		graph.stateIndex = nil
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if key(rows[i]) == id {
			return rows[i], true
		}
	}
	var zero T
	return zero, false
}

func (graph *SourceGraphSnapshot) stateNode(id string) (Node, bool) {
	return sourceStateRow(graph, graph.State.Nodes, id, func(x *sourceStateIndex) map[string]int { return x.nodes }, func(n Node) string { return n.ID })
}

func (graph *SourceGraphSnapshot) stateEdge(id string) (Edge, bool) {
	return sourceStateRow(graph, graph.State.Edges, id, func(x *sourceStateIndex) map[string]int { return x.edges }, func(e Edge) string { return e.ID })
}

func (graph *SourceGraphSnapshot) stateEvidence(id string) (Evidence, bool) {
	return sourceStateRow(graph, graph.State.Evidence, id, func(x *sourceStateIndex) map[string]int { return x.evidence }, func(e Evidence) string { return e.ID })
}
