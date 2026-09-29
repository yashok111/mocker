package apidesign

import (
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testspec"
)

// A single shared input-schema edit in the production-sized corpus must remain
// analyzable; dependency cost must not discard its known consumer.
func TestImpactAcceptanceCorpusFindsSharedInputConsumer(t *testing.T) {
	t.Parallel()
	before := string(testspec.Bytes(t))
	root, err := decodeDocument(before)
	if err != nil {
		t.Fatal(err)
	}
	schema := root["components"].(map[string]any)["schemas"].(map[string]any)["AcceptRoomRequestBodyData"].(map[string]any)
	delete(schema["properties"].(map[string]any), "roles")
	after, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeImpactDocuments(t.Context(), before, string(after))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changes) != 1 || report.Changes[0].Pointer != "/components/schemas/AcceptRoomRequestBodyData/properties/roles" {
		t.Fatalf("wrong change: %+v", report.Changes)
	}
	if len(report.Coverage.TruncatedReasons) != 0 {
		t.Fatalf("ordinary corpus was truncated: %+v", report.Coverage)
	}
	for _, entity := range report.Affected {
		if entity.Kind == "operation" && entity.Before != nil && entity.Before.Method == "POST" && entity.Before.Path == "/api/v1/rooms/{roomId}/requests/{requestId}/accept" {
			return
		}
	}
	t.Fatalf("shared input consumer missing: %+v", report.Affected)
}
