package designscenario

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const branchFixture = `{"formatVersion":2,"title":"Branches","participants":[{"id":"p","kind":"service"}],"messages":[{"id":"a","fromId":"p","toId":"p","kind":"request"},{"id":"b","fromId":"p","toId":"p","kind":"request"},{"id":"c","fromId":"p","toId":"p","kind":"request"}],"contracts":[],"fragments":[{"id":"root","kind":"alt","label":"Decision","fromMessageId":"a","toMessageId":"c","branches":[{"id":"yes","label":"ok","fromMessageId":"a","toMessageId":"b"},{"id":"no","label":"else","fromMessageId":"c","toMessageId":"c"}]},{"id":"child","kind":"loop","label":"retry","fromMessageId":"a","toMessageId":"b","parentFragmentId":"root","parentBranchId":"yes"}]}`

func branchDocument(t *testing.T, raw string) Document {
	t.Helper()
	var doc Document
	if err := jsonx.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestBranchTreeRoundtrip(t *testing.T) {
	t.Parallel()
	doc := branchDocument(t, branchFixture)
	if got := validateDocument(doc); len(got) != 0 {
		t.Fatalf("valid tree: %+v", got)
	}
	raw, err := jsonx.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"parentBranchId":"yes"`) || !strings.Contains(string(raw), `"branches"`) {
		t.Fatalf("lost tree: %s", raw)
	}
}

func TestBranchTreeRejectsInvalidTrees(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, old, replacement string }{
		{"legacy", `"formatVersion":2`, `"formatVersion":1`},
		{"gap", `"fromMessageId":"c","toMessageId":"c"`, `"fromMessageId":"b","toMessageId":"c"`},
		{"missing parent", `"parentFragmentId":"root"`, `"parentFragmentId":"absent"`},
		{"missing branch", `"parentBranchId":"yes"`, `"parentBranchId":"absent"`},
		{"wrong branch", `"parentBranchId":"yes"`, `"parentBranchId":"no"`},
		{"cycle", `"parentFragmentId":"root"`, `"parentFragmentId":"child"`},
		{"siblings", `,"parentFragmentId":"root","parentBranchId":"yes"`, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateDocument(branchDocument(t, strings.Replace(branchFixture, tc.old, tc.replacement, 1))); len(got) == 0 {
				t.Fatal("accepted invalid tree")
			}
		})
	}
}

func TestRemoveBranchFramePreservesMessages(t *testing.T) {
	t.Parallel()
	doc := branchDocument(t, branchFixture)
	if err := applyFragmentCommand(&doc, Command{Type: "remove_fragment", ID: "root"}); err != nil {
		t.Fatal(err)
	}
	if len(doc.Fragments) != 0 || len(doc.Messages) != 3 {
		t.Fatalf("unexpected document: %+v", doc)
	}
}

func TestRemoveBranchBoundaryRefused(t *testing.T) {
	t.Parallel()
	doc := branchDocument(t, branchFixture)
	if err := applyMessageCommand(&doc, Command{Type: "remove_message", ID: "b"}); err == nil {
		t.Fatal("removed branch boundary")
	}
	if len(doc.Messages) != 3 {
		t.Fatal("mutated failed command")
	}
}

func TestBranchMovePreservesMembership(t *testing.T) {
	t.Parallel()
	doc := branchDocument(t, branchFixture)
	// Move a non-boundary message into another branch: ranges can stay valid
	// while its condition changes, which must not be allowed silently.
	doc.Messages = append(doc.Messages[:1], append([]Message{{ID: "middle", FromID: "p", ToID: "p", Kind: "request"}}, doc.Messages[1:]...)...)
	before, _ := jsonx.Marshal(doc)
	target := 3
	if err := applyMessageCommand(&doc, Command{Type: "move_message", ID: "middle", Index: &target}); err == nil {
		t.Fatal("changed branch membership")
	}
	after, _ := jsonx.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("failed move mutated document")
	}
}

func TestBranchCloneIsIndependent(t *testing.T) {
	t.Parallel()
	doc := branchDocument(t, branchFixture)
	copy := cloneRunDocument(doc)
	copy.Fragments[0].Branches[0].Label = "changed"
	if doc.Fragments[0].Branches[0].Label != "ok" {
		t.Fatal("clone shares branches")
	}
}

func TestBranchLimitsAndEqualBounds(t *testing.T) {
	t.Parallel()
	doc := branchDocument(t, branchFixture)
	for depth := 3; depth <= 17; depth++ {
		parent := doc.Fragments[len(doc.Fragments)-1].ID
		doc.Fragments = append(doc.Fragments, Fragment{ID: fmt.Sprint(depth), Kind: "opt", FromMessageID: "a", ToMessageID: "b", ParentFragmentID: parent})
		diagnostics := validateDocument(doc)
		if depth <= 16 && len(diagnostics) != 0 {
			t.Fatalf("valid depth %d: %+v", depth, diagnostics)
		}
		if depth == 17 && len(diagnostics) == 0 {
			t.Fatal("accepted depth 17")
		}
	}
}

func TestLegacyRejectsPresentNewFragmentFields(t *testing.T) {
	t.Parallel()
	for _, field := range []string{`"parentFragmentId":""`, `"parentBranchId":null`, `"branches":null`, `"branches":[]`} {
		raw := `{"formatVersion":1,"title":"","participants":[],"messages":[],"contracts":[],"fragments":[{"id":"f","kind":"opt","label":"","fromMessageId":"a","toMessageId":"a",` + field + `}]}`
		var doc Document
		err := jsonx.Unmarshal([]byte(raw), &doc)
		if err == nil { // Must identify the new field, independent of missing messages.
			found := false
			for _, d := range validateDocument(doc) {
				if strings.Contains(d.Message, "formatVersion 2") {
					found = true
				}
			}
			if !found {
				t.Fatalf("accepted v1 field %s", field)
			}
		}
	}
}

func TestBranchCountLimits(t *testing.T) {
	t.Parallel()
	doc := validDocument("Limits")
	doc.FormatVersion = 2
	doc.Participants = []Participant{{ID: "p", Kind: "service"}}
	for group := 0; group < 11; group++ {
		f := Fragment{ID: fmt.Sprintf("f%d", group), Kind: "alt"}
		for i := 0; i < 100; i++ {
			id := fmt.Sprintf("m%d-%d", group, i)
			doc.Messages = append(doc.Messages, Message{ID: id, Kind: "request", FromID: "p", ToID: "p"})
			f.Branches = append(f.Branches, FragmentBranch{ID: id, FromMessageID: id, ToMessageID: id})
		}
		f.FromMessageID = f.Branches[0].FromMessageID
		f.ToMessageID = f.Branches[99].ToMessageID
		doc.Fragments = append(doc.Fragments, f)
		got := validateDocument(doc)
		if group < 10 && len(got) != 0 {
			t.Fatalf("valid branch limit: %+v", got)
		}
		if group == 10 && len(got) == 0 {
			t.Fatal("accepted >1000 branches")
		}
	}
	for _, count := range []int{0, 1, 101} {
		doc := branchDocument(t, branchFixture)
		doc.Fragments[0].Branches = make([]FragmentBranch, count)
		if len(validateDocument(doc)) == 0 {
			t.Fatalf("accepted %d branches", count)
		}
	}
}

func TestBranchBoundaryResponseCascadeRefused(t *testing.T) {
	t.Parallel()
	doc := branchDocument(t, branchFixture)
	doc.Messages[1].Kind = "response"
	doc.Messages[1].ReplyToID = "a"
	// The directly removed request has no boundary; its response does.
	doc.Messages = append([]Message{{ID: "request", Kind: "request", FromID: "p", ToID: "p"}}, doc.Messages...)
	doc.Messages[2].ReplyToID = "request"
	if err := applyMessageCommand(&doc, Command{Type: "remove_message", ID: "request"}); err == nil {
		t.Fatal("removed response boundary through cascade")
	}
	if len(doc.Messages) != 4 {
		t.Fatal("failed cascade mutated document")
	}
}

func TestBranchRevisionsPreserveLegacyHash(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	legacy := branchDocument(t, branchFixture)
	legacy.FormatVersion = 1
	legacy.Fragments = []Fragment{}
	original, err := repo.Create(t.Context(), CreateInput{Document: legacy, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := repo.Save(t.Context(), original.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: branchDocument(t, branchFixture), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	reread, err := repo.Detail(t.Context(), original.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Draft.Document.Fragments[1].ParentBranchID != "yes" {
		t.Fatal("lost branch parent on persistence")
	}
	old, err := repo.Revision(t.Context(), original.Scenario.ID, original.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Hash != original.Draft.Hash || old.Document.FormatVersion != 1 {
		t.Fatal("rewrote immutable legacy revision")
	}
	removed, err := repo.Apply(t.Context(), original.Scenario.ID, CommandsInput{ExpectedVersion: upgraded.Scenario.Version, Commands: []Command{{Type: "remove_fragment", ID: "root"}}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Draft.Document.Fragments) != 0 || len(removed.Draft.Document.Messages) != 3 {
		t.Fatal("incorrect persisted cascade")
	}
}

func TestRepoRejectsPresentEmptyBranches(t *testing.T) {
	t.Parallel()
	for _, version := range []int{1, 2} {
		for _, kind := range []string{"opt", "loop"} {
			t.Run(fmt.Sprintf("v%d/%s", version, kind), func(t *testing.T) {
				t.Parallel()
				repo := newTestRepo(t)
				doc := branchDocument(t, branchFixture)
				doc.FormatVersion = version
				doc.Fragments = []Fragment{{ID: "frame", Kind: kind, FromMessageID: "a", ToMessageID: "c"}}
				original, err := repo.Create(t.Context(), CreateInput{Document: doc, Source: "ui"})
				if err != nil {
					t.Fatal(err)
				}
				doc.Fragments[0].Branches = []FragmentBranch{}
				if _, err := repo.Create(t.Context(), CreateInput{Document: doc, Source: "ui"}); !errors.Is(err, ErrInvalid) {
					t.Errorf("Create accepted present empty branches: %v", err)
				}
				if _, err := repo.Save(t.Context(), original.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: doc, Source: "ui"}); !errors.Is(err, ErrInvalid) {
					t.Errorf("Save accepted present empty branches: %v", err)
				}
				after, err := repo.Detail(t.Context(), original.Scenario.ID)
				if err != nil {
					t.Fatal(err)
				}
				if after.Scenario.Version != 1 || after.Draft.Hash != original.Draft.Hash || len(after.Revisions) != 1 {
					t.Error("failed save changed persisted revision")
				}
				list, err := repo.List(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if len(list) != 1 {
					t.Error("failed create persisted a scenario")
				}
			})
		}
	}
}
