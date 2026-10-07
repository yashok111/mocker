package designscenario

import (
	"database/sql"
	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
	"strings"
	"testing"
)

func TestTransferHistoryRoundTrip(t *testing.T) {
	source, target := newTestRepo(t), newTestRepo(t)
	first, err := source.Create(t.Context(), CreateInput{Document: validDocument("First"), Source: "ui", Summary: "original"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Save(t.Context(), first.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: validDocument("Second"), FormDrafts: map[string]string{"note": "draft"}, Source: "mcp", Summary: "changed"})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportTransfer(t.Context(), []int64{first.Scenario.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.ImportTransfer(t.Context(), bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := target.ExportTransfer(t.Context(), []int64{result.Scenarios[0].ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := jsonx.Marshal(bundle)
	after, _ := jsonx.Marshal(imported)
	if string(before) != string(after) {
		t.Fatalf("history changed:\n%s\n%s", before, after)
	}
	if result.Scenarios[0].Version != 2 {
		t.Fatal(result)
	}
}

func TestTransferRollbackWholePackage(t *testing.T) {
	repo := newTestRepo(t)
	bundle := TransferBundle{Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: []TransferScenario{
		{Revisions: []TransferRevision{{Document: validDocument("Good"), Source: "ui"}}},
		{Revisions: []TransferRevision{{Document: Document{FormatVersion: 99}, Source: "ui"}}},
	}}
	if _, err := repo.ImportTransfer(t.Context(), bundle, false); err == nil {
		t.Fatal("accepted invalid document")
	}
	list, err := repo.List(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("partial import: %v %v", list, err)
	}
}

func TestTransferRelinksUniqueExactAPIWithoutWritingIt(t *testing.T) {
	repo := newTestRepo(t)
	api, err := repo.designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("orders"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	doc := validDocument("Linked")
	doc.Contracts = []Contract{{ID: "api", Name: "API", Mode: "linked", Source: &ContractSource{DesignID: 999, RevisionID: 999, Version: 999}, Document: jsonx.RawMessage(api.Draft.Document)}}
	bundle := TransferBundle{Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: []TransferScenario{{Revisions: []TransferRevision{{Document: doc, Source: "ui"}}}}}
	result, err := repo.ImportTransfer(t.Context(), bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := repo.Detail(t.Context(), result.Scenarios[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	contract := detail.Draft.Document.Contracts[0]
	if result.LinkedContracts != 1 || contract.Source == nil || contract.Source.DesignID != api.Design.ID {
		t.Fatalf("not linked: %+v", contract)
	}
	after, err := repo.designs.Detail(t.Context(), api.Design.ID)
	if err != nil || after.Design.Version != api.Design.Version {
		t.Fatal("API changed", err)
	}
	if bundle.Scenarios[0].Revisions[0].Document.Contracts[0].Source.DesignID != 999 {
		t.Fatal("input mutated")
	}
	_, err = repo.designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: api.Draft.Document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err := repo.ImportTransfer(t.Context(), bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if ambiguous.LinkedContracts != 0 || ambiguous.CopiedContracts != 1 {
		t.Fatalf("ambiguous match linked: %+v", ambiguous)
	}
	detached, err := repo.ImportTransfer(t.Context(), bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if detached.LinkedContracts != 0 {
		t.Fatal("foreign IDs accepted")
	}
}

// Review 2026-10-06, F181: ImportTransfer indexes the API catalog on a reader
// and catches up under the writer only on revisions committed after that
// scan. A matching revision of another design committed in between must
// still make the match ambiguous, exactly as one scan under the writer did.
func TestTransferAPIIndexCatchesUpOnLaterRevisions(t *testing.T) {
	repo := newTestRepo(t)
	api, err := repo.designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("orders"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := transferJSONKey([]byte(api.Draft.Document))
	if err != nil {
		t.Fatal(err)
	}
	needed := map[string]bool{key: true}
	var index map[string]*ContractSource
	var indexed int64
	err = repo.db.Read(t.Context(), func(tx *sql.Tx) error {
		index, indexed, err = repo.transferAPIIndex(t.Context(), tx, needed, 0, nil)
		return err
	})
	if err != nil || index[key] == nil || index[key].DesignID != api.Design.ID || indexed < api.Draft.ID {
		t.Fatalf("read-side index: %+v %d %v", index, indexed, err)
	}
	if _, err := repo.designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders copy", Document: api.Draft.Document, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	err = repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		index, _, err = repo.transferAPIIndex(t.Context(), tx, needed, indexed, index)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if found, ok := index[key]; !ok || found != nil {
		t.Fatalf("later revision of another design did not make the match ambiguous: %+v", found)
	}
}

func TestTransferRejectsVersionsLimitsAndDuplicateExportIDs(t *testing.T) {
	repo := newTestRepo(t)
	for _, bundle := range []TransferBundle{{Kind: "wrong", FormatVersion: 1}, {Kind: "mocker.scenarios", FormatVersion: 99}, {Kind: "mocker.scenarios", FormatVersion: 1}, {Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: make([]TransferScenario, 21)}} {
		if _, err := repo.ImportTransfer(t.Context(), bundle, false); err == nil {
			t.Fatal("accepted invalid bundle")
		}
	}
	if _, err := repo.ExportTransfer(t.Context(), []int64{1, 1}, true); err == nil {
		t.Fatal("duplicate export IDs")
	}
}

func TestTransferRegressionLongSummary(t *testing.T) {
	r := newTestRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Document: validDocument("test"), Source: "ui", Summary: strings.Repeat("я", 3000)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.ExportTransfer(t.Context(), []int64{d.Scenario.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ImportTransfer(t.Context(), b, false)
	if err != nil {
		t.Fatalf("exported saved scenario cannot import: %v", err)
	}
}
func TestTransferRegressionLegacyRelink(t *testing.T) {
	r := newTestRepo(t)
	api, err := r.designs.Create(t.Context(), apidesign.CreateInput{Name: "Legacy", Document: apiDocument("legacy"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO api_design_revisions(id,design_id,version,hash,document,source,summary,created_at,spec_id) VALUES(101,?,2,'legacy',?,'ui','',1,(SELECT spec_id FROM api_design_revisions LIMIT 1))`, api.Design.ID, `{"openapi":"3.0.3","info":{"title":"Legacy","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := r.designs.Revision(t.Context(), api.Design.ID, 101)
	if err != nil {
		t.Fatal(err)
	}
	doc := validDocument("legacy")
	doc.Contracts = []Contract{{ID: "api", Name: "legacy", Mode: "copy", Document: jsonx.RawMessage(rev.Document)}}
	b := TransferBundle{Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: []TransferScenario{{Revisions: []TransferRevision{{Document: doc, Source: "ui"}}}}}
	result, err := r.ImportTransfer(t.Context(), b, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.LinkedContracts != 1 {
		t.Fatalf("exact public revision left unlinked: %+v document=%s", result, rev.Document)
	}
}
func TestTransferRegressionTwoCopies(t *testing.T) {
	r := newTestRepo(t)
	api, err := r.designs.Create(t.Context(), apidesign.CreateInput{Name: "API", Document: apiDocument("same"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	doc := validDocument("two copies")
	doc.Contracts = []Contract{{ID: "a", Name: "A", Mode: "copy", Document: jsonx.RawMessage(api.Draft.Document)}, {ID: "b", Name: "B", Mode: "copy", Document: jsonx.RawMessage(api.Draft.Document)}}
	b := TransferBundle{Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: []TransferScenario{{Revisions: []TransferRevision{{Document: doc, Source: "ui"}}}}}
	if _, err := r.ImportTransfer(t.Context(), b, false); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Each revision and each scenario must independently retain one link.
	b.Scenarios[0].Revisions = append(b.Scenarios[0].Revisions, b.Scenarios[0].Revisions[0])
	b.Scenarios = append(b.Scenarios, b.Scenarios[0])
	result, err := r.ImportTransfer(t.Context(), b, true)
	if err != nil {
		t.Fatalf("valid package relink fails: %v", err)
	}
	if result.LinkedContracts != 4 || result.CopiedContracts != 4 {
		t.Fatalf("counts: %+v", result)
	}
	for _, scenario := range result.Scenarios {
		detail, err := r.Detail(t.Context(), scenario.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, summary := range detail.Revisions {
			revision, err := r.Revision(t.Context(), scenario.ID, summary.ID)
			if err != nil {
				t.Fatal(err)
			}
			contracts := revision.Document.Contracts
			if contracts[0].Mode != "linked" || contracts[1].Mode != "copy" || contracts[1].Source != nil {
				t.Fatalf("link/copy: %+v", contracts)
			}
			if equal, err := equalJSON(contracts[1].Document, []byte(api.Draft.Document)); err != nil || !equal {
				t.Fatal("copy changed", err)
			}
		}
	}
}

func TestTransferRegressionSummaryBeyondOldLimit(t *testing.T) {
	for _, summary := range []string{strings.Repeat("x", 6000), strings.Repeat("🙂", 6000)} {
		repo := newTestRepo(t)
		original, err := repo.Create(t.Context(), CreateInput{Document: validDocument("Long history"), Source: "ui", Summary: summary})
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := repo.ExportTransfer(t.Context(), []int64{original.Scenario.ID}, true)
		if err != nil {
			t.Fatal(err)
		}
		result, err := repo.ImportTransfer(t.Context(), bundle, false)
		if err != nil {
			t.Fatal(err)
		}
		detail, err := repo.Detail(t.Context(), result.Scenarios[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Draft.Summary != summary {
			t.Fatal("summary lost")
		}
	}
}

func TestTransferRegressionBrowserNormalizedNumbersRelink(t *testing.T) {
	for _, token := range []string{"1.0", "1e0"} {
		t.Run(token, func(t *testing.T) {
			repo := newTestRepo(t)
			raw := strings.Replace(apiDocument("numbers"), `"version":"1"`, `"version":"1","x-number":`+token, 1)
			api, err := repo.designs.Create(t.Context(), apidesign.CreateInput{Name: "Numbers", Document: raw, Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			// This is the precision-safe normalization performed by the browser codec.
			var browserValue any
			if err := jsonx.Unmarshal([]byte(api.Draft.Document), &browserValue); err != nil {
				t.Fatal(err)
			}
			browserJSON, err := jsonx.Marshal(browserValue)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(browserJSON), `"x-number":`+token) {
				t.Fatalf("fixture did not normalize %s: %s", token, browserJSON)
			}
			document := validDocument("Numbers")
			document.Contracts = []Contract{{ID: "api", Name: "API", Mode: "copy", Document: browserJSON}}
			bundle := TransferBundle{Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: []TransferScenario{{Revisions: []TransferRevision{{Document: document, Source: "ui"}}}}}
			result, err := repo.ImportTransfer(t.Context(), bundle, true)
			if err != nil {
				t.Fatal(err)
			}
			if result.LinkedContracts != 1 {
				t.Fatalf("normalized numbers did not relink: %+v", result)
			}
			detail, err := repo.Detail(t.Context(), result.Scenarios[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if equal, err := equalJSON(detail.Draft.Document.Contracts[0].Document, []byte(api.Draft.Document)); err != nil || !equal {
				t.Fatal("target snapshot not pinned exactly", err)
			}
			unchanged, err := repo.designs.Detail(t.Context(), api.Design.ID)
			if err != nil || unchanged.Design.Version != api.Design.Version {
				t.Fatal("target API changed", err)
			}
		})
	}
}

func TestTransferJSONKeyExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		equal       bool
	}{
		{`{"n":1.0}`, `{"n":1e0}`, true},
		{`{"n":-0}`, `{"n":0}`, true},
		{`{"n":9007199254740993}`, `{"n":9007199254740993.0}`, true},
		{`{"n":9007199254740993}`, `{"n":9007199254740992}`, false},
		{`{"n":0.10000000000000001}`, `{"n":0.1}`, false},
		{`{"n":1e1000000000}`, `{"n":10e999999999}`, true},
		{`{"n":1}`, `{"n":"1e0"}`, false},
		{`{"a":[1.00,{"b":2e1}]}`, `{"a":[1,{"b":20}]}`, true},
	} {
		left, err := transferJSONKey([]byte(tc.left))
		if err != nil {
			t.Fatal(err)
		}
		right, err := transferJSONKey([]byte(tc.right))
		if err != nil {
			t.Fatal(err)
		}
		if (left == right) != tc.equal {
			t.Errorf("keys %s / %s: equal=%v, want %v", tc.left, tc.right, left == right, tc.equal)
		}
	}
}
