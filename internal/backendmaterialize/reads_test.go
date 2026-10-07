package backendmaterialize

import (
	"reflect"
	"testing"
)

func TestReadPersistedMaterialization(t *testing.T) {
	s, pid, in := fixture(t)
	request := applyInput(t, s, pid, in, "saved-before-ui")
	receipt, err := s.Apply(t.Context(), pid, request)
	must(t, err)
	expected, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	before := counts(t, s)
	fresh := NewService(s.db, s.models, s.apis, s.scenarios)
	page, err := fresh.ListResults(t.Context(), pid, 20, "")
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].ID != receipt.ID || page.Items[0].TargetHash != in.TargetHash {
		t.Fatalf("catalog %+v", page)
	}
	result, err := fresh.ReadResult(t.Context(), pid, receipt.ID)
	must(t, err)
	if !reflect.DeepEqual(result.Preview.Input, expected.Input) || !reflect.DeepEqual(result.Receipt.Owners, receipt.Owners) {
		t.Fatalf("lost plan: got %#v want %#v; owners %#v %#v", result.Preview.Input, in, result.Receipt.Owners, receipt.Owners)
	}
	if !reflect.DeepEqual(before, counts(t, s)) {
		t.Fatal("reads wrote state")
	}
	_, err = s.Apply(t.Context(), pid, request)
	must(t, err)
	page, err = fresh.ListResults(t.Context(), pid, 20, "")
	must(t, err)
	if len(page.Items) != 1 {
		t.Fatal("duplicate idempotent result")
	}
	if _, err = fresh.ReadResult(t.Context(), "00000001-0000-4000-8000-000000000001", receipt.ID); err == nil {
		t.Fatal("cross-project result exposed")
	}
}
