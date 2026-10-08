package backendmodel

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

type CoverageQueryInput struct {
	RevisionID string `json:"revisionId"`
	Section    string `json:"section"`
	SnapshotID string `json:"snapshotId,omitempty"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor,omitempty"`
}
type CoverageSummary struct {
	ProjectID           string      `json:"projectId"`
	RevisionID          string      `json:"revisionId"`
	SemanticHash        string      `json:"semanticHash"`
	ModelSchemaVersion  string      `json:"modelSchemaVersion"`
	Status              string      `json:"status"`
	KnownObjects        int64       `json:"knownObjects"`
	Denominator         *int64      `json:"denominator"`
	Snapshots           int         `json:"snapshots"`
	FileEntries         int         `json:"fileEntries"`
	GapCount            int         `json:"gapCount"`
	InventoryCategories int         `json:"inventoryCategories"`
	StaleCounts         StaleCounts `json:"staleCounts"`
}
type CoverageSnapshotSummary struct {
	ID                string    `json:"id"`
	RepositoryID      string    `json:"repositoryId"`
	Role              string    `json:"role"`
	ProviderName      string    `json:"providerName"`
	ProviderNamespace string    `json:"providerNamespace"`
	ProviderVersion   string    `json:"providerVersion"`
	Profiles          []string  `json:"profiles"`
	FileCount         int       `json:"fileCount"`
	LimitationCount   int       `json:"limitationCount"`
	Consistency       string    `json:"consistency"`
	CapturedAt        time.Time `json:"capturedAt"`
	Dirty             bool      `json:"dirty"`
}
type CoverageInventorySummary struct {
	Category        string `json:"category"`
	Status          string `json:"status"`
	KnownCount      int64  `json:"knownCount"`
	Denominator     *int64 `json:"denominator"`
	DiscoverySource string `json:"discoverySource"`
	GapCount        int    `json:"gapCount"`
}
type CoverageGapDetail struct {
	Code    string `json:"code"`
	Scope   string `json:"scope"`
	Message string `json:"message"`
}
type CoverageDetailRow struct {
	ID         string                    `json:"id"`
	Type       string                    `json:"type"`
	SnapshotID string                    `json:"snapshotId,omitempty"`
	Snapshot   *CoverageSnapshotSummary  `json:"snapshot,omitzero"`
	File       *ManifestFile             `json:"file,omitzero"`
	Inventory  *CoverageInventorySummary `json:"inventory,omitzero"`
	Gap        *CoverageGapDetail        `json:"gap,omitzero"`
}
type CoverageDetailPage struct {
	Version    string              `json:"version"`
	Summary    CoverageSummary     `json:"summary"`
	Section    string              `json:"section"`
	Total      int                 `json:"total"`
	Items      []CoverageDetailRow `json:"items"`
	NextCursor string              `json:"nextCursor"`
}

func (in *CoverageQueryInput) UnmarshalJSON(raw []byte) error {
	type plain CoverageQueryInput
	*in = CoverageQueryInput{}
	return strictAPIObject(raw, []string{"revisionId", "section", "limit"}, []string{"snapshotId", "cursor"}, (*plain)(in))
}
func (in CoverageQueryInput) Validate() error {
	if !ValidID(in.RevisionID) || !slices.Contains([]string{"summary", "snapshots", "files", "inventory", "gaps"}, in.Section) || in.Limit < 1 || in.Limit > MaxGraphPageSize {
		return invalid("query", "Select an exact revision, coverage section and limit1–500")
	}
	if in.SnapshotID != "" && (in.Section != "files" || !ValidID(in.SnapshotID)) {
		return invalid("snapshotId", "Snapshot selector is only available for file pages")
	}
	if in.Section == "summary" && in.Cursor != "" {
		return invalid("cursor", "Summary has no pagination")
	}
	return nil
}

// Stored coverage contains all the manifest information needed here. Resolving
// source6 assertions/evidence merely to count files multiplied allocations and
// repeated whole-project payloads on small read requests.
func (r *Repo) coverageDocument(ctx context.Context, pid, rid string) (*Revision, *RevisionCoverage, error) {
	revision, err := r.Revision(ctx, pid, rid)
	if err != nil {
		return nil, nil, err
	}
	var raw string
	err = r.db.R.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources_documents WHERE revision_id=?`, rid).Scan(&raw)
	out := &RevisionCoverage{Coverage: revision.Coverage, Inventory: []InventoryItem{}, Snapshots: []SourceSnapshot{}, ReconciliationGaps: []string{}}
	if errors.Is(err, sql.ErrNoRows) {
		return revision, out, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, nil, err
	}
	if revision.SchemaVersion != ComposedSchemaVersion && len(out.Snapshots) == 1 && out.Snapshots[0].Role == "" {
		out.Snapshots[0].Role = "primary"
	}
	return revision, out, nil
}

func coverageSummary(rev *Revision, coverage *RevisionCoverage) CoverageSummary {
	summary := CoverageSummary{ProjectID: rev.ProjectID, RevisionID: rev.ID, SemanticHash: rev.SemanticHash, ModelSchemaVersion: rev.SchemaVersion, Status: coverage.Coverage.Status, KnownObjects: coverage.Coverage.KnownObjects, Denominator: coverage.Coverage.Denominator, Snapshots: len(coverage.Snapshots), InventoryCategories: len(coverage.Inventory), StaleCounts: coverage.StaleCounts, GapCount: len(coverage.Coverage.Gaps) + len(coverage.ReconciliationGaps)}
	for _, snapshot := range coverage.Snapshots {
		summary.FileEntries += len(snapshot.Files)
		summary.GapCount += len(snapshot.Provider.Limitations)
	}
	for _, item := range coverage.Inventory {
		summary.GapCount += len(item.Gaps)
		if item.Reason != "" {
			summary.GapCount++
		}
	}
	return summary
}

func (r *Repo) QueryCoverage(ctx context.Context, pid string, in CoverageQueryInput) (*CoverageDetailPage, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	revision, coverage, err := r.coverageDocument(ctx, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	page := &CoverageDetailPage{Version: "coverage-details-v1", Summary: coverageSummary(revision, coverage), Section: in.Section, Items: []CoverageDetailRow{}}
	if in.Section == "summary" {
		return page, nil
	}
	if in.SnapshotID != "" && !slices.ContainsFunc(coverage.Snapshots, func(s SourceSnapshot) bool { return s.ID == in.SnapshotID }) {
		return nil, notFound()
	}
	rows, err := coverageRows(ctx, coverage, in)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b CoverageDetailRow) int { return cmp.Compare(a.ID, b.ID) })
	input := in
	input.Cursor = ""
	scope, err := requestDigest(struct {
		ProjectID, SemanticHash string
		Input                   CoverageQueryInput
	}{pid, revision.SemanticHash, input})
	if err != nil {
		return nil, err
	}
	_, after, err := decodeGraphPage(in.Limit, in.Cursor, "coverage-details", pid, scope, false)
	if err != nil {
		return nil, err
	}
	start := 0
	if after != "" {
		start, err = strconv.Atoi(after)
		if err != nil || start < 0 || start >= len(rows) {
			return nil, invalid("cursor", "Invalid coverage page cursor")
		}
	}
	page.Total = len(rows)
	end, err := appendCoverageRows(ctx, page, rows, start, in.Limit)
	if err != nil {
		return nil, err
	}
	if end < len(rows) {
		page.NextCursor = encodeGraphPage("coverage-details", pid, scope, strconv.Itoa(end))
	}
	return page, nil
}

func appendCoverageRows(ctx context.Context, page *CoverageDetailPage, rows []CoverageDetailRow, start, limit int) (int, error) {
	budget := 1<<20 - 4096
	end := start
	for end < len(rows) && len(page.Items) < limit {
		if err := ctx.Err(); err != nil {
			return end, err
		}
		raw, err := json.Marshal(rows[end])
		if err != nil {
			return end, err
		}
		if len(raw) > budget {
			if len(page.Items) == 0 {
				return end, &FaultError{Status: 413, Code: "backend_coverage_limit", Message: "One coverage detail exceeds the compact response budget; use the explicit full coverage read", Details: map[string]any{"maxResponseBytes": 1 << 20, "offset": end}}
			}
			break
		}
		budget -= len(raw) + 1
		page.Items = append(page.Items, rows[end])
		end++
	}
	return end, nil
}

func coverageRows(ctx context.Context, coverage *RevisionCoverage, in CoverageQueryInput) ([]CoverageDetailRow, error) {
	if in.Section == "gaps" {
		return coverageGapRows(coverage), nil
	}
	rows := []CoverageDetailRow{}
	if in.Section == "inventory" {
		for _, item := range coverage.Inventory {
			rows = append(rows, CoverageDetailRow{ID: "inventory:" + item.Category, Type: "inventory", Inventory: &CoverageInventorySummary{Category: item.Category, Status: item.Status, KnownCount: item.KnownCount, Denominator: item.Denominator, DiscoverySource: item.DiscoverySource, GapCount: len(item.Gaps)}})
		}
		return rows, nil
	}
	for _, snapshot := range coverage.Snapshots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if in.SnapshotID != "" && snapshot.ID != in.SnapshotID {
			continue
		}
		if in.Section == "snapshots" {
			rows = append(rows, CoverageDetailRow{ID: "snapshot:" + snapshot.ID, Type: "snapshot", SnapshotID: snapshot.ID, Snapshot: &CoverageSnapshotSummary{ID: snapshot.ID, RepositoryID: snapshot.RepositoryID, Role: snapshot.Role, ProviderName: snapshot.Provider.Name, ProviderNamespace: snapshot.Provider.Namespace, ProviderVersion: snapshot.Provider.Version, Profiles: snapshot.Provider.Profiles, FileCount: len(snapshot.Files), LimitationCount: len(snapshot.Provider.Limitations), Consistency: snapshot.Consistency, CapturedAt: snapshot.CapturedAt, Dirty: snapshot.Dirty}})
		} else {
			for _, file := range snapshot.Files {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				rows = append(rows, CoverageDetailRow{ID: "file:" + snapshot.ID + ":" + file.Path, Type: "file", SnapshotID: snapshot.ID, File: new(file)})
			}
		}
	}
	return rows, nil
}

func coverageGapRows(coverage *RevisionCoverage) []CoverageDetailRow {
	rows := []CoverageDetailRow{}
	add := func(code, scope, text string) {
		id := fmt.Sprint(len(rows))
		rows = append(rows, CoverageDetailRow{ID: "gap:" + id, Type: "gap", Gap: &CoverageGapDetail{Code: code, Scope: scope, Message: text}})
	}
	for i, gap := range coverage.Coverage.Gaps {
		add("coverage", fmt.Sprintf("/coverage/gaps/%d", i), gap)
	}
	for i, gap := range coverage.ReconciliationGaps {
		add("reconciliation", fmt.Sprintf("/reconciliationGaps/%d", i), gap)
	}
	for i, item := range coverage.Inventory {
		for j, gap := range item.Gaps {
			add("inventory_gap", fmt.Sprintf("/inventory/%d/gaps/%d", i, j), gap)
		}
		if item.Reason != "" {
			add("inventory_reason", fmt.Sprintf("/inventory/%d/reason", i), item.Reason)
		}
	}
	for _, snapshot := range coverage.Snapshots {
		for i, gap := range snapshot.Provider.Limitations {
			add("provider_limitation", fmt.Sprintf("/snapshots/%s/provider/limitations/%d", snapshot.ID, i), gap)
		}
	}
	return rows
}
