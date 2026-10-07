package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
)

type preparedArtifactPins struct {
	preview      *ArtifactPinsPreview
	frozen       ArtifactContext
	baselineHash string
	digests      map[editorSnapshotKey]string
}

func artifactPinsLimit() error {
	return &FaultError{Status: 413, Code: "backend_artifact_pins_limit", Message: "Artifact pin vector exceeds work limits"}
}
func artifactPinsBlocked(d []ArtifactDiagnostic) error {
	return &FaultError{Status: 422, Code: "backend_artifact_pins_blocked", Message: "Artifact pin preview contains blocked diagnostics", Details: map[string]any{"diagnostics": d}}
}
func artifactKey(pin ArtifactPin) ArtifactKey { return ArtifactKey{pin.Kind, pin.ID} }
func artifactRecordID(identity string) string { return "artifact:" + hashBytes([]byte(identity)) }

func (s *ArtifactService) prepareArtifacts(ctx context.Context, pid string, in PreviewArtifactPinsInput) (*preparedArtifactPins, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err = artifactBodyLimit(body); err != nil {
		return nil, err
	}
	if !ValidID(pid) {
		return nil, notFound()
	}
	state, frozen, baselineHash, err := s.loadArtifactPinsBaseline(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	commands := map[ArtifactKey]ArtifactPinCommand{}
	for _, c := range in.Commands {
		commands[c.Artifact] = c
	}
	pins := map[ArtifactKey]ArtifactPin{}
	for _, pin := range state.Revision.ArtifactPins {
		pins[artifactKey(pin)] = pin
	}
	if err := admitArtifactVector(state, frozen, pins, in.Commands); err != nil {
		return nil, err
	}
	nodes := map[string]Node{}
	for _, n := range state.Nodes {
		nodes[n.ID] = n
	}
	usedAPI := map[string]bool{}
	out := &ArtifactPinsPreview{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Pins: []ArtifactPin{}, APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}, SourceSnapshotIDs: slices.Clone(state.Revision.SourceSnapshotIDs), Diagnostics: []ArtifactDiagnostic{}, Diff: []ArtifactPinsDiff{}, CanApply: true}
	prepared := &preparedArtifactPins{preview: out, frozen: *frozen, baselineHash: baselineHash, digests: map[editorSnapshotKey]string{}}
	if err := retainArtifactGroups(state, frozen, commands, in.Commands, nodes, usedAPI, out); err != nil {
		return nil, err
	}
	request := NewEditorArtifactRequest(ctx, s.api, s.scenarios)
	builder := artifactPreviewBuilder{out: out, request: request, nodes: nodes, inputAPI: map[string]ArtifactKey{}}
	for _, c := range in.Commands {
		for _, b := range c.APIBindings {
			builder.inputAPI[b.SourceNodeID] = c.Artifact
		}
	}
	keys := slices.Collect(maps.Keys(commands))
	slices.SortFunc(keys, func(a, b ArtifactKey) int {
		if n := strings.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	for _, key := range keys {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if err := builder.applyCommand(ctx, commands[key], pins, frozen); err != nil {
			return nil, err
		}
	}
	return finishArtifactPinsPreview(ctx, in, prepared, frozen, request)
}

func artifactSidePin(side ArtifactComparisonSide) ArtifactPin {
	if side.Group != nil {
		return side.Group.Pin
	}
	if side.Editor != nil {
		return side.Editor.Pin
	}
	if side.API != nil {
		return ArtifactPin{Kind: side.API.Kind, ID: side.API.ArtifactID, RevisionID: side.API.RevisionID, ContentHash: side.API.ContentHash}
	}
	return ArtifactPin{}
}
func artifactDiffStatus(before, after *ArtifactComparisonSide) string {
	if before == nil {
		return "added"
	}
	if after == nil {
		return "removed"
	}
	a, _ := requestDigest(before)
	b, _ := requestDigest(after)
	if a == b {
		return "unchanged"
	}
	return "changed"
}

func (s *ArtifactService) loadArtifactPinsBaseline(ctx context.Context, pid string, in PreviewArtifactPinsInput) (*RevisionState, *ArtifactContext, string, error) {
	tx, err := s.repo.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, "", err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
	if err != nil {
		return nil, nil, "", err
	}
	state, err := loadRevisionState(ctx, tx, pid, in.BaseRevisionID)
	if err != nil {
		return nil, nil, "", err
	}
	if p.Version != in.ExpectedVersion {
		return nil, nil, "", importConflict("backend_version_conflict", "Project changed; read before retrying", p.Version)
	}
	if p.CurrentRevisionID != in.BaseRevisionID {
		return nil, nil, "", importConflict("backend_artifact_pins_base_conflict", "Current source baseline changed", p.Version)
	}
	if !isArtifactSourceSchema(state.Revision.SchemaVersion) || len(state.Sources) == 0 || len(state.Revision.SourceSnapshotIDs) == 0 || len(p.Repositories) == 0 {
		return nil, nil, "", &FaultError{Status: 422, Code: "backend_artifact_pins_unsupported", Message: "Artifact pins require an imported source4, source5 or source6 baseline"}
	}
	if state.ArtifactContextV3 != nil {
		return nil, nil, "", legacyPinsOnV3("backend_artifact_pins_unsupported")
	}
	baselineHash, err := artifactBaselineDigest(ctx, tx, pid, in.BaseRevisionID)
	if err != nil {
		return nil, nil, "", err
	}
	frozen := revisionArtifactContext(state)
	content, anchor, err := artifactSourceAnchors(ctx, tx, state)
	if err != nil {
		return nil, nil, "", err
	}
	if frozen == nil {
		frozen = &ArtifactContext{SourceContentHash: content, SourceSemanticHash: anchor, APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}}
	}
	if err = tx.Rollback(); err != nil {
		return nil, nil, "", err
	}
	return state, frozen, baselineHash, nil
}

func admitArtifactVector(state *RevisionState, frozen *ArtifactContext, pins map[ArtifactKey]ArtifactPin, commands []ArtifactPinCommand) error {
	count := len(state.Revision.ArtifactPins)
	bindings := len(frozen.APIBindings) + len(frozen.EditorBindings)
	for _, c := range commands {
		oldAPI, oldEditor, e := SelectedArtifactBindings(*frozen, c.Artifact)
		if e != nil {
			return e
		}
		bindings -= len(oldAPI) + len(oldEditor)
		_, had := pins[c.Artifact]
		if c.Type == "remove_artifact_pin" {
			if had {
				count--
			}
		} else {
			if !had {
				count++
			}
			bindings += len(c.APIBindings) + len(c.EditorBindings)
		}
	}
	if count > MaxAPIArtifactPins || bindings > MaxAPIArtifactBindings {
		return artifactPinsLimit()
	}
	return nil
}

func retainArtifactGroups(state *RevisionState, frozen *ArtifactContext, commands map[ArtifactKey]ArtifactPinCommand, inputs []ArtifactPinCommand, nodes map[string]Node, usedAPI map[string]bool, out *ArtifactPinsPreview) error {
	for _, pin := range state.Revision.ArtifactPins {
		if _, changed := commands[artifactKey(pin)]; !changed {
			out.Pins = append(out.Pins, pin)
		}
	}
	for _, b := range frozen.APIBindings {
		if _, changed := commands[ArtifactKey{b.Ref.Kind, b.Ref.ArtifactID}]; !changed {
			out.APIBindings = append(out.APIBindings, b)
			usedAPI[b.SourceNodeID] = true
		}
	}
	for _, b := range frozen.EditorBindings {
		if _, changed := commands[ArtifactKey{b.ArtifactKind, b.ArtifactID}]; !changed {
			out.EditorBindings = append(out.EditorBindings, b)
		}
	}
	// Retained unrelated groups are opaque immutable context: no owner reads.
	for _, c := range inputs {
		for _, b := range c.APIBindings {
			if usedAPI[b.SourceNodeID] {
				return invalid("sourceNodeId", "Source is already bound in another group")
			}
			usedAPI[b.SourceNodeID] = true
			if n, ok := nodes[b.SourceNodeID]; ok && (n.Kind != "http_operation" && n.Kind != "api_field" || (n.Kind == "http_operation") != (b.Selector.ObjectKey != "")) {
				return invalid("selector", "Source kind requires explicit operation key or schema pointer")
			}
		}
	}
	return nil
}

type artifactPreviewBuilder struct {
	out     *ArtifactPinsPreview
	request *EditorArtifactRequest
	nodes   map[string]Node
	changes int
	// inputAPI maps each source this request binds to the command binding it.
	inputAPI map[string]ArtifactKey
}

func (b *artifactPreviewBuilder) diagnostic(code string, key ArtifactKey, selector *EditorSelector, ids []string, message string, block bool) {
	b.out.Diagnostics = append(b.out.Diagnostics, ArtifactDiagnostic{Code: code, Severity: "warning", Message: message, Artifact: new(key), Selector: selector, SourceNodeIDs: ids})
	if block {
		b.out.CanApply = false
	}
}

func (b *artifactPreviewBuilder) truncated() {
	b.out.DiffTruncated = true
	b.out.CanApply = false
	if !slices.ContainsFunc(b.out.Diagnostics, func(d ArtifactDiagnostic) bool { return d.Code == "backend_artifact_diff_truncated" }) {
		b.out.Diagnostics = append(b.out.Diagnostics, ArtifactDiagnostic{Code: "backend_artifact_diff_truncated", Message: "Selected object diff exceeds entries or escaped pointer bytes; narrow the request"})
	}
}

func (b *artifactPreviewBuilder) addDiff(ctx context.Context, identity, kind, source, status string, before, after *ArtifactComparisonSide, left, right string) error {
	if len(b.out.Diff) >= MaxAPIArtifactDiffEntries {
		b.truncated()
		return nil
	}
	d := ArtifactPinsDiff{Identity: identity, Kind: kind, SourceNodeID: source, Status: status, Before: before, After: after, Changes: []APIArtifactObjectChange{}}
	if before != nil && after != nil {
		d.ContextChanged = artifactSidePin(*before).ContentHash != artifactSidePin(*after).ContentHash
	}
	if left != "" || right != "" {
		delta, overflow, e := apiArtifactObjectDiff(ctx, left, right, MaxAPIArtifactDiffEntries-b.changes)
		if e != nil {
			return e
		}
		d.Changes = delta
		b.changes += len(delta)
		if overflow {
			b.truncated()
		}
	}
	b.out.Diff = append(b.out.Diff, d)
	return nil
}

type artifactPreviewGroup struct {
	key             ArtifactKey
	oldPin, nextPin ArtifactPin
	remove          bool
}

// Frozen labels go through boundedArtifactLabel, as on the legacy API path:
// owners allow names far above the 4096-byte cap Validate enforces, and one
// long participant or source-node name failed the whole preview with a bare
// "Invalid frozen editor binding" (review 2026-10-06, F59). A label is a
// last-known hint, never an identity, so a truncated one loses nothing.
func (b *artifactPreviewBuilder) resolveAPIBindings(ctx context.Context, c ArtifactPinCommand, nextPin ArtifactPin) ([]APIArtifactBinding, error) {
	key, nodes, request := c.Artifact, b.nodes, b.request
	nextAPI := []APIArtifactBinding{}
	for _, input := range c.APIBindings {
		n, exists := nodes[input.SourceNodeID]
		if !exists {
			b.diagnostic("backend_artifact_source_missing", key, nil, []string{input.SourceNodeID}, "Selected source node is absent", true)
			continue
		}
		object, e := request.ResolveAPIObject(nextPin, input.Selector)
		if f := requiredArtifactError(ctx, e); f != nil {
			return nil, f
		}
		if e != nil {
			b.diagnostic("backend_artifact_object_missing", key, nil, []string{n.ID}, "Selected API object is unavailable or unsupported", true)
			continue
		}
		nextAPI = append(nextAPI, APIArtifactBinding{SourceNodeID: n.ID, SourceKind: n.Kind, SourceLastKnownLabel: boundedArtifactLabel(n.Name), Ref: ArtifactRef{Kind: key.Kind, ArtifactID: key.ID, RevisionID: nextPin.RevisionID, ContentHash: nextPin.ContentHash, Selector: input.Selector, ObjectHash: object.ObjectHash, LastKnownLabel: boundedArtifactLabel(object.Label), ResolvedPointer: object.Pointer}, Origin: "manual", Reason: c.Reason})
	}
	return nextAPI, nil
}

func (b *artifactPreviewBuilder) resolveEditorBindings(ctx context.Context, c ArtifactPinCommand, nextPin ArtifactPin) ([]EditorBinding, error) {
	key, nodes, request := c.Artifact, b.nodes, b.request
	out := b.out
	nextEditor := []EditorBinding{}
	for _, input := range c.EditorBindings {
		sourceIDs := slices.Clone(input.SourceNodeIDs)
		slices.Sort(sourceIDs)
		labels := make([]string, 0, len(sourceIDs))
		missing := false
		for _, id := range sourceIDs {
			n, ok := nodes[id]
			if !ok {
				missing = true
				b.diagnostic("backend_artifact_source_missing", key, new(input.Selector), []string{id}, "Selected source node is absent", true)
			} else {
				labels = append(labels, boundedArtifactLabel(n.Name))
			}
		}
		if missing {
			continue
		}
		object, e := request.ResolveObject(nextPin, input.Selector)
		if f := requiredArtifactError(ctx, e); f != nil {
			return nil, f
		}
		if e != nil {
			b.diagnostic("backend_artifact_object_missing", key, new(input.Selector), sourceIDs, "Selected editor object is unavailable or unsupported", true)
			continue
		}
		out.Diagnostics = append(out.Diagnostics, object.Diagnostics...)
		nextEditor = append(nextEditor, EditorBinding{ArtifactKind: key.Kind, ArtifactID: key.ID, Selector: input.Selector, SourceNodeIDs: sourceIDs, SourceLabels: labels, ObjectHash: object.ObjectHash, LastKnownLabel: boundedArtifactLabel(object.Label), Origin: "manual", Reason: c.Reason})
	}
	return nextEditor, nil
}

func (b *artifactPreviewBuilder) compareAPIBindings(ctx context.Context, group artifactPreviewGroup, previous, nextBindings []APIArtifactBinding) error {
	key, oldPin, nextPin, remove := group.key, group.oldPin, group.nextPin, group.remove
	request := b.request
	var err error

	oldAPI := map[string]APIArtifactBinding{}
	newAPI := map[string]APIArtifactBinding{}
	sources := map[string]bool{}
	for _, b := range previous {
		oldAPI[b.SourceNodeID] = b
		sources[b.SourceNodeID] = true
	}
	for _, b := range nextBindings {
		newAPI[b.SourceNodeID] = b
		sources[b.SourceNodeID] = true
	}
	for _, source := range slices.Sorted(maps.Keys(sources)) {
		old, oldOK := oldAPI[source]
		next, nextOK := newAPI[source]
		var before, after *ArtifactComparisonSide
		left, right := "", ""
		if oldOK {
			before = &ArtifactComparisonSide{API: new(old.Ref)}
			if !remove {
				object, e := request.ResolveAPIObject(oldPin, old.Ref.Selector)
				if f := requiredArtifactError(ctx, e); f != nil {
					return f
				}
				if e != nil || object.ObjectHash != old.Ref.ObjectHash || object.Pointer != old.Ref.ResolvedPointer {
					b.diagnostic("backend_artifact_previous_unavailable", key, nil, []string{source}, "Previous exact API object differs; remove explicitly before replacement", true)
				} else {
					left = object.Document
				}
				if nextPin.RevisionID != oldPin.RevisionID {
					_, e := request.ResolveAPIObject(nextPin, old.Ref.Selector)
					if f := requiredArtifactError(ctx, e); f != nil {
						return f
					}
					if e != nil {
						if err = b.addDiff(ctx, "api-missing:"+source, "api", source, "missing", before, nil, left, ""); err != nil {
							return err
						}
						b.diagnostic("backend_artifact_previous_object_missing", key, nil, []string{source}, "Previous API selector is absent at the proposed revision", false)
					}
				}
			}
		}
		if nextOK {
			after = &ArtifactComparisonSide{API: new(next.Ref)}
			object, e := request.ResolveAPIObject(nextPin, next.Ref.Selector)
			if e != nil {
				return e
			}
			right = object.Document
		}
		status := artifactDiffStatus(before, after)
		if err = b.addDiff(ctx, "api:"+source, "api", source, status, before, after, left, right); err != nil {
			return err
		}
	}
	return nil
}

func (b *artifactPreviewBuilder) compareEditorBindings(ctx context.Context, group artifactPreviewGroup, previous, nextBindings []EditorBinding) error {
	key, oldPin, nextPin, remove := group.key, group.oldPin, group.nextPin, group.remove
	request := b.request
	var err error
	out := b.out

	oldEditor := map[string]EditorBinding{}
	newEditor := map[string]EditorBinding{}
	identities := map[string]bool{}
	for _, b := range previous {
		id, e := EditorArtifactComparisonIdentity(b)
		if e != nil {
			return e
		}
		oldEditor[id] = b
		identities[id] = true
	}
	for _, b := range nextBindings {
		id, e := EditorArtifactComparisonIdentity(b)
		if e != nil {
			return e
		}
		newEditor[id] = b
		identities[id] = true
	}
	for _, identity := range slices.Sorted(maps.Keys(identities)) {
		old, oldOK := oldEditor[identity]
		next, nextOK := newEditor[identity]
		var before, after *ArtifactComparisonSide
		left, right := "", ""
		if oldOK {
			before = &ArtifactComparisonSide{Editor: &EditorArtifactSide{Pin: oldPin, Binding: old}}
			if !remove {
				object, e := request.ResolveObject(oldPin, old.Selector)
				if f := requiredArtifactError(ctx, e); f != nil {
					return f
				}
				if e != nil || object.ObjectHash != old.ObjectHash {
					b.diagnostic("backend_artifact_previous_unavailable", key, new(old.Selector), old.SourceNodeIDs, "Previous exact editor object differs; remove explicitly before replacement", true)
				} else {
					left = object.DocumentJSON
					out.Diagnostics = append(out.Diagnostics, object.Diagnostics...)
				}
				if nextPin.RevisionID != oldPin.RevisionID {
					_, e := request.ResolveObject(nextPin, old.Selector)
					if f := requiredArtifactError(ctx, e); f != nil {
						return f
					}
					if e != nil {
						if err = b.addDiff(ctx, identity+":missing", "editor", "", "missing", before, nil, left, ""); err != nil {
							return err
						}
						b.diagnostic("backend_artifact_previous_object_missing", key, new(old.Selector), old.SourceNodeIDs, "Previous editor selector is absent at the proposed revision", false)
					}
				}
			}
		}
		if nextOK {
			after = &ArtifactComparisonSide{Editor: &EditorArtifactSide{Pin: nextPin, Binding: next}}
			object, e := request.ResolveObject(nextPin, next.Selector)
			if e != nil {
				return e
			}
			right = object.DocumentJSON
		}
		if err = b.addDiff(ctx, identity, "editor", "", artifactDiffStatus(before, after), before, after, left, right); err != nil {
			return err
		}
	}
	return nil
}

func (b *artifactPreviewBuilder) applyCommand(ctx context.Context, c ArtifactPinCommand, pins map[ArtifactKey]ArtifactPin, frozen *ArtifactContext) error {
	key := c.Artifact
	out, request := b.out, b.request
	var err error
	oldPin, hadOld := pins[key]
	previousAPI, previousEditor, e := SelectedArtifactBindings(*frozen, key)
	if e != nil {
		return e
	}
	var nextPin ArtifactPin
	nextAPI := []APIArtifactBinding{}
	nextEditor := []EditorBinding{}
	remove := c.Type == "remove_artifact_pin"
	if !remove {
		nextPin, err = request.SnapshotPin(key, c.RevisionID)
		if e := requiredArtifactError(ctx, err); e != nil {
			return e
		}
		if err != nil {
			// The group keeps its old bindings, except a source another command
			// of this request rebinds: keeping both made the vector admission
			// fail with 400 "Duplicate API source binding" instead of this
			// blocking diagnostic, which now names those sources
			// (review 2026-10-06, F62).
			var claimed []string
			kept := []APIArtifactBinding{}
			for _, binding := range previousAPI {
				if owner, ok := b.inputAPI[binding.SourceNodeID]; ok && owner != key {
					claimed = append(claimed, binding.SourceNodeID)
					continue
				}
				kept = append(kept, binding)
			}
			b.diagnostic("backend_artifact_target_unavailable", key, nil, claimed, "Selected immutable artifact is unavailable or unverified", true)
			if hadOld {
				out.Pins = append(out.Pins, oldPin)
				out.APIBindings = append(out.APIBindings, kept...)
				out.EditorBindings = append(out.EditorBindings, previousEditor...)
			}
			return nil
		}
		nextAPI, err = b.resolveAPIBindings(ctx, c, nextPin)
		if err != nil {
			return err
		}
		nextEditor, err = b.resolveEditorBindings(ctx, c, nextPin)
		if err != nil {
			return err
		}
		out.Pins = append(out.Pins, nextPin)
		out.APIBindings = append(out.APIBindings, nextAPI...)
		out.EditorBindings = append(out.EditorBindings, nextEditor...)
	}
	if err := b.compareAPIBindings(ctx, artifactPreviewGroup{key, oldPin, nextPin, remove}, previousAPI, nextAPI); err != nil {
		return err
	}
	if err := b.compareEditorBindings(ctx, artifactPreviewGroup{key, oldPin, nextPin, remove}, previousEditor, nextEditor); err != nil {
		return err
	}
	if !hadOld || remove || oldPin != nextPin {
		identity, e := ArtifactGroupIdentity(ArtifactPin{Kind: key.Kind, ID: key.ID})
		if e != nil {
			return e
		}
		var before, after *ArtifactComparisonSide
		if hadOld {
			before = &ArtifactComparisonSide{Group: &ArtifactGroupSide{Pin: oldPin}}
		}
		if !remove {
			after = &ArtifactComparisonSide{Group: &ArtifactGroupSide{Pin: nextPin}}
		}
		if before != nil || after != nil {
			if err = b.addDiff(ctx, identity, "group", "", artifactDiffStatus(before, after), before, after, "", ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func finishArtifactPinsPreview(ctx context.Context, in PreviewArtifactPinsInput, prepared *preparedArtifactPins, frozen *ArtifactContext, request *EditorArtifactRequest) (*preparedArtifactPins, error) {
	out := prepared.preview
	var err error
	sortArtifactBindings(out.APIBindings)
	out.EditorBindings, err = CanonicalEditorBindings(out.EditorBindings)
	if err != nil {
		return nil, err
	}
	out.Pins = canonicalAPIPins(out.Pins)
	prepared.frozen.APIBindings = out.APIBindings
	prepared.frozen.EditorBindings = out.EditorBindings
	// Admission includes every retained/new binding, frozen label and both anchors.
	if _, err = EncodeArtifactContext(prepared.frozen, out.Pins); err != nil {
		return nil, err
	}
	if ArtifactContextUsesV1(out.Pins, prepared.frozen) {
		prepared.frozen.DocumentVersion = ""
	} else {
		prepared.frozen.DocumentVersion = EditorArtifactDocumentVersion
	}
	out.SemanticHash, err = ArtifactSemanticHash(frozen.SourceContentHash, frozen.SourceSemanticHash, out.Pins, out.APIBindings, out.EditorBindings)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out.Diff, func(a, b ArtifactPinsDiff) int { return strings.Compare(a.Identity, b.Identity) })
	slices.SortStableFunc(out.Diagnostics, func(a, b ArtifactDiagnostic) int {
		ad, _ := requestDigest(a)
		bd, _ := requestDigest(b)
		return strings.Compare(ad, bd)
	})
	out.CandidateHash, err = ArtifactCandidateHash(in, *out)
	if err != nil {
		return nil, err
	}
	// Successful nested verification used by resolved association semantics joins CAS.
	request.mu.Lock()
	for key, snapshot := range request.snapshots {
		if snapshot.err == nil && editorOwnerVerified(snapshot) {
			if snapshot.api != nil {
				prepared.digests[key] = snapshot.api.ContentHash
			} else {
				prepared.digests[key] = snapshot.inspection.ContentHash
			}
		}
	}
	request.mu.Unlock()
	return prepared, ctx.Err()
}
