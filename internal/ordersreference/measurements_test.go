package ordersreference

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"uuid"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func TestMeasuredReadOrdersIndependentSQLOracle(t *testing.T) {
	s, err := Open(Config{DBPath: filepath.Join(t.TempDir(), "orders.db"), Token: strings.Repeat("x", 32), IsolationID: uuid.New().String(), TargetID: "orders", ConfigVersion: 1}, Build{Variant: "fixed", ServiceVersion: "test", SourceTreeHash: strings.Repeat("a", 64), BuildHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PrepareReadFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	a, err := s.MeasureReadOrder(t.Context(), "n_plus_one", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.MeasureReadOrder(t.Context(), "batched", "run-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items) != 50 || !reflect.DeepEqual(a.Items, b.Items) {
		t.Fatal("variants returned different data")
	}
	for i, item := range a.Items {
		if item.ID != i+1 || item.SKU != "sku-"+strconv.Itoa(i+1) || item.Quantity != i+1 {
			t.Fatal("fixture oracle", item)
		}
	}
	for _, test := range []struct {
		v ReadMeasurement
		n int
	}{{a, 51}, {b, 2}} {
		count := 0
		for _, r := range test.v.Records {
			if r.Category == "sql" && r.Kind == "client" {
				count++
				if r.StartTimeUnixNano == "" || r.EndTimeUnixNano == "" {
					t.Fatal("no timing")
				}
			}
		}
		if count != test.n {
			t.Fatal(count, test.n)
		}
	}
}

func TestTelemetryBudgetDoesNotBlockResetAndReceipt(t *testing.T) {
	s, err := Open(Config{DBPath: filepath.Join(t.TempDir(), "orders.db"), Token: strings.Repeat("x", 32), IsolationID: uuid.New().String(), TargetID: "orders", ConfigVersion: 1}, Build{Variant: "fixed", ServiceVersion: "test", SourceTreeHash: strings.Repeat("a", 64), BuildHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	run := uuid.New().String()
	f := p.Fence{RunID: run, StepID: uuid.New().String(), RequestKey: uuid.New().String(), IdentityHash: s.identityHash}
	reset := p.ResetRequest{Fence: f, FixtureHash: p.FixtureHash(), Authorization: p.ResetAuthorization{ID: uuid.New().String(), Version: 1, TargetID: "orders", ConfigVersion: 1, IdentityHash: s.identityHash, IsolationID: s.identity.IsolationID, AllowReset: true}}
	status, first, err := s.mutate(t.Context(), p.ResetEndpoint, f, reset)
	if err != nil || status != 200 {
		t.Fatal(status, err)
	}
	for i := 0; i < 1001; i++ {
		s.observations[strconv.Itoa(i)] = nil
	}
	status, again, err := s.mutate(t.Context(), p.ResetEndpoint, f, reset)
	if err != nil || status != 200 || !bytes.Equal(first, again) {
		t.Fatal("telemetry broke receipt replay", status, err)
	}
	reset.RunID = uuid.New().String()
	reset.RequestKey = uuid.New().String()
	reset.StepID = uuid.New().String()
	reset.Epoch = 1
	if status, _, err = s.mutate(t.Context(), p.ResetEndpoint, reset.Fence, reset); err != nil || status != 200 {
		t.Fatal("telemetry blocked control", status, err)
	}
}
