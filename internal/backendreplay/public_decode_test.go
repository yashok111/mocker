package backendreplay

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestReplayPublicClosedRequests(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	saved, err := s.GetPackage(t.Context(), pid, in.Package.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := SavePackageInput{ID: newReplayID(), Package: saved.Package, Provenance: saved.Provenance, IdempotencyKey: newReplayID()}
	raw, err := marshalReplay(input)
	if err != nil {
		t.Fatal(err)
	}
	var valid SavePackageInput
	if err = json.Unmarshal(raw, &valid); err != nil {
		t.Fatal(err)
	}
	for _, mutant := range []string{
		strings.Replace(string(raw), `"expectedVersion":0,`, "", 1),
		strings.Replace(string(raw), `"excludedIds":[]`, `"excludedIds":null`, 1),
		strings.Replace(string(raw), `"excludedIds":[]`, `"excludedIds":[],"url":"http://arbitrary"`, 1),
		strings.Replace(string(raw), `"id":`, `"id":null,"id":`, 1),
	} {
		var out SavePackageInput
		if err = json.Unmarshal([]byte(mutant), &out); err == nil {
			t.Fatal("non-closed input accepted")
		}
	}
}
