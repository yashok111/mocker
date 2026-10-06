package backendmodel

import (
	"context"
	"fmt"
	"sync"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
)

const MaxEditorArtifactSnapshots = 20

type editorSnapshotKey struct {
	ArtifactKey
	revision string
}
type editorOwnerSnapshot struct {
	api               *apidesign.ArtifactSnapshot
	scenario          *designscenario.ArtifactSnapshot
	inspection        *designscenario.ArtifactInspectionSnapshot
	scenarioDecodeErr error
	tree              *editorRawTree
	objects           map[APIArtifactSelector]*apidesign.ArtifactObject
	objectErrors      map[APIArtifactSelector]error
	err               error
}

// EditorArtifactRequest owns one immutable request scope and a shared budget.
// Construct it once for all old/new/top/nested reads. Failed distinct attempts
// consume budget; repeated identities reuse both the snapshot and lossless tree.
// No cache survives the request. Methods are safe to call concurrently.
type EditorArtifactRequest struct {
	effective *EffectiveGraphSnapshot
	ctx       context.Context
	api       APIArtifactReader
	scenario  ScenarioArtifactReader
	mu        sync.Mutex
	snapshots map[editorSnapshotKey]*editorOwnerSnapshot
}

func NewEditorArtifactRequest(ctx context.Context, api APIArtifactReader, scenario ScenarioArtifactReader) *EditorArtifactRequest {
	return &EditorArtifactRequest{ctx: ctx, api: api, scenario: scenario, snapshots: map[editorSnapshotKey]*editorOwnerSnapshot{}}
}
func editorSnapshotLimit() error {
	return &FaultError{Status: 413, Code: "backend_artifact_work_limit", Message: "Artifact request exceeds 20 distinct immutable snapshots", Details: map[string]any{"allowedSnapshots": MaxEditorArtifactSnapshots}}
}
func (r *EditorArtifactRequest) snapshot(key ArtifactKey, revision string) (*editorOwnerSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if !ValidAPIArtifactID(revision) {
		return nil, invalid("revisionId", "Expected exact positive revision")
	}
	k := editorSnapshotKey{key, revision}
	if s := r.snapshots[k]; s != nil {
		return s, s.err
	}
	if len(r.snapshots) >= MaxEditorArtifactSnapshots {
		return nil, editorSnapshotLimit()
	}
	s := &editorOwnerSnapshot{}
	r.snapshots[k] = s
	if key.Kind == "api_design" {
		r.readAPISnapshot(s, key, revision)
	} else if !r.readScenarioSnapshot(s, key, revision) {
		return s, s.err
	}
	if err := r.ctx.Err(); err != nil {
		s.err = err
	}
	return s, s.err
}

// SnapshotPin resolves only the requested owner, without expanding embedded
// origins. Projection-only scenario pins can therefore exceed 20 saved origins.
func (r *EditorArtifactRequest) SnapshotPin(key ArtifactKey, revision string) (ArtifactPin, error) {
	s, err := r.snapshot(key, revision)
	if err != nil {
		return ArtifactPin{}, err
	}
	if !editorOwnerVerified(s) {
		return ArtifactPin{}, invalid("artifact", "Unverified scenario envelope cannot be pinned")
	}
	var hash string
	if s.api != nil {
		hash = s.api.ContentHash
	} else {
		hash = s.scenario.ContentHash
	}
	if !validHash(hash) {
		return ArtifactPin{}, invalid("contentHash", "Invalid owner digest")
	}
	return ArtifactPin{Kind: key.Kind, ID: key.ID, RevisionID: revision, ContentHash: hash}, nil
}

func editorOwnerVerified(s *editorOwnerSnapshot) bool {
	return s.scenario == nil || (s.inspection != nil && s.inspection.EnvelopeVerification == "verified" && s.scenarioDecodeErr == nil)
}
func (r *EditorArtifactRequest) pinnedSnapshot(pin ArtifactPin) (*editorOwnerSnapshot, error) {
	s, err := r.snapshot(ArtifactKey{pin.Kind, pin.ID}, pin.RevisionID)
	if err != nil {
		return nil, err
	}
	var digest string
	if s.api != nil {
		digest = s.api.ContentHash
	} else {
		digest = s.scenario.ContentHash
	}
	if digest != pin.ContentHash {
		return nil, invalid("contentHash", "Selected immutable owner differs from frozen pin")
	}
	return s, nil
}

// ResolveAPIObject shares the exact snapshot work budget with editor resolution.
// The B24 owner resolver retains its existing object hash/pointer policy.
func (r *EditorArtifactRequest) ResolveAPIObject(pin ArtifactPin, selector APIArtifactSelector) (*apidesign.ArtifactObject, error) {
	if pin.Kind != "api_design" {
		return nil, invalid("artifact", "API object requires API owner")
	}
	if err := selector.Validate(); err != nil {
		return nil, err
	}
	s, err := r.pinnedSnapshot(pin)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if s.objects == nil {
		s.objects = map[APIArtifactSelector]*apidesign.ArtifactObject{}
		s.objectErrors = map[APIArtifactSelector]error{}
	}
	if object, ok := s.objects[selector]; ok {
		if object == nil {
			return nil, s.objectErrors[selector]
		}
		snapshotCopy := *object
		return &snapshotCopy, nil
	}
	object, err := apidesign.ResolveArtifactObject(r.ctx, s.api, apidesign.ArtifactSelector{ObjectKey: selector.ObjectKey, JSONPointer: selector.JSONPointer})
	if r.ctx.Err() != nil {
		return nil, r.ctx.Err()
	}
	s.objects[selector], s.objectErrors[selector] = object, err
	if err != nil {
		return nil, err
	}
	snapshotCopy := *object
	return &snapshotCopy, nil
}

func (r *EditorArtifactRequest) readAPISnapshot(s *editorOwnerSnapshot, key ArtifactKey, revision string) {
	if r.api == nil {
		s.err = editorReaderUnavailable("API")
	} else {
		s.api, s.err = r.api.ArtifactSnapshot(r.ctx, apiArtifactID(key.ID), apiArtifactID(revision))
		if s.err == nil && (s.api == nil || s.api.DesignID != apiArtifactID(key.ID) || s.api.RevisionID != apiArtifactID(revision)) {
			s.err = invalid("snapshot", "Owner returned a different exact API scope")
		}
		if s.err == nil {
			snapshotCopy := *s.api
			s.api = &snapshotCopy
			s.tree, s.err = newEditorRawTree(s.api.Document)
		}
	}
}

func (r *EditorArtifactRequest) readScenarioSnapshot(s *editorOwnerSnapshot, key ArtifactKey, revision string) bool {
	if r.scenario == nil {
		s.err = editorReaderUnavailable("Scenario")
	} else {
		s.inspection, s.err = r.scenario.ArtifactInspectionSnapshot(r.ctx, apiArtifactID(key.ID), apiArtifactID(revision))
		if s.err == nil && (s.inspection == nil || s.inspection.ScenarioID != apiArtifactID(key.ID) || s.inspection.RevisionID != apiArtifactID(revision)) {
			s.err = invalid("snapshot", "Owner returned a different exact scenario scope")
		}
		if s.err == nil {
			snapshotCopy := *s.inspection
			s.inspection = &snapshotCopy
			if !validEditorInspection(snapshotCopy) {
				s.err = invalid("inspection", "Invalid owner verification status")
				return false
			}
			s.scenario = &designscenario.ArtifactSnapshot{ScenarioID: snapshotCopy.ScenarioID, RevisionID: snapshotCopy.RevisionID, Version: snapshotCopy.Version, ContentHash: snapshotCopy.StoredContentHash, DocumentHash: snapshotCopy.DocumentHash, DocumentJSON: snapshotCopy.DocumentJSON, FormDraftsJSON: snapshotCopy.FormDraftsJSON}
			s.tree, s.err = newEditorRawTree(s.scenario.DocumentJSON)
			// Decode from the saved raw document so external mutable typed slices are
			// neither trusted as another scope nor shared with the request cache.
			if s.err == nil {
				s.scenarioDecodeErr = decodeEditorRaw([]byte(s.scenario.DocumentJSON), &s.scenario.Document)
			}
		}
	}
	return true
}

// Keep owner names in the existing diagnostic vocabulary.
func editorReaderUnavailable(owner string) error {
	return fmt.Errorf("%s artifact reader unavailable", owner)
}

func validEditorInspection(snapshot designscenario.ArtifactInspectionSnapshot) bool {
	supported := snapshot.TypedStatus == "supported" && snapshot.EnvelopeVerification == "verified" && snapshot.ContentHash == snapshot.StoredContentHash
	unsupported := snapshot.TypedStatus == "unsupported" && snapshot.EnvelopeVerification == "unavailable" && snapshot.ContentHash == ""
	return validHash(snapshot.StoredContentHash) && validHash(snapshot.DocumentHash) && (supported || unsupported)
}

// ResolveNamespacedPin gates the lookup before touching either owner adapter.
func (r *EditorArtifactRequest) ResolveNamespacedPin(installationID string, scoped NamespacedArtifactPin) (ArtifactPin, error) {
	pin, err := scoped.LocalPin(installationID)
	if err != nil {
		return ArtifactPin{}, err
	}
	actual, err := r.SnapshotPin(ArtifactKey{Kind: pin.Kind, ID: pin.ID}, pin.RevisionID)
	if err != nil {
		return ArtifactPin{}, err
	}
	if actual != pin {
		return ArtifactPin{}, invalid("contentHash", "Exact owner hash differs from namespaced pin")
	}
	return actual, nil
}
