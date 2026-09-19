package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestAPIDesignToolsUseAdminRoutes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, args, method, path string
	}{
		{"list_api_designs", `{}`, "GET", "/api/designs"},
		{"create_api_design", `{"name":"Orders","document":"{\"paths\":{}}"}`, "POST", "/api/designs"},
		{"get_api_design", `{"designId":7}`, "GET", "/api/designs/7"},
		{"save_api_design_draft", `{"designId":7,"expectedVersion":3,"document":"{\"paths\":{}}","summary":"Add orders","changeSetId":4}`, "PUT", "/api/designs/7/draft"},
		{"get_api_design_revision", `{"designId":7,"revisionId":8}`, "GET", "/api/designs/7/revisions/8"},
		{"get_api_design_diff", `{"designId":7,"fromRevisionId":1,"toRevisionId":8}`, "GET", "/api/designs/7/diff?fromRevisionId=1&toRevisionId=8"},
		{"validate_api_design", `{"designId":7,"document":"{\"paths\":{}}"}`, "POST", "/api/designs/7/validate"},
		{"create_api_design_change_set", `{"designId":7,"expectedVersion":3,"title":"Add orders"}`, "POST", "/api/designs/7/change-sets"},
		{"close_api_design_change_set", `{"designId":7,"changeSetId":4,"expectedVersion":3}`, "PUT", "/api/designs/7/change-sets/4"},
		{"request_api_design_review", `{"designId":7,"expectedVersion":3,"summary":"Ready"}`, "POST", "/api/designs/7/reviews"},
		{"restore_api_design_revision", `{"designId":7,"revisionId":8,"expectedVersion":3,"summary":"Restore"}`, "POST", "/api/designs/7/restore"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"id":42,"document":"verbatim","unknown":{"kept":true}}`)}
			raw, errMsg := callTool(t, calls, tt.name, tt.args)
			if errMsg != "" {
				t.Fatalf("tool error: %s", errMsg)
			}
			if calls.method != tt.method || calls.path != tt.path {
				t.Fatalf("called %s %s, want %s %s", calls.method, calls.path, tt.method, tt.path)
			}
			var got map[string]any
			if err := jsonx.Unmarshal(raw, &got); err != nil || got["document"] != "verbatim" || got["unknown"] == nil {
				t.Fatalf("response lost fields: %s (%v)", raw, err)
			}
			if tt.method != "GET" {
				var sent map[string]any
				if err := jsonx.Unmarshal(calls.sent, &sent); err != nil {
					t.Fatal(err)
				}
				if sent["designId"] != nil {
					t.Errorf("path identity leaked into body: %s", calls.sent)
				}
				if strings.Contains(tt.args, "expectedVersion") && sent["expectedVersion"] != float64(3) {
					t.Errorf("expected version lost: %s", calls.sent)
				}
			}
			if tt.name == "request_api_design_review" && got["reviewUrl"] != "/designs/7?reviewId=42" {
				t.Errorf("missing review link: %s", raw)
			}
		})
	}
}

func TestAPIDesignToolRejectsMissingVersionWithoutAdminCall(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
	_, errMsg := callTool(t, calls, "save_api_design_draft", `{"designId":7,"expectedVersion":0,"document":"{}","summary":"edit"}`)
	if errMsg == "" || calls.method != "" {
		t.Fatalf("missing CAS reached admin: call=%q error=%q", calls.method, errMsg)
	}
}

func TestAPIDesignToolPreservesConflictDetails(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusConflict, body: []byte(`{"error":{"code":"design_conflict","message":"draft changed","details":{"version":9,"draftRevisionId":23}}}`)}
	_, errMsg := callTool(t, calls, "save_api_design_draft", `{"designId":7,"expectedVersion":3,"document":"{}","summary":"edit"}`)
	if !strings.Contains(errMsg, "409") || !strings.Contains(errMsg, `"version":9`) || !strings.Contains(errMsg, `"draftRevisionId":23`) {
		t.Fatalf("agent cannot resolve conflict from %q", errMsg)
	}
}

func TestAPIDesignToolDoesNotDiscloseInternalFailure(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusInternalServerError, body: []byte(`{"error":{"code":"internal","message":"database secret","details":{"secret":"private"}}}`)}
	_, errMsg := callTool(t, calls, "get_api_design", `{"designId":7}`)
	if !strings.Contains(errMsg, "500") || strings.Contains(errMsg, "secret") || strings.Contains(errMsg, "private") {
		t.Fatalf("internal failure disclosure: %q", errMsg)
	}
}

func TestAPIDesignReviewLinkUsesDecimalIDs(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusCreated, body: []byte(`{"id":12000000}`)}
	raw, errMsg := callTool(t, calls, "request_api_design_review", `{"designId":7,"expectedVersion":3,"summary":"Ready"}`)
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	var got struct {
		ReviewURL string `json:"reviewUrl"`
	}
	if err := jsonx.Unmarshal(raw, &got); err != nil || got.ReviewURL != "/designs/7?reviewId=12000000" {
		t.Fatalf("review link cannot resolve the integer route id: %s (%v)", raw, err)
	}
}
