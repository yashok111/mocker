package admin

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendCompareReadTransport(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects", []byte(`{"name":"Compare","idempotencyKey":"compare"}`))
	if err != nil || status != 201 {
		t.Fatalf("create: %d %s %v", status, data, err)
	}
	var p backendmodel.Project
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	status, data, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects", []byte(`{"name":"Foreign","idempotencyKey":"foreign-compare"}`))
	if err != nil || status != 201 {
		t.Fatalf("foreign create: %d %s %v", status, data, err)
	}
	var foreign backendmodel.Project
	if err := json.Unmarshal(data, &foreign); err != nil {
		t.Fatal(err)
	}
	base := "/api/backend-projects/" + p.ID
	pins := `"fromRevisionId":"` + p.CurrentRevisionID + `","toRevisionId":"` + p.CurrentRevisionID + `"`
	for _, tt := range []struct {
		body string
		want int
	}{
		{`{` + pins + `,"recordType":""}`, 400}, {`{` + pins + `,"changeKind":""}`, 400},
		{`{` + pins + `}`, 200}, {`{"fromRevisionId":"` + foreign.CurrentRevisionID + `","toRevisionId":"` + p.CurrentRevisionID + `"}`, 404}, {`{` + pins + `,"cursor":"bad"}`, 400}, {`{` + pins + `,"limit":0}`, 400}, {`{` + pins + `,"limit":501}`, 400}, {`{` + pins + `,"limit":null}`, 400}, {`{` + pins + `,"extra":true}`, 400}, {`{` + pins + `,"fromRevisionId":"` + p.CurrentRevisionID + `"}`, 400}, {`{"fromRevisionId":"bad","toRevisionId":"` + p.CurrentRevisionID + `"}`, 400},
	} {
		status, data, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", base+"/revisions/compare", []byte(tt.body))
		if err != nil || status != tt.want {
			t.Fatalf("compare %s: %d %s %v", tt.body, status, data, err)
		}
		if status == 200 && !strings.Contains(string(data), `"items":[]`) {
			t.Fatalf("self comparison not empty: %s", data)
		}
	}
	for _, q := range []string{"", "?previewVersion=1", "?previewVersion=0&recordType=source", "?previewVersion=9223372036854775808&recordType=source", "?previewVersion=1&recordType=source&limit=0", "?previewVersion=1&previewVersion=2&recordType=source", "?previewVersion=1&recordType=unknown"} {
		status, data, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", base+"/imports/"+p.ID+"/changes"+q, nil)
		if err != nil || status != 400 {
			t.Fatalf("changes %s: %d %s %v", q, status, data, err)
		}
	}
}
