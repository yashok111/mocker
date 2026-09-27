package scenarioexport

import (
	"errors"
	"fmt"
	"html"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestDocumentationSnapshotAndEscaping(t *testing.T) {
	t.Parallel()
	for _, format := range []Format{"markdown", "html"} {
		t.Run(string(format), func(t *testing.T) {
			rev := revisionFixture()
			rev.Version = 5
			rev.Document.Title = "Title </title><script>alert(1)</script>\n# injected"
			rev.Document.Messages[0].Description = "```\n<img src=x onerror=alert(1)>\n```"
			rev.Document.Contracts = []designscenario.Contract{{ID: "api", Name: "API", Document: jsonx.RawMessage(apiDocument)}}
			rev.Document.Messages[0].Operation = &designscenario.OperationBinding{ContractID: "api", OperationKey: "status"}
			rev.Document.Messages[0].Execution = &designscenario.StepExecution{Body: "PRIVATE_EXECUTION_BODY"}
			artifact, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"saved-hash", "9007199254740993", "/status", "Клиент", "200 OK"} {
				if !strings.Contains(strings.ReplaceAll(artifact.Content, `\-`, `-`), value) {
					t.Fatalf("missing %q", value)
				}
			}
			for _, value := range []string{"<script>", "<img src=", "PRIVATE_EXECUTION_BODY"} {
				if strings.Contains(artifact.Content, value) {
					t.Fatalf("unsafe %q", value)
				}
			}
			if artifact.RevisionID != 11 || artifact.ScenarioID != 7 {
				t.Fatal(artifact)
			}
			if format == "html" {
				if !strings.Contains(html.UnescapeString(artifact.Content), apiDocument) {
					t.Fatal("raw contract changed")
				}
				for _, value := range []string{"default-src 'none'", `id="doc-sequence-content"`, `href="#doc-sequence-content"`, "@page", "landscape"} {
					if !strings.Contains(artifact.Content, value) {
						t.Fatalf("missing %q", value)
					}
				}
			} else if !strings.Contains(artifact.Content, "```mermaid") || !strings.Contains(artifact.Content, apiDocument) {
				t.Fatal("missing diagram or raw contract")
			} else if strings.Contains(artifact.Content, "\n# injected") {
				t.Fatal("Markdown heading injection")
			}
		})
	}
}

func TestDocumentationWarningsAndFailures(t *testing.T) {
	t.Parallel()
	rev := revisionFixture()
	rev.FormDrafts["all"] = `{"/canvas-contract/api/x":{"source":"pending","propertySource":""}}`
	for _, format := range []Format{"markdown", "html"} {
		a, err := New(nil, 1<<20).Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Diagnostics) == 0 || !strings.Contains(strings.ReplaceAll(a.Content, `\_`, `_`), "api_forms_pending") {
			t.Fatal("pending forms hidden")
		}
		if _, err = New(nil, 1<<20).Export(rev, Request{Format: format, ContractID: "api"}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
		if _, err = New(nil, 100).Export(rev, Request{Format: format}); !errors.Is(err, ErrTooLarge) {
			t.Fatal(err)
		}
		if _, err = New(nil, int64(len(a.Content))).Export(rev, Request{Format: format}); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("wire budget ignored: %v", err)
		}
	}
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Document: jsonx.RawMessage(apiDocument)}}
	svc := New(func(string) ([]designscenario.Diagnostic, error) {
		return []designscenario.Diagnostic{{Severity: "error", Message: "invalid saved API"}}, nil
	}, 1<<20)
	options, err := svc.Options(rev)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range options {
		if o.Format == "markdown" || o.Format == "html" {
			if !o.Ready {
				t.Fatal(o)
			}
			for _, d := range o.Diagnostics {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
		}
	}
	for _, f := range []Format{"markdown", "html"} {
		a, err := svc.Export(rev, Request{Format: f})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(a.Content, "invalid saved API") {
			t.Fatal("missing warning")
		}
	}
	rev.Document.Messages[0].ToID = "missing"
	if _, err := svc.Export(rev, Request{Format: "html"}); err == nil {
		t.Fatal("invalid diagram exported")
	}
}

func TestDocumentationContractValidatorFailure(t *testing.T) {
	t.Parallel()
	rev := revisionFixture()
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Document: jsonx.RawMessage(apiDocument)}}
	cause := errors.New("validator infrastructure")
	for _, f := range []Format{"markdown", "html"} {
		_, err := New(func(string) ([]designscenario.Diagnostic, error) { return nil, cause }, 1<<20).Export(rev, Request{Format: f})
		if !errors.Is(err, cause) {
			t.Fatal(err)
		}
	}
}

func TestDocumentationLiteralContractFences(t *testing.T) {
	t.Parallel()
	raw := "{ \"openapi\":\"3.1.0\", \"info\":{\"title\":\"</code></pre><script>alert(1)</script>``````\",\"version\":\"1\"}, \"paths\":{}, \"x-large\":9007199254740993, \"x-small\":1.234567890123456789e-42 }"
	rev := revisionFixture()
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Name: "</h3><iframe src=//evil>", Document: jsonx.RawMessage(raw)}}
	for _, format := range []Format{Markdown, HTML} {
		a, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if format == Markdown {
			if !strings.Contains(a.Content, "```````json\n"+raw+"\n```````\n") {
				t.Fatal("contract fence can close early")
			}
		} else {
			start := strings.Index(a.Content, "<pre><code>") + len("<pre><code>")
			end := strings.Index(a.Content[start:], "</code></pre>") + start
			if start < len("<pre><code>") || end < start || html.UnescapeString(a.Content[start:end]) != raw {
				t.Fatal("HTML contract text changed")
			}
			for _, unsafe := range []string{"<script", "<iframe", "<foreignObject", "<img", "<a href="} {
				if strings.Contains(a.Content, unsafe) {
					t.Fatalf("unsafe HTML %q", unsafe)
				}
			}
			if strings.Count(a.Content, "<style>") != 1 {
				t.Fatal("unexpected authored styles")
			}
		}
	}
}

func TestDocumentationPrintTilesCoverImageWithOverlap(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{1, 1}, {1040, 650}, {1041, 651}, {3100, 1940}, {101624, 1}} {
		tiles, err := documentationTiles(size[0], size[1])
		if err != nil {
			t.Fatal(err)
		}
		coveredX, coveredY := 0, 0
		for _, tile := range tiles {
			if tile.X > coveredX || tile.Y > coveredY {
				t.Fatalf("gap before %+v", tile)
			}
			if tile.X > 0 && tile.X >= coveredX {
				t.Fatal("horizontal overlap missing")
			}
			coveredX = max(coveredX, tile.X+tile.Width)
			coveredY = max(coveredY, tile.Y+tile.Height)
		}
		if coveredX < size[0] || coveredY < size[1] || len(tiles) > 200 {
			t.Fatalf("incomplete coverage %v %v", size, tiles)
		}
	}
	if _, err := documentationTiles(1040+200*(1040-24), 650); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if _, err := documentationTiles(0, 650); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestDocumentationPrintPageLimitLeavesMarkdownAvailable(t *testing.T) {
	t.Parallel()
	rev := revisionFixture()
	for i := range 1000 {
		rev.Document.Participants = append(rev.Document.Participants, designscenario.Participant{ID: fmt.Sprint("extra", i), Name: "Extra", Kind: "service"})
	}
	svc := New(nil, 16<<20)
	if _, err := svc.Export(rev, Request{Format: HTML}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("HTML page limit: %v", err)
	}
	if _, err := svc.Export(rev, Request{Format: Markdown}); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentationWriterStopsEscapingAtLimit(t *testing.T) {
	t.Parallel()
	w := documentationWriter{buffer: boundedBuffer{limit: 32}, html: true}
	w.text(strings.Repeat("<", 100000))
	if !errors.Is(w.err, ErrTooLarge) || w.buffer.Len() > 32 {
		t.Fatal("escaped text exceeded limit")
	}
	before := w.buffer.Len()
	w.raw("after failure")
	if w.buffer.Len() != before {
		t.Fatal("writer resumed after limit")
	}
	w = documentationWriter{buffer: boundedBuffer{limit: 32}}
	w.code("json", strings.Repeat("`", 100000))
	if !errors.Is(w.err, ErrTooLarge) || w.buffer.Len() != 0 {
		t.Fatal("oversized fence was eagerly written")
	}
}

func TestDocumentationNestedBranchesAndWarningParity(t *testing.T) {
	t.Parallel()
	rev := revisionFixture()
	rev.Document.FormatVersion = 2
	rev.Document.Fragments = []designscenario.Fragment{
		{ID: "outer", Kind: "alt", Label: "Choice", FromMessageID: "call", ToMessageID: "reply", Branches: []designscenario.FragmentBranch{{ID: "yes", Label: "Accepted", FromMessageID: "call", ToMessageID: "call"}, {ID: "no", Label: "Rejected", FromMessageID: "reply", ToMessageID: "reply"}}},
		{ID: "inner", Kind: "opt", Label: "Optional", FromMessageID: "call", ToMessageID: "call", ParentFragmentID: "outer", ParentBranchID: "yes"},
	}
	rev.FormDrafts["all"] = "null"
	svc := New(nil, 1<<20)
	options, err := svc.Options(rev)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []Format{Markdown, HTML} {
		a, err := svc.Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"Choice", "Accepted", "Rejected", "Optional", "outer", "yes"} {
			if !strings.Contains(a.Content, text) {
				t.Fatalf("lost nested fragment %q", text)
			}
		}
		found := false
		for _, option := range options {
			if option.Format == format {
				found = true
				if !option.Ready || !reflect.DeepEqual(option.Diagnostics, a.Diagnostics) {
					t.Fatal("document/options warnings differ")
				}
			}
		}
		if !found || len(a.Diagnostics) == 0 {
			t.Fatal("documentation options or pending forms warning absent")
		}
	}
}
