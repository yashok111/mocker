package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/apidesign"
)

type preparedAPIPins struct {
	preview *APIPinsPreview
	frozen  APIArtifactContext
	// Only checked changed groups participate in owner CAS. Retained broken groups
	// remain representable and cannot block an unrelated change.
	digests         map[string]string
	baselineHash    string
	fullContext     *ArtifactContext
	artifactDigests map[editorSnapshotKey]string
}

func boundedArtifactLabel(s string) string {
	if len(s) <= MaxAPIArtifactLabelBytes {
		return s
	}
	s = s[:MaxAPIArtifactLabelBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func frozenArtifactRef(c APIPinCommand, snapshot *apidesign.ArtifactSnapshot, selector APIArtifactSelector, object *apidesign.ArtifactObject) ArtifactRef {
	return ArtifactRef{Kind: "api_design", ArtifactID: c.ArtifactID, RevisionID: c.RevisionID, ContentHash: snapshot.ContentHash, Selector: selector, ObjectHash: object.ObjectHash, LastKnownLabel: boundedArtifactLabel(object.Label), ResolvedPointer: object.Pointer}
}

func (s *APIArtifactService) Preview(ctx context.Context, pid string, in PreviewAPIPinsInput) (*APIPinsPreview, error) {
	prepared, err := s.prepare(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	return prepared.preview, nil
}

func (s *APIArtifactService) prepare(ctx context.Context, pid string, in PreviewAPIPinsInput) (*preparedAPIPins, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxAPIPinBodyBytes {
		return nil, apiPinsLimit()
	}
	if !ValidID(pid) {
		return nil, notFound()
	}
	tx, err := s.repo.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
	if err != nil {
		return nil, err
	}
	state, err := loadSourceState(ctx, tx, pid, in.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	if p.Version != in.ExpectedVersion {
		return nil, importConflict("backend_version_conflict", "Project changed; read before retrying", p.Version)
	}
	if p.CurrentRevisionID != in.BaseRevisionID {
		return nil, importConflict("backend_api_pins_base_conflict", "Current source baseline changed", p.Version)
	}
	if !isArtifactSourceSchema(state.Revision.SchemaVersion) || len(state.Sources) == 0 || len(state.Revision.SourceSnapshotIDs) == 0 || len(p.Repositories) == 0 {
		return nil, &FaultError{Status: 422, Code: "backend_api_pins_unsupported", Message: "API pins require an imported source4, source5 or source6 baseline"}
	}
	if state.ArtifactContextV3 != nil {
		return nil, legacyPinsOnV3("backend_api_pins_unsupported")
	}
	frozen := state.APIArtifactContext
	if state.ArtifactContext != nil && state.ArtifactContext.DocumentVersion == EditorArtifactDocumentVersion {
		if err := tx.Rollback(); err != nil {
			return nil, err
		}
		return s.prepareV2APIPins(ctx, pid, in, state)
	}
	return s.prepareV1APIPins(ctx, tx, pid, in, state, frozen)
}

func validateAPIPinsWork(revision Revision, bindings []APIArtifactBinding, commands []APIPinCommand) error {
	commandByID := map[string]APIPinCommand{}
	for _, c := range commands {
		commandByID[c.ArtifactID] = c
	}
	oldPins := map[string]ArtifactPin{}
	finalCount := len(revision.ArtifactPins)
	bindingCount := len(bindings)
	targets := map[string]bool{}
	for _, pin := range revision.ArtifactPins {
		if pin.Kind != "api_design" {
			continue
		}
		oldPins[pin.ID] = pin
		c, changed := commandByID[pin.ID]
		if changed && c.Type == "remove_api_pin" {
			finalCount--
			continue
		}
		targets[pin.ID+"/"+pin.RevisionID] = true
	}
	for _, c := range commands {
		for _, b := range bindings {
			if b.Ref.ArtifactID == c.ArtifactID {
				bindingCount--
			}
		}
		if c.Type == "set_api_pin" {
			if _, ok := oldPins[c.ArtifactID]; !ok {
				finalCount++
			}
			bindingCount += len(c.Bindings)
			targets[c.ArtifactID+"/"+c.RevisionID] = true
		}
	}
	if finalCount > MaxAPIArtifactPins || bindingCount > MaxAPIArtifactBindings || len(targets) > MaxAPIArtifactPins {
		return apiPinsLimit()
	}
	return nil
}

// prepareV1APIPins retains the legacy continuation after the version dispatch.
func (s *APIArtifactService) prepareV1APIPins(ctx context.Context, tx *sql.Tx, pid string, in PreviewAPIPinsInput, state *RevisionState, frozen *APIArtifactContext) (*preparedAPIPins, error) {
	var err error
	if err := validateAPIPinsWork(state.Revision, artifactBindings(frozen), in.Commands); err != nil {
		return nil, err
	}
	state, err = loadRevisionState(ctx, tx, pid, in.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	content, anchor, err := artifactSourceAnchors(ctx, tx, state)
	if err != nil {
		return nil, err
	}
	baselineHash, err := apiPinsSourceBaselineDigest(ctx, tx, pid, in.BaseRevisionID, state.Revision, frozen)
	if err != nil {
		return nil, err
	}
	if frozen == nil {
		frozen = &APIArtifactContext{SourceContentHash: content, SourceSemanticHash: anchor, Bindings: []APIArtifactBinding{}}
	}
	if err := tx.Rollback(); err != nil {
		return nil, err
	}
	nodes := map[string]Node{}
	for _, n := range state.Nodes {
		nodes[n.ID] = n
	}
	if err := checkAPIPinSources(in.Commands, frozen.Bindings, nodes); err != nil {
		return nil, err
	}
	b := newAPIPinsBuilder(ctx, s, in, state, frozen, nodes, baselineHash)
	for _, id := range b.orderedIDs() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := b.pin(id); err != nil {
			return nil, err
		}
	}
	return b.finish(in, frozen)
}

// checkAPIPinSources runs the cheap source kind and cross-group uniqueness
// checks before reading any API documents. Missing nodes remain
// deterministic diagnostics.
func checkAPIPinSources(commands []APIPinCommand, retained []APIArtifactBinding, nodes map[string]Node) error {
	changed := map[string]bool{}
	for _, c := range commands {
		changed[c.ArtifactID] = true
	}
	seenSources := map[string]bool{}
	for _, b := range retained {
		if !changed[b.Ref.ArtifactID] {
			seenSources[b.SourceNodeID] = true
		}
	}
	for _, c := range commands {
		for _, b := range c.Bindings {
			if seenSources[b.SourceNodeID] {
				return invalid("sourceNodeId", "Source is already bound in another retained group")
			}
			seenSources[b.SourceNodeID] = true
			if n, ok := nodes[b.SourceNodeID]; ok && !apiPinSourceKindMatches(n, b.Selector) {
				return invalid("selector", "Source kind requires its explicit operation key or schema pointer")
			}
		}
	}
	return nil
}

// apiPinSourceKindMatches: an operation binds by operation key, a field by
// schema pointer, and no other node kind binds at all.
func apiPinSourceKindMatches(n Node, selector APIArtifactSelector) bool {
	return (n.Kind == "http_operation" || n.Kind == "api_field") && (n.Kind == "http_operation") == (selector.ObjectKey != "")
}

// apiPinsBuilder accumulates one preview: the pins and bindings it keeps,
// its diff (bounded across every group by totalChanges) and diagnostics.
type apiPinsBuilder struct {
	ctx          context.Context
	s            *APIArtifactService
	nodes        map[string]Node
	oldPins      map[string]ArtifactPin
	commands     map[string]APIPinCommand
	oldBindings  map[string][]APIArtifactBinding
	finalPins    []ArtifactPin
	preview      *APIPinsPreview
	prepared     *preparedAPIPins
	totalChanges int
}

func newAPIPinsBuilder(ctx context.Context, s *APIArtifactService, in PreviewAPIPinsInput, state *RevisionState, frozen *APIArtifactContext, nodes map[string]Node, baselineHash string) *apiPinsBuilder {
	b := &apiPinsBuilder{ctx: ctx, s: s, nodes: nodes, oldPins: map[string]ArtifactPin{}, commands: map[string]APIPinCommand{}, oldBindings: map[string][]APIArtifactBinding{}, finalPins: []ArtifactPin{}}
	for _, pin := range state.Revision.ArtifactPins {
		if pin.Kind == "api_design" {
			b.oldPins[pin.ID] = pin
		} else {
			b.finalPins = append(b.finalPins, pin)
		}
	}
	for _, c := range in.Commands {
		b.commands[c.ArtifactID] = c
	}
	b.preview = &APIPinsPreview{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Pins: []ArtifactPin{}, Bindings: []APIArtifactBinding{}, SourceSnapshotIDs: slices.Clone(state.Revision.SourceSnapshotIDs), Diagnostics: []APIArtifactDiagnostic{}, Diff: []APIArtifactDiff{}, CanApply: true}
	b.prepared = &preparedAPIPins{preview: b.preview, frozen: *frozen, digests: map[string]string{}, baselineHash: baselineHash}
	for _, binding := range frozen.Bindings {
		b.oldBindings[binding.Ref.ArtifactID] = append(b.oldBindings[binding.Ref.ArtifactID], binding)
	}
	return b
}

// orderedIDs is every artifact either pinned before or named by a command.
func (b *apiPinsBuilder) orderedIDs() []string {
	ids := map[string]bool{}
	for id := range b.oldPins {
		ids[id] = true
	}
	for id := range b.commands {
		ids[id] = true
	}
	return slices.Sorted(maps.Keys(ids))
}

func (b *apiPinsBuilder) addDiagnostic(code string, binding APIArtifactBinding, message string, block bool) {
	b.preview.Diagnostics = append(b.preview.Diagnostics, apiArtifactDiagnostic(code, binding, message))
	if block {
		b.preview.CanApply = false
	}
}

func (b *apiPinsBuilder) truncateDiff(source string, before, after *ArtifactRef) {
	b.preview.DiffTruncated = true
	b.preview.CanApply = false
	if slices.ContainsFunc(b.preview.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_diff_truncated" }) {
		return
	}
	id := ""
	if after != nil {
		id = after.ArtifactID
	} else if before != nil {
		id = before.ArtifactID
	}
	b.preview.Diagnostics = append(b.preview.Diagnostics, APIArtifactDiagnostic{Code: "backend_api_diff_truncated", SourceNodeID: source, ArtifactID: id, Message: "Selected object diff exceeds bounded entries or pointer bytes; narrow the request"})
}

func (b *apiPinsBuilder) addDiff(before, after *ArtifactRef, source, status string, left, right *apidesign.ArtifactObject) error {
	if len(b.preview.Diff) >= MaxAPIArtifactDiffEntries {
		b.truncateDiff(source, before, after)
		return nil
	}
	item := APIArtifactDiff{SourceNodeID: source, Status: status, Before: before, After: after, Changes: []APIArtifactObjectChange{}}
	if before != nil && after != nil {
		item.ContextChanged = before.ContentHash != after.ContentHash
	}
	if left != nil && right != nil {
		changes, truncated, err := apiArtifactObjectDiff(b.ctx, left.Document, right.Document, MaxAPIArtifactDiffEntries-b.totalChanges)
		if err != nil {
			return err
		}
		item.Changes = changes
		b.totalChanges += len(changes)
		b.preview.DiffTruncated = b.preview.DiffTruncated || truncated
		if truncated {
			b.truncateDiff(source, before, after)
		}
	}
	b.preview.Diff = append(b.preview.Diff, item)
	return nil
}

func (b *apiPinsBuilder) pin(id string) error {
	c, changed := b.commands[id]
	oldPin, hadOld := b.oldPins[id]
	previous := b.oldBindings[id]
	sortArtifactBindings(previous)
	if changed && c.Type == "remove_api_pin" {
		return b.removePin(id, oldPin, hadOld, previous)
	}
	var old *apidesign.ArtifactSnapshot
	var oldErr error
	if hadOld {
		old, oldErr = b.s.snapshot(b.ctx, id, oldPin.RevisionID)
		if err := fatalArtifactReadError(b.ctx, oldErr); err != nil {
			return err
		}
		if oldErr == nil && old.ContentHash != oldPin.ContentHash {
			oldErr = invalid("contentHash", "Previous immutable digest differs")
		}
	}
	if !changed {
		return b.retainPin(oldPin, previous, old, oldErr)
	}
	return b.changePin(id, c, oldPin, hadOld, previous, old, oldErr)
}

func (b *apiPinsBuilder) removePin(id string, oldPin ArtifactPin, hadOld bool, previous []APIArtifactBinding) error {
	// Removing an artifact that has no pin (a typo or a stale id)
	// changed nothing, yet previewed as applicable and Apply wrote a
	// new revision and bumped the project version (review 2026-10-06,
	// F95).
	if !hadOld {
		b.preview.Diagnostics = append(b.preview.Diagnostics, APIArtifactDiagnostic{Code: "backend_api_pin_absent", ArtifactID: id, Message: "Artifact is not pinned in this revision; nothing to remove"})
		b.preview.CanApply = false
		return nil
	}
	// A detach never needs to decode an unavailable or oversized previous body.
	unavailable := true
	if b.s.artifacts != nil {
		var err error
		unavailable, err = b.previousDigestUnavailable(id, oldPin)
		if err != nil {
			return err
		}
	}
	if unavailable {
		for _, binding := range previous {
			b.addDiagnostic("backend_api_unavailable_previous", binding, "Previous artifact is unavailable; explicit removal remains allowed", false)
		}
	}
	for _, binding := range previous {
		if err := b.addDiff(new(binding.Ref), nil, binding.SourceNodeID, "removed", nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// previousDigestUnavailable checks the previous pin by digest only, in its
// own read transaction.
func (b *apiPinsBuilder) previousDigestUnavailable(id string, oldPin ArtifactPin) (bool, error) {
	tx, err := b.s.repo.db.R.BeginTx(b.ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	digest, e := b.s.artifacts.ArtifactDigestTx(b.ctx, tx, apiArtifactID(id), apiArtifactID(oldPin.RevisionID))
	_ = tx.Rollback()
	if err := fatalArtifactReadError(b.ctx, e); err != nil {
		return false, err
	}
	return !artifactDigestMatches(digest, e, oldPin.ContentHash), nil
}

// artifactDigestMatches folds a non-fatal read failure into "does not
// match": an unreadable previous artifact is a diagnostic, not an error.
func artifactDigestMatches(digest string, readErr error, want string) bool {
	return readErr == nil && digest == want
}

// retainPin keeps an untouched group as is, reporting orphaned sources and
// retained objects that no longer resolve without blocking the apply.
func (b *apiPinsBuilder) retainPin(oldPin ArtifactPin, previous []APIArtifactBinding, old *apidesign.ArtifactSnapshot, oldErr error) error {
	b.finalPins = append(b.finalPins, oldPin)
	b.preview.Bindings = append(b.preview.Bindings, previous...)
	for _, binding := range previous {
		if _, ok := b.nodes[binding.SourceNodeID]; !ok {
			b.addDiagnostic("backend_api_source_orphaned", binding, "Source node is absent from this revision", false)
		}
		broken := oldErr != nil
		if !broken {
			o, e := resolveAPIArtifact(b.ctx, old, binding.Ref.Selector)
			if err := fatalArtifactError(b.ctx, e); err != nil {
				return err
			}
			broken = e != nil || o.ObjectHash != binding.Ref.ObjectHash || o.Pointer != binding.Ref.ResolvedPointer
		}
		if broken {
			b.addDiagnostic("backend_api_retained_unavailable", binding, "Retained exact artifact or object is unavailable", false)
		}
	}
	return nil
}

func (b *apiPinsBuilder) changePin(id string, c APIPinCommand, oldPin ArtifactPin, hadOld bool, previous []APIArtifactBinding, old *apidesign.ArtifactSnapshot, oldErr error) error {
	if hadOld && oldErr != nil {
		for _, binding := range previous {
			b.addDiagnostic("backend_api_unavailable_previous", binding, "Previous artifact is unavailable; checked replacement requires explicit removal first", true)
		}
	}
	newSnapshot, newErr := old, oldErr
	if !hadOld || c.RevisionID != oldPin.RevisionID {
		newSnapshot, newErr = b.s.snapshot(b.ctx, id, c.RevisionID)
	}
	if err := fatalArtifactReadError(b.ctx, newErr); err != nil {
		return err
	}
	if newErr != nil {
		b.addDiagnostic("backend_api_target_unavailable", APIArtifactBinding{Ref: ArtifactRef{ArtifactID: id}}, "Selected immutable artifact is unavailable", true)
		if hadOld {
			b.finalPins = append(b.finalPins, oldPin)
			b.preview.Bindings = append(b.preview.Bindings, previous...)
		}
		return nil
	}
	b.prepared.digests[id+"/"+c.RevisionID] = newSnapshot.ContentHash
	if hadOld && oldErr == nil {
		b.prepared.digests[id+"/"+oldPin.RevisionID] = oldPin.ContentHash
	}
	next, err := b.nextBindings(id, c, newSnapshot)
	if err != nil {
		return err
	}
	for _, binding := range previous {
		if err := b.comparePrevious(binding, old, oldErr, newSnapshot, next); err != nil {
			return err
		}
	}
	for _, binding := range next {
		b.preview.Bindings = append(b.preview.Bindings, binding)
		if err := b.addDiff(nil, new(binding.Ref), binding.SourceNodeID, "added", nil, nil); err != nil {
			return err
		}
	}
	if len(next) > 0 || slices.ContainsFunc(b.preview.Bindings, func(binding APIArtifactBinding) bool { return binding.Ref.ArtifactID == id }) {
		b.finalPins = append(b.finalPins, ArtifactPin{Kind: "api_design", ID: id, RevisionID: c.RevisionID, ContentHash: newSnapshot.ContentHash})
	}
	return nil
}

// nextBindings resolves a command's requested bindings at the selected
// revision, keyed by source node.
func (b *apiPinsBuilder) nextBindings(id string, c APIPinCommand, newSnapshot *apidesign.ArtifactSnapshot) (map[string]APIArtifactBinding, error) {
	next := map[string]APIArtifactBinding{}
	for _, input := range c.Bindings {
		n, exists := b.nodes[input.SourceNodeID]
		if !exists {
			b.addDiagnostic("backend_api_source_missing", APIArtifactBinding{SourceNodeID: input.SourceNodeID, Ref: ArtifactRef{ArtifactID: id}}, "Selected source node is absent", true)
			continue
		}
		if !apiPinSourceKindMatches(n, input.Selector) {
			return nil, invalid("selector", "Source kind requires its explicit operation key or schema pointer")
		}
		object, e := resolveAPIArtifact(b.ctx, newSnapshot, input.Selector)
		if err := fatalArtifactError(b.ctx, e); err != nil {
			return nil, err
		}
		if e != nil {
			b.addDiagnostic("backend_api_object_missing", APIArtifactBinding{SourceNodeID: n.ID, Ref: ArtifactRef{ArtifactID: id}}, "Selected object is unavailable or unsupported", true)
			continue
		}
		next[n.ID] = APIArtifactBinding{SourceNodeID: n.ID, SourceKind: n.Kind, SourceLastKnownLabel: boundedArtifactLabel(n.Name), Ref: frozenArtifactRef(c, newSnapshot, input.Selector, object), Origin: "manual", Reason: c.Reason}
	}
	return next, nil
}

// comparePrevious diffs one previous binding against its replacement in
// next (consuming it) or records its removal.
func (b *apiPinsBuilder) comparePrevious(binding APIArtifactBinding, old *apidesign.ArtifactSnapshot, oldErr error, newSnapshot *apidesign.ArtifactSnapshot, next map[string]APIArtifactBinding) error {
	var left *apidesign.ArtifactObject
	if oldErr == nil {
		var err error
		left, err = b.previousObject(old, binding)
		if err != nil {
			return err
		}
	}
	if err := b.inspectPrevious(newSnapshot, binding); err != nil {
		return err
	}
	after, exists := next[binding.SourceNodeID]
	if !exists {
		return b.addDiff(new(binding.Ref), nil, binding.SourceNodeID, "removed", nil, nil)
	}
	right, e := resolveAPIArtifact(b.ctx, newSnapshot, after.Ref.Selector)
	if e != nil {
		return e
	}
	status := "unchanged"
	if binding.Ref.ObjectHash != after.Ref.ObjectHash || binding.Ref.Selector != after.Ref.Selector {
		status = "changed"
	} else if binding.Ref.ResolvedPointer != after.Ref.ResolvedPointer {
		status = "moved"
	}
	if err := b.addDiff(new(binding.Ref), new(after.Ref), binding.SourceNodeID, status, left, right); err != nil {
		return err
	}
	delete(next, binding.SourceNodeID)
	b.preview.Bindings = append(b.preview.Bindings, after)
	return nil
}

// previousObject resolves a previous binding at its old revision; an object
// that no longer matches its frozen reference blocks the change and diffs
// as absent (nil).
func (b *apiPinsBuilder) previousObject(old *apidesign.ArtifactSnapshot, binding APIArtifactBinding) (*apidesign.ArtifactObject, error) {
	left, resolveErr := resolveAPIArtifact(b.ctx, old, binding.Ref.Selector)
	if err := fatalArtifactError(b.ctx, resolveErr); err != nil {
		return nil, err
	}
	if !frozenObjectMatches(left, resolveErr, binding.Ref) {
		b.addDiagnostic("backend_api_unavailable_previous", binding, "Previous selected object differs from its frozen reference", true)
		return nil, nil
	}
	return left, nil
}

// frozenObjectMatches folds a non-fatal resolve failure into "does not
// match": a previous object that no longer resolves is a diagnostic.
func frozenObjectMatches(object *apidesign.ArtifactObject, resolveErr error, ref ArtifactRef) bool {
	return resolveErr == nil && object.ObjectHash == ref.ObjectHash && object.Pointer == ref.ResolvedPointer
}

// inspectPrevious reports a previous selector that is absent or moved at
// the proposed revision.
func (b *apiPinsBuilder) inspectPrevious(newSnapshot *apidesign.ArtifactSnapshot, binding APIArtifactBinding) error {
	inspected, err := resolveAPIArtifact(b.ctx, newSnapshot, binding.Ref.Selector)
	if err := fatalArtifactError(b.ctx, err); err != nil {
		return err
	}
	if err != nil {
		if e := b.addDiff(new(binding.Ref), nil, binding.SourceNodeID, "missing", nil, nil); e != nil {
			return e
		}
		b.addDiagnostic("backend_api_previous_object_missing", binding, "Previous selector is absent at the proposed revision", false)
	} else if inspected.Pointer != binding.Ref.ResolvedPointer {
		b.addDiagnostic("backend_api_previous_object_moved", binding, "Previous object moved at the proposed revision", false)
	}
	return nil
}

func (b *apiPinsBuilder) finish(in PreviewAPIPinsInput, frozen *APIArtifactContext) (*preparedAPIPins, error) {
	preview := b.preview
	sortArtifactBindings(preview.Bindings)
	slices.SortFunc(b.finalPins, func(x, y ArtifactPin) int {
		if n := strings.Compare(x.Kind, y.Kind); n != 0 {
			return n
		}
		return strings.Compare(x.ID, y.ID)
	})
	preview.Pins = b.finalPins
	if err := ValidateAPIArtifactVector(preview.Pins, preview.Bindings); err != nil {
		return nil, err
	}
	// Equal source IDs deliberately carry multiple entries (prior selector check,
	// transfer/remap result). Keep their deterministic group/check order even when
	// other added bindings were collected from maps.
	slices.SortStableFunc(preview.Diff, func(x, y APIArtifactDiff) int { return strings.Compare(x.SourceNodeID, y.SourceNodeID) })
	slices.SortFunc(preview.Diagnostics, func(x, y APIArtifactDiagnostic) int {
		if n := strings.Compare(x.SourceNodeID, y.SourceNodeID); n != 0 {
			return n
		}
		if n := strings.Compare(x.ArtifactID, y.ArtifactID); n != 0 {
			return n
		}
		return strings.Compare(x.Code, y.Code)
	})
	var err error
	preview.SemanticHash, err = APIArtifactSemanticHash(frozen.SourceContentHash, frozen.SourceSemanticHash, preview.Pins, preview.Bindings)
	if err != nil {
		return nil, err
	}
	preview.CandidateHash, err = APIArtifactCandidateHash(in, *preview)
	if err != nil {
		return nil, err
	}
	b.prepared.frozen.Bindings = preview.Bindings
	return b.prepared, b.ctx.Err()
}
