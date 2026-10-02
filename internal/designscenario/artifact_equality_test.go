package designscenario

import "testing"

func TestSameContractDocumentOwnerPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, left, right string
		equal             bool
	}{
		{"format", ` {"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{"numeric spelling", `{"a":1}`, `{"a":1.0}`, false},
		{"unsafe adjacent", `{"a":9007199254740992}`, `{"a":9007199254740993}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			equal, err := SameContractDocument([]byte(tc.left), []byte(tc.right))
			if err != nil || equal != tc.equal {
				t.Fatalf("equal=%v err=%v", equal, err)
			}
		})
	}
	if _, err := SameContractDocument([]byte(`{`), []byte(`{}`)); err == nil {
		t.Fatal("malformed left accepted")
	}
}
