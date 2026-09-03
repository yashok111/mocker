package customep

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// TestValidateOperation_refusesEachShapeByName is D3's validator, one
// refusal per row: a parameter whose `in` is outside the closed set, a
// path parameter naming no segment of the row's own path, a repeated
// parameter, a bad operationId, and the size ceilings.
func TestValidateOperation_refusesEachShapeByName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		op   *Operation
		path string
		want string // substring of the error; "" means accepted
	}{
		{"nil passes", nil, "/a", ""},
		{"empty passes", &Operation{}, "/a", ""},
		{"cookie is refused", &Operation{Parameters: []Parameter{{Name: "s", In: "cookie"}}}, "/a", "not one of query, path, header"},
		{"path param must name a segment", &Operation{Parameters: []Parameter{{Name: "id", In: "path"}}}, "/a", "names no {id} segment"},
		{"path param naming its segment passes", &Operation{Parameters: []Parameter{{Name: "id", In: "path"}}}, "/a/{id}", ""},
		{"a repeated parameter", &Operation{Parameters: []Parameter{{Name: "q", In: "query"}, {Name: "q", In: "query"}}}, "/a", "repeats query parameter"},
		{"the same name in two places is fine", &Operation{Parameters: []Parameter{{Name: "q", In: "query"}, {Name: "q", In: "header"}}}, "/a", ""},
		{"an empty parameter name", &Operation{Parameters: []Parameter{{Name: " ", In: "query"}}}, "/a", "name is empty"},
		{"a bad operationId", &Operation{OperationID: "has space"}, "/a", "must match"},
		{"a good operationId", &Operation{OperationID: "list.things-v2_x"}, "/a", ""},
		{"a parameter schema must be an object", &Operation{Parameters: []Parameter{{Name: "q", In: "query", Schema: jsonx.RawMessage(`[1]`)}}}, "/a", "must be a JSON object"},
		{"a blank tag", &Operation{Tags: []string{"ok", " "}}, "/a", "tags[1]"},
		{"summary over the cap", &Operation{Summary: strings.Repeat("s", maxOperationSummary+1)}, "/a", "operation.summary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateOperation(&Row{Path: tc.path, Operation: tc.op})
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("want accepted, got %v", err)
			case tc.want != "" && err == nil:
				t.Errorf("want a refusal containing %q, got nil", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("error %q does not contain %q", err, tc.want)
			case err != nil && !errors.Is(err, ErrInvalidRow):
				t.Errorf("refusal does not wrap ErrInvalidRow: %v", err)
			}
		})
	}
}

type stubResolver map[string]bool

func (s stubResolver) Resolve(pointer string) (any, error) {
	if s[pointer] {
		return map[string]any{}, nil
	}
	return nil, errors.New("not found")
}

// TestValidateRefs_walksEveryDocumentOfTheRow is D2/D6's write-time
// half: a $ref in the response schema, in reqSchema and in a parameter's
// schema all resolve through the same resolver; a non-local, non-string
// or unresolvable one is refused naming where it sits; a nil resolver
// refuses any $ref at all.
func TestValidateRefs_walksEveryDocumentOfTheRow(t *testing.T) {
	t.Parallel()
	res := stubResolver{"#/components/schemas/User": true}
	ok := jsonx.RawMessage(`{"type":"object","properties":{"u":{"$ref":"#/components/schemas/User"}}}`)
	bad := jsonx.RawMessage(`{"items":{"$ref":"#/components/schemas/Nope"}}`)

	if err := ValidateRefs(&Row{Responses: map[string]overrides.Variant{"200": {Schema: ok}}, ReqSchema: ok,
		Operation: &Operation{Parameters: []Parameter{{Name: "q", In: "query", Schema: ok}}}}, res); err != nil {
		t.Fatalf("every $ref resolves, got %v", err)
	}
	for name, row := range map[string]*Row{
		"responses[200].schema":          {Responses: map[string]overrides.Variant{"200": {Schema: bad}}},
		"reqSchema":                      {ReqSchema: bad},
		"operation.parameters[0].schema": {Operation: &Operation{Parameters: []Parameter{{Name: "q", In: "query", Schema: bad}}}},
	} {
		err := ValidateRefs(row, res)
		if err == nil || !errors.Is(err, ErrRefUnresolved) || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "#/components/schemas/Nope") {
			t.Errorf("%s: err = %v; want ErrRefUnresolved naming the place and the pointer", name, err)
		}
	}
	if err := ValidateRefs(&Row{ReqSchema: ok}, nil); err == nil || !strings.Contains(err.Error(), "no spec is bound") {
		t.Errorf("nil resolver: err = %v; want 'no spec is bound'", err)
	}
	if err := ValidateRefs(&Row{ReqSchema: jsonx.RawMessage(`{"$ref":"other.json#/X"}`)}, res); err == nil || !strings.Contains(err.Error(), "not a local pointer") {
		t.Errorf("external $ref: err = %v; want 'not a local pointer'", err)
	}
	if err := ValidateRefs(&Row{ReqSchema: jsonx.RawMessage(`{"$ref":7}`)}, res); err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("non-string $ref: err = %v", err)
	}
	if err := ValidateRefs(&Row{ReqSchema: jsonx.RawMessage(`{"type":"object"}`)}, nil); err != nil {
		t.Errorf("no $ref and no resolver: err = %v; want nil", err)
	}
}
