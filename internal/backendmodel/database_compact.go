package backendmodel

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"maps"
	"slices"
	"strconv"
)

type DatabaseLimitation struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	SubjectID string `json:"subjectId,omitempty"`
	Message   string `json:"message"`
}
type DatabaseLimitationSummary struct {
	Total  int            `json:"total"`
	ByCode map[string]int `json:"byCode"`
}

func validateDatabaseCompactInput(in DatabaseQueryInput) error {
	if in.ResponseMode != "" && in.ResponseMode != "compact-v1" {
		return invalid("responseMode", "Supported database response mode is compact-v1")
	}
	if in.Section != "" && (in.ResponseMode != "compact-v1" || !slices.Contains([]string{"data", "limitations"}, in.Section)) {
		return invalid("section", "Select data or limitations with compact-v1")
	}
	return nil
}

func (r *Repo) databaseReadCoverage(ctx context.Context, pid string, in DatabaseQueryInput) (*RevisionCoverage, *CoverageSummary, error) {
	if in.ResponseMode == "" {
		coverage, err := r.RevisionCoverage(ctx, pid, in.RevisionID)
		return coverage, nil, err
	}
	revision, coverage, err := r.coverageDocument(ctx, pid, in.RevisionID)
	if err != nil {
		return nil, nil, err
	}
	summary := coverageSummary(revision, coverage)
	// The explicit compact mode carries counts and the source pin separately.
	// Complete manifests/gaps remain available through query_backend_coverage.
	coverage.Coverage.Gaps = []string{}
	coverage.ReconciliationGaps = []string{}
	coverage.Inventory = []InventoryItem{}
	coverage.Snapshots = []SourceSnapshot{}
	coverage.Source = nil
	return coverage, &summary, nil
}

func (p *databaseProjection) recordLimitation(subject, code, message string) {
	if p.in.ResponseMode != "compact-v1" {
		return
	}
	if p.limitationDetails == nil {
		p.limitationDetails = map[string]DatabaseLimitation{}
	}
	id := diagramIdentity("database-limitation-v1", subject, code, message)
	p.limitationDetails[id] = DatabaseLimitation{ID: id, Code: code, SubjectID: subject, Message: message}
}

func (p *databaseProjection) databaseLimitations() []DatabaseLimitation {
	known := map[string]bool{}
	for _, item := range p.limitationDetails {
		known[item.Message] = true
	}
	for _, message := range p.page.Limitations {
		if !known[message] {
			p.recordLimitation("", "projection_limitation", message)
			known[message] = true
		}
	}
	rows := slices.Collect(maps.Values(p.limitationDetails))
	slices.SortFunc(rows, func(a, b DatabaseLimitation) int { return cmp.Compare(a.ID, b.ID) })
	return rows
}

func (p *databaseProjection) compactDatabaseEnvelope() {
	rows := p.databaseLimitations()
	summary := &DatabaseLimitationSummary{Total: len(rows), ByCode: map[string]int{}}
	for _, row := range rows {
		summary.ByCode[row.Code]++
	}
	p.page.LimitationSummary = summary
	p.page.Limitations = []string{}
}

func (p *databaseProjection) compactLimitationPage(ctx context.Context, pid, scope string) (*DatabasePage, error) {
	rows := p.databaseLimitations()
	// Bind detail cursors to normalized result identities as well as source/view
	// pins, so a changed explanation policy cannot silently shift an old offset.
	scope, err := requestDigest(struct {
		Scope string
		Rows  []DatabaseLimitation
	}{scope, rows})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(p.in.Limit, p.in.Cursor, "database-limitations", pid, scope, false)
	if err != nil {
		return nil, err
	}
	start := 0
	if after != "" {
		start, err = strconv.Atoi(after)
		if err != nil || start < 0 || start >= len(rows) {
			return nil, invalid("cursor", "Invalid limitation cursor")
		}
	}
	p.page.TableItems = []TableItem{}
	p.page.RelationshipItems = []RelationshipItem{}
	p.page.LimitationItems = []DatabaseLimitation{}
	budget, end := 1<<20-4096, start
	for end < len(rows) && len(p.page.LimitationItems) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(rows[end])
		if err != nil {
			return nil, err
		}
		if len(raw) > budget {
			if len(p.page.LimitationItems) == 0 {
				return nil, &FaultError{Status: 413, Code: "backend_database_limit", Message: "One limitation exceeds the compact response budget; use the explicit full database read"}
			}
			break
		}
		budget -= len(raw) + 1
		p.page.LimitationItems = append(p.page.LimitationItems, rows[end])
		end++
	}
	if end < len(rows) {
		p.page.NextCursor = encodeGraphPage("database-limitations", pid, scope, strconv.Itoa(end))
	}
	p.compactDatabaseEnvelope()
	return p.page, nil
}

func (p *databaseProjection) finishDatabasePage(ctx context.Context, pid, scope string) (*DatabasePage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	in := p.in
	if in.Section == "limitations" {
		return p.compactLimitationPage(ctx, pid, scope)
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "database", pid, scope, true)
	if err != nil {
		return nil, err
	}
	p.paginate(pid, scope, limit, after)
	slices.Sort(p.page.Limitations)
	p.page.Limitations = slices.Compact(p.page.Limitations)
	if in.ResponseMode == "compact-v1" {
		p.compactDatabaseEnvelope()
	}
	return p.page, nil
}
