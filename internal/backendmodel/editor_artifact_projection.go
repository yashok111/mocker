package backendmodel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/statediagram"
)

const DefaultEditorProjectionPageSize = 50
const MaxEditorProjectionPageSize = 100

type editorProjectionRow struct {
	kind, label, pointer string
	identity             string
	owner                ArtifactOwnerAddress
	selector             *EditorSelector
	value                any
	data                 *ArtifactProjectionData
	embeddedID           string
	operationKey         string
}
type editorProjectionCursor struct {
	Scope  string `json:"scope"`
	Offset int    `json:"offset"`
}

func editorProjectionScope(state *RevisionState, in ArtifactQueryInput, limit int) (string, error) {
	in.Cursor = ""
	in.Limit = limit
	return hashAPIJSON(struct {
		Domain       string             `json:"domain"`
		RevisionID   string             `json:"revisionId"`
		SemanticHash string             `json:"semanticHash"`
		Pins         []ArtifactPin      `json:"pins"`
		Sources      []string           `json:"sources"`
		Query        ArtifactQueryInput `json:"query"`
	}{"backend-editor-projection-cursor-v1", state.Revision.ID, state.Revision.SemanticHash, state.Revision.ArtifactPins, state.Revision.SourceSnapshotIDs, in})
}
func editorProjectionOffset(scope, cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > 256 {
		return 0, invalid("cursor", "Invalid bounded cursor")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil {
		return 0, invalid("cursor", "Invalid cursor encoding")
	}
	var c editorProjectionCursor
	if err := strictAPIObject(raw, []string{"scope", "offset"}, nil, &c); err != nil {
		return 0, err
	}
	if c.Scope != scope || c.Offset < 1 {
		return 0, invalid("cursor", "Cursor does not match immutable projection")
	}
	return c.Offset, nil
}
func editorProjectionNext(scope string, offset int) string {
	raw, _ := json.Marshal(editorProjectionCursor{scope, offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func editorProjectionID(scope, kind, pointer string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(scope+"\x00"+kind+"\x00"+pointer)))
}
func editorProjectionDiagnostic(code, message string) ArtifactDiagnostic {
	return ArtifactDiagnostic{Code: code, Severity: "warning", Message: message}
}

// Project receives an already loaded immutable backend state and its separately
// decoded frozen artifact context. It never loads or writes backend persistence.
// Only items are paged; the selected group's complete frozen roster is retained
// even when the owner cannot be read or any referenced source/object is missing.
func (r *EditorArtifactRequest) Project(state *RevisionState, c ArtifactContext, in ArtifactQueryInput) (*ArtifactProjectionPage, error) {
	if state == nil {
		return nil, invalid("revisionId", "Projection needs an exact loaded backend revision")
	}
	artifactPins, semanticHash, snapshotIDs := state.Revision.ArtifactPins, state.Revision.SemanticHash, state.Revision.SourceSnapshotIDs
	if r.effective != nil {
		artifactPins = r.effective.Pins.ArtifactPins
		semanticHash = r.effective.Pins.EffectiveSemanticHash
		snapshotIDs = r.effective.Pins.SourceSnapshotIDs
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if state == nil || state.Revision.ID != in.RevisionID {
		return nil, invalid("revisionId", "Projection needs the exact loaded backend revision")
	}
	if err := c.Validate(artifactPins); err != nil {
		return nil, err
	}
	if c.DocumentVersion == EditorArtifactDocumentVersion {
		if _, err := CheckEditorArtifactContextSize(c); err != nil {
			return nil, err
		}
	}
	pin, err := editorProjectionPin(artifactPins, in.Artifact)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultEditorProjectionPageSize
	}
	scope, err := editorProjectionScope(state, in, limit)
	if err != nil {
		return nil, err
	}
	if r.effective != nil {
		scope, err = requestDigest(struct {
			Scope string
			Pins  EffectiveGraphPins
		}{scope, r.effective.Pins})
		if err != nil {
			return nil, err
		}
	}
	offset, err := editorProjectionOffset(scope, in.Cursor)
	if err != nil {
		return nil, err
	}
	api, editor, err := SelectedArtifactBindings(c, in.Artifact)
	if err != nil {
		return nil, err
	}
	page := &ArtifactProjectionPage{RevisionID: state.Revision.ID, SemanticHash: semanticHash, SourceSnapshotIDs: slices.Clone(snapshotIDs), Pins: slices.Clone(artifactPins), SelectedPin: pin, View: in.View, EmbeddedContractID: in.EmbeddedContractID, APIBindings: api, EditorBindings: editor, BindingsComplete: true, Items: []ArtifactProjectionItem{}, Resolution: ArtifactResolution{Status: "resolved", Diagnostics: []ArtifactDiagnostic{}}, Diagnostics: []ArtifactDiagnostic{}, Coverage: ArtifactProjectionCoverage{TruncatedReasons: []string{}}, Complete: true}
	if r.effective != nil {
		page.Target = new(r.effective.Target)
		page.EffectivePins = new(r.effective.Pins)
	}
	if page.SourceSnapshotIDs == nil {
		page.SourceSnapshotIDs = []string{}
	}
	if pin.Kind == "design_scenario" {
		page.HashPolicy = "design-scenario-envelope-v1"
	} else {
		page.HashPolicy = "api-design-raw-document-v1"
	}
	return r.populateProjectionPage(state, c, in, page, scope, offset, limit)
}

func editorProjectionPin(artifactPins []ArtifactPin, artifact ArtifactKey) (ArtifactPin, error) {
	var pin ArtifactPin
	found := false
	for _, p := range artifactPins {
		if p.Kind == artifact.Kind && p.ID == artifact.ID {
			pin, found = p, true
		}
	}
	if !found {
		return ArtifactPin{}, invalid("artifact", "Artifact is not pinned in this backend revision")
	}
	return pin, nil
}

func (r *EditorArtifactRequest) editorLinkedSources(c ArtifactContext, e ArtifactEmbeddedContract, key string) []string {
	ids := []string{}
	if e.Mode != "linked" || e.OriginStatus != "verified" || e.Origin == nil {
		return ids
	}
	for _, b := range c.APIBindings {
		ref := b.Ref
		if ref.Kind == "api_design" && ref.ArtifactID == e.Origin.DesignID && ref.RevisionID == e.Origin.RevisionID && ref.ContentHash == e.APIContentHash && ref.Selector.ObjectKey == key {
			object, err := r.ResolveAPIObject(ArtifactPin{Kind: ref.Kind, ID: ref.ArtifactID, RevisionID: ref.RevisionID, ContentHash: ref.ContentHash}, ref.Selector)
			if err == nil && object.ObjectHash == ref.ObjectHash && object.Pointer == ref.ResolvedPointer {
				ids = append(ids, b.SourceNodeID)
			}
		}
	}
	return ids
}
func (r *EditorArtifactRequest) projectionRows(s *editorOwnerSnapshot, in ArtifactQueryInput) ([]editorProjectionRow, []ArtifactDiagnostic, bool, []string, error) {
	b := editorProjectionBuilder{rows: []editorProjectionRow{}, diagnostics: []ArtifactDiagnostic{}, complete: true, reasons: []string{}, embeddedID: in.EmbeddedContractID}
	var blocked []ArtifactDiagnostic
	b.root, b.prefix, blocked = editorProjectionRoot(s, in)
	if blocked != nil {
		return b.rows, blocked, false, b.reasons, nil
	}
	var err error
	switch in.View {
	case "sequence":
		err = b.sequenceRows(r, s)
	case "states":
		b.stateRows()
	case "response_rules":
		b.responseRows()
	case "event_model":
		if s.scenarioDecodeErr != nil {
			b.unsupported("/eventModel", b.root["eventModel"], "Saved scenario contains constructs unsupported by the event-map owner")
			break
		}
		var stopped bool
		stopped, err = b.eventRows(r, s)
		if stopped {
			return b.rows, b.diagnostics, b.complete, b.reasons, err
		}
	}
	if err != nil {
		return nil, nil, false, nil, err
	}

	// Unknown top-level fields remain readable individually, without repeating the
	// whole document. Owner execution data stays authored and is not source proof.
	known := []string{"formatVersion", "title", "participants", "messages", "fragments", "contracts", "execution", "eventModel"}
	if s.scenario != nil && in.EmbeddedContractID == "" {
		keys := []string{}
		for key := range b.root {
			if !slices.Contains(known, key) {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			b.unsupported("/"+editorPointerPart(key), b.root[key], "Unsupported saved scenario field: "+key)
		}
	}
	return b.rows, b.diagnostics, b.complete, b.reasons, r.ctx.Err()
}
func resolveEditorValueForEvent(s *editorOwnerSnapshot, selector EditorSelector) (string, string, ArtifactOwnerAddress, any, error) {
	tree := s.tree
	prefix := ""
	if selector.EmbeddedContractID != "" {
		v, i, err := editorUniqueID(editorArray(tree.root["contracts"]), selector.EmbeddedContractID)
		if err != nil {
			return "", "", ArtifactOwnerAddress{}, nil, err
		}
		tree = &editorRawTree{root: editorObject(editorObject(v)["document"])}
		prefix = fmt.Sprintf("/contracts/%d/document", i)
	}
	p, l, o, v, err := resolveEditorValue(tree, selector)
	return prefix + p, l, o, v, err
}
func editorEventLocator(l designscenario.EventMapLocator) ArtifactEventMapLocator {
	revision := ""
	if l.PinnedRevisionID != 0 {
		revision = strconv.FormatInt(l.PinnedRevisionID, 10)
	}
	return ArtifactEventMapLocator{Pointer: l.Pointer, EntityID: l.EntityID, ContractID: l.ContractID, OperationID: l.OperationID, ParticipantID: l.ParticipantID, HTTPContractID: l.HTTPContractID, OperationKey: l.OperationKey, DiagramID: l.DiagramID, TransitionID: l.TransitionID, Mode: l.Mode, PinnedRevisionID: revision, Method: l.Method, Path: l.Path}
}

func (r *EditorArtifactRequest) populateProjectionPage(state *RevisionState, c ArtifactContext, in ArtifactQueryInput, page *ArtifactProjectionPage, scope string, offset, limit int) (*ArtifactProjectionPage, error) {
	pin := page.SelectedPin
	s, ownerErr := r.pinnedSnapshot(pin)
	if ownerErr != nil {
		if r.ctx.Err() != nil {
			return nil, r.ctx.Err()
		}
		page.Resolution.Status = "unavailable"
		page.Complete = false
		page.Resolution.Diagnostics = append(page.Resolution.Diagnostics, editorProjectionDiagnostic("artifact_owner_unavailable", "Exact pinned owner is unavailable or differs from its frozen digest"))
		page.Coverage.DiagnosticsReturned = len(page.Resolution.Diagnostics)
		if offset != 0 {
			return nil, invalid("cursor", "Unavailable projection has no continuation")
		}
		out, cloneErr := editorClone(*page)
		if cloneErr != nil {
			return nil, cloneErr
		}
		return &out, r.ctx.Err()
	}
	rows, diagnostics, complete, reasons, err := r.projectionRows(s, in)
	if err != nil {
		return nil, err
	}
	page.Diagnostics = diagnostics
	verified := editorOwnerVerified(s)
	page.Complete = complete && verified
	if !verified {
		page.Resolution.Status = "unverified"
		page.Resolution.Diagnostics = append(page.Resolution.Diagnostics, editorProjectionDiagnostic("scenario_envelope_unverified", "Saved scenario typed content is unsupported and its envelope hash cannot be verified"))
	}
	page.Coverage.TruncatedReasons = reasons
	page.Coverage.TotalItems = len(rows)
	if offset > len(rows) {
		return nil, invalid("cursor", "Projection offset exceeds immutable view")
	}
	end := min(offset+limit, len(rows))
	if end < len(rows) {
		page.NextCursor = editorProjectionNext(scope, end)
		page.Coverage.TruncatedReasons = append(page.Coverage.TruncatedReasons, "page")
	}
	// Each embedded scope is verified separately even if exact origin snapshots
	// are shared. Only returned rows expand optional origins; saved owner data and
	// frozen binding rosters do not require eager whole-scenario verification.
	rowScope, err := editorProjectionScope(state, in, 0)
	if err != nil {
		return nil, err
	}
	if err := r.appendProjectionItems(state, c, in, page, s, rows[offset:end], rowScope, verified); err != nil {
		return nil, err
	}
	// Frozen orphan objects stay in the full roster independently of visible rows.
	// Their diagnostics can be evaluated by E4 without changing these associations.
	page.Coverage.ItemsReturned = len(page.Items)
	page.Coverage.DiagnosticsReturned = len(page.Diagnostics) + len(page.Resolution.Diagnostics)
	for _, item := range page.Items {
		page.Coverage.DiagnosticsReturned += len(item.Diagnostics)
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	out, err := editorClone(*page)
	if err != nil {
		return nil, err
	}
	return &out, r.ctx.Err()
}
func (r *EditorArtifactRequest) appendProjectionItems(state *RevisionState, c ArtifactContext, in ArtifactQueryInput, page *ArtifactProjectionPage, s *editorOwnerSnapshot, rows []editorProjectionRow, rowScope string, verified bool) error {
	pin, editor := page.SelectedPin, page.EditorBindings
	var err error
	embeddedCache := map[string]*ArtifactEmbeddedContract{}
	embeddedDiagnostics := map[string][]ArtifactDiagnostic{}
	sources := map[string]bool{}
	for _, n := range state.Nodes {
		sources[n.ID] = true
	}
	for _, row := range rows {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		item := ArtifactProjectionItem{ID: editorProjectionID(rowScope, row.kind, row.pointer+"\x00"+row.identity), Kind: row.kind, Label: row.label, Locator: ArtifactProjectionLocator{Pin: pin, View: in.View, Owner: row.owner}, BindingSelector: row.selector, SourceNodeIDs: []string{}, Diagnostics: []ArtifactDiagnostic{}}
		item.Locator.Owner.Pointer = row.pointer
		if !verified {
			item.BindingSelector = nil
		}
		if row.embeddedID != "" {
			info, ok := embeddedCache[row.embeddedID]
			if !ok {
				_, _, info, embeddedDiagnostics[row.embeddedID], err = r.embedded(s, row.embeddedID)
				if err != nil {
					if r.ctx.Err() != nil {
						return r.ctx.Err()
					}
					item.Diagnostics = append(item.Diagnostics, editorProjectionDiagnostic("embedded_contract_unavailable", "Exact embedded scope could not be resolved"))
				}
				embeddedCache[row.embeddedID] = info
			}
			item.Locator.Embedded = info
			item.Diagnostics = append(item.Diagnostics, embeddedDiagnostics[row.embeddedID]...)
		}
		if err := populateProjectionItemData(page, s, row, &item); err != nil {
			return err
		}
		r.populateProjectionItemSources(c, editor, row, &item, verified, sources)
		if row.kind == "event_node" {
			page.Coverage.NodesReturned++
		}
		if row.kind == "event_edge" {
			page.Coverage.EdgesReturned++
		}
		page.Items = append(page.Items, item)
	}
	return nil
}

func populateProjectionItemData(page *ArtifactProjectionPage, s *editorOwnerSnapshot, row editorProjectionRow, item *ArtifactProjectionItem) error {
	var err error
	if row.data != nil {
		item.Data = *row.data
		item.ObjectHash, err = editorAuthoredHash(row.value)
	} else {
		raw, rawErr := jsonx.Marshal(row.value)
		if rawErr != nil {
			return rawErr
		}
		item.ObjectHash, err = editorAuthoredHash(row.value)
		if row.kind == "api_operation" {
			// Only returned authored API rows extract exact raw bytes. Building
			// every operation's raw subtree before paging would amplify shared refs.
			var exact string
			exact, rawErr = editorExactRaw(s.tree.raw, row.pointer)
			loc := row.owner.EventMap
			item.Data = ArtifactProjectionData{Kind: "api_operation", APIOperation: &ArtifactAPIOperationData{OperationKey: row.operationKey, Method: loc.Method, Path: loc.Path, Summary: editorString(editorObject(row.value)["summary"]), DocumentJSON: exact}}
		} else {
			item.Data, rawErr = editorTypedData(row.kind, string(raw))
		}
		if rawErr != nil {
			page.Complete = false
			item.BindingSelector = nil
			item.Data = ArtifactProjectionData{Kind: "unsupported", Unsupported: &ArtifactUnsupportedData{DocumentJSON: string(raw), Description: "Unsupported authored owner object"}}
			item.Diagnostics = append(item.Diagnostics, editorProjectionDiagnostic("owner_construct_unsupported", "Saved object cannot be decoded by its declared owner type"))
		}
		// Parent rows expose their own metadata. Their full authored subtree is
		// hashed, but child arrays appear as separate rows, never copied per parent.
		if item.Data.StateDiagram != nil {
			item.Data.StateDiagram.States = []statediagram.State{}
			item.Data.StateDiagram.Transitions = []statediagram.Transition{}
		}
		if item.Data.ResponseRule != nil {
			item.Data.ResponseRule.Nodes = []responserules.Node{}
			item.Data.ResponseRule.Edges = []responserules.Edge{}
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func (r *EditorArtifactRequest) populateProjectionItemSources(c ArtifactContext, editor []EditorBinding, row editorProjectionRow, item *ArtifactProjectionItem, verified bool, sources map[string]bool) {
	if verified && row.selector != nil {
		for _, b := range editor {
			if b.Selector == *row.selector {
				item.SourceNodeIDs = append(item.SourceNodeIDs, b.SourceNodeIDs...)
				if b.ObjectHash != item.ObjectHash {
					item.Diagnostics = append(item.Diagnostics, editorProjectionDiagnostic("editor_binding_object_mismatch", "Frozen association object differs from the exact authored object"))
				}
			}
		}
	}
	if verified && row.operationKey != "" && item.Locator.Embedded != nil {
		item.SourceNodeIDs = append(item.SourceNodeIDs, r.editorLinkedSources(c, *item.Locator.Embedded, row.operationKey)...)
	}
	item.SourceNodeIDs = sortedAPIStrings(item.SourceNodeIDs)
	item.SourceNodeIDs = slices.Compact(item.SourceNodeIDs)
	for _, id := range item.SourceNodeIDs {
		if !sources[id] {
			item.Diagnostics = append(item.Diagnostics, ArtifactDiagnostic{Code: "source_node_missing", Severity: "warning", Message: "Frozen source association is orphaned", SourceNodeIDs: []string{id}})
		}
	}
}

type editorProjectionBuilder struct {
	rows               []editorProjectionRow
	diagnostics        []ArtifactDiagnostic
	complete           bool
	reasons            []string
	root               map[string]any
	prefix, embeddedID string
}

func editorProjectionRoot(s *editorOwnerSnapshot, in ArtifactQueryInput) (map[string]any, string, []ArtifactDiagnostic) {
	root := s.tree.root
	if in.EmbeddedContractID == "" {
		return root, "", nil
	}
	value, index, err := editorUniqueID(editorArray(root["contracts"]), in.EmbeddedContractID)
	if err != nil {
		return nil, "", []ArtifactDiagnostic{editorProjectionDiagnostic("embedded_contract_missing", "Explicit embedded owner is missing or ambiguous")}
	}
	root = editorObject(editorObject(value)["document"])
	if root == nil {
		return nil, "", []ArtifactDiagnostic{editorProjectionDiagnostic("embedded_contract_invalid", "Saved embedded document is not an object")}
	}
	return root, fmt.Sprintf("/contracts/%d/document", index), nil
}

func (build *editorProjectionBuilder) appendRow(kind, pointer, label string, owner ArtifactOwnerAddress, selector *EditorSelector, value any) {
	build.rows = append(build.rows, editorProjectionRow{kind: kind, pointer: build.prefix + pointer, label: label, owner: owner, selector: selector, value: value, embeddedID: build.embeddedID})
}
func (build *editorProjectionBuilder) unsupported(pointer string, value any, message string) {
	raw, _ := jsonx.Marshal(value)
	d := ArtifactProjectionData{Kind: "unsupported", Unsupported: &ArtifactUnsupportedData{string(raw), message}}
	build.rows = append(build.rows, editorProjectionRow{kind: "unsupported", pointer: build.prefix + pointer, label: message, value: value, data: &d, embeddedID: build.embeddedID})
	build.diagnostics = append(build.diagnostics, editorProjectionDiagnostic("owner_construct_unsupported", message))
	build.complete = false
}

func (build *editorProjectionBuilder) sequenceRows(r *EditorArtifactRequest, s *editorOwnerSnapshot) error {
	if s.scenarioDecodeErr != nil {
		build.complete = false
		build.diagnostics = append(build.diagnostics, editorProjectionDiagnostic("owner_construct_unsupported", "Saved scenario contains unsupported authored fields; exact rows retain readable raw values"))
	}
	// Resolve fragment endpoints once. Re-scanning all messages for every
	// message/fragment pair would turn the admitted 2000 x 500 model into
	// billions of comparisons, even when only one page is requested.
	messageIndexes := map[string]int{}
	for i, v := range editorArray(build.root["messages"]) {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		id := editorString(editorObject(v)["id"])
		if _, exists := messageIndexes[id]; exists {
			messageIndexes[id] = -1
		} else {
			messageIndexes[id] = i
		}
	}
	messageIndex := func(id string) int {
		if index, ok := messageIndexes[id]; ok {
			return index
		}
		return -1
	}
	for i, v := range editorArray(build.root["participants"]) {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		m := editorObject(v)
		id := editorString(m["id"])
		sel := EditorSelector{Kind: "participant", ParticipantID: id}
		build.appendRow("participant", fmt.Sprintf("/participants/%d", i), editorString(m["name"]), ArtifactOwnerAddress{ParticipantID: id}, &sel, v)
	}
	for i, v := range editorArray(build.root["messages"]) {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		m := editorObject(v)
		id := editorString(m["id"])
		sel := EditorSelector{Kind: "sequence_message", MessageID: id}
		owner := ArtifactOwnerAddress{MessageID: id}
		if err := build.locateSequenceMessage(r, i, messageIndex, &owner); err != nil {
			return err
		}
		build.appendRow("sequence_message", fmt.Sprintf("/messages/%d", i), editorString(m["label"]), owner, &sel, v)
		if op := editorObject(m["operation"]); op != nil {
			row := &build.rows[len(build.rows)-1]
			row.embeddedID = editorString(op["contractId"])
			row.operationKey = editorString(op["operationKey"])
		}
	}
	for i, v := range editorArray(build.root["fragments"]) {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		m := editorObject(v)
		build.appendRow("fragment", fmt.Sprintf("/fragments/%d", i), editorString(m["label"]), ArtifactOwnerAddress{FragmentID: editorString(m["id"])}, nil, v)
	}
	return nil
}

func (build *editorProjectionBuilder) stateRows() {
	env, err := statediagram.Decode(build.root)
	if err != nil {
		build.unsupported("/"+statediagram.Extension, build.root[statediagram.Extension], "Unsupported saved state-diagram extension")
		return
	}
	values := editorArray(editorObject(build.root[statediagram.Extension])["diagrams"])
	for i, d := range env.Diagrams {
		v := values[i]
		base := fmt.Sprintf("/%s/diagrams/%d", statediagram.Extension, i)
		sel := EditorSelector{Kind: "state_diagram", DiagramID: d.ID, EmbeddedContractID: build.embeddedID}
		build.appendRow("state_diagram", base, d.Name, ArtifactOwnerAddress{DiagramID: d.ID}, &sel, v)
		for j, state := range d.States {
			build.appendRow("state", fmt.Sprintf("%s/states/%d", base, j), state.Name, ArtifactOwnerAddress{DiagramID: d.ID, StateID: state.ID}, nil, editorArray(editorObject(v)["states"])[j])
		}
		for j, tr := range d.Transitions {
			sel := EditorSelector{Kind: "state_transition", DiagramID: d.ID, TransitionID: tr.ID, EmbeddedContractID: build.embeddedID}
			build.appendRow("state_transition", fmt.Sprintf("%s/transitions/%d", base, j), tr.Name, ArtifactOwnerAddress{DiagramID: d.ID, TransitionID: tr.ID}, &sel, editorArray(editorObject(v)["transitions"])[j])
		}
	}
}

func (build *editorProjectionBuilder) responseRows() {
	env, err := responserules.Decode(build.root)
	if err != nil {
		build.unsupported("/"+responserules.Extension, build.root[responserules.Extension], "Unsupported saved response-rule extension")
		return
	}
	values := editorArray(editorObject(build.root[responserules.Extension])["rules"])
	for i, rule := range env.Rules {
		v := values[i]
		base := fmt.Sprintf("/%s/rules/%d", responserules.Extension, i)
		sel := EditorSelector{Kind: "response_rule", RuleID: rule.ID, EmbeddedContractID: build.embeddedID}
		build.appendRow("response_rule", base, rule.Name, ArtifactOwnerAddress{RuleID: rule.ID}, &sel, v)
		for j, node := range rule.Nodes {
			sel := EditorSelector{Kind: "response_node", RuleID: rule.ID, NodeID: node.ID, EmbeddedContractID: build.embeddedID}
			build.appendRow("response_node", fmt.Sprintf("%s/nodes/%d", base, j), node.Name, ArtifactOwnerAddress{RuleID: rule.ID, NodeID: node.ID}, &sel, editorArray(editorObject(v)["nodes"])[j])
		}
		for j, edge := range rule.Edges {
			build.appendRow("response_edge", fmt.Sprintf("%s/edges/%d", base, j), edge.ID, ArtifactOwnerAddress{RuleID: rule.ID, EdgeID: edge.ID}, nil, editorArray(editorObject(v)["edges"])[j])
		}
	}
}

func (build *editorProjectionBuilder) eventRows(r *EditorArtifactRequest, s *editorOwnerSnapshot) (bool, error) {
	budget, budgetErr := editorEventPreflight(r.ctx, s)
	if budgetErr != nil {
		build.rows, build.diagnostics, build.reasons = nil, nil, nil
		build.complete = false
		return true, budgetErr
	}
	if budget.exceeded != "" {
		build.diagnostics = []ArtifactDiagnostic{editorProjectionDiagnostic("event_model_construction_budget", "Saved event model exceeds bounded B31 construction admission; inspect its exact saved snapshot or narrow authored event links")}
		build.complete = false
		build.reasons = []string{budget.exceeded}
		return true, nil
	}
	analysis, err := designscenario.AnalyzeEventMap(r.ctx, s.scenario.Document)
	if err != nil {
		build.rows, build.diagnostics, build.reasons = nil, nil, nil
		build.complete = false
		return true, err
	}
	build.complete = analysis.Complete
	build.reasons = slices.Clone(analysis.Coverage.TruncatedReasons)
	for _, diag := range analysis.Diagnostics {
		build.diagnostics = append(build.diagnostics, ArtifactDiagnostic{Code: diag.Code, Severity: diag.Severity, Message: diag.Message, Pointer: diag.Pointer})
	}
	for _, n := range analysis.Nodes {
		loc := editorEventLocator(n.Locator)
		data := ArtifactProjectionData{Kind: "event_node", EventNode: &ArtifactEventMapNode{ID: n.ID, Kind: n.Kind, Label: n.Label, Locator: loc, GroupID: n.GroupID, ClientID: n.ClientID}}
		row := editorProjectionRow{kind: "event_node", identity: n.ID, pointer: n.Locator.Pointer, label: n.Label, value: n, data: &data, owner: ArtifactOwnerAddress{EntityID: n.Locator.EntityID, EventMap: &loc}, embeddedID: n.Locator.HTTPContractID, operationKey: n.Locator.OperationKey}
		authoredKind := ""
		switch n.Kind {
		case "server":
			authoredKind = "event_server"
			row.owner.ServerID = n.Locator.EntityID
		case "schema":
			authoredKind = "event_schema"
			row.owner.SchemaID = n.Locator.EntityID
		case "api_operation":
			authoredKind = "api_operation"
			row.owner.OperationKey = n.Locator.OperationKey
		case "participant":
			row.selector = &EditorSelector{Kind: "participant", ParticipantID: n.Locator.ParticipantID}
		case "channel":
			row.selector = &EditorSelector{Kind: "event_channel", ChannelID: n.Locator.EntityID}
		case "message":
			row.selector = &EditorSelector{Kind: "event_message", MessageID: n.Locator.EntityID}
		case "operation":
			row.selector = &EditorSelector{Kind: "event_operation", ContractID: n.Locator.ContractID, OperationID: n.Locator.OperationID}
		case "state_transition":
			row.selector = &EditorSelector{Kind: "state_transition", DiagramID: n.Locator.DiagramID, TransitionID: n.Locator.TransitionID, EmbeddedContractID: n.Locator.HTTPContractID}
		}
		if row.selector != nil {
			pointer, _, owner, value, resolveErr := resolveEditorValueForEvent(s, *row.selector)
			if resolveErr == nil {
				row.value = value
				row.owner = owner
				row.owner.EventMap = &loc
				row.pointer = pointer
			} else {
				row.selector = nil
				build.diagnostics = append(build.diagnostics, editorProjectionDiagnostic("event_binding_object_unavailable", "Readable event-map node has no unambiguous bindable authored object"))
			}
		}
		if authoredKind != "" {
			value, valueErr := editorAt(s.tree.root, row.pointer)
			if valueErr != nil {
				build.complete = false
				build.diagnostics = append(build.diagnostics, editorProjectionDiagnostic("event_authored_object_unavailable", "Readable event-map node has no exact authored value at its saved pointer"))
				row.value = nil
				authoredKind = ""
			} else {
				row.value = value
			}
		}
		build.rows = append(build.rows, row)
		if authoredKind != "" {
			// Keep the graph row and its typed metadata. A separate readable
			// authored row uses the existing data union, without binding eligibility.
			authored := row
			authored.kind, authored.data = authoredKind, nil
			build.rows = append(build.rows, authored)
		}
	}
	for _, e := range analysis.Edges {
		loc := editorEventLocator(e.Locator)
		data := ArtifactProjectionData{Kind: "event_edge", EventEdge: &ArtifactEventMapEdge{ID: e.ID, Kind: e.Kind, Source: e.Source, Target: e.Target, Label: e.Label, Locator: loc}}
		build.rows = append(build.rows, editorProjectionRow{kind: "event_edge", identity: e.ID, pointer: e.Locator.Pointer, label: e.Label, value: e, data: &data, owner: ArtifactOwnerAddress{EdgeID: e.ID, EventMap: &loc}, embeddedID: e.Locator.HTTPContractID})
	}
	return false, nil
}

func (build *editorProjectionBuilder) locateSequenceMessage(r *EditorArtifactRequest, i int, messageIndex func(string) int, owner *ArtifactOwnerAddress) error {
	// Range membership is authored by fragments/branches. The last matching
	// authored context provides navigation; fragment rows retain all nesting.
	for _, f := range editorArray(build.root["fragments"]) {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		fm := editorObject(f)
		a, b := messageIndex(editorString(fm["fromMessageId"])), messageIndex(editorString(fm["toMessageId"]))
		if a >= 0 && a <= i && i <= b {
			owner.FragmentID = editorString(fm["id"])
			owner.BranchID = ""
			for _, branch := range editorArray(fm["branches"]) {
				bm := editorObject(branch)
				a, b := messageIndex(editorString(bm["fromMessageId"])), messageIndex(editorString(bm["toMessageId"]))
				if a >= 0 && a <= i && i <= b {
					owner.BranchID = editorString(bm["id"])
				}
			}
		}
	}
	return nil
}
