package apidesign

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/responserules"
)

func predicateAdmissionDocument(extension, predicate string) string {
	rule := `{"id":"r","name":"Rule","nodes":[{"id":"c","name":"Check","type":"condition","x":0,"y":0,"resultCondition":` + predicate + `}],"edges":[]}`
	return strings.TrimSuffix(testDocument, "}") + `,"` + extension + `":{"formatVersion":1,"rules":[` + rule + `]}}`
}

func TestResponseRulePredicateDocumentAdmissionRejectsDuplicates(t *testing.T) {
	t.Parallel()
	const source = `{"source":"result","nodeId":"a"}`
	for _, tc := range []struct{ name, predicate string }{
		{"operator", `{"source":` + source + `,"op":"exists","op":"not_exists"}`},
		{"escaped operator", `{"source":` + source + `,"op":"exists","o\u0070":"not_exists"}`},
		{"left producer", `{"source":{"source":"result","nodeId":"a","nodeId":"b"},"op":"exists"}`},
		{"right producer", `{"source":` + source + `,"op":"equals","valueFrom":{"source":"result","nodeId":"a","nodeId":"b"}}`},
		{"right pointer", `{"source":` + source + `,"op":"equals","valueFrom":{"source":"result","nodeId":"a","pointer":"/x","pointer":"/y"}}`},
		{"nested", `{"all":[{"source":` + source + `,"op":"exists"},{"any":[{"source":` + source + `,"op":"exists"},{"source":` + source + `,"op":"exists","op":"not_exists"}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, _ := testRepo(t)
			baseline, err := repo.Create(t.Context(), CreateInput{Name: "Baseline", Document: testDocument, Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			document := predicateAdmissionDocument(responserules.Extension, tc.predicate)
			if _, err := repo.Create(t.Context(), CreateInput{Name: "Duplicate", Document: document, Source: "ui"}); !errors.Is(err, ErrInvalid) {
				t.Errorf("import accepted duplicate predicate: %v", err)
			}
			if _, err := repo.Save(t.Context(), baseline.Design.ID, SaveInput{ExpectedVersion: baseline.Design.Version, Document: document, Source: "ui"}); !errors.Is(err, ErrInvalid) {
				t.Errorf("save accepted duplicate predicate: %v", err)
			}
			if _, err := repo.ResolveResponseRule(t.Context(), baseline.Design.ID, "r", ResponseRuleProposal{Document: new(document)}); !errors.Is(err, ErrInvalid) {
				t.Errorf("proposal accepted duplicate predicate: %v", err)
			}
			if diagnostics, err := repo.Validate(t.Context(), document); err != nil || len(diagnostics) == 0 {
				t.Errorf("validate accepted duplicate predicate: diagnostics=%+v err=%v", diagnostics, err)
			}
			after, err := repo.Detail(t.Context(), baseline.Design.ID)
			if err != nil || after.Design.Version != 1 || after.Draft.ID != baseline.Draft.ID {
				t.Errorf("refused predicate changed saved design: after=%+v err=%v", after, err)
			}
			for _, extension := range []string{responserules.Extension, responserules.ExecutionExtension} {
				_, err := decodeDocument(predicateAdmissionDocument(extension, tc.predicate))
				problem, ok := errors.AsType[*InvalidError](err)
				if !ok || len(problem.Diagnostics) == 0 || !strings.Contains(problem.Diagnostics[0].Pointer, "resultCondition") {
					t.Errorf("%s raw admission: %v", extension, err)
				}
			}
		})
	}
}

func TestResponseRulePredicateDocumentAdmissionPreservesOtherDuplicatePolicy(t *testing.T) {
	t.Parallel()
	document := strings.Replace(testDocument, `"title":"Orders"`, `"title":"Original","title":"Orders"`, 1)
	document = strings.TrimSuffix(document, "}") + `,"x-unrelated":{"op":"first","op":"second"}}`
	root, err := decodeDocument(document)
	if err != nil || root["info"].(map[string]any)["title"] != "Orders" {
		t.Fatalf("unrelated duplicate policy changed: root=%+v err=%v", root, err)
	}
}

func TestResponseRuleDocumentAdmissionRejectsDuplicateWrappers(t *testing.T) {
	t.Parallel()
	const first = `{"id":"case","name":"Case","request":{"query":[],"headers":[],"bodyJSON":"9007199254740993"}}`
	const duplicate = `{"id":"case","name":"Case","request":{"query":[],"headers":[],"bodyJSON":"9007199254740993","bodyJSON":"0"}}`
	const template = `{"id":"r","name":"Rule","nodes":[],"edges":[],"examples":[EXAMPLE]}`
	for _, tc := range []struct{ name, extension string }{
		{"duplicate exact body", `"x-mocker-response-rules":{"formatVersion":1,"rules":[` + strings.Replace(template, "EXAMPLE", duplicate, 1) + `]}`},
		{"duplicate root extension", `"x-mocker-response-rules":{"formatVersion":1,"rules":[` + strings.Replace(template, "EXAMPLE", first, 1) + `]},"x-mocker-response-rules":{"formatVersion":1,"rules":[` + strings.Replace(template, "EXAMPLE", strings.Replace(first, "9007199254740993", "0", 1), 1) + `]}`},
		{"escaped root extension", `"x-mocker-response-rules":{"formatVersion":1,"rules":[` + strings.Replace(template, "EXAMPLE", first, 1) + `]},"\u0078-mocker-response-rules":{"formatVersion":1,"rules":[` + strings.Replace(template, "EXAMPLE", strings.Replace(first, "9007199254740993", "0", 1), 1) + `]}`},
		{"execution root extension", `"x-mocker-response-rules-execution":{"formatVersion":1,"rules":[]},"x-mocker-response-rules-execution":{"formatVersion":1,"rules":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, _ := testRepo(t)
			baseline, err := repo.Create(t.Context(), CreateInput{Name: "Baseline", Document: testDocument, Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			document := strings.TrimSuffix(testDocument, "}") + "," + tc.extension + "}"
			if _, err := repo.Create(t.Context(), CreateInput{Name: "Duplicate wrappers", Document: document, Source: "ui"}); !errors.Is(err, ErrInvalid) {
				t.Errorf("import folded wrapper duplicate: %v", err)
			}
			if _, err := repo.Save(t.Context(), baseline.Design.ID, SaveInput{ExpectedVersion: 1, Document: document, Source: "ui"}); !errors.Is(err, ErrInvalid) {
				t.Errorf("save folded wrapper duplicate: %v", err)
			}
			if _, err := repo.ResolveResponseRule(t.Context(), baseline.Design.ID, "r", ResponseRuleProposal{Document: new(document)}); !errors.Is(err, ErrInvalid) {
				t.Errorf("proposal folded wrapper duplicate: %v", err)
			}
			after, err := repo.Detail(t.Context(), baseline.Design.ID)
			if err != nil || after.Design.Version != 1 || after.Draft.ID != baseline.Draft.ID {
				t.Errorf("duplicate wrappers changed saved design: version=%d err=%v", after.Design.Version, err)
			}
		})
	}
}
