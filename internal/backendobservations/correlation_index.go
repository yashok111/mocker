package backendobservations

import (
	bm "github.com/yashok111/mocker/internal/backendmodel"
)

// correlationIndex holds the lookups Correlate's per-record inference used to
// recompute by scanning the whole effective graph for EVERY record: source
// identities, every evidence row (plus an edge scan per hit) and every node's
// queryFingerprint, the last with one json.Unmarshal per node per record.
// At the documented limits (100,000 records, 50,000 nodes, 250,000 evidence
// rows) that is ~10^10 comparisons and ~5×10^9 decodes in one request, guarded
// only by ctx (review 2026-10-06, F158, F34). Each map is built once, on first
// use, in O(graph); a record then costs O(its candidates). The fingerprint
// scan is gone rather than indexed: no node kind can carry a queryFingerprint
// attribute, so it could never match (review 2026-10-06, F157).
//
// Every map keeps graph order, so candidates are appended in the order the
// scans found them and addCandidate sees the same sequence.
type correlationIndex struct {
	graph *bm.EffectiveGraphSnapshot
	// repository is the observation context's repository: the locator scan
	// compared each evidence row against it, so the index is built for it.
	repository string
	identities map[correlationIdentityKey][]string
	locators   map[correlationLocatorKey][]bm.DiagramRef
}

type correlationIdentityKey struct {
	repository, namespace, externalKey, recordType string
}

type correlationLocatorKey struct {
	file string
	line int64
}

func (x *correlationIndex) identity(key correlationIdentityKey) []string {
	if x.identities == nil {
		x.identities = map[correlationIdentityKey][]string{}
		for _, candidate := range x.graph.Source.Identities {
			k := correlationIdentityKey{candidate.RepositoryID, candidate.ProviderNamespace, candidate.ExternalKey, candidate.RecordType}
			x.identities[k] = append(x.identities[k], candidate.ID)
		}
	}
	return x.identities[key]
}

func (x *correlationIndex) locator(file string, line int64) []bm.DiagramRef {
	if x.locators == nil {
		x.locators = map[correlationLocatorKey][]bm.DiagramRef{}
		edges := make(map[string]bool, len(x.graph.State.Edges))
		for _, e := range x.graph.State.Edges {
			edges[e.ID] = true
		}
		for _, ev := range x.graph.State.Evidence {
			if ev.Source.RepositoryID != x.repository || ev.Source.StartLine == nil {
				continue
			}
			ref := bm.DiagramRef{Kind: "record", RecordType: "node", ID: ev.SubjectID}
			if edges[ev.SubjectID] {
				ref.RecordType = "edge"
			}
			k := correlationLocatorKey{ev.Source.File, *ev.Source.StartLine}
			x.locators[k] = append(x.locators[k], ref)
		}
	}
	return x.locators[correlationLocatorKey{file, line}]
}
