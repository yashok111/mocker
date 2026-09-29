package designscenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func impactUsageReport(designID, revisionID int64) apidesign.ImpactReport {
	return apidesign.ImpactReport{
		DesignID: designID, FromRevisionID: revisionID,
		ImpactAnalysis: apidesign.ImpactAnalysis{
			Changes: []apidesign.ImpactChange{{ID: "change", Pointer: "/paths/~1orders/get/responses"}},
			Affected: []apidesign.ImpactEntity{{
				ID: "operation", Kind: "operation", Label: "GET /orders",
				Before: &apidesign.ImpactLocator{Pointer: "/paths/~1orders/get", OperationKey: "orders", Method: "get", Path: "/orders"},
				After:  &apidesign.ImpactLocator{Pointer: "/paths/~1orders/get", OperationKey: "orders", Method: "get", Path: "/orders"},
			}},
			Evidence: []apidesign.ImpactEvidence{{
				ID: "operation-before", ChangeID: "change", EntityID: "operation", Side: "before", Direction: "response",
				ReferenceSites: []apidesign.ImpactReferenceSite{{Pointer: "/paths/~1orders/get/responses/200", TargetPointer: "/components/responses/Orders", Kind: "$ref"}},
			}, {
				ID: "operation-after", ChangeID: "change", EntityID: "operation", Side: "after", Direction: "response",
				ReferenceSites: []apidesign.ImpactReferenceSite{},
			}},
		},
	}
}

func impactUsageDocument() Document {
	doc := validDocument("Checkout")
	doc.Participants = []Participant{{ID: "client", Kind: "client"}, {ID: "api", Kind: "service"}}
	doc.Contracts = []Contract{{
		ID: "orders", Name: "Orders", Mode: "copy", Source: &ContractSource{DesignID: 7, RevisionID: 11, Version: 1},
		Document: jsonx.RawMessage(`{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders","responses":{"200":{"description":"OK"}}}}}}`),
	}}
	doc.Messages = []Message{{ID: "call", FromID: "client", ToID: "api", Kind: "request", Operation: &OperationBinding{ContractID: "orders", OperationKey: "orders"}}}
	return doc
}

// The raw fixture writer can represent historical/oversized drafts that normal
// write validation rejects, so the reader's own limits remain independently tested.
func insertImpactScenario(t *testing.T, repo *Repo, id int64, raw string) {
	t.Helper()
	err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenarios
			(id,name,version,draft_revision_id,created_at,updated_at) VALUES (?, ?,1,NULL,1,1)`, id, "Checkout"); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenario_revisions
			(id,scenario_id,version,hash,source,summary,created_at,document,form_drafts)
			VALUES (?,?,1,'hash','ui','',1,?,'{}')`, id, id, raw)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), `UPDATE design_scenarios SET draft_revision_id=? WHERE id=?`, id, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func rawImpactDocument(t *testing.T, document Document) string {
	t.Helper()
	raw, err := jsonx.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestImpactUsagesExtendsOperationEvidenceAndKeepsSnapshotLocators(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	doc := impactUsageDocument()
	doc.FormatVersion = 2
	doc.Contracts = append(doc.Contracts, Contract{
		ID: "linked", Name: "Linked orders", Mode: "linked", Source: &ContractSource{DesignID: 7, RevisionID: 12, Version: 2},
		Document: slices.Clone(doc.Contracts[0].Document),
	})
	doc.Messages = append(doc.Messages, Message{ID: "nested", Operation: &OperationBinding{ContractID: "linked", OperationKey: "orders"}})
	doc.Fragments = []Fragment{
		{ID: "outer", Kind: "opt", FromMessageID: "call", ToMessageID: "nested"},
		{ID: "inner", Kind: "loop", FromMessageID: "nested", ToMessageID: "nested", ParentFragmentID: "outer"},
	}
	insertImpactScenario(t, repo, 1, rawImpactDocument(t, doc))
	report := impactUsageReport(7, 11)
	got, err := repo.ImpactUsages(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || len(got.Affected) != 2 || len(got.Evidence) != 4 || got.Coverage.ScenariosScanned != 1 || got.Coverage.ScenarioUsagesReturned != 2 {
		t.Fatalf("result=%+v", got)
	}
	first := got.Affected[0]
	if first.Kind != "scenario_message" || first.Before == nil || first.After == nil {
		t.Fatalf("entity=%+v", first)
	}
	locator := first.Before
	if locator.ScenarioID != 1 || locator.ScenarioRevision != 1 || locator.ScenarioName != "Checkout" || locator.ContractID != "orders" || locator.PinnedRevisionID != 11 || locator.Mode != "copy" || locator.MessageID != "call" || locator.Pointer != "/messages/0/operation" {
		t.Fatalf("locator=%+v", locator)
	}
	if got.Evidence[0].ChangeID != "change" || got.Evidence[0].Side != "before" || got.Evidence[0].Direction != "response" || len(got.Evidence[0].ReferenceSites) != 2 || got.Evidence[0].ReferenceSites[0] != report.Evidence[0].ReferenceSites[0] {
		t.Fatalf("evidence=%+v", got.Evidence[0])
	}
	codes := []string{}
	for _, diagnostic := range got.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	if !slices.Contains(codes, "scenario_copy") || !slices.Contains(codes, "scenario_revision_mismatch") {
		t.Fatalf("diagnostics=%+v", got.Diagnostics)
	}
	again, err := repo.ImpactUsages(t.Context(), report)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("non-deterministic result: err=%v", err)
	}
}

func TestImpactUsagesUnknownEmbeddedBindingIsIncomplete(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"openapi":"3.1.0","paths":{"/orders":{"get":{"operationId":"orders","x-mocker-canvas-operation-id":"changed"}}}}`,
		`{"openapi":"3.1.0","paths":{"/orders":{"$ref":"#/components/pathItems/Orders","x-mocker-canvas-operation-ids":{"get":"orders"}}},"components":{"pathItems":{"Orders":{"get":{"responses":{}}}}}}`,
		`{"openapi":"3.1.0","paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders"}},"/duplicate":{"get":{"x-mocker-canvas-operation-id":"orders"}}}}`,
	} {
		repo := newTestRepo(t)
		doc := impactUsageDocument()
		doc.Contracts[0].Document = jsonx.RawMessage(raw)
		insertImpactScenario(t, repo, 1, rawImpactDocument(t, doc))
		got, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
		if err != nil {
			t.Fatal(err)
		}
		if got.Complete || len(got.Affected) != 1 || len(got.Evidence) != 0 || len(got.Coverage.TruncatedReasons) != 0 {
			t.Fatalf("unknown join falsely complete: %+v", got)
		}
		if !slices.ContainsFunc(got.Diagnostics, func(d apidesign.ImpactDiagnostic) bool {
			return d.Code == "scenario_binding_unknown" && d.EntityID == got.Affected[0].ID
		}) {
			t.Fatalf("missing locator diagnostic: %+v", got.Diagnostics)
		}
	}
}

func TestImpactUsagesReadsOnlyCurrentDraftAndDoesNotWrite(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	doc := impactUsageDocument()
	insertImpactScenario(t, repo, 1, rawImpactDocument(t, doc))
	err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenario_revisions
			(id,scenario_id,version,hash,source,summary,created_at,document,form_drafts)
			VALUES (2,1,2,'hash2','ui','',2,?,'{}')`, rawImpactDocument(t, validDocument("Current"))); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `UPDATE design_scenarios SET version=2,draft_revision_id=2 WHERE id=1`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
	if err != nil || !got.Complete || len(got.Affected) != 0 || got.Coverage.ScenariosScanned != 1 {
		t.Fatalf("historical usage leaked: %+v err=%v", got, err)
	}
	detail, err := repo.Detail(t.Context(), 1)
	if err != nil || detail.Scenario.Version != 2 || detail.Draft.ID != 2 || len(detail.Revisions) != 2 {
		t.Fatalf("analysis mutated scenario: %+v err=%v", detail, err)
	}
}

func TestImpactUsagesCancellation(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := repo.ImpactUsages(ctx, impactUsageReport(7, 11))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestImpactUsagesRejectsOversizedDraftBeforeDecoding(t *testing.T) {
	repo := newTestRepo(t)
	// Deliberately invalid JSON: a decode error would prove the oversized body
	// was loaded instead of being rejected from its byte length.
	insertImpactScenario(t, repo, 1, strings.Repeat("x", 64*1024*1024+1))
	insertImpactScenario(t, repo, 2, rawImpactDocument(t, impactUsageDocument()))
	got, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
	if err != nil || got.Complete || got.Coverage.ScenariosScanned != 0 || len(got.Affected) != 0 || !slices.Contains(got.Coverage.TruncatedReasons, "scenario_bytes") {
		t.Fatalf("size cap result=%+v err=%v", got, err)
	}
}

func TestImpactUsagesScanBoundaryDoesNotClaimAbsencePastCap(t *testing.T) {
	t.Parallel()
	for _, count := range []int{500, 501} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			repo := newTestRepo(t)
			err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(t.Context(), `WITH RECURSIVE ids(n) AS
					(SELECT 1 UNION ALL SELECT n+1 FROM ids WHERE n<?)
					INSERT INTO design_scenarios(id,name,version,created_at,updated_at)
					SELECT n,'Checkout',1,1,1 FROM ids`, count); err != nil {
					return err
				}
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenario_revisions
					(id,scenario_id,version,hash,source,summary,created_at,document,form_drafts)
					SELECT id,id,1,'hash','ui','',1,?,'{}' FROM design_scenarios`, rawImpactDocument(t, validDocument("Empty"))); err != nil {
					return err
				}
				if _, err := tx.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=? WHERE id=501`, rawImpactDocument(t, impactUsageDocument())); err != nil {
					return err
				}
				_, err := tx.ExecContext(t.Context(), `UPDATE design_scenarios SET draft_revision_id=id`)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
			if err != nil || got.Coverage.ScenariosScanned != 500 || len(got.Affected) != 0 || got.Complete != (count == 500) || slices.Contains(got.Coverage.TruncatedReasons, "scenario_scan") != (count == 501) {
				t.Fatalf("scan result=%+v err=%v", got, err)
			}
		})
	}
}

func TestImpactUsagesUsageBoundaryIncludesUnknownJoins(t *testing.T) {
	t.Parallel()
	for _, unknown := range []bool{false, true} {
		for _, count := range []int{1000, 1001} {
			t.Run(fmt.Sprintf("unknown=%t/count=%d", unknown, count), func(t *testing.T) {
				repo := newTestRepo(t)
				doc := impactUsageDocument()
				doc.Contracts[0].Mode = "linked"
				if unknown {
					doc.Contracts[0].Document = jsonx.RawMessage(`{"paths":{}}`)
				}
				doc.Messages = []Message{}
				for index := range count {
					doc.Messages = append(doc.Messages, Message{ID: strconv.Itoa(index), Operation: &OperationBinding{ContractID: "orders", OperationKey: "orders"}})
				}
				insertImpactScenario(t, repo, 1, rawImpactDocument(t, doc))
				got, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
				if err != nil || got.Coverage.ScenarioUsagesReturned != 1000 || len(got.Affected) != 1000 || slices.Contains(got.Coverage.TruncatedReasons, "scenario_usages") != (count == 1001) {
					t.Fatalf("usage result coverage=%+v complete=%t err=%v", got.Coverage, got.Complete, err)
				}
				if got.Complete != (!unknown && count == 1000) {
					t.Fatalf("complete=%t unknown=%t count=%d", got.Complete, unknown, count)
				}
			})
		}
	}
}

func TestImpactUsagesByteBudgetCountsCumulativeDocuments(t *testing.T) {
	repo := newTestRepo(t)
	empty := rawImpactDocument(t, validDocument("Empty"))
	padding := strings.Repeat(" ", 64*1024*1024-len(empty))
	insertImpactScenario(t, repo, 1, empty+padding)
	got, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
	if err != nil || !got.Complete || got.Coverage.ScenariosScanned != 1 {
		t.Fatalf("exact budget coverage=%+v err=%v", got.Coverage, err)
	}
	insertImpactScenario(t, repo, 2, "invalid JSON")
	insertImpactScenario(t, repo, 3, rawImpactDocument(t, impactUsageDocument()))
	got, err = repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
	if err != nil || got.Complete || got.Coverage.ScenariosScanned != 1 || !slices.Contains(got.Coverage.TruncatedReasons, "scenario_bytes") {
		t.Fatalf("cumulative budget coverage=%+v err=%v", got.Coverage, err)
	}
}

func TestImpactUsagesDoesNotUseUnrelatedAPIOrInventAfterForRemovedOperation(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	doc := impactUsageDocument()
	unrelated := doc.Contracts[0]
	unrelated.ID = "unrelated"
	unrelated.Source = &ContractSource{DesignID: 8, RevisionID: 11}
	doc.Contracts = append(doc.Contracts, unrelated)
	doc.Messages = append(doc.Messages, Message{ID: "unrelated", Operation: &OperationBinding{ContractID: "unrelated", OperationKey: "orders"}})
	insertImpactScenario(t, repo, 1, rawImpactDocument(t, doc))
	report := impactUsageReport(7, 11)
	report.Affected[0].After = nil
	report.Evidence = report.Evidence[:1]
	got, err := repo.ImpactUsages(t.Context(), report)
	if err != nil || len(got.Affected) != 1 || len(got.Evidence) != 1 || got.Affected[0].After != nil || got.Affected[0].Before == nil {
		t.Fatalf("removed operation usage=%+v err=%v", got, err)
	}
}

func TestImpactUsagesPropagatesReadError(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	insertImpactScenario(t, repo, 1, "invalid JSON")
	_, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
	if err == nil {
		t.Fatal("invalid saved document was reported as successful analysis")
	}
}

func TestImpactUsagesLocatorUsesActualEmbeddedOperation(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	doc := impactUsageDocument()
	doc.Contracts[0].Document = jsonx.RawMessage(strings.ReplaceAll(string(doc.Contracts[0].Document), "/orders", "/copied"))
	insertImpactScenario(t, repo, 1, rawImpactDocument(t, doc))
	got, err := repo.ImpactUsages(t.Context(), impactUsageReport(7, 11))
	if err != nil || len(got.Affected) != 1 {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	locator := got.Affected[0].After
	if locator.Path != "/copied" || locator.SourcePointer != "/contracts/0/document/paths/~1copied/get" {
		t.Fatalf("locator points outside actual copy: %+v", locator)
	}
}

func TestImpactUsagesDoesNotGuessAmbiguousAPIKey(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	insertImpactScenario(t, repo, 1, rawImpactDocument(t, impactUsageDocument()))
	report := impactUsageReport(7, 11)
	report.Affected = append(report.Affected, apidesign.ImpactEntity{
		ID: "duplicate", Kind: "operation", Before: &apidesign.ImpactLocator{OperationKey: "orders", Pointer: "/paths/~1other/get"},
	})
	report.Evidence = append(report.Evidence, apidesign.ImpactEvidence{ID: "duplicate", EntityID: "duplicate", ChangeID: "change", Side: "before", Direction: "response"})
	got, err := repo.ImpactUsages(t.Context(), report)
	if err != nil || got.Complete || len(got.Evidence) != 0 || len(got.Affected) != 1 {
		t.Fatalf("ambiguous key was joined: %+v err=%v", got, err)
	}
}
