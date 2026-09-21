package designscenario

import (
	"database/sql"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRepo_DetailDoesNotAllocateHistoricalPayloadsWithStoredSummaries(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("History"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	// Large pending buffers belong only to old snapshots. Listing their already
	// stored descriptions must not allocate a copy of every historical payload.
	prepared, _, err := repo.prepare(created.Draft.Document, map[string]string{"all": strings.Repeat("x", 256*1024)})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		parentID := created.Draft.ID
		for version := int64(2); version <= 33; version++ {
			id, err := insertRevision(t.Context(), tx, created.Scenario.ID, version, &parentID, prepared, "ui", "Сохранено", version)
			if err != nil {
				return err
			}
			parentID = id
		}
		current, _, err := repo.prepare(validDocument("Current"), nil)
		if err != nil {
			return err
		}
		id, err := insertRevision(t.Context(), tx, created.Scenario.ID, 34, &parentID, current, "ui", "Текущая версия", 34)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), "UPDATE design_scenarios SET version=34,draft_revision_id=? WHERE id=?", id, created.Scenario.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	detail, err := repo.Detail(t.Context(), created.Scenario.ID)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Revisions) != 34 || detail.Revisions[0].Summary != "Текущая версия" || detail.Draft.Document.Title != "Current" {
		t.Fatalf("history or draft changed: %+v", detail)
	}
	// Metadata and the tiny draft need well below 1 MiB. Leave broad headroom
	// for runtime/driver changes while catching the 8 MiB of historical buffers.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4*1024*1024 {
		t.Fatalf("Detail allocated %d bytes for populated summaries; historical payloads must not be loaded", allocated)
	}
}

func TestRepo_LegacyBlankSummariesAreDerivedWithoutChangingStoredRevisions(t *testing.T) {
	repo := newTestRepo(t)
	var scenarioID int64
	wantSummaries := []string{
		"Создан сценарий",
		"Цвет объекта «API»: #ABCDEF",
		"Согласовано вручную",
		"Сброшен цвет стрелки сообщения «POST /login»",
		"Цвет карточки сообщения «POST /login»: #654321",
		"Изменён сценарий",
		"Подготовлен цвет",
		"Проверен цвет",
		"Цвет стрелки сообщения «POST /login»: #999999",
	}
	// Seed the immutable snapshots as an older server did, including empty
	// summaries. Production read paths must never patch those stored rows.
	if err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		row, err := tx.ExecContext(t.Context(), "INSERT INTO design_scenarios(name,created_at,updated_at) VALUES (?,?,?)", "Colors", 1, 4)
		if err != nil {
			return err
		}
		scenarioID, err = row.LastInsertId()
		if err != nil {
			return err
		}
		document := colorHistoryDocument()
		var parentID *int64
		var revisionID int64
		for index := range wantSummaries {
			summary := ""
			switch index {
			case 1:
				document.Participants[1].Color = "#abcdef"
			case 2:
				document.Messages[0].ArrowColor = "#123456"
				summary = "Согласовано вручную"
			case 3:
				document.Messages[0].ArrowColor = ""
			case 4:
				document.Messages[0].Color = "#654321"
				summary = " \t\u2003\n"
			case 5:
				document.Title = "Renamed"
			case 6:
				document.Participants[1].Color = "#111111"
				summary = "Подготовлен цвет"
			case 7:
				document.Title = "Reviewed"
				summary = "Проверен цвет"
			case 8:
				document.Messages[0].ArrowColor = "#999999"
			}
			prepared, _, err := repo.prepare(document, nil)
			if err != nil {
				return err
			}
			revisionID, err = insertRevision(t.Context(), tx, scenarioID, int64(index+1), parentID, prepared, "ui", summary, int64(index+1))
			if err != nil {
				return err
			}
			parentID = new(revisionID)
		}
		_, err = tx.ExecContext(t.Context(), "UPDATE design_scenarios SET version=?,draft_revision_id=? WHERE id=?", len(wantSummaries), revisionID, scenarioID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A different scenario must never become the baseline for the first one.
	if _, err := repo.Create(t.Context(), CreateInput{Document: validDocument("Unrelated"), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	type storedRevision struct {
		ID, ScenarioID, Version, CreatedAt              int64
		ParentID                                        sql.NullInt64
		Hash, Document, FormDrafts, Source, Description string
	}
	readStored := func() []storedRevision {
		t.Helper()
		var result []storedRevision
		if err := repo.db.Read(t.Context(), func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(t.Context(), "SELECT id,scenario_id,version,parent_id,hash,document,form_drafts,source,summary,created_at FROM design_scenario_revisions ORDER BY id")
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var row storedRevision
				if err := rows.Scan(&row.ID, &row.ScenarioID, &row.Version, &row.ParentID, &row.Hash, &row.Document, &row.FormDrafts, &row.Source, &row.Description, &row.CreatedAt); err != nil {
					return err
				}
				result = append(result, row)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := readStored()
	detail, err := repo.Detail(t.Context(), scenarioID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Revisions) != len(wantSummaries) || detail.Draft.Summary != wantSummaries[len(wantSummaries)-1] {
		t.Fatalf("legacy draft/history = %+v", detail)
	}
	for index, summary := range detail.Revisions {
		wantVersion := int64(len(wantSummaries) - index)
		want := wantSummaries[wantVersion-1]
		if summary.Version != wantVersion || summary.Summary != want {
			t.Fatalf("legacy history summary = %+v, want version %d and %q", summary, wantVersion, want)
		}
		revision, err := repo.Revision(t.Context(), scenarioID, summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		if revision.RevisionSummary != summary {
			t.Fatalf("direct revision differs from history: %+v vs %+v", revision.RevisionSummary, summary)
		}
	}
	if after := readStored(); !reflect.DeepEqual(before, after) {
		t.Fatal("reading derived summaries modified immutable stored revisions")
	}
	if before[0].Description != "" || before[1].Description != "" || before[3].Description != "" {
		t.Fatal("legacy test fixture does not contain blank stored summaries")
	}
}
