package designscenario

import "slices"

const CoverageSampleLimit = 50

type Coverage struct {
	RevisionID  int64             `json:"revisionId"`
	RunCount    int               `json:"runCount"`
	SampleLimit int               `json:"sampleLimit"`
	Messages    []MessageCoverage `json:"messages"`
	Paths       []PathCoverage    `json:"paths"`
}
type MessageCoverage struct {
	MessageID string `json:"messageId"`
	Attempted int    `json:"attempted"`
	Passed    int    `json:"passed"`
	Failed    int    `json:"failed"`
	Skipped   int    `json:"skipped"`
}
type PathCoverage struct {
	FragmentID string `json:"fragmentId"`
	BranchID   string `json:"branchId,omitempty"`
	Outcome    string `json:"outcome"`
	Hits       int    `json:"hits"`
}

// BuildCoverage counts observed occurrences and decisions from retained terminal runs.
// It does not mutate the revision or reports.
func BuildCoverage(revision Revision, reports []RunReport) Coverage {
	coverage := Coverage{RevisionID: revision.ID, SampleLimit: CoverageSampleLimit, Messages: []MessageCoverage{}, Paths: []PathCoverage{}}
	messageIndex := map[string]int{}
	for _, m := range revision.Document.Messages {
		if m.Kind == "request" && m.Operation != nil && (m.Execution == nil || m.Execution.Enabled) {
			messageIndex[m.ID] = len(coverage.Messages)
			coverage.Messages = append(coverage.Messages, MessageCoverage{MessageID: m.ID})
		}
	}
	pathIndex := coverage.addFragmentPaths(revision.Document.Fragments)
	selected := coverageSample(revision, reports)
	coverage.RunCount = len(selected)
	for _, r := range selected {
		for _, s := range r.Steps {
			if i, ok := messageIndex[s.MessageID]; ok {
				coverage.Messages[i].count(s.Status)
			}
		}
		for _, decision := range r.ControlFlow {
			if i, ok := pathIndex[coveragePathKey{decision.FragmentID, decision.BranchID, decision.Outcome}]; ok {
				coverage.Paths[i].Hits++
			}
		}
	}
	return coverage
}

type coveragePathKey struct{ fragment, branch, outcome string }

// addFragmentPaths lists every decision outcome a fragment can record and
// returns where each landed in Paths.
func (coverage *Coverage) addFragmentPaths(fragments []Fragment) map[coveragePathKey]int {
	pathIndex := map[coveragePathKey]int{}
	addPath := func(fragment, branch, outcome string) {
		pathIndex[coveragePathKey{fragment, branch, outcome}] = len(coverage.Paths)
		coverage.Paths = append(coverage.Paths, PathCoverage{FragmentID: fragment, BranchID: branch, Outcome: outcome})
	}
	for _, f := range fragments {
		switch f.Kind {
		case "alt":
			for _, b := range f.Branches {
				addPath(f.ID, b.ID, "taken")
			}
		case "opt":
			addPath(f.ID, "", "taken")
			addPath(f.ID, "", "skipped")
		case "loop":
			addPath(f.ID, "", "taken")
			if f.Execution != nil && f.Execution.Condition != nil {
				addPath(f.ID, "", "skipped")
			}
		}
	}
	return pathIndex
}

// coverageSample keeps the newest terminal runs of this exact revision.
func coverageSample(revision Revision, reports []RunReport) []RunReport {
	selected := make([]RunReport, 0, min(len(reports), CoverageSampleLimit))
	for _, r := range reports {
		if r.ScenarioID == revision.ScenarioID && r.RevisionID == revision.ID && (r.Status == "passed" || r.Status == "failed" || r.Status == "cancelled") {
			selected = append(selected, r)
		}
	}
	slices.SortFunc(selected, func(a, b RunReport) int {
		if a.StartedAt > b.StartedAt {
			return -1
		}
		if a.StartedAt < b.StartedAt {
			return 1
		}
		return 0
	})
	if len(selected) > CoverageSampleLimit {
		selected = selected[:CoverageSampleLimit]
	}
	return selected
}

func (row *MessageCoverage) count(status string) {
	switch status {
	case "passed":
		row.Attempted++
		row.Passed++
	case "failed", "cancelled", "running":
		row.Attempted++
		row.Failed++
	case "skipped":
		row.Skipped++
	}
}
