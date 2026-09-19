package apidesign

import "testing"

func TestDiffDistinguishesDocumentationFromAuthoredNames(t *testing.T) {
	for _, name := range []string{"description", "summary", "example"} {
		for _, prefix := range []string{"/components/schemas/", "/components/schemas/Order/properties/"} {
			t.Run(prefix+name, func(t *testing.T) {
				changes := []Change{}
				appendChange(prefix+name, "removed", map[string]any{"type": "string"}, nil, &changes)
				if changes[0].Impact != "review" {
					t.Fatalf("removing an authored schema name was declared safe: %+v", changes[0])
				}
			})
		}
	}
	for _, tt := range []struct {
		pointer       string
		before, after any
		want          string
	}{
		{"/components/schemas/Order/properties/description", map[string]any{"type": "string"}, false, "review"},
		{"/components/schemas/Order/description", "Old", "New", "compatible"},
		{"/components/schemas/properties/description", "Old", "New", "compatible"},
		{"/paths/~1orders/get/summary", "Old", "New", "compatible"},
		{"/paths/~1orders/get/parameters/0/description", "Old", "New", "compatible"},
		{"/paths/~1orders/get/responses/200/content/application~1json/example", "Old", "New", "compatible"},
		{"/info/version", "1", "2", "compatible"},
		{"/x-policy/description", "allow", "deny", "review"},
	} {
		t.Run(tt.pointer, func(t *testing.T) {
			changes := []Change{}
			diffValue(tt.pointer, tt.before, tt.after, &changes)
			if len(changes) != 1 || changes[0].Impact != tt.want {
				t.Fatalf("want one %s change, got %+v", tt.want, changes)
			}
		})
	}
}

func TestDiffRequiredInputsRespectsCompatibilityDirection(t *testing.T) {
	required := map[string]any{"name": "tenant", "in": "header", "required": true, "schema": map[string]any{"type": "string"}}
	optional := map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer"}}
	for _, tt := range []struct {
		name, pointer, kind string
		before, after       any
		want                string
	}{
		{"new required parameter", "/paths/~1orders/get/parameters/0", "added", nil, required, "breaking"},
		{"new parameter collection", "/paths/~1orders/get/parameters", "added", nil, []any{required}, "breaking"},
		{"new inherited required parameter", "/paths/~1orders/parameters/0", "added", nil, required, "breaking"},
		{"new optional parameter", "/paths/~1orders/get/parameters/0", "added", nil, optional, "compatible"},
		{"optional becomes required", "/paths/~1orders/get/parameters/0/required", "changed", false, true, "breaking"},
		{"required becomes optional", "/paths/~1orders/get/parameters/0/required", "changed", true, false, "compatible"},
		{"new required body", "/paths/~1orders/post/requestBody", "added", nil, map[string]any{"required": true}, "breaking"},
		{"body becomes required", "/paths/~1orders/post/requestBody/required", "added", nil, true, "breaking"},
		{"shared parameter becomes required", "/components/parameters/Tenant/required", "added", nil, true, "breaking"},
		{"required request property", "/paths/~1orders/post/requestBody/content/application~1json/schema/required/0", "added", nil, "id", "breaking"},
		{"required request property collection", "/paths/~1orders/post/requestBody/content/application~1json/schema/required", "added", nil, []any{"id"}, "breaking"},
		{"relaxed request property", "/paths/~1orders/post/requestBody/content/application~1json/schema/required/0", "removed", "id", nil, "compatible"},
		{"response required change remains uncertain", "/paths/~1orders/get/responses/200/content/application~1json/schema/required/0", "removed", "id", nil, "review"},
		{"negated request requirements stay uncertain", "/paths/~1orders/post/requestBody/content/application~1json/schema/not/required/0", "removed", "id", nil, "review"},
		{"shared schema direction remains uncertain", "/components/schemas/Order/required/0", "added", nil, "id", "review"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changes := []Change{}
			appendChange(tt.pointer, tt.kind, tt.before, tt.after, &changes)
			if changes[0].Impact != tt.want {
				t.Fatalf("want %s, got %+v", tt.want, changes[0])
			}
		})
	}
}
