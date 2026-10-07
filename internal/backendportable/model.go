package backendportable

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

const (
	Format          = "backend-portable-v1"
	MaxChunkRecords = 500
	MaxChunkBytes   = 1 << 20
	MaxBundleBytes  = 256 << 20
)

type Identity struct {
	InstallationID string `json:"installationId"`
	ProjectID      string `json:"projectId"`
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	Version        string `json:"version"`
}
type Record struct {
	Kind        string         `json:"kind"`
	Identity    Identity       `json:"identity"`
	ContentHash string         `json:"contentHash"`
	Document    jsontext.Value `json:"document"`
}
type ChunkDescriptor struct {
	Index   int    `json:"index"`
	SHA256  string `json:"sha256"`
	Bytes   int    `json:"bytes"`
	Records int    `json:"records"`
}
type SavedViewPin struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}
type Selection struct {
	SavedViews   []SavedViewPin       `json:"savedViews,omitzero"`
	ProjectID    string               `json:"projectId"`
	Target       bm.BackendReadTarget `json:"target"`
	TargetHash   string               `json:"targetHash"`
	DiagramViews []SVGInput           `json:"diagramViews"`
}
type Manifest struct {
	Format               string            `json:"format"`
	OriginInstallationID string            `json:"originInstallationId"`
	Schemas              []string          `json:"schemas"`
	Selection            Selection         `json:"selection"`
	Chunks               []ChunkDescriptor `json:"chunks"`
}

var recordKinds = []string{"project", "repository", "source_snapshot", "revision", "node", "edge", "evidence", "annotation", "proposal", "proposal_revision", "change_proposal", "change_proposal_revision", "diagram", "diagram_version", "diagram_element", "diagram_link", "saved_view", "saved_view_version", "diagram_view", "diagram_view_version", "artifact_owner", "artifact_revision"}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func bytesHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (i Identity) Validate() error {
	if !bm.ValidID(i.InstallationID) || !bm.ValidID(i.ProjectID) || !slices.Contains(recordKinds, i.Kind) {
		return fault(422, "Invalid portable identity namespace or kind")
	}
	if strings.HasPrefix(i.Kind, "artifact_") {
		n, err := strconv.ParseInt(i.ID, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != i.ID {
			return fault(422, "Canonical owner identity required")
		}
	} else if !bm.ValidID(i.ID) {
		return fault(422, "Canonical record UUID required")
	}
	n, err := strconv.ParseInt(i.Version, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != i.Version {
		return fault(422, "Canonical lossless version required")
	}
	// The partition is two-sided (review 2026-10-06, F70): an unversioned kind
	// carries exactly "0", the version every exporter writes for it. Accepting
	// any version there let two records with one annotation ID and versions
	// "0" and "1" pass as distinct keys, remap to one local UUID and fail
	// Preview with a raw SQLite PRIMARY KEY error (500) instead of a 422.
	unversioned := slices.Contains([]string{"project", "repository", "source_snapshot", "node", "edge", "evidence", "annotation", "diagram", "diagram_element", "diagram_link", "saved_view", "diagram_view", "artifact_owner"}, i.Kind)
	if n == 0 && !unversioned {
		return fault(422, "Exact version required")
	}
	if n != 0 && unversioned {
		return fault(422, "Unversioned record kind requires version 0")
	}
	return nil
}
func (r Record) Validate() error {
	if r.Kind != r.Identity.Kind {
		return fault(422, "Record identity kind mismatch")
	}
	if err := r.Identity.Validate(); err != nil {
		return err
	}
	if !validHash(r.ContentHash) {
		return fault(422, "Invalid record hash")
	}
	raw := bytes.TrimSpace(r.Document)
	if len(raw) == 0 || raw[0] != '{' {
		return fault(422, "Record document must be an object")
	}
	if _, err := r.key(); err != nil {
		return err
	}
	h, err := DocumentHash(r.Document)
	if err != nil {
		return err
	}
	if h != r.ContentHash {
		return fault(422, "Record content hash mismatch")
	}
	return nil
}

// Member UUIDs are scoped to a diagram identity, carried in document.parent.
type recordKey struct {
	Identity Identity
	Parent   Identity
}

func (r Record) key() (recordKey, error) {
	k := recordKey{Identity: r.Identity}
	sourceMember := slices.Contains([]string{"node", "edge", "evidence"}, r.Kind)
	if r.Kind != "diagram_element" && r.Kind != "diagram_link" && !sourceMember {
		return k, nil
	}
	var d struct {
		Parent *Identity `json:"parent"`
	}
	if err := json.Unmarshal(r.Document, &d); err != nil {
		return k, err
	}
	if d.Parent == nil {
		return k, fault(422, "Member parent diagram identity required")
	}
	p := *d.Parent
	if err := p.Validate(); err != nil {
		return k, err
	}
	expectedKind, expectedVersion := "diagram", "0"
	if sourceMember {
		expectedKind, expectedVersion = "revision", "1"
	}
	if p.Kind != expectedKind || p.Version != expectedVersion || p.InstallationID != r.Identity.InstallationID || p.ProjectID != r.Identity.ProjectID {
		return k, fault(422, "Member parent namespace mismatch")
	}
	k.Parent = p
	return k, nil
}

// DocumentHash sorts object keys recursively and preserves exact numeric tokens.
// It hashes exported record documents; it does not confer domain validity.
func DocumentHash(v any) (string, error) {
	b, err := canonical(v)
	if err != nil {
		return "", err
	}
	return bytesHash(b), nil
}
func canonical(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return canonicalRaw(b)
}
func canonicalRaw(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fault(422, "Empty JSON")
	}
	switch raw[0] {
	case '{':
		var m map[string]jsontext.Value
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		for k, v := range m {
			b, err := canonicalRaw(v)
			if err != nil {
				return nil, err
			}
			m[k] = b
		}
		return json.Marshal(m, json.Deterministic(true))
	case '[':
		var vs []jsontext.Value
		if err := json.Unmarshal(raw, &vs); err != nil {
			return nil, err
		}
		for i, v := range vs {
			b, err := canonicalRaw(v)
			if err != nil {
				return nil, err
			}
			vs[i] = b
		}
		return json.Marshal(vs)
	default:
		var v jsontext.Value
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}
}
func ValidatePath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00:") {
		return fault(422, "Unsafe relative path")
	}
	for segment := range strings.SplitSeq(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fault(422, "Unsafe relative path segment")
		}
	}
	return nil
}
func EncodeChunk(index int, records []Record) ([]byte, ChunkDescriptor, error) {
	if index < 0 || len(records) < 1 || len(records) > MaxChunkRecords {
		return nil, ChunkDescriptor{}, fault(413, "Chunk record quota")
	}
	for _, r := range records {
		if err := r.Validate(); err != nil {
			return nil, ChunkDescriptor{}, err
		}
	}
	b, err := canonical(records)
	if err != nil {
		return nil, ChunkDescriptor{}, err
	}
	if len(b) > MaxChunkBytes {
		return nil, ChunkDescriptor{}, fault(413, "Chunk byte quota")
	}
	return b, ChunkDescriptor{index, bytesHash(b), len(b), len(records)}, nil
}
func DecodeChunk(d ChunkDescriptor, b []byte) ([]Record, error) {
	if len(b) > MaxChunkBytes || d.Records > MaxChunkRecords {
		return nil, fault(413, "Chunk quota")
	}
	if d.Index < 0 || d.Records < 1 || d.Bytes != len(b) || !validHash(d.SHA256) || bytesHash(b) != d.SHA256 {
		return nil, fault(422, "Chunk hash/count/size mismatch")
	}
	var records []Record
	if err := json.Unmarshal(b, &records, json.RejectUnknownMembers(true)); err != nil {
		return nil, fault(422, "Invalid portable chunk document: "+err.Error())
	}
	if len(records) != d.Records {
		return nil, fault(422, "Chunk record count mismatch")
	}
	seen := map[recordKey]bool{}
	for _, r := range records {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		key, err := r.key()
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, fault(422, "Duplicate record")
		}
		seen[key] = true
	}
	return records, nil
}
func (m Manifest) Validate() error {
	if m.Format != Format || !bm.ValidID(m.OriginInstallationID) || !bm.ValidID(m.Selection.ProjectID) || !validHash(m.Selection.TargetHash) {
		return fault(422, "Unsupported manifest")
	}
	if err := m.Selection.Target.Validate(); err != nil {
		return err
	}
	if m.Selection.Target.ImportCandidate != nil {
		return fault(422, "Mutable candidate cannot be exported")
	}
	if len(m.Schemas) == 0 || m.Selection.DiagramViews == nil {
		return fault(422, "Schema and exact view lists required")
	}
	if err := m.validateSchemas(); err != nil {
		return err
	}
	if err := m.Selection.validatePins(); err != nil {
		return err
	}
	return m.validateChunks()
}

// validateSchemas admits each known schema once and requires a source
// schema 5 or 6 and the v3 artifact context.
func (m Manifest) validateSchemas() error {
	seen := map[string]bool{}
	for _, v := range m.Schemas {
		if seen[v] || !slices.Contains([]string{"1", "2", "3", "4", "5", "6", "proposal-graph-v1", "proposal-relational-v1", "artifact-context-v3", "backend-diagram-v1", "backend-diagram-provenance-v1", "diagram-view-v1", "saved-view-v1", "saved-view-v2"}, v) {
			return fault(422, "Duplicate or unsupported schema")
		}
		seen[v] = true
	}
	if (!seen["5"] && !seen["6"]) || !seen["artifact-context-v3"] {
		return fault(422, "Source5 and context-v3 required")
	}
	return nil
}

// validatePins requires every selected saved view and diagram view to be an
// exact, distinct pin.
func (m Selection) validatePins() error {
	saved := map[SavedViewPin]bool{}
	for _, v := range m.SavedViews {
		if !bm.ValidID(v.ID) || v.Version <= 0 || saved[v] {
			return fault(422, "Invalid or duplicate saved view pin")
		}
		saved[v] = true
	}
	views := map[SVGInput]bool{}
	for _, v := range m.DiagramViews {
		if !bm.ValidID(v.ViewID) || v.ViewVersion <= 0 || views[v] {
			return fault(422, "Duplicate or invalid exact view")
		}
		views[v] = true
	}
	return nil
}

// validateChunks bounds the chunk count, each chunk and the bundle total.
func (m Manifest) validateChunks() error {
	total := int64(0)
	if len(m.Chunks) == 0 || len(m.Chunks) > 262144 {
		return fault(413, "Chunk count quota")
	}
	for i, c := range m.Chunks {
		if c.Index != i || !validHash(c.SHA256) || c.Bytes < 1 || c.Records < 1 {
			return fault(422, "Invalid manifest chunk")
		}
		if c.Bytes > MaxChunkBytes || c.Records > MaxChunkRecords {
			return fault(413, "Chunk quota")
		}
		total += int64(c.Bytes)
	}
	if total > MaxBundleBytes {
		return fault(413, "Bundle byte quota")
	}
	return nil
}
