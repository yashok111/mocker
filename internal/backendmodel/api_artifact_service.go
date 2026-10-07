package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/specs"
)

type APIArtifactReader interface {
	ArtifactSnapshot(context.Context, int64, int64) (*apidesign.ArtifactSnapshot, error)
	ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error)
	ArtifactHead(context.Context, int64) (int64, error)
}

type APIArtifactService struct {
	repo      *Repo
	artifacts APIArtifactReader
}

func NewAPIArtifactService(repo *Repo, artifacts APIArtifactReader) *APIArtifactService {
	return &APIArtifactService{repo: repo, artifacts: artifacts}
}

func apiPinsLimit() error {
	return &FaultError{Status: 413, Code: "backend_api_pins_limit", Message: "API artifact work limit exceeded; narrow the request"}
}
func apiPinsBlocked(d []APIArtifactDiagnostic) error {
	return &FaultError{Status: 422, Code: "backend_api_pins_blocked", Message: "API pin preview contains blocked diagnostics", Details: map[string]any{"diagnostics": d}}
}

func loadAPIArtifactContext(ctx context.Context, q importReader, rid string) (*APIArtifactContext, error) {
	return loadLegacyArtifactContext(ctx, q, rid)
}

func loadAPIArtifactCoverage(ctx context.Context, q importReader, state *RevisionState) (*RevisionCoverage, error) {
	var doc string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources_documents WHERE revision_id=?`, state.Revision.ID).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return &RevisionCoverage{Coverage: state.Revision.Coverage, Snapshots: []SourceSnapshot{}, Inventory: []InventoryItem{}, ReconciliationGaps: []string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var out RevisionCoverage
	if err = json.Unmarshal([]byte(doc), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func apiArtifactID(id string) int64 { n, _ := strconv.ParseInt(id, 10, 64); return n }
func apiArtifactDiagnostic(code string, b APIArtifactBinding, message string) APIArtifactDiagnostic {
	return APIArtifactDiagnostic{Code: code, SourceNodeID: b.SourceNodeID, ArtifactID: b.Ref.ArtifactID, Pointer: b.Ref.ResolvedPointer, Message: message}
}
func artifactBindings(context *APIArtifactContext) []APIArtifactBinding {
	if context == nil {
		return []APIArtifactBinding{}
	}
	return slices.Clone(context.Bindings)
}
func sortArtifactBindings(bindings []APIArtifactBinding) {
	slices.SortFunc(bindings, func(a, b APIArtifactBinding) int { return strings.Compare(a.SourceNodeID, b.SourceNodeID) })
}

func (s *APIArtifactService) snapshot(ctx context.Context, id, rid string) (*apidesign.ArtifactSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.artifacts == nil {
		return nil, apidesign.ErrNotFound
	}
	out, err := s.artifacts.ArtifactSnapshot(ctx, apiArtifactID(id), apiArtifactID(rid))
	if errors.Is(err, specs.ErrTooLarge) {
		return nil, apiPinsLimit()
	}
	if err == nil && (out == nil || out.DesignID != apiArtifactID(id) || out.RevisionID != apiArtifactID(rid) || !validHash(out.ContentHash) || hashBytes([]byte(out.Document)) != out.ContentHash) {
		return nil, invalid("snapshot", "Invalid exact artifact snapshot")
	}
	return out, err
}
func fatalArtifactError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if f, ok := errors.AsType[*FaultError](err); ok && f.Status == 413 {
		return err
	}
	return nil
}

// fatalArtifactReadError classifies an owner READ (snapshot, head, digest).
// Only an absent owner revision and owner validation faults are business
// states ("broken", "unavailable"); any other error is a storage fault and is
// returned so it surfaces as a 5xx. A busy or I/O error used to become a 200
// "broken" item or a blocking preview diagnostic, inviting the agent to remove
// a healthy pin (review 2026-10-06, F94). In-memory selector resolution keeps
// fatalArtifactError: its errors are all document facts.
func fatalArtifactReadError(ctx context.Context, err error) error {
	if fatal := fatalArtifactError(ctx, err); fatal != nil || err == nil || ownerDigestGone(err) {
		return fatal
	}
	if _, ok := errors.AsType[*FaultError](err); ok {
		return nil
	}
	if _, ok := errors.AsType[*apidesign.InvalidError](err); ok {
		return nil
	}
	return err
}
func resolveAPIArtifact(ctx context.Context, snapshot *apidesign.ArtifactSnapshot, selector APIArtifactSelector) (*apidesign.ArtifactObject, error) {
	return apidesign.ResolveArtifactObject(ctx, snapshot, apidesign.ArtifactSelector{ObjectKey: selector.ObjectKey, JSONPointer: selector.JSONPointer})
}

func (s *APIArtifactService) Query(ctx context.Context, pid string, in APIArtifactQueryInput) (*APIArtifactPage, error) {
	if in.Proposal != nil || in.ChangeProposal != nil || in.ImportCandidate != nil {
		return s.queryEffectiveAPIArtifacts(ctx, pid, in)
	}
	if in.RevisionID != "" && in.Proposal == nil && in.ChangeProposal == nil {
		revision, err := s.repo.Revision(ctx, pid, in.RevisionID)
		if err != nil {
			return nil, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return s.queryEffectiveAPIArtifacts(ctx, pid, in)
		}
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, s.repo.db.R, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	frozen, err := loadAPIArtifactContext(ctx, s.repo.db.R, in.RevisionID)
	if err != nil {
		return nil, err
	}
	return s.projectAPIArtifactBindings(ctx, pid, in, state, frozen, nil)
}
func (s *APIArtifactService) projectAPIArtifactBindings(ctx context.Context, pid string, in APIArtifactQueryInput, state *RevisionState, frozen *APIArtifactContext, effective *EffectiveGraphSnapshot) (*APIArtifactPage, error) {
	bindings := artifactBindings(frozen)
	bindings = slices.DeleteFunc(bindings, func(b APIArtifactBinding) bool { return in.SourceNodeID != "" && b.SourceNodeID != in.SourceNodeID })
	sortArtifactBindings(bindings)
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	scope, err := apiArtifactPageScope(in, limit, state, effective)
	if err != nil {
		return nil, err
	}
	_, after, err := decodeGraphPage(limit, in.Cursor, "api-artifacts", pid, scope, true)
	if err != nil {
		return nil, err
	}
	bindings = slices.DeleteFunc(bindings, func(b APIArtifactBinding) bool { return b.SourceNodeID <= after })
	out := &APIArtifactPage{RevisionID: in.RevisionID, SemanticHash: state.Revision.SemanticHash, SourceSnapshotIDs: state.Revision.SourceSnapshotIDs, Pins: state.Revision.ArtifactPins, Items: []APIArtifactItem{}}
	if effective != nil {
		out.Target = new(effective.Target)
		out.EffectivePins = new(effective.Pins)
		out.SemanticHash = effective.Pins.EffectiveSemanticHash
		out.Pins = effective.Pins.ArtifactPins
		out.SourceSnapshotIDs = effective.Pins.SourceSnapshotIDs
	}
	if len(bindings) > limit {
		out.NextCursor = encodeGraphPage("api-artifacts", pid, scope, bindings[limit-1].SourceNodeID)
		bindings = bindings[:limit]
	}
	nodes := map[string]bool{}
	for _, n := range state.Nodes {
		nodes[n.ID] = true
	}
	groups := map[string][]int{}
	for _, b := range bindings {
		i := len(out.Items)
		out.Items = append(out.Items, APIArtifactItem{Binding: b, Resolution: APIArtifactResolution{Status: "resolved", Diagnostics: []APIArtifactDiagnostic{}}})
		groups[b.Ref.ArtifactID+"/"+b.Ref.RevisionID] = append(groups[b.Ref.ArtifactID+"/"+b.Ref.RevisionID], i)
	}
	if len(groups) > MaxAPIArtifactPins {
		return nil, apiPinsLimit()
	}
	heads := map[string]int64{}
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		if err := s.resolveAPIArtifactGroup(ctx, out, groups[key], nodes, heads, effective); err != nil {
			return nil, err
		}
	}
	return out, ctx.Err()
}

// apiArtifactPageScope is the digest a page cursor binds to; an effective
// read also binds the effective pins.
func apiArtifactPageScope(in APIArtifactQueryInput, limit int, state *RevisionState, effective *EffectiveGraphSnapshot) (string, error) {
	scope, err := requestDigest(struct {
		Revision, Hash, Filter string
		Limit                  int
	}{in.RevisionID, state.Revision.SemanticHash, in.SourceNodeID, limit})
	if err != nil {
		return "", err
	}
	if effective != nil {
		return requestDigest(struct {
			Scope string
			Pins  EffectiveGraphPins
		}{scope, effective.Pins})
	}
	return scope, nil
}

// resolveAPIArtifactGroup resolves every item pinned to one artifact
// revision against one snapshot read. heads memoises each artifact's
// current draft (0 when unknown) across groups.
func (s *APIArtifactService) resolveAPIArtifactGroup(ctx context.Context, out *APIArtifactPage, indices []int, nodes map[string]bool, heads map[string]int64, effective *EffectiveGraphSnapshot) error {
	b := out.Items[indices[0]].Binding
	snap, readErr := s.snapshot(ctx, b.Ref.ArtifactID, b.Ref.RevisionID)
	if err := fatalArtifactReadError(ctx, readErr); err != nil {
		return err
	}
	if _, seen := heads[b.Ref.ArtifactID]; !seen {
		head, err := s.artifactDraftHead(ctx, b.Ref.ArtifactID, effective)
		if err != nil {
			return err
		}
		heads[b.Ref.ArtifactID] = head
	}
	for _, i := range indices {
		if err := resolveAPIArtifactItem(ctx, &out.Items[i], snap, readErr, nodes, heads); err != nil {
			return err
		}
	}
	return nil
}

// artifactDraftHead reads an artifact's current draft revision; an effective
// read or an unavailable owner answers 0 (unknown).
func (s *APIArtifactService) artifactDraftHead(ctx context.Context, artifactID string, effective *EffectiveGraphSnapshot) (int64, error) {
	if effective != nil || s.artifacts == nil {
		return 0, nil
	}
	head, e := s.artifacts.ArtifactHead(ctx, apiArtifactID(artifactID))
	if err := fatalArtifactReadError(ctx, e); err != nil {
		return 0, err
	}
	return knownArtifactHead(head, e), nil
}

// knownArtifactHead folds a non-fatal head read failure into 0 (unknown):
// the page still lists the binding, without an update hint.
func knownArtifactHead(head int64, readErr error) int64 {
	if readErr != nil {
		return 0
	}
	return head
}

func resolveAPIArtifactItem(ctx context.Context, item *APIArtifactItem, snap *apidesign.ArtifactSnapshot, readErr error, nodes map[string]bool, heads map[string]int64) error {
	ref := item.Binding.Ref
	if head := heads[ref.ArtifactID]; head > 0 {
		item.Resolution.CurrentDraftRevisionID = strconv.FormatInt(head, 10)
		item.Resolution.UpdateAvailable = item.Resolution.CurrentDraftRevisionID != ref.RevisionID
	}
	if !nodes[item.Binding.SourceNodeID] {
		item.Resolution.Status = "orphaned"
		item.Resolution.Diagnostics = append(item.Resolution.Diagnostics, apiArtifactDiagnostic("backend_api_source_orphaned", item.Binding, "Source node is absent from this revision"))
		return nil
	}
	broken := readErr != nil || snap.ContentHash != ref.ContentHash
	if !broken {
		object, e := resolveAPIArtifact(ctx, snap, ref.Selector)
		if err := fatalArtifactError(ctx, e); err != nil {
			return err
		}
		broken = e != nil || object.ObjectHash != ref.ObjectHash || object.Pointer != ref.ResolvedPointer
	}
	if broken {
		item.Resolution.Status = "broken"
		item.Resolution.Diagnostics = append(item.Resolution.Diagnostics, apiArtifactDiagnostic("backend_api_artifact_unavailable", item.Binding, "Exact artifact or selected object is unavailable"))
	}
	return nil
}
