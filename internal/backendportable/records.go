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

// recordBuilder emits a model's records in export order, each validated.
type recordBuilder struct {
	installation, project string
	out                   []Record
}

func (b *recordBuilder) add(kind, id string, version int64, value any) error {
	raw, err := canonical(value)
	if err != nil {
		return err
	}
	hash, err := DocumentHash(jsontext.Value(raw))
	if err != nil {
		return err
	}
	r := Record{Kind: kind, Identity: Identity{InstallationID: b.installation, ProjectID: b.project, Kind: kind, ID: id, Version: strconv.FormatInt(version, 10)}, ContentHash: hash, Document: raw}
	if err := r.Validate(); err != nil {
		return err
	}
	b.out = append(b.out, r)
	return nil
}

func modelRecords(model bm.PortableModel, installation string) ([]Record, error) {
	b := &recordBuilder{installation: installation, project: model.Project.ID, out: []Record{}}
	if err := b.add("project", model.Project.ID, 0, projectRecord{Project: model.Project, Target: model.Target}); err != nil {
		return nil, err
	}
	for _, s := range model.Sources {
		if err := b.addSource(s); err != nil {
			return nil, err
		}
	}
	for _, p := range model.Proposals {
		if err := b.addProposal(p); err != nil {
			return nil, err
		}
	}
	for _, v := range model.Diagrams {
		if err := b.add("diagram_version", v.Pin.ID, v.Pin.Version, v); err != nil {
			return nil, err
		}
	}
	for _, v := range model.DiagramViews {
		if err := b.add("diagram_view_version", v.ID, v.Version, v); err != nil {
			return nil, err
		}
	}
	for _, v := range model.SavedViews {
		if err := b.add("saved_view_version", v.ID, v.Version, v); err != nil {
			return nil, err
		}
	}
	for _, a := range model.Annotations {
		if err := b.add("annotation", a.ID, 0, a); err != nil {
			return nil, err
		}
	}
	return b.out, nil
}

// addSource emits a revision as an empty header plus one record per node,
// edge and evidence item, each naming the header as its parent.
func (b *recordBuilder) addSource(s bm.PortableSource) error {
	header := s
	header.Nodes = []bm.Node{}
	header.Edges = []bm.Edge{}
	header.Evidence = []bm.Evidence{}
	header.RawEvidence = map[string]jsontext.Value{}
	if err := b.add("revision", s.Revision.ID, 1, revisionRecord{Source: header}); err != nil {
		return err
	}
	parent := Identity{InstallationID: b.installation, ProjectID: b.project, Kind: "revision", ID: s.Revision.ID, Version: "1"}
	for _, n := range s.Nodes {
		if err := b.add("node", n.ID, 0, sourceRecord{Parent: parent, Node: &n}); err != nil {
			return err
		}
	}
	for _, e := range s.Edges {
		if err := b.add("edge", e.ID, 0, sourceRecord{Parent: parent, Edge: &e}); err != nil {
			return err
		}
	}
	for _, e := range s.Evidence {
		if err := b.add("evidence", e.ID, 0, sourceRecord{Parent: parent, Evidence: &e, Raw: string(s.RawEvidence[e.ID])}); err != nil {
			return err
		}
	}
	return nil
}

// addProposal emits a proposal as an empty header plus one record per
// revision; a full revision carries its applied batch and the identities it
// introduced.
func (b *recordBuilder) addProposal(p bm.PortableProposal) error {
	header := p
	header.FullRevisions = []bm.ChangeProposalRevision{}
	header.LegacyRevisions = []bm.ProposalRevision{}
	header.Batches = []bm.ChangeAppliedBatch{}
	header.Identities = []bm.ChangeObjectIdentity{}
	switch {
	case p.Full != nil:
		if err := b.add("change_proposal", p.Full.ID, p.Full.Version, proposalRecord{Proposal: header}); err != nil {
			return err
		}
		for _, v := range p.FullRevisions {
			record, err := fullRevision(p, v)
			if err != nil {
				return err
			}
			if err := b.add("change_proposal_revision", v.ID, 1, record); err != nil {
				return err
			}
		}
	case p.Legacy != nil:
		if err := b.add("proposal", p.Legacy.ID, p.Legacy.Version, proposalRecord{Proposal: header}); err != nil {
			return err
		}
		for _, v := range p.LegacyRevisions {
			if err := b.add("proposal_revision", v.ID, 1, v); err != nil {
				return err
			}
		}
	default:
		return fault(422, "Missing typed proposal owner")
	}
	return nil
}

func fullRevision(p bm.PortableProposal, v bm.ChangeProposalRevision) (fullRevisionRecord, error) {
	var batch *bm.ChangeAppliedBatch
	for i := range p.Batches {
		if p.Batches[i].RevisionID == v.ID {
			batch = &p.Batches[i]
		}
	}
	if batch == nil {
		return fullRevisionRecord{}, fault(422, "Missing immutable proposal batch")
	}
	identities := []bm.ChangeObjectIdentity{}
	for _, id := range p.Identities {
		if id.FirstRevisionID == v.ID {
			identities = append(identities, id)
		}
	}
	return fullRevisionRecord{Revision: v, Batch: *batch, Identities: identities}, nil
}

func decodeRecord(record Record, out any) error {
	if err := json.Unmarshal(record.Document, out, json.RejectUnknownMembers(true)); err != nil {
		return fault(422, "Invalid typed portable record: "+err.Error())
	}
	return nil
}

// modelDecoder rebuilds a model from records in two passes: headers (the
// project, revisions, proposals) first, then the members that name them.
type modelDecoder struct {
	manifest    Manifest
	model       *bm.PortableModel
	sources     map[string]int
	proposals   map[string]int
	haveProject bool
	seen        map[recordKey]bool
}

func decodeModel(records []Record, manifest Manifest) (*bm.PortableModel, error) {
	d := &modelDecoder{
		manifest:  manifest,
		model:     &bm.PortableModel{Sources: []bm.PortableSource{}, Proposals: []bm.PortableProposal{}, Diagrams: []bm.DiagramVersion{}, DiagramViews: []bm.DiagramView{}, SavedViews: []bm.SavedView{}, Annotations: []bm.Annotation{}},
		sources:   map[string]int{},
		proposals: map[string]int{},
		seen:      map[recordKey]bool{},
	}
	for _, record := range records {
		if err := d.header(record); err != nil {
			return nil, err
		}
	}
	if !d.haveProject {
		return nil, fault(422, "Missing project record")
	}
	for _, record := range records {
		if err := d.member(record); err != nil {
			return nil, err
		}
	}
	target, _ := DocumentHash(d.model.Target)
	selected, _ := DocumentHash(manifest.Selection.Target)
	if target != selected {
		return nil, fault(422, "Project target differs from manifest")
	}
	return d.model, nil
}

// header admits each record once, within the manifest's identity, and
// decodes the header kinds.
func (d *modelDecoder) header(record Record) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if record.Identity.ProjectID != d.manifest.Selection.ProjectID || record.Identity.InstallationID != d.manifest.OriginInstallationID {
		return fault(422, "Record outside manifest identity")
	}
	key, err := record.key()
	if err != nil {
		return err
	}
	if d.seen[key] {
		return fault(422, "Duplicate exact record")
	}
	d.seen[key] = true
	switch record.Kind {
	case "project":
		return d.project(record)
	case "revision":
		return d.revision(record)
	case "proposal", "change_proposal":
		return d.proposal(record)
	}
	return nil
}

func (d *modelDecoder) project(record Record) error {
	var v projectRecord
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	if d.haveProject || v.Project.ID != record.Identity.ID || v.Project.ID != d.manifest.Selection.ProjectID {
		return fault(422, "Conflicting project record")
	}
	d.haveProject = true
	d.model.Project, d.model.Target = v.Project, v.Target
	return nil
}

func (d *modelDecoder) revision(record Record) error {
	var v revisionRecord
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	s := v.Source
	if s.Revision.ID != record.Identity.ID || s.Revision.ProjectID != d.manifest.Selection.ProjectID || len(s.Nodes) != 0 || len(s.Edges) != 0 || len(s.Evidence) != 0 || len(s.RawEvidence) != 0 {
		return fault(422, "Invalid source header")
	}
	if _, ok := d.sources[record.Identity.ID]; ok {
		return fault(422, "Duplicate revision identity")
	}
	d.sources[record.Identity.ID] = len(d.model.Sources)
	d.model.Sources = append(d.model.Sources, s)
	return nil
}

func (d *modelDecoder) proposal(record Record) error {
	var v proposalRecord
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	p := v.Proposal
	if len(p.FullRevisions) != 0 || len(p.LegacyRevisions) != 0 || len(p.Batches) != 0 || len(p.Identities) != 0 {
		return fault(422, "Proposal header cannot hide revision records")
	}
	project := d.manifest.Selection.ProjectID
	valid := record.Kind == "proposal" && p.Legacy != nil && p.Full == nil && p.Legacy.ID == record.Identity.ID && p.Legacy.ProjectID == project || record.Kind == "change_proposal" && p.Full != nil && p.Legacy == nil && p.Full.ID == record.Identity.ID && p.Full.ProjectID == project
	if !valid {
		return fault(422, "Invalid proposal header")
	}
	d.proposals[record.Kind+":"+record.Identity.ID] = len(d.model.Proposals)
	d.model.Proposals = append(d.model.Proposals, p)
	return nil
}

// member decodes every non-header kind into the model; an unknown kind is
// refused.
func (d *modelDecoder) member(record Record) error {
	switch record.Kind {
	case "project", "revision", "proposal", "change_proposal":
		return nil
	case "node", "edge", "evidence":
		return d.sourceMember(record)
	case "change_proposal_revision":
		return d.fullRevision(record)
	case "proposal_revision":
		return d.legacyRevision(record)
	case "diagram_version":
		return d.diagram(record)
	case "diagram_view_version":
		return d.diagramView(record)
	case "saved_view_version":
		return d.savedView(record)
	case "annotation":
		var v bm.Annotation
		if err := decodeRecord(record, &v); err != nil {
			return err
		}
		if v.ID != record.Identity.ID {
			return fault(422, "Annotation identity differs")
		}
		d.model.Annotations = append(d.model.Annotations, v)
		return nil
	default:
		return fault(422, "Unsupported domain record kind: "+record.Kind)
	}
}

func (d *modelDecoder) sourceMember(record Record) error {
	var v sourceRecord
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	at, ok := d.sources[v.Parent.ID]
	if !ok || v.Parent.Kind != "revision" || v.Parent.ProjectID != d.manifest.Selection.ProjectID || v.Parent.InstallationID != d.manifest.OriginInstallationID || v.Parent.Version != "1" {
		return fault(422, "Missing source record parent")
	}
	s := &d.model.Sources[at]
	switch record.Kind {
	case "node":
		if v.Node == nil || v.Edge != nil || v.Evidence != nil || len(v.Raw) != 0 || v.Node.ID != record.Identity.ID {
			return fault(422, "Wrong node document")
		}
		s.Nodes = append(s.Nodes, *v.Node)
	case "edge":
		if v.Edge == nil || v.Node != nil || v.Evidence != nil || len(v.Raw) != 0 || v.Edge.ID != record.Identity.ID {
			return fault(422, "Wrong edge document")
		}
		s.Edges = append(s.Edges, *v.Edge)
	case "evidence":
		return addEvidence(s, record, v)
	}
	return nil
}

// addEvidence requires the raw proof to decode to exactly the typed evidence
// it travels with.
func addEvidence(s *bm.PortableSource, record Record, v sourceRecord) error {
	if v.Evidence == nil || v.Node != nil || v.Edge != nil || v.Evidence.ID != record.Identity.ID {
		return fault(422, "Wrong evidence document")
	}
	var proof bm.Evidence
	if err := json.Unmarshal([]byte(v.Raw), &proof, json.RejectUnknownMembers(true)); err != nil {
		// Review 2026-10-06, F69: the envelope hash covers whatever the
		// exporter wrote, so an empty or unknown raw proof reaches here
		// intact; the bare decoder error was a logged 500.
		return fault(422, "Invalid raw evidence proof: "+err.Error())
	}
	a, _ := DocumentHash(proof)
	b, _ := DocumentHash(*v.Evidence)
	if a != b {
		return fault(422, "Raw proof differs from typed evidence")
	}
	s.Evidence = append(s.Evidence, *v.Evidence)
	if s.RawEvidence == nil {
		s.RawEvidence = map[string]jsontext.Value{}
	}
	s.RawEvidence[v.Evidence.ID] = jsontext.Value(v.Raw)
	return nil
}

func (d *modelDecoder) fullRevision(record Record) error {
	var v fullRevisionRecord
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	at, ok := d.proposals["change_proposal:"+v.Revision.ProposalID]
	if !ok || v.Revision.ID != record.Identity.ID || v.Batch.RevisionID != v.Revision.ID {
		return fault(422, "Missing full proposal revision owner")
	}
	p := &d.model.Proposals[at]
	p.FullRevisions = append(p.FullRevisions, v.Revision)
	p.Batches = append(p.Batches, v.Batch)
	p.Identities = append(p.Identities, v.Identities...)
	return nil
}

func (d *modelDecoder) legacyRevision(record Record) error {
	var v bm.ProposalRevision
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	at, ok := d.proposals["proposal:"+v.ProposalID]
	if !ok || v.ID != record.Identity.ID {
		return fault(422, "Missing proposal revision owner")
	}
	d.model.Proposals[at].LegacyRevisions = append(d.model.Proposals[at].LegacyRevisions, v)
	return nil
}

func (d *modelDecoder) diagram(record Record) error {
	var v bm.DiagramVersion
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	if v.Pin.ID != record.Identity.ID || strconv.FormatInt(v.Pin.Version, 10) != record.Identity.Version || v.ProjectID != d.manifest.Selection.ProjectID {
		return fault(422, "Diagram identity differs from envelope")
	}
	d.model.Diagrams = append(d.model.Diagrams, v)
	return nil
}

func (d *modelDecoder) diagramView(record Record) error {
	var v bm.DiagramView
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	if v.ID != record.Identity.ID || strconv.FormatInt(v.Version, 10) != record.Identity.Version {
		return fault(422, "View identity differs")
	}
	d.model.DiagramViews = append(d.model.DiagramViews, v)
	return nil
}

func (d *modelDecoder) savedView(record Record) error {
	var v bm.SavedView
	if err := decodeRecord(record, &v); err != nil {
		return err
	}
	if v.ID != record.Identity.ID || v.ProjectID != d.manifest.Selection.ProjectID || strconv.FormatInt(v.Version, 10) != record.Identity.Version {
		return fault(422, "Saved view identity differs")
	}
	d.model.SavedViews = append(d.model.SavedViews, v)
	return nil
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
