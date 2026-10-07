package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
)

func (r *Repo) changeArtifactRequest(ctx context.Context, readBudget *changeReadBudget) *EditorArtifactRequest {
	// Owner snapshot adapters require a read budget. The same immutable input
	// cap applies to all proposal materialization; this opens no new connection.
	budget := &config.Config{MaxBody: MaxRevisionBytes}
	api := apidesign.NewRepo(r.db, budget)
	scenarios := designscenario.NewRepo(r.db, budget, api)
	return NewEditorArtifactRequest(ctx, changeAPIArtifactReader{APIArtifactReader: api, budget: readBudget}, changeScenarioArtifactReader{ScenarioArtifactReader: scenarios, budget: readBudget})
}
func (r *Repo) prepareChangeArtifacts(ctx context.Context, e *changeEvaluation, commands []ChangeProposalCommand) error {
	e.artifactRequest = r.changeArtifactRequest(ctx, e.readBudget)
	return prepareChangeArtifactCommands(ctx, e.artifactRequest, e, commands)
}
func prepareChangeArtifactCommands(ctx context.Context, request *EditorArtifactRequest, e *changeEvaluation, commands []ChangeProposalCommand) error {
	nodes := map[string]Node{}
	for id, r := range e.records {
		if r.RecordType == "node" {
			nodes[id] = Node{ID: id, Kind: r.Payload.Kind, Name: r.Payload.Name, ParentID: r.Payload.ParentID, Attributes: r.Payload.Attributes}
		}
	}
	for _, c := range commands {
		if c.Type != "set_artifact_pin" && c.Type != "remove_artifact_pin" {
			continue
		}
		if e.revision.ArtifactContextV3 != nil {
			return invalid("context", "Legacy artifact commands cannot mutate a namespaced context")
		}
		key := *c.Artifact
		pins := slices.DeleteFunc(slices.Clone(e.revision.ArtifactPins), func(p ArtifactPin) bool { return artifactKey(p) == key })
		context := e.revision.ArtifactContext
		context.APIBindings = slices.DeleteFunc(slices.Clone(context.APIBindings), func(b APIArtifactBinding) bool { return b.Ref.Kind == key.Kind && b.Ref.ArtifactID == key.ID })
		context.EditorBindings = slices.DeleteFunc(slices.Clone(context.EditorBindings), func(b EditorBinding) bool { return b.ArtifactKind == key.Kind && b.ArtifactID == key.ID })
		if c.Type == "set_artifact_pin" {
			pin, err := request.SnapshotPin(key, c.RevisionID)
			if e := requiredArtifactError(ctx, err); e != nil {
				return e
			}
			out := &ArtifactPinsPreview{CanApply: true, Diagnostics: []ArtifactDiagnostic{}}
			builder := artifactPreviewBuilder{out: out, request: request, nodes: nodes}
			if err != nil {
				// Review 2026-10-06, F174: a missing artifact revision came back
				// as the read budget's raw sql.ErrNoRows, a logged 500. Classify
				// it as the legacy artifact-pins preview does: cancellation and
				// 413 stay as they are, anything else blocks the pin.
				builder.diagnostic("backend_artifact_target_unavailable", key, nil, nil, "Selected immutable artifact is unavailable or unverified", true)
				return artifactPinsBlocked(out.Diagnostics)
			}
			api, err := builder.resolveAPIBindings(ctx, c.artifactCommand(), pin)
			if err != nil {
				return err
			}
			editor, err := builder.resolveEditorBindings(ctx, c.artifactCommand(), pin)
			if err != nil {
				return err
			}
			if !out.CanApply {
				return artifactPinsBlocked(out.Diagnostics)
			}
			pins = append(pins, pin)
			context.APIBindings = append(context.APIBindings, api...)
			context.EditorBindings = append(context.EditorBindings, editor...)
		}
		if err := ValidateArtifactVector(pins, context.APIBindings, context.EditorBindings); err != nil {
			return err
		}
		sortArtifactBindings(context.APIBindings)
		var err error
		context.EditorBindings, err = CanonicalEditorBindings(context.EditorBindings)
		if err != nil {
			return err
		}
		pins = canonicalAPIPins(pins)
		if ArtifactContextUsesV1(pins, context) {
			context.DocumentVersion = ""
		} else {
			context.DocumentVersion = EditorArtifactDocumentVersion
		}
		if _, err = EncodeArtifactContext(context, pins); err != nil {
			return err
		}
		e.revision.ArtifactPins, e.revision.ArtifactContext = pins, context
		intent := ChangeArtifactIntent{Artifact: key, Removed: c.Type == "remove_artifact_pin", Origin: EffectiveOrigin{Kind: "intent", CommandID: c.CommandID, Reason: c.Reason}}
		index := slices.IndexFunc(e.revision.Delta.ArtifactIntents, func(i ChangeArtifactIntent) bool { return i.Artifact == key })
		if index < 0 {
			e.revision.Delta.ArtifactIntents = append(e.revision.Delta.ArtifactIntents, intent)
		} else {
			e.revision.Delta.ArtifactIntents[index] = intent
		}
	}
	return nil
}

type changeCriteriaReader struct {
	q         importReader
	pid       string
	e         *changeEvaluation
	artifacts *EditorArtifactRequest
}

func (r *Repo) validateChangeCriteriaReferences(ctx context.Context, q importReader, pid string, e *changeEvaluation) error {
	if e.artifactRequest == nil {
		e.artifactRequest = r.changeArtifactRequest(ctx, e.readBudget)
	}
	reader := changeCriteriaReader{q: q, pid: pid, e: e, artifacts: e.artifactRequest}
	for _, criterion := range e.revision.Criteria {
		if err := criterion.Validate(); err != nil {
			return err
		}
		if err := reader.validate(ctx, criterion); err != nil {
			return err
		}
	}
	return nil
}
func (r changeCriteriaReader) validate(ctx context.Context, c ChangeCriterion) error {
	switch c.Kind {
	case "object_exists", "object_absent":
		tracked, ok := r.e.used[c.ID]
		if !ok || tracked.RecordType != c.RecordType || tracked.Kind != c.ObjectKind {
			return invalid("criteria", "Criterion must target a tracked exact object kind")
		}
	case "edge_exists":
		edge, err := r.e.live("edge", c.ID, c.EdgeKind)
		if err != nil {
			return err
		}
		if edge.Payload.From != c.From || edge.Payload.To != c.To {
			return invalid("criteria", "Exact edge endpoints do not match")
		}
	case "field_equals":
		return r.field(c)
	case "artifact_object_matches":
		return r.artifact(c)
	case "artifact_object_matches_v3":
		scoped := c.NamespacedArtifact
		if scoped == nil || r.e.revision.ArtifactContextV3 == nil {
			return invalid("criteria", "Exact namespaced artifact context required")
		}
		found := false
		for _, g := range r.e.revision.ArtifactContextV3.Groups {
			if g.Namespace == scoped.Namespace && slices.Contains(g.Pins, scoped.Pin) {
				found = true
			}
		}
		if !found {
			return invalid("criteria", "Namespaced criterion is outside its target")
		}
		if scoped.Namespace.Scope == "foreign" {
			return nil
		}
		installation, err := installationID(ctx, r.q)
		if err != nil {
			return err
		}
		pin, err := r.artifacts.ResolveNamespacedPin(installation, *scoped)
		if err != nil {
			return err
		}
		c.Artifact = &pin
		return r.artifactObject(c)
	case "test_attachment", "runtime_check":
		return r.check(ctx, c)
	}
	return nil
}
func (r changeCriteriaReader) field(c ChangeCriterion) error {
	record, err := r.e.live(c.RecordType, c.ID, "")
	if err != nil {
		return err
	}
	var selector EffectivePropertySelector
	if err = json.Unmarshal(c.Selector, &selector); err != nil {
		return err
	}
	if selector.Kind == "source" {
		return validateChangeSourceExpected(r.e.revision.BaseSchemaVersion, record.Payload, *selector.Source, *c.Expected)
	}
	return r.proposalField(c, selector)
}
func (r changeCriteriaReader) artifact(c ChangeCriterion) error {
	if !slices.Contains(r.e.revision.ArtifactPins, *c.Artifact) {
		return invalid("criteria/artifact", "Criterion artifact is outside the selected exact context")
	}
	return r.artifactObject(c)
}
func (r changeCriteriaReader) artifactObject(c ChangeCriterion) error {
	if c.Artifact.Kind == "api_design" {
		var selector APIArtifactSelector
		if err := json.Unmarshal(c.Selector, &selector); err != nil {
			return err
		}
		_, err := r.artifacts.ResolveAPIObject(*c.Artifact, selector)
		return err
	}
	var selector EditorSelector
	if err := json.Unmarshal(c.Selector, &selector); err != nil {
		return err
	}
	_, err := r.artifacts.ResolveObject(*c.Artifact, selector)
	return err
}
func (r changeCriteriaReader) check(ctx context.Context, c ChangeCriterion) error {
	for _, id := range c.TargetIDs {
		if _, ok := r.e.used[id]; !ok {
			return invalid("criteria/targetIds", "Criterion target is not tracked by this proposal")
		}
	}
	if c.Kind == "runtime_check" {
		return nil
	}
	attachment := c.Attachment
	if attachment.Kind == "artifact_v3" {
		if attachment.NamespacedArtifact.Namespace.Scope == "foreign" {
			return nil
		}
		installation, err := installationID(ctx, r.q)
		if err != nil {
			return err
		}
		_, err = r.artifacts.ResolveNamespacedPin(installation, *attachment.NamespacedArtifact)
		return err
	}
	if attachment.Kind == "artifact" {
		actual, err := r.artifacts.SnapshotPin(artifactKey(*attachment.Artifact), attachment.Artifact.RevisionID)
		if err != nil {
			return err
		}
		if actual != *attachment.Artifact {
			return invalid("attachment", "Authored scenario hash differs from exact immutable owner")
		}
		return nil
	}
	source, err := r.e.referencedSource(ctx, r.q, r.pid, attachment.RevisionID)
	if err != nil {
		return err
	}
	for _, snapshot := range source.State.Sources {
		if snapshot.ID != attachment.SnapshotID || snapshot.RepositoryID != attachment.RepositoryID {
			continue
		}
		for _, file := range snapshot.Files {
			if file.Path == attachment.File && file.ContentHash == attachment.ContentHash && file.AnalysisStatus == "analyzed" {
				return nil
			}
		}
	}
	return invalid("attachment", "Source locator must match an analyzed file in the exact owned snapshot")
}

func (e *changeEvaluation) checkArtifactDigests(ctx context.Context, tx *sql.Tx, version int64) error {
	if e.artifactRequest == nil {
		return nil
	}
	request := e.artifactRequest
	digests := map[editorSnapshotKey]string{}
	request.mu.Lock()
	for key, snapshot := range request.snapshots {
		if snapshot.err != nil || !editorOwnerVerified(snapshot) {
			continue
		}
		if snapshot.api != nil {
			digests[key] = snapshot.api.ContentHash
		} else {
			digests[key] = snapshot.inspection.ContentHash
		}
	}
	request.mu.Unlock()
	service := ArtifactService{api: request.api, scenarios: request.scenario}
	return service.checkArtifactDigests(ctx, tx, digests, version)
}
