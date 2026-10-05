package backendmodel

import (
	"encoding/json/v2"
	"os"
	"testing"
)

// Expected membership is authored separately; never ask the projector to make its oracle.
func loadDiagramFixture(t *testing.T, name string, out any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/diagrams/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
}

type diagramFixture struct {
	IDs          map[string]string   `json:"ids"`
	Elements     []fixtureElement    `json:"elements"`
	Dependencies []fixtureDependency `json:"dependencies"`
	Behavior     fixtureBehavior     `json:"behavior"`
}
type fixtureElement struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Role      string   `json:"role"`
	ParentID  string   `json:"parentId,omitempty"`
	SourceIDs []string `json:"sourceIds"`
}
type fixtureDependency struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Relation   string `json:"relation"`
	EvidenceID string `json:"evidenceId"`
}
type fixtureBehavior struct {
	States              []string `json:"states"`
	ForbiddenTransition []string `json:"forbiddenTransition"`
	BusinessEvent       string   `json:"businessEvent"`
	TransportMessages   []string `json:"transportMessages"`
}
type diagramExpected struct {
	Context           []string `json:"context"`
	Containers        []string `json:"containers"`
	Components        []string `json:"components"`
	PaymentMembers    []string `json:"paymentMembers"`
	PaymentEvidence   []string `json:"paymentEvidence"`
	UnresolvedMembers []string `json:"unresolvedMembers"`
	PaymentCount      int      `json:"paymentCount"`
}

func TestDiagramFixtureIndependentOrders(t *testing.T) {
	var fixture diagramFixture
	var expected diagramExpected
	loadDiagramFixture(t, "orders.json", &fixture)
	loadDiagramFixture(t, "orders_expected.json", &expected)
	if len(fixture.Elements) != 8 || len(fixture.Dependencies) != 3 {
		t.Fatal("Orders fixture must preserve eight C4 identities and three dependencies")
	}
	if expected.PaymentCount != 2 || len(expected.PaymentMembers) != 2 || len(expected.PaymentEvidence) != 2 {
		t.Fatal("Payment aggregate must expand to two independent source members/proofs")
	}
	seen := map[string]bool{}
	for _, e := range fixture.Elements {
		if !ValidID(e.ID) || seen[e.ID] {
			t.Fatalf("invalid or duplicate fixture identity %q", e.ID)
		}
		seen[e.ID] = true
	}
	for i, id := range expected.PaymentMembers {
		found := false
		for _, d := range fixture.Dependencies {
			if d.ID == id && d.EvidenceID == expected.PaymentEvidence[i] {
				found = true
			}
		}
		if !found {
			t.Fatalf("independent member/proof %q is absent", id)
		}
	}
}
