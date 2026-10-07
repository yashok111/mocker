package backendmodel

import (
	"context"
	"errors"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/specs"
)

func requiredArtifactError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, specs.ErrTooLarge) || errors.Is(err, designscenario.ErrTooLarge) {
		return artifactPinsLimit()
	}
	return fatalArtifactError(ctx, err)
}

// ArtifactService changes only the backend's immutable artifact associations.
type ArtifactService struct {
	repo               *Repo
	api                APIArtifactReader
	scenarios          ScenarioArtifactReader
	maxApplyBodyBytes  int64
	globalMaxBodyBytes *int64
}

func NewArtifactService(repo *Repo, api APIArtifactReader, scenarios ScenarioArtifactReader) *ArtifactService {
	return &ArtifactService{repo: repo, api: api, scenarios: scenarios, maxApplyBodyBytes: MaxAPIPinBodyBytes}
}

// NewArtifactServiceWithBodyLimit fixes generic admission at the global raw-body
// limit, including required RPC framing. The original constructor keeps its
// 128KiB default without a global constraint; private legacy prepare is unchanged.
func NewArtifactServiceWithBodyLimit(repo *Repo, api APIArtifactReader, scenarios ScenarioArtifactReader, globalMaxBodyBytes int64) *ArtifactService {
	allowed := int64(0)
	if globalMaxBodyBytes > ArtifactApplyRPCFramingBytes {
		allowed = min(globalMaxBodyBytes-ArtifactApplyRPCFramingBytes, int64(MaxAPIPinBodyBytes))
	}
	return &ArtifactService{repo: repo, api: api, scenarios: scenarios, maxApplyBodyBytes: allowed, globalMaxBodyBytes: new(globalMaxBodyBytes)}
}

func (s *ArtifactService) Query(ctx context.Context, pid string, in ArtifactQueryInput) (*ArtifactProjectionPage, error) {
	if in.Proposal != nil || in.ChangeProposal != nil || in.ImportCandidate != nil {
		return s.queryEffectiveArtifact(ctx, pid, in)
	}
	if in.RevisionID != "" && in.Proposal == nil && in.ChangeProposal == nil {
		revision, err := s.repo.Revision(ctx, pid, in.RevisionID)
		if err != nil {
			return nil, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return s.queryEffectiveArtifact(ctx, pid, in)
		}
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, s.repo.db.R, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	// The same refusal the composed path gives: a v3 head is not "no pins"
	// (review 2026-10-06, F60).
	if state.ArtifactContextV3 != nil {
		return nil, invalid("context", "Use an explicit namespaced artifact resolver for v3")
	}
	if state.ArtifactContext == nil {
		return nil, notFound()
	}
	return NewEditorArtifactRequest(ctx, s.api, s.scenarios).Project(state, *state.ArtifactContext, in)
}

func (s *ArtifactService) Preview(ctx context.Context, pid string, in PreviewArtifactPinsInput) (*ArtifactPinsPreview, error) {
	if err := s.admitApplyBody(pid, in); err != nil {
		return nil, err
	}
	p, err := s.prepareArtifacts(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	return p.preview, nil
}
