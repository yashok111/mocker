package backendanalysis

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// Review 2026-10-06, F135/F189/F190: a legitimate run whose gaps, revision
// coverage gaps or resolved diagram scope outgrew the 64 KiB manifest ended
// failed (result_unpersistable since 2e6cc4d, a server stop before). The
// manifest now carries a bounded summary; the full lists stay in the gaps
// section, the revision and the immutable input.

func fitTestSnapshot(m ResultManifest) PreparedSnapshot {
	m.JobID, m.AnalysisInputHash, m.ResultVersion, m.Verdict = "job", "input", 1, "unknown"
	if m.ChangedIDs == nil {
		m.ChangedIDs = []ObjectAddress{}
	}
	if m.CoveredChangedIDs == nil {
		m.CoveredChangedIDs = []ObjectAddress{}
	}
	if m.TruncationReasons == nil {
		m.TruncationReasons = []Diagnostic{}
	}
	return PreparedSnapshot{Manifest: m}
}

func manyGaps(n int) []Diagnostic {
	gaps := make([]Diagnostic, n)
	for i := range gaps {
		code := fmt.Sprintf("unused_table:%036d", i)
		gaps[i] = Diagnostic{ID: code, Code: code, Message: "Static analysis cannot confirm this dependency or check", Objects: []ObjectAddress{{RecordType: "diagnostic", ID: code}}}
	}
	return gaps
}

func TestPrepareSnapshotSummarizesGapsBeyondManifestBound(t *testing.T) {
	s, err := prepareSnapshot(fitTestSnapshot(ResultManifest{Complete: true, Gaps: manyGaps(400)}))
	if err != nil {
		t.Fatalf("legitimate gaps refused: %v", err)
	}
	if len(s.ManifestJSON) > maxManifestBytes {
		t.Fatalf("manifest %d bytes", len(s.ManifestJSON))
	}
	m := s.Manifest
	if len(m.Gaps) == 0 || len(m.Gaps) >= 400 || m.Gaps[0].ID != manyGaps(1)[0].ID {
		t.Fatalf("want a sorted capped sample, got %d gaps", len(m.Gaps))
	}
	// A hidden gap id cannot be acknowledged, so the ready gate (which refuses
	// any truncation) must not accept this manifest; traversal stays complete.
	i := slices.IndexFunc(m.TruncationReasons, func(d Diagnostic) bool { return d.ID == "manifest_gap_limit" })
	if i < 0 || !m.Complete || !strings.Contains(m.TruncationReasons[i].Message, fmt.Sprintf("%d of 400", 400-len(m.Gaps))) {
		t.Fatalf("summary not stated: %+v complete=%v", m.TruncationReasons, m.Complete)
	}
}

func TestPrepareSnapshotSummarizesRevisionCoverageGaps(t *testing.T) {
	gaps := make([]string, 1200)
	for i := range gaps {
		gaps[i] = fmt.Sprintf("stale_assertion: %036d", i)
	}
	known := int64(7)
	coverage := backendmodel.Coverage{Status: "partial", Denominator: &known, KnownObjects: 3, Gaps: gaps}
	s, err := prepareSnapshot(fitTestSnapshot(ResultManifest{Complete: true, Gaps: []Diagnostic{}, SourceCoverageBefore: coverage, SourceCoverageAfter: coverage}))
	if err != nil {
		t.Fatalf("legitimate coverage refused: %v", err)
	}
	if len(s.ManifestJSON) > maxManifestBytes {
		t.Fatalf("manifest %d bytes", len(s.ManifestJSON))
	}
	for _, c := range []backendmodel.Coverage{s.Manifest.SourceCoverageBefore, s.Manifest.SourceCoverageAfter} {
		last := c.Gaps[len(c.Gaps)-1]
		if c.Status != "partial" || c.KnownObjects != 3 || len(c.Gaps) >= 1200 || !strings.HasPrefix(last, "manifest_summarized:") || !strings.Contains(last, fmt.Sprint(1200-len(c.Gaps)+1)) {
			t.Fatalf("coverage summary %d gaps, last %q", len(c.Gaps), last)
		}
	}
	if len(gaps) != 1200 || gaps[len(gaps)-1] != fmt.Sprintf("stale_assertion: %036d", 1199) {
		t.Fatal("fitting mutated the revision's coverage")
	}
	if len(s.Manifest.TruncationReasons) != 0 {
		t.Fatalf("coverage summary hides nothing the gate reads: %+v", s.Manifest.TruncationReasons)
	}
}

func TestPrepareSnapshotPinsOversizedDiagramScope(t *testing.T) {
	scope := &backendmodel.DiagramScope{Pin: backendmodel.DiagramPin{ID: "diagram", Version: 3, ContentHash: "hash"}, TargetHash: "target", ScopeHash: "scope", Gaps: []backendmodel.DiagramGap{}}
	for i := range 400 {
		id := fmt.Sprintf("node-%036d", i)
		scope.Selectors = append(scope.Selectors, backendmodel.DiagramScopeSelector{Kind: "semantic", ID: id})
		scope.SourceRefs = append(scope.SourceRefs, backendmodel.DiagramRef{Kind: "record", RecordType: "node", ID: id})
		scope.Members = append(scope.Members, backendmodel.DiagramMember{Ref: backendmodel.DiagramRef{Kind: "record", RecordType: "node", ID: id}, TargetHash: strings.Repeat("t", 64)})
	}
	s, err := prepareSnapshot(fitTestSnapshot(ResultManifest{Complete: true, Gaps: []Diagnostic{}, DiagramScope: scope}))
	if err != nil {
		t.Fatalf("legitimate diagram scope refused: %v", err)
	}
	got := s.Manifest.DiagramScope
	if len(s.ManifestJSON) > maxManifestBytes || got == nil || got.Pin != scope.Pin || got.ScopeHash != "scope" || got.TargetHash != "target" {
		t.Fatalf("scope pin lost: %+v", got)
	}
	if len(got.Selectors)+len(got.Members)+len(got.SourceRefs) != 0 || len(got.Gaps) != 1 || got.Gaps[0].Code != "manifest_summarized" || !strings.Contains(got.Gaps[0].Explanation, "400 selectors") {
		t.Fatalf("scope not summarized: %+v", got.Gaps)
	}
	if len(scope.Members) != 400 {
		t.Fatal("fitting mutated the immutable input's scope")
	}
}

// A manifest that fits is never rewritten, so existing semantic hashes hold.
func TestPrepareSnapshotLeavesFittingManifestUntouched(t *testing.T) {
	in := fitTestSnapshot(ResultManifest{Complete: true, Gaps: manyGaps(20)})
	s, err := prepareSnapshot(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Manifest.Gaps) != 20 || len(s.Manifest.TruncationReasons) != 0 {
		t.Fatalf("fitting manifest changed: %+v", s.Manifest.TruncationReasons)
	}
	again, err := prepareSnapshot(fitTestSnapshot(ResultManifest{Complete: true, Gaps: manyGaps(400)}))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := prepareSnapshot(fitTestSnapshot(ResultManifest{Complete: true, Gaps: manyGaps(400)}))
	if err != nil || again.Manifest.SemanticResultHash != twice.Manifest.SemanticResultHash || string(again.ManifestJSON) != string(twice.ManifestJSON) {
		t.Fatalf("summary is not deterministic: %v", err)
	}
}

// gapHeavyEngine completes with 400 distinct gaps: the shape of a diagnostics
// run over a partially covered project with a few hundred tables (F135).
type gapHeavyEngine struct{}

func (gapHeavyEngine) Analyze(_ context.Context, _ *ImmutableInput, _ func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	return &TerminalSnapshot{Status: "completed", Snapshot: PreparedSnapshot{Manifest: ResultManifest{Complete: true, Verdict: "unknown", ChangedIDs: []ObjectAddress{}, CoveredChangedIDs: []ObjectAddress{}, TruncationReasons: []Diagnostic{}, Gaps: manyGaps(400)}}}, nil
}

func TestAnalysisGapHeavyResultCompletes(t *testing.T) {
	r, _ := testRepo(t)
	s := NewService(r, nil, gapHeavyEngine{})
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	j := mustStart(t, r, "start")
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := r.Get(t.Context(), projectID, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != "queued" && current.Status != "running" {
			if current.Status != "completed" {
				t.Fatalf("want completed, got %+v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
