package backendanalysis

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// Budgets for the graph- and revision-sized manifest fields. Together with the
// 16 KiB scope (maxScopeBytes) and the half that trackChange reserves for
// changedIds/coveredChangedIds they stay inside maxManifestBytes. They apply
// only to a manifest that would not fit otherwise, so every manifest that was
// storable before keeps its bytes and its semanticResultHash. 16 (scope) + 32
// (changed ids) + 8 (gaps) + 2*2 (coverage) KiB leave the fixed fields about
// 4 KiB; the diagram scope only appears on kinds that track no changed ids.
const (
	manifestGapBytes          = 8 << 10
	manifestCoverageBytes     = 2 << 10 // per side
	manifestDiagramScopeBytes = 4 << 10
)

// fitManifest summarizes the manifest fields whose size the caller's request
// does not bound. A legitimate diagnostics run over a few hundred partially
// covered tables (one gap per unknown check), a source6 revision with a few
// hundred stale assertions (one coverage gap each, copied for both sides) and
// a diagram scope of a few hundred identities each outgrew the 64 KiB manifest,
// so the job could only end failed (review 2026-10-06, F135/F189/F190). Nothing
// is lost: every gap is a record of the gaps section, the revision keeps its
// coverage and the immutable input (get_backend_analysis) keeps the scope.
// Pure and deterministic, so a retried publication rebuilds identical bytes.
func fitManifest(m ResultManifest) ResultManifest {
	m.SourceCoverageBefore = fitCoverage(m.SourceCoverageBefore)
	m.SourceCoverageAfter = fitCoverage(m.SourceCoverageAfter)
	m.DiagramScope = fitDiagramScope(m.DiagramScope)
	m.Gaps, m.TruncationReasons = fitGaps(m.Gaps, m.TruncationReasons)
	return m
}

func encodedLen(v any) int {
	raw, err := canonical(v)
	if err != nil {
		return maxManifestBytes
	}
	return len(raw) + 1
}

// The ready gate acknowledges gaps by id, so a gap missing from the manifest
// could slip past it. The summary is therefore a truncation reason, which the
// gate refuses outright; traversal completeness is a separate fact and keeps
// its value.
func fitGaps(gaps, truncations []Diagnostic) ([]Diagnostic, []Diagnostic) {
	used, kept := 0, 0
	for kept < len(gaps) {
		next := encodedLen(gaps[kept])
		if used+next > manifestGapBytes {
			break
		}
		used += next
		kept++
	}
	if kept == len(gaps) {
		return gaps, truncations
	}
	const code = "manifest_gap_limit"
	reason := Diagnostic{ID: code, Code: code, Message: fmt.Sprintf("%d of %d gaps exceed the manifest bound and are listed only in the gaps section", len(gaps)-kept, len(gaps))}
	out := slices.DeleteFunc(slices.Clone(truncations), func(d Diagnostic) bool { return d.ID == code })
	out = append(out, reason)
	slices.SortFunc(out, func(a, b Diagnostic) int { return cmp.Compare(a.ID, b.ID) })
	return slices.Clone(gaps[:kept]), out
}

// The coverage gap list is informational (the gate reads the analysis gaps,
// not these strings), so its summary is a trailing marker, not a truncation.
func fitCoverage(c backendmodel.Coverage) backendmodel.Coverage {
	if encodedLen(c.Gaps) <= manifestCoverageBytes {
		return c
	}
	used, kept := 0, 0
	for kept < len(c.Gaps) && used+len(c.Gaps[kept])+3 <= manifestCoverageBytes-256 {
		used += len(c.Gaps[kept]) + 3
		kept++
	}
	gaps := slices.Clone(c.Gaps[:kept])
	c.Gaps = append(gaps, fmt.Sprintf("manifest_summarized: %d more coverage gaps exceed the manifest bound; the revision's coverage lists them all", len(c.Gaps)-kept))
	return c
}

// The scope hash pins the exact resolved scope; the job input keeps it whole.
func fitDiagramScope(s *backendmodel.DiagramScope) *backendmodel.DiagramScope {
	if s == nil || encodedLen(s) <= manifestDiagramScopeBytes {
		return s
	}
	out := *s
	out.Selectors, out.SourceRefs, out.Members = []backendmodel.DiagramScopeSelector{}, []backendmodel.DiagramRef{}, []backendmodel.DiagramMember{}
	out.Gaps = []backendmodel.DiagramGap{{ID: "manifest_summarized", SubjectID: s.Pin.ID, Code: "manifest_summarized", Explanation: fmt.Sprintf("%d selectors, %d members, %d source refs and %d gaps exceed the manifest bound; scopeHash pins them and the job input keeps them", len(s.Selectors), len(s.Members), len(s.SourceRefs), len(s.Gaps))}}
	return &out
}
