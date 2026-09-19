package apidesign

import (
	"errors"
	"strings"
	"testing"
)

func TestAuthoredContractRejectsMalformedOpenAPIFields(t *testing.T) {
	r, _ := testRepo(t)
	cases := map[string]string{
		"invalid regex":            `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"Value":{"type":"string","pattern":"["}}}}`,
		"schema constraints":       `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"Value":{"type":"integer","minimum":"not-a-number","enum":42}}}}`,
		"security":                 `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{},"security":42}`,
		"security scheme":          `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{},"components":{"securitySchemes":{"api":{"type":"apiKey"}}}}`,
		"3.0 boolean schema":       `{"openapi":"3.0.3","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"Value":true}}}`,
		"parameter content":        `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a":{"get":{"parameters":[{"name":"limit","in":"query","content":42}],"responses":{"200":{"description":"OK"}}}}}}`,
		"extension only responses": `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a":{"get":{"responses":{"x-note":"empty"}}}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := r.Create(t.Context(), CreateInput{Name: name, Document: raw, Source: "ui"})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("malformed authored contract accepted: %v", err)
			}
		})
	}
	list, err := r.List(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("validation failure wrote projects: %v %v", list, err)
	}
}

func TestMalformedSaveLeavesDraftAndReleaseUntouched(t *testing.T) {
	r, _ := testRepo(t)
	design, err := r.Create(t.Context(), CreateInput{Name: "Stable", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.RequestReview(t.Context(), design.Design.ID, 1, "initial", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(t.Context(), design.Design.ID, review.ID, 1, "ui"); err != nil {
		t.Fatal(err)
	}
	invalid := strings.Replace(testDocument, `"openapi":`, `"security":42,"openapi":`, 1)
	if _, err = r.Save(t.Context(), design.Design.ID, SaveInput{ExpectedVersion: 1, Document: invalid, Source: "mcp"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed save accepted: %v", err)
	}
	after, err := r.Detail(t.Context(), design.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Design.Version != 1 || after.Draft.ID != design.Draft.ID || after.Published == nil || after.Published.ID != design.Draft.ID || len(after.Revisions) != 1 || len(after.Releases) != 1 {
		t.Fatalf("rejected save changed draft/release: %+v", after)
	}
}

func TestAuthoredContractAcceptsOpenAPIDialectsAndOpaqueExtensions(t *testing.T) {
	r, _ := testRepo(t)
	for _, raw := range []string{
		`{"openapi":"3.1.0","jsonSchemaDialect":"https://json-schema.org/draft/2020-12/schema","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"Value":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"integer"}}}}`,
		`{"openapi":"3.0.3","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"Value":{"type":"integer","minimum":0,"exclusiveMinimum":true,"nullable":true}}}}`,
		`{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"Value":{"type":["integer","null"],"exclusiveMinimum":0,"enum":[1,2,null]},"Allowed":true}},"x-data":{"schema":42,"security":42}}`,
	} {
		if _, err := r.prepare(raw); err != nil {
			t.Fatalf("valid authored contract rejected: %v", err)
		}
	}
}
