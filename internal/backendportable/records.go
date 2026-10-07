package backendportable

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strconv"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

type sourceRecord struct {
	Parent   Identity     `json:"parent"`
	Node     *bm.Node     `json:"node,omitzero"`
	Edge     *bm.Edge     `json:"edge,omitzero"`
	Evidence *bm.Evidence `json:"evidence,omitzero"`
	Raw      string       `json:"raw,omitempty"`
}
type revisionRecord struct {
	Source bm.PortableSource `json:"source"`
}
type projectRecord struct {
	Project bm.Project           `json:"project"`
	Target  bm.BackendReadTarget `json:"target"`
}
type proposalRecord struct {
	Proposal bm.PortableProposal `json:"proposal"`
}
type fullRevisionRecord struct {
	Revision   bm.ChangeProposalRevision `json:"revision"`
	Batch      bm.ChangeAppliedBatch     `json:"batch"`
	Identities []bm.ChangeObjectIdentity `json:"identities"`
}

func modelRecords(model bm.PortableModel, installation string) ([]Record, error) {
	out := []Record{}
	add := func(kind, id string, version int64, value any) error {
		raw, err := canonical(value)
		if err != nil {
			return err
		}
		hash, err := DocumentHash(jsontext.Value(raw))
		if err != nil {
			return err
		}
		r := Record{Kind: kind, Identity: Identity{InstallationID: installation, ProjectID: model.Project.ID, Kind: kind, ID: id, Version: strconv.FormatInt(version, 10)}, ContentHash: hash, Document: raw}
		if err := r.Validate(); err != nil {
			return err
		}
		out = append(out, r)
		return nil
	}
	if err := add("project", model.Project.ID, 0, projectRecord{Project: model.Project, Target: model.Target}); err != nil {
		return nil, err
	}
	for _, s := range model.Sources {
		header := s
		header.Nodes = []bm.Node{}
		header.Edges = []bm.Edge{}
		header.Evidence = []bm.Evidence{}
		header.RawEvidence = map[string]jsontext.Value{}
		if err := add("revision", s.Revision.ID, 1, revisionRecord{Source: header}); err != nil {
			return nil, err
		}
		parent := Identity{InstallationID: installation, ProjectID: model.Project.ID, Kind: "revision", ID: s.Revision.ID, Version: "1"}
		for _, n := range s.Nodes {
			if err := add("node", n.ID, 0, sourceRecord{Parent: parent, Node: &n}); err != nil {
				return nil, err
			}
		}
		for _, e := range s.Edges {
			if err := add("edge", e.ID, 0, sourceRecord{Parent: parent, Edge: &e}); err != nil {
				return nil, err
			}
		}
		for _, e := range s.Evidence {
			if err := add("evidence", e.ID, 0, sourceRecord{Parent: parent, Evidence: &e, Raw: string(s.RawEvidence[e.ID])}); err != nil {
				return nil, err
			}
		}
	}
	for _, p := range model.Proposals {
		header := p
		header.FullRevisions = []bm.ChangeProposalRevision{}
		header.LegacyRevisions = []bm.ProposalRevision{}
		header.Batches = []bm.ChangeAppliedBatch{}
		header.Identities = []bm.ChangeObjectIdentity{}
		if p.Full != nil {
			if err := add("change_proposal", p.Full.ID, p.Full.Version, proposalRecord{Proposal: header}); err != nil {
				return nil, err
			}
			for _, v := range p.FullRevisions {
				var batch *bm.ChangeAppliedBatch
				for i := range p.Batches {
					if p.Batches[i].RevisionID == v.ID {
						batch = &p.Batches[i]
					}
				}
				if batch == nil {
					return nil, fault(422, "Missing immutable proposal batch")
				}
				identities := []bm.ChangeObjectIdentity{}
				for _, id := range p.Identities {
					if id.FirstRevisionID == v.ID {
						identities = append(identities, id)
					}
				}
				if err := add("change_proposal_revision", v.ID, 1, fullRevisionRecord{Revision: v, Batch: *batch, Identities: identities}); err != nil {
					return nil, err
				}
			}
		} else if p.Legacy != nil {
			if err := add("proposal", p.Legacy.ID, p.Legacy.Version, proposalRecord{Proposal: header}); err != nil {
				return nil, err
			}
			for _, v := range p.LegacyRevisions {
				if err := add("proposal_revision", v.ID, 1, v); err != nil {
					return nil, err
				}
			}
		} else {
			return nil, fault(422, "Missing typed proposal owner")
		}
	}
	for _, v := range model.Diagrams {
		if err := add("diagram_version", v.Pin.ID, v.Pin.Version, v); err != nil {
			return nil, err
		}
	}
	for _, v := range model.DiagramViews {
		if err := add("diagram_view_version", v.ID, v.Version, v); err != nil {
			return nil, err
		}
	}
	for _, v := range model.SavedViews {
		if err := add("saved_view_version", v.ID, v.Version, v); err != nil {
			return nil, err
		}
	}
	for _, a := range model.Annotations {
		if err := add("annotation", a.ID, 0, a); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func decodeModel(records []Record, manifest Manifest) (*bm.PortableModel, error) {
	model := &bm.PortableModel{Sources: []bm.PortableSource{}, Proposals: []bm.PortableProposal{}, Diagrams: []bm.DiagramVersion{}, DiagramViews: []bm.DiagramView{}, SavedViews: []bm.SavedView{}, Annotations: []bm.Annotation{}}
	sources := map[string]int{}
	proposals := map[string]int{}
	haveProject := false
	seen := map[recordKey]bool{}
	decode := func(record Record, out any) error {
		if err := json.Unmarshal(record.Document, out, json.RejectUnknownMembers(true)); err != nil {
			return fault(422, "Invalid typed portable record: "+err.Error())
		}
		return nil
	}
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return nil, err
		}
		if record.Identity.ProjectID != manifest.Selection.ProjectID || record.Identity.InstallationID != manifest.OriginInstallationID {
			return nil, fault(422, "Record outside manifest identity")
		}
		key, err := record.key()
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, fault(422, "Duplicate exact record")
		}
		seen[key] = true
		switch record.Kind {
		case "project":
			var v projectRecord
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			if haveProject || v.Project.ID != record.Identity.ID || v.Project.ID != manifest.Selection.ProjectID {
				return nil, fault(422, "Conflicting project record")
			}
			haveProject = true
			model.Project, model.Target = v.Project, v.Target
		case "revision":
			var v revisionRecord
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			if v.Source.Revision.ID != record.Identity.ID || v.Source.Revision.ProjectID != manifest.Selection.ProjectID || len(v.Source.Nodes) != 0 || len(v.Source.Edges) != 0 || len(v.Source.Evidence) != 0 || len(v.Source.RawEvidence) != 0 {
				return nil, fault(422, "Invalid source header")
			}
			if _, ok := sources[record.Identity.ID]; ok {
				return nil, fault(422, "Duplicate revision identity")
			}
			sources[record.Identity.ID] = len(model.Sources)
			model.Sources = append(model.Sources, v.Source)
		case "proposal", "change_proposal":
			var v proposalRecord
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			p := v.Proposal
			if len(p.FullRevisions) != 0 || len(p.LegacyRevisions) != 0 || len(p.Batches) != 0 || len(p.Identities) != 0 {
				return nil, fault(422, "Proposal header cannot hide revision records")
			}
			valid := record.Kind == "proposal" && p.Legacy != nil && p.Full == nil && p.Legacy.ID == record.Identity.ID && p.Legacy.ProjectID == manifest.Selection.ProjectID || record.Kind == "change_proposal" && p.Full != nil && p.Legacy == nil && p.Full.ID == record.Identity.ID && p.Full.ProjectID == manifest.Selection.ProjectID
			if !valid {
				return nil, fault(422, "Invalid proposal header")
			}
			proposals[record.Kind+":"+record.Identity.ID] = len(model.Proposals)
			model.Proposals = append(model.Proposals, p)
		}
	}
	if !haveProject {
		return nil, fault(422, "Missing project record")
	}
	for _, record := range records {
		switch record.Kind {
		case "project", "revision", "proposal", "change_proposal":
		case "node", "edge", "evidence":
			var v sourceRecord
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			at, ok := sources[v.Parent.ID]
			if !ok || v.Parent.Kind != "revision" || v.Parent.ProjectID != manifest.Selection.ProjectID || v.Parent.InstallationID != manifest.OriginInstallationID || v.Parent.Version != "1" {
				return nil, fault(422, "Missing source record parent")
			}
			s := &model.Sources[at]
			switch record.Kind {
			case "node":
				if v.Node == nil || v.Edge != nil || v.Evidence != nil || len(v.Raw) != 0 || v.Node.ID != record.Identity.ID {
					return nil, fault(422, "Wrong node document")
				}
				s.Nodes = append(s.Nodes, *v.Node)
			case "edge":
				if v.Edge == nil || v.Node != nil || v.Evidence != nil || len(v.Raw) != 0 || v.Edge.ID != record.Identity.ID {
					return nil, fault(422, "Wrong edge document")
				}
				s.Edges = append(s.Edges, *v.Edge)
			case "evidence":
				if v.Evidence == nil || v.Node != nil || v.Edge != nil || v.Evidence.ID != record.Identity.ID {
					return nil, fault(422, "Wrong evidence document")
				}
				var proof bm.Evidence
				if err := json.Unmarshal([]byte(v.Raw), &proof, json.RejectUnknownMembers(true)); err != nil {
					// Review 2026-10-06, F69: the envelope hash covers whatever the
					// exporter wrote, so an empty or unknown raw proof reaches here
					// intact; the bare decoder error was a logged 500.
					return nil, fault(422, "Invalid raw evidence proof: "+err.Error())
				}
				a, _ := DocumentHash(proof)
				b, _ := DocumentHash(*v.Evidence)
				if a != b {
					return nil, fault(422, "Raw proof differs from typed evidence")
				}
				s.Evidence = append(s.Evidence, *v.Evidence)
				if s.RawEvidence == nil {
					s.RawEvidence = map[string]jsontext.Value{}
				}
				s.RawEvidence[v.Evidence.ID] = jsontext.Value(v.Raw)
			}
		case "change_proposal_revision":
			var v fullRevisionRecord
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			at, ok := proposals["change_proposal:"+v.Revision.ProposalID]
			if !ok || v.Revision.ID != record.Identity.ID || v.Batch.RevisionID != v.Revision.ID {
				return nil, fault(422, "Missing full proposal revision owner")
			}
			p := &model.Proposals[at]
			p.FullRevisions = append(p.FullRevisions, v.Revision)
			p.Batches = append(p.Batches, v.Batch)
			p.Identities = append(p.Identities, v.Identities...)
		case "proposal_revision":
			var v bm.ProposalRevision
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			at, ok := proposals["proposal:"+v.ProposalID]
			if !ok || v.ID != record.Identity.ID {
				return nil, fault(422, "Missing proposal revision owner")
			}
			model.Proposals[at].LegacyRevisions = append(model.Proposals[at].LegacyRevisions, v)
		case "diagram_version":
			var v bm.DiagramVersion
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			if v.Pin.ID != record.Identity.ID || strconv.FormatInt(v.Pin.Version, 10) != record.Identity.Version || v.ProjectID != manifest.Selection.ProjectID {
				return nil, fault(422, "Diagram identity differs from envelope")
			}
			model.Diagrams = append(model.Diagrams, v)
		case "diagram_view_version":
			var v bm.DiagramView
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			if v.ID != record.Identity.ID || strconv.FormatInt(v.Version, 10) != record.Identity.Version {
				return nil, fault(422, "View identity differs")
			}
			model.DiagramViews = append(model.DiagramViews, v)
		case "saved_view_version":
			var v bm.SavedView
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			if v.ID != record.Identity.ID || v.ProjectID != manifest.Selection.ProjectID || strconv.FormatInt(v.Version, 10) != record.Identity.Version {
				return nil, fault(422, "Saved view identity differs")
			}
			model.SavedViews = append(model.SavedViews, v)
		case "annotation":
			var v bm.Annotation
			if err := decode(record, &v); err != nil {
				return nil, err
			}
			if v.ID != record.Identity.ID {
				return nil, fault(422, "Annotation identity differs")
			}
			model.Annotations = append(model.Annotations, v)
		default:
			return nil, fault(422, "Unsupported domain record kind: "+record.Kind)
		}
	}
	target, _ := DocumentHash(model.Target)
	selected, _ := DocumentHash(manifest.Selection.Target)
	if target != selected {
		return nil, fault(422, "Project target differs from manifest")
	}
	return model, nil
}

// uniqueRecords is the exporter's half of the cross-chunk uniqueness
// guarantee, one linear pass over one key set. putChunk used to provide it by
// re-decoding every earlier chunk on each insert, O(n²) under the writer
// (review 2026-10-06, F2/F5/F33/F68); without this pass an exporter bug that
// emitted the same record twice in different chunks would freeze a bundle
// that only the importer's Preview refuses. It is separate from splitRecords
// because tests use splitRecords to forge such bundles on purpose.
func uniqueRecords(ctx context.Context, records []Record) error {
	seen := make(map[recordKey]bool, len(records))
	for i, record := range records {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		key, err := record.key()
		if err != nil {
			return err
		}
		if seen[key] {
			return fault(422, "Duplicate exact record")
		}
		seen[key] = true
	}
	return nil
}

// splitRecords packs records into the fewest chunks that fit MaxChunkRecords
// and MaxChunkBytes, in order.
//
// The size of a pending chunk is kept as a running total (review 2026-10-06,
// F32): the canonical form of an array is `[` + the canonical elements joined
// by `,` + `]` (canonicalRaw re-encodes each element on its own and marshals
// the array compactly), so a chunk of n records whose canonical sizes are
// s₁…sₙ encodes to exactly 2 + Σsᵢ + (n−1) bytes. The previous loop
// re-canonicalised `append(clone(pending), record)` for every record — O(n²)
// recursive parse+marshal per chunk, ~10 GB for an export of tens of
// thousands of records, all under the single writer. Now each record is
// canonicalised once here and once more by EncodeChunk at flush; EncodeChunk
// also re-checks the byte bound, so an accounting error could only turn into
// a 413, never into an oversized chunk. TestSplitRecordsRunningSizeMatchesCanonical
// pins the identity.
//
// The loop checks ctx, because Export runs it inside db.Write and a client
// that went away must release the writer instead of finishing the bundle.
func splitRecords(ctx context.Context, records []Record) ([][]byte, []ChunkDescriptor, error) {
	chunks := [][]byte{}
	descriptors := []ChunkDescriptor{}
	pending := []Record{}
	pendingBytes := 0 // canonical size of pending as a JSON array; 0 when empty
	total := 0
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		body, d, err := EncodeChunk(len(chunks), pending)
		if err != nil {
			return err
		}
		total += len(body)
		if total > MaxBundleBytes {
			return fault(413, "Portable bundle byte quota")
		}
		chunks = append(chunks, body)
		descriptors = append(descriptors, d)
		pending = []Record{}
		pendingBytes = 0
		return nil
	}
	for i, record := range records {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		raw, err := canonical(record)
		if err != nil {
			return nil, nil, err
		}
		size := len(raw)
		if 2+size > MaxChunkBytes {
			return nil, nil, fault(413, fmt.Sprintf("Portable %s record exceeds chunk bound", record.Kind))
		}
		// Appending to a non-empty chunk costs the element plus its comma;
		// the first element costs the element plus the two brackets.
		trialBytes := 2 + size
		if len(pending) > 0 {
			trialBytes = pendingBytes + 1 + size
		}
		if len(pending)+1 > MaxChunkRecords || trialBytes > MaxChunkBytes {
			if err := flush(); err != nil {
				return nil, nil, err
			}
			trialBytes = 2 + size
		}
		pending = append(pending, record)
		pendingBytes = trialBytes
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	return chunks, descriptors, nil
}
