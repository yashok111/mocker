package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

const savedViewValidFlow = `{"name":"x","target":{"revisionId":"00000000-0000-4000-8000-000000000001"},"state":{"kind":"flow","scope":{},"filters":{"search":"","accessKind":"","reverseAccessKind":""},"selection":null,"positions":[],"collapsedGroupIds":[]},"idempotencyKey":"key"}`

func TestSavedViewRejectsMixedTarget(t *testing.T) {
	var in CreateSavedViewInput
	raw := []byte(`{"name":"x","target":{"revisionId":"00000000-0000-4000-8000-000000000001","proposal":{"proposalId":"00000000-0000-4000-8000-000000000002","proposalRevisionId":"00000000-0000-4000-8000-000000000003"}},"state":{"kind":"flow","scope":{},"filters":{"search":"","accessKind":"","reverseAccessKind":""},"selection":null,"positions":[],"collapsedGroupIds":[]},"idempotencyKey":"mixed"}`)
	if err := json.Unmarshal(raw, &in); err == nil {
		t.Fatal("accepted mixed target")
	}
}

func TestSavedViewStrictDecode(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"unknown", "\"name\":\"x\"", "\"unknown\":true,\"name\":\"x\""},
		{"empty-name", "\"name\":\"x\"", "\"name\":\"\""},
		{"null-name", "\"name\":\"x\"", "\"name\":null"},
		{"scalar-name", "\"name\":\"x\"", "\"name\":1"},
		{"duplicate", "\"name\":\"x\"", "\"name\":\"a\",\"name\":\"x\""},
		{"missing-name", "\"name\":\"x\",", ""},
		{"null-target", "\"target\":{\"revisionId\":\"00000000-0000-4000-8000-000000000001\"}", "\"target\":null"},
		{"bad-id", "00000000-0000-4000-8000-000000000001", "00000000-0000-0000-0000-000000000000"},
		{"null-state", "\"state\":{", "\"state\":null,\"ignored\":{"},
		{"unknown-kind", "\"kind\":\"flow\"", "\"kind\":\"overview\""},
		{"mixed-scope", "\"scope\":{}", "\"scope\":{\"datastoreId\":\"00000000-0000-4000-8000-000000000001\"}"},
		{"null-optional", "\"scope\":{}", "\"scope\":{\"flowId\":null}"},
		{"empty-optional", "\"scope\":{}", "\"scope\":{\"flowId\":\"\"}"},
		{"missing-filter", "\"search\":\"\",", ""},
		{"mixed-filter", "\"search\":\"\"", "\"search\":\"\",\"relationshipTableId\":\"00000000-0000-4000-8000-000000000001\""},
		{"bad-access", "\"accessKind\":\"\"", "\"accessKind\":\"executes\""},
		{"control-search", "\"search\":\"\"", "\"search\":\"a\\n\""},
		{"long-search", "\"search\":\"\"", "\"search\":\"" + strings.Repeat("я", 201) + "\""},
		{"missing-selection", "\"selection\":null,", ""},
		{"bad-selection", "\"selection\":null", "\"selection\":{\"recordType\":\"nodes\",\"id\":\"00000000-0000-4000-8000-000000000001\"}"},
		{"null-positions", "\"positions\":[]", "\"positions\":null"},
		{"null-groups", "\"collapsedGroupIds\":[]", "\"collapsedGroupIds\":null"},
		{"missing-groups", ",\"collapsedGroupIds\":[]", ""},
		{"duplicate-nested", "\"search\":\"\"", "\"search\":\"a\",\"search\":\"\""},
		{"selection-extra", "\"selection\":null", "\"selection\":{\"recordType\":\"node\",\"id\":\"00000000-0000-4000-8000-000000000001\",\"html\":\"x\"}"},
		{"missing-position-y", "\"positions\":[]", "\"positions\":[{\"nodeId\":\"00000000-0000-4000-8000-000000000001\",\"x\":0}]"},
		{"string-x", "\"positions\":[]", "\"positions\":[{\"nodeId\":\"00000000-0000-4000-8000-000000000001\",\"x\":\"1\",\"y\":0}]"},
		{"range-x", "\"positions\":[]", "\"positions\":[{\"nodeId\":\"00000000-0000-4000-8000-000000000001\",\"x\":1000001,\"y\":0}]"},
		{"duplicate-groups", "\"collapsedGroupIds\":[]", "\"collapsedGroupIds\":[\"00000000-0000-4000-8000-000000000001\",\"00000000-0000-4000-8000-000000000001\"]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in CreateSavedViewInput
			raw := strings.Replace(savedViewValidFlow, tc.old, tc.replacement, 1)
			if err := json.Unmarshal([]byte(raw), &in); err == nil {
				t.Fatal("accepted malformed saved view", raw)
			}
		})
	}
	var in CreateSavedViewInput
	if err := json.Unmarshal([]byte(savedViewValidFlow), &in); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"accessKind":""`) || !strings.Contains(string(b), `"reverseAccessKind":""`) || !strings.Contains(string(b), `"search":""`) {
		t.Fatal("required empty filter vanished", string(b))
	}
}

func TestSavedViewSaveVersionAndArrayWireLimits(t *testing.T) {
	const state = `{"kind":"flow","scope":{},"filters":{"search":"","accessKind":"","reverseAccessKind":""},"selection":null,"positions":[],"collapsedGroupIds":[]}`
	for _, version := range []string{"0", "-1", "1.5", "1.0", "null", `"1"`, "9223372036854775808"} {
		raw := `{"name":"View","state":` + state + `,"expectedVersion":` + version + `,"idempotencyKey":"key"}`
		var in SaveSavedViewInput
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatal("accepted version", version)
		}
	}
	for _, suffix := range []string{` {}`, ` true`} {
		var in CreateSavedViewInput
		if err := json.Unmarshal([]byte(savedViewValidFlow+suffix), &in); err == nil {
			t.Fatal("accepted trailing JSON")
		}
	}
	groups := make([]string, 201)
	for i := range groups {
		groups[i] = `"00000000-0000-4000-8000-000000000001"`
	}
	raw := strings.Replace(savedViewValidFlow, `"collapsedGroupIds":[]`, `"collapsedGroupIds":[`+strings.Join(groups, ",")+`]`, 1)
	var in CreateSavedViewInput
	if err := json.Unmarshal([]byte(raw), &in); err == nil {
		t.Fatal("accepted 201 groups")
	}
}
