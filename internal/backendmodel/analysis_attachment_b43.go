package backendmodel

import "context"

func (r *Repo) AnalysisArtifactRequest(ctx context.Context, pid string) (*EditorArtifactRequest, error) {
	lease := analysisLease(ctx)
	if lease == nil {
		return nil, invalid("analysisInput", "Artifact evidence requires reserved input")
	}
	if err := lease.valid(r, pid); err != nil {
		return nil, err
	}
	return r.changeArtifactRequest(ctx, &changeReadBudget{repo: r, pid: pid, lease: lease}), nil
}
func (r *Repo) ReadAnalysisAttachmentEvidence(ctx context.Context, pid string, attachment TestAttachmentRef) (*AnalysisAttachmentEvidence, error) {
	if err := attachment.Validate(); err != nil {
		return nil, err
	}
	out := &AnalysisAttachmentEvidence{Attachment: attachment, Reason: "Exact immutable locator is unavailable", Pins: []AnalysisEvidenceDocumentPin{}}
	if attachment.Kind == "source" {
		return r.readSourceAttachmentEvidence(ctx, pid, attachment, out)
	}
	if attachment.Kind == "artifact_v3" && attachment.NamespacedArtifact.Namespace.Scope == "foreign" {
		out.Reason = "Imported foreign attachment is unresolved; no local owner lookup"
		return out, nil
	}
	request, err := r.AnalysisArtifactRequest(ctx, pid)
	if err != nil {
		return nil, err
	}
	if attachment.Kind == "artifact_v3" {
		installation, err := r.InstallationID(ctx)
		if err != nil {
			return nil, err
		}
		pin, err := request.ResolveNamespacedPin(installation, *attachment.NamespacedArtifact)
		if err != nil {
			return out, err
		}
		attachment.Artifact = &pin
	}
	pin, err := request.SnapshotPin(artifactKey(*attachment.Artifact), attachment.Artifact.RevisionID)
	if err != nil {
		return out, err
	}
	if pin == *attachment.Artifact && attachment.JSONPointer == "" {
		out.Verified = true
		out.Reason = "Exact immutable design scenario root; execution is unverified"
	}
	return out, nil
}

// readSourceAttachmentEvidence verifies a source-file attachment against the
// analyzed files of its pinned revision and pins the revision's decision and
// coverage documents it was judged against.
func (r *Repo) readSourceAttachmentEvidence(ctx context.Context, pid string, attachment TestAttachmentRef, out *AnalysisAttachmentEvidence) (*AnalysisAttachmentEvidence, error) {
	g, err := r.ResolveEffectiveGraph(ctx, pid, BackendReadTarget{RevisionID: attachment.RevisionID})
	if err != nil {
		return out, err
	}
	for _, snapshot := range g.State.Sources {
		if snapshot.ID != attachment.SnapshotID || snapshot.RepositoryID != attachment.RepositoryID {
			continue
		}
		for _, file := range snapshot.Files {
			if file.Path == attachment.File && file.ContentHash == attachment.ContentHash && file.AnalysisStatus == "analyzed" {
				out.Verified = true
				out.Reason = "Exact analyzed immutable file locator; execution is unverified"
			}
		}
	}
	for _, kind := range []string{"revision_decisions", "revision_coverage"} {
		raw, err := r.readAnalysisEvidenceDocument(ctx, pid, attachment.RevisionID, kind)
		if err != nil {
			return nil, err
		}
		if raw != nil {
			out.Pins = append(out.Pins, analysisPin(raw, attachment.RevisionID, kind))
		}
	}
	return out, nil
}
