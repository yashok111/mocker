package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// callGuide drives get_guide through the real handler — guard, transport,
// the SDK's own dispatch — the way every other tool test in this package
// does, and decodes the structured result.
func callGuide(t *testing.T, args string) (GetGuideOutput, string) {
	t.Helper()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_guide","arguments":`+args+`}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result struct {
			IsError           bool                    `json:"isError"`
			Content           []struct{ Text string } `json:"content"`
			StructuredContent json.RawMessage         `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if env.Result.IsError {
		msg := ""
		if len(env.Result.Content) > 0 {
			msg = env.Result.Content[0].Text
		}
		return GetGuideOutput{}, msg
	}
	var out GetGuideOutput
	if err := json.Unmarshal(env.Result.StructuredContent, &out); err != nil {
		t.Fatalf("decode structuredContent: %v; body=%s", err, rec.Body.String())
	}
	return out, ""
}

func TestGetGuide_defaultsToOverview(t *testing.T) {
	t.Parallel()
	out, errMsg := callGuide(t, `{}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if out.Topic != "overview" {
		t.Errorf("topic = %q, want overview", out.Topic)
	}
	if !strings.HasPrefix(out.Markdown, "# mocker") {
		t.Errorf("overview does not start with the skill title: %q", firstLineOf(out.Markdown))
	}
	if strings.Contains(out.Markdown, "\nname: mocker\n") {
		t.Error("overview carries the skill frontmatter")
	}
	if len(out.Topics) != 6 {
		t.Errorf("topics = %v, want six", out.Topics)
	}
}

func TestGetGuide_everyTopicAnswers(t *testing.T) {
	t.Parallel()
	for _, topic := range []string{"tools", "shapes", "cookbook", "http", "design", " Overview "} {
		out, errMsg := callGuide(t, `{"topic":"`+topic+`"}`)
		if errMsg != "" {
			t.Errorf("topic %q: tool error: %s", topic, errMsg)
			continue
		}
		if out.Topic != strings.ToLower(strings.TrimSpace(topic)) || !strings.HasPrefix(out.Markdown, "# ") {
			t.Errorf("topic %q answered topic=%q first line %q", topic, out.Topic, firstLineOf(out.Markdown))
		}
	}
}

func TestGetGuide_unknownTopicIsAToolError(t *testing.T) {
	t.Parallel()
	_, errMsg := callGuide(t, `{"topic":"recipes"}`)
	if !strings.Contains(errMsg, `unknown topic "recipes"`) || !strings.Contains(errMsg, "cookbook") {
		t.Errorf("error = %q, want the unknown topic named and the list of known ones", errMsg)
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
