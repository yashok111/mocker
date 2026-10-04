package backendmodel

import "context"

// AppendAnalysisSourceClaimDeltas appends qualified source-claim changes to delta
// and sorts its changes using the exact before/after source snapshots. It leaves
// both snapshots unchanged and exposes the existing comparison to analysis;
// it does not change the public API or its codecs.
func AppendAnalysisSourceClaimDeltas(ctx context.Context, delta *RevisionDelta, before, after *SourceGraphSnapshot) error {
	return appendSourceClaimDeltas(ctx, delta, before, after)
}
