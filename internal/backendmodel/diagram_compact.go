package backendmodel

import (
	"context"
	"strings"
)

// The summary describes the entire pinned projection, independent of page
// filters. Full gap records remain available through section:gaps. An explicit
// response mode prevents older clients from interpreting an empty gaps array as
// complete coverage.
type DiagramGapSummary struct {
	ByScope map[string]int `json:"byScope,omitzero"`
	Total   int            `json:"total"`
	ByCode  map[string]int `json:"byCode"`
}

func compactDiagramPage(ctx context.Context, page *DiagramPage, in DiagramQueryInput) error {
	if page == nil || in.ResponseMode != "compact-v1" {
		return ctx.Err()
	}
	summary := &DiagramGapSummary{Total: len(page.Gaps), ByCode: map[string]int{}}
	for _, gap := range page.Gaps {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Qualified historical-ref codes include one digest per member. Group by
		// their stable category so a summary does not grow with the entire set.
		category, _, _ := strings.Cut(gap.Code, ":")
		summary.ByCode[category]++
		if gap.Scope != "" {
			if summary.ByScope == nil {
				summary.ByScope = map[string]int{}
			}
			summary.ByScope[gap.Scope]++
		}
	}
	page.GapSummary = summary
	page.Gaps = []DiagramGap{}
	return ctx.Err()
}
