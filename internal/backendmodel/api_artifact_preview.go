package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
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
	defer tx.Rollback()
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
	// Source kind and cross-group uniqueness are cheap checks performed before
	// reading any API documents. Missing nodes remain deterministic diagnostics.
	changed := map[string]bool{}
	for _, c := range in.Commands {
		changed[c.ArtifactID] = true
	}
	seenSources := map[string]bool{}
	for _, b := range frozen.Bindings {
		if !changed[b.Ref.ArtifactID] {
			seenSources[b.SourceNodeID] = true
		}
	}
	for _, c := range in.Commands {
		for _, b := range c.Bindings {
			if seenSources[b.SourceNodeID] {
				return nil, invalid("sourceNodeId", "Source is already bound in another retained group")
			}
			seenSources[b.SourceNodeID] = true
			if n, ok := nodes[b.SourceNodeID]; ok && (n.Kind != "http_operation" && n.Kind != "api_field" || (n.Kind == "http_operation") != (b.Selector.ObjectKey != "")) {
				return nil, invalid("selector", "Source kind requires its explicit operation key or schema pointer")
			}
		}
	}
	oldPins := map[string]ArtifactPin{}
	finalPins := []ArtifactPin{}
	for _, pin := range state.Revision.ArtifactPins {
		if pin.Kind == "api_design" {
			oldPins[pin.ID] = pin
		} else {
			finalPins = append(finalPins, pin)
		}
	}
	commands := map[string]APIPinCommand{}
	for _, c := range in.Commands {
		commands[c.ArtifactID] = c
	}
	preview := &APIPinsPreview{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Pins: []ArtifactPin{}, Bindings: []APIArtifactBinding{}, SourceSnapshotIDs: slices.Clone(state.Revision.SourceSnapshotIDs), Diagnostics: []APIArtifactDiagnostic{}, Diff: []APIArtifactDiff{}, CanApply: true}
	prepared := &preparedAPIPins{preview: preview, frozen: *frozen, digests: map[string]string{}, baselineHash: baselineHash}
	oldBindings := map[string][]APIArtifactBinding{}
	for _, b := range frozen.Bindings {
		oldBindings[b.Ref.ArtifactID] = append(oldBindings[b.Ref.ArtifactID], b)
	}
	ids := map[string]bool{}
	for id := range oldPins {
		ids[id] = true
	}
	for id := range commands {
		ids[id] = true
	}
	ordered := []string{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	totalChanges := 0
	addDiagnostic := func(code string, b APIArtifactBinding, message string, block bool) {
		preview.Diagnostics = append(preview.Diagnostics, apiArtifactDiagnostic(code, b, message))
		if block {
			preview.CanApply = false
		}
	}
	truncateDiff := func(source string, before, after *ArtifactRef) {
		preview.DiffTruncated = true
		preview.CanApply = false
		if slices.ContainsFunc(preview.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_diff_truncated" }) {
			return
		}
		id := ""
		if after != nil {
			id = after.ArtifactID
		} else if before != nil {
			id = before.ArtifactID
		}
		preview.Diagnostics = append(preview.Diagnostics, APIArtifactDiagnostic{Code: "backend_api_diff_truncated", SourceNodeID: source, ArtifactID: id, Message: "Selected object diff exceeds bounded entries or pointer bytes; narrow the request"})
	}
	addDiff := func(before, after *ArtifactRef, source, status string, left, right *apidesign.ArtifactObject) error {
		if len(preview.Diff) >= MaxAPIArtifactDiffEntries {
			truncateDiff(source, before, after)
			return nil
		}
		item := APIArtifactDiff{SourceNodeID: source, Status: status, Before: before, After: after, Changes: []APIArtifactObjectChange{}}
		if before != nil && after != nil {
			item.ContextChanged = before.ContentHash != after.ContentHash
		}
		if left != nil && right != nil {
			changes, truncated, err := apiArtifactObjectDiff(ctx, left.Document, right.Document, MaxAPIArtifactDiffEntries-totalChanges)
			if err != nil {
				return err
			}
			item.Changes = changes
			totalChanges += len(changes)
			preview.DiffTruncated = preview.DiffTruncated || truncated
			if truncated {
				truncateDiff(source, before, after)
			}
		}
		preview.Diff = append(preview.Diff, item)
		return nil
	}
	for _, id := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, changed := commands[id]
		oldPin, hadOld := oldPins[id]
		previous := oldBindings[id]
		sortArtifactBindings(previous)
		if changed && c.Type == "remove_api_pin" {
			// Removing an artifact that has no pin (a typo or a stale id)
			// changed nothing, yet previewed as applicable and Apply wrote a
			// new revision and bumped the project version (review 2026-10-06,
			// F95).
			if !hadOld {
				preview.Diagnostics = append(preview.Diagnostics, APIArtifactDiagnostic{Code: "backend_api_pin_absent", ArtifactID: id, Message: "Artifact is not pinned in this revision; nothing to remove"})
				preview.CanApply = false
				continue
			}
			// A detach never needs to decode an unavailable or oversized previous body.
			if hadOld && s.artifacts != nil {
				tx, err := s.repo.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					return nil, err
				}
				digest, e := s.artifacts.ArtifactDigestTx(ctx, tx, apiArtifactID(id), apiArtifactID(oldPin.RevisionID))
				tx.Rollback()
				if err := fatalArtifactReadError(ctx, e); err != nil {
					return nil, err
				}
				if e != nil || digest != oldPin.ContentHash {
					for _, b := range previous {
						addDiagnostic("backend_api_unavailable_previous", b, "Previous artifact is unavailable; explicit removal remains allowed", false)
					}
				}
			} else if hadOld {
				for _, b := range previous {
					addDiagnostic("backend_api_unavailable_previous", b, "Previous artifact is unavailable; explicit removal remains allowed", false)
				}
			}
			for _, b := range previous {
				if err := addDiff(new(b.Ref), nil, b.SourceNodeID, "removed", nil, nil); err != nil {
					return nil, err
				}
			}
			continue
		}
		var old, newSnapshot *apidesign.ArtifactSnapshot
		var oldErr, newErr error
		if hadOld {
			old, oldErr = s.snapshot(ctx, id, oldPin.RevisionID)
			if err := fatalArtifactReadError(ctx, oldErr); err != nil {
				return nil, err
			}
			if oldErr == nil && old.ContentHash != oldPin.ContentHash {
				oldErr = invalid("contentHash", "Previous immutable digest differs")
			}
		}
		if !changed {
			finalPins = append(finalPins, oldPin)
			preview.Bindings = append(preview.Bindings, previous...)
			for _, b := range previous {
				if _, ok := nodes[b.SourceNodeID]; !ok {
					addDiagnostic("backend_api_source_orphaned", b, "Source node is absent from this revision", false)
				}
				broken := oldErr != nil
				if !broken {
					o, e := resolveAPIArtifact(ctx, old, b.Ref.Selector)
					if err := fatalArtifactError(ctx, e); err != nil {
						return nil, err
					}
					broken = e != nil || o.ObjectHash != b.Ref.ObjectHash || o.Pointer != b.Ref.ResolvedPointer
				}
				if broken {
					addDiagnostic("backend_api_retained_unavailable", b, "Retained exact artifact or object is unavailable", false)
				}
			}
			continue
		}
		if hadOld && oldErr != nil {
			for _, b := range previous {
				addDiagnostic("backend_api_unavailable_previous", b, "Previous artifact is unavailable; checked replacement requires explicit removal first", true)
			}
		}
		if hadOld && c.RevisionID == oldPin.RevisionID {
			newSnapshot, newErr = old, oldErr
		} else {
			newSnapshot, newErr = s.snapshot(ctx, id, c.RevisionID)
		}
		if err := fatalArtifactReadError(ctx, newErr); err != nil {
			return nil, err
		}
		if newErr != nil {
			addDiagnostic("backend_api_target_unavailable", APIArtifactBinding{Ref: ArtifactRef{ArtifactID: id}}, "Selected immutable artifact is unavailable", true)
			if hadOld {
				finalPins = append(finalPins, oldPin)
				preview.Bindings = append(preview.Bindings, previous...)
			}
			continue
		}
		prepared.digests[id+"/"+c.RevisionID] = newSnapshot.ContentHash
		if hadOld && oldErr == nil {
			prepared.digests[id+"/"+oldPin.RevisionID] = oldPin.ContentHash
		}
		next := map[string]APIArtifactBinding{}
		for _, input := range c.Bindings {
			n, exists := nodes[input.SourceNodeID]
			if !exists {
				addDiagnostic("backend_api_source_missing", APIArtifactBinding{SourceNodeID: input.SourceNodeID, Ref: ArtifactRef{ArtifactID: id}}, "Selected source node is absent", true)
				continue
			}
			if n.Kind != "http_operation" && n.Kind != "api_field" || (n.Kind == "http_operation") != (input.Selector.ObjectKey != "") {
				return nil, invalid("selector", "Source kind requires its explicit operation key or schema pointer")
			}
			object, e := resolveAPIArtifact(ctx, newSnapshot, input.Selector)
			if err := fatalArtifactError(ctx, e); err != nil {
				return nil, err
			}
			if e != nil {
				addDiagnostic("backend_api_object_missing", APIArtifactBinding{SourceNodeID: n.ID, Ref: ArtifactRef{ArtifactID: id}}, "Selected object is unavailable or unsupported", true)
				continue
			}
			next[n.ID] = APIArtifactBinding{SourceNodeID: n.ID, SourceKind: n.Kind, SourceLastKnownLabel: boundedArtifactLabel(n.Name), Ref: frozenArtifactRef(c, newSnapshot, input.Selector, object), Origin: "manual", Reason: c.Reason}
		}
		for _, b := range previous {
			var left, inspected *apidesign.ArtifactObject
			if oldErr == nil {
				left, err = resolveAPIArtifact(ctx, old, b.Ref.Selector)
				if err := fatalArtifactError(ctx, err); err != nil {
					return nil, err
				}
				if err != nil || left.ObjectHash != b.Ref.ObjectHash || left.Pointer != b.Ref.ResolvedPointer {
					addDiagnostic("backend_api_unavailable_previous", b, "Previous selected object differs from its frozen reference", true)
					left = nil
				}
			}
			inspected, err = resolveAPIArtifact(ctx, newSnapshot, b.Ref.Selector)
			if err := fatalArtifactError(ctx, err); err != nil {
				return nil, err
			}
			if err != nil {
				if e := addDiff(new(b.Ref), nil, b.SourceNodeID, "missing", nil, nil); e != nil {
					return nil, e
				}
				addDiagnostic("backend_api_previous_object_missing", b, "Previous selector is absent at the proposed revision", false)
			} else if inspected.Pointer != b.Ref.ResolvedPointer {
				addDiagnostic("backend_api_previous_object_moved", b, "Previous object moved at the proposed revision", false)
			}
			after, exists := next[b.SourceNodeID]
			if !exists {
				if err := addDiff(new(b.Ref), nil, b.SourceNodeID, "removed", nil, nil); err != nil {
					return nil, err
				}
				continue
			}
			right, e := resolveAPIArtifact(ctx, newSnapshot, after.Ref.Selector)
			if e != nil {
				return nil, e
			}
			status := "unchanged"
			if b.Ref.ObjectHash != after.Ref.ObjectHash || b.Ref.Selector != after.Ref.Selector {
				status = "changed"
			} else if b.Ref.ResolvedPointer != after.Ref.ResolvedPointer {
				status = "moved"
			}
			if err := addDiff(new(b.Ref), new(after.Ref), b.SourceNodeID, status, left, right); err != nil {
				return nil, err
			}
			delete(next, b.SourceNodeID)
			preview.Bindings = append(preview.Bindings, after)
		}
		for _, b := range next {
			preview.Bindings = append(preview.Bindings, b)
			if err := addDiff(nil, new(b.Ref), b.SourceNodeID, "added", nil, nil); err != nil {
				return nil, err
			}
		}
		if len(next) > 0 || slices.ContainsFunc(preview.Bindings, func(b APIArtifactBinding) bool { return b.Ref.ArtifactID == id }) {
			finalPins = append(finalPins, ArtifactPin{Kind: "api_design", ID: id, RevisionID: c.RevisionID, ContentHash: newSnapshot.ContentHash})
		}
	}
	sortArtifactBindings(preview.Bindings)
	slices.SortFunc(finalPins, func(a, b ArtifactPin) int {
		if n := strings.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	preview.Pins = finalPins
	if err := ValidateAPIArtifactVector(preview.Pins, preview.Bindings); err != nil {
		return nil, err
	}
	// Equal source IDs deliberately carry multiple entries (prior selector check,
	// transfer/remap result). Keep their deterministic group/check order even when
	// other added bindings were collected from maps.
	slices.SortStableFunc(preview.Diff, func(a, b APIArtifactDiff) int { return strings.Compare(a.SourceNodeID, b.SourceNodeID) })
	slices.SortFunc(preview.Diagnostics, func(a, b APIArtifactDiagnostic) int {
		if n := strings.Compare(a.SourceNodeID, b.SourceNodeID); n != 0 {
			return n
		}
		if n := strings.Compare(a.ArtifactID, b.ArtifactID); n != 0 {
			return n
		}
		return strings.Compare(a.Code, b.Code)
	})
	preview.SemanticHash, err = APIArtifactSemanticHash(frozen.SourceContentHash, frozen.SourceSemanticHash, preview.Pins, preview.Bindings)
	if err != nil {
		return nil, err
	}
	preview.CandidateHash, err = APIArtifactCandidateHash(in, *preview)
	if err != nil {
		return nil, err
	}
	prepared.frozen.Bindings = preview.Bindings
	return prepared, ctx.Err()
}
