package ordersreference

import (
	"bytes"
	"encoding/json/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"uuid"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func TestIndependentFixtures(t *testing.T) {
	for _, variant := range []string{"buggy", "fixed"} {
		t.Run(variant, func(t *testing.T) {
			s, err := Open(Config{DBPath: filepath.Join(t.TempDir(), "orders.db"), Token: strings.Repeat("x", 32), IsolationID: uuid.New().String(), TargetID: "orders", ConfigVersion: 1}, Build{Variant: variant, ServiceVersion: "test", SourceTreeHash: strings.Repeat("a", 64), BuildHash: strings.Repeat("b", 64)})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var oracle struct {
				Reset    p.Counters
				Attempts []struct {
					Number, Status, Orders, Charges, Triggers int
					Outcome                                   string
				}
				Events  []string
				Charges []struct {
					AmountMinor     int64
					Currency, Scope string
				}
				Orders []struct {
					SKU         string
					Quantity    int
					AmountMinor int64
					Currency    string
				}
			}
			data, err := os.ReadFile("../ordersprotocol/testdata/" + variant + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &oracle, json.MatchCaseInsensitiveNames(true)); err != nil {
				t.Fatal(err)
			}
			call := func(ep p.Endpoint, run string, body any, status int) []byte {
				t.Helper()
				method, path, e := ep.Route(run)
				if e != nil {
					t.Fatal(e)
				}
				var raw []byte
				if body != nil {
					raw, e = p.Encode(body)
					if e != nil {
						t.Fatal(e)
					}
				}
				r := httptest.NewRequest(method, path, bytes.NewReader(raw))
				r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("%s: %d %s", ep, w.Code, w.Body.String())
				}
				return w.Body.Bytes()
			}
			var identity p.IdentityResponse
			if err = p.Decode(call(p.IdentityEndpoint, "", nil, 200), &identity, p.BodyLimit); err != nil {
				t.Fatal(err)
			}
			run := uuid.New().String()
			f := func(epoch int64) p.Fence {
				return p.Fence{RunID: run, StepID: uuid.New().String(), RequestKey: uuid.New().String(), Epoch: epoch, IdentityHash: identity.IdentityHash}
			}
			reset := p.ResetRequest{Fence: f(0), FixtureHash: p.FixtureHash(), Authorization: p.ResetAuthorization{ID: uuid.New().String(), Version: 1, TargetID: "orders", ConfigVersion: 1, IdentityHash: identity.IdentityHash, IsolationID: identity.Identity.IsolationID, AllowReset: true}}
			resetRaw := call(p.ResetEndpoint, run, reset, 200)
			var receipt p.Receipt
			if err = p.Decode(resetRaw, &receipt, p.BodyLimit); err != nil {
				t.Fatal(err)
			}
			if receipt.Counters != oracle.Reset {
				t.Fatal(receipt)
			}
			arm := p.FailureRequest{Fence: f(1), Point: p.FailurePoint, Count: 1}
			call(p.FailureEndpoint, run, arm, 200)
			business := uuid.New().String()
			for _, want := range oracle.Attempts {
				req := p.OrderRequest{Fence: f(1), BusinessKey: business, Attempt: want.Number, Order: p.Fixture().Order}
				raw := call(p.OrderEndpoint, run, req, want.Status)
				if err = p.Decode(raw, &receipt, p.BodyLimit); err != nil {
					t.Fatal(err)
				}
				if receipt.Outcome != want.Outcome || receipt.Counters != (p.Counters{Orders: want.Orders, Charges: want.Charges, Attempts: want.Number, Triggers: want.Triggers}) {
					t.Fatal(receipt)
				}
				if !bytes.Equal(raw, call(p.OrderEndpoint, run, req, want.Status)) {
					t.Fatal("duplicate changed")
				}
				var n int
				if err = s.db.QueryRow("SELECT count(*) FROM orders WHERE run_id=?", run).Scan(&n); err != nil || n != want.Orders {
					t.Fatalf("actual orders %d %v", n, err)
				}
			}
			var j p.Journal
			if err = p.Decode(call(p.JournalEndpoint, run, nil, 200), &j, p.JournalLimit); err != nil {
				t.Fatal(err)
			}
			kinds := []string{}
			for _, e := range j.Events {
				kinds = append(kinds, e.Kind)
			}
			if !reflect.DeepEqual(kinds, oracle.Events) || len(j.Charges) != len(oracle.Charges) || len(j.Orders) != len(oracle.Orders) {
				t.Fatalf("journal %#v", j)
			}
			for i, c := range j.Charges {
				w := oracle.Charges[i]
				if c.AmountMinor != w.AmountMinor || c.Currency != w.Currency || c.Scope != w.Scope || c.BusinessKey != business {
					t.Fatal(c)
				}
			}
			expectedOrder := p.Order{SKU: oracle.Orders[0].SKU, Quantity: oracle.Orders[0].Quantity, AmountMinor: oracle.Orders[0].AmountMinor, Currency: oracle.Orders[0].Currency}
			if j.Orders[0].Order != expectedOrder || j.Orders[0].BusinessKey != business {
				t.Fatal(j.Orders)
			}
			var orderRaw []byte
			if err = s.db.QueryRow("SELECT document FROM orders WHERE run_id=?", run).Scan(&orderRaw); err != nil {
				t.Fatal(err)
			}
			var storedOrder p.OrderRecord
			if err = p.Decode(orderRaw, &storedOrder, p.BodyLimit); err != nil {
				t.Fatal(err)
			}
			if storedOrder.Order != expectedOrder || storedOrder.BusinessKey != business {
				t.Fatal(storedOrder)
			}
			rows, err := s.db.Query("SELECT document FROM charges WHERE run_id=? ORDER BY rowid", run)
			if err != nil {
				t.Fatal(err)
			}
			chargeIndex := 0
			for rows.Next() {
				var raw []byte
				if err = rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var c p.ChargeRecord
				if err = p.Decode(raw, &c, p.BodyLimit); err != nil {
					t.Fatal(err)
				}
				if chargeIndex >= len(oracle.Charges) {
					t.Fatal("extra charge")
				}
				want := oracle.Charges[chargeIndex]
				if c.AmountMinor != want.AmountMinor || c.Currency != want.Currency || c.Scope != want.Scope || c.BusinessKey != business {
					t.Fatal(c)
				}
				chargeIndex++
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if chargeIndex != len(oracle.Charges) {
				t.Fatal("missing charge")
			}
			if !bytes.Equal(resetRaw, call(p.ResetEndpoint, run, reset, 200)) {
				t.Fatal("reset replay changed")
			}
			reset.RequestKey = uuid.New().String()
			call(p.ResetEndpoint, run, reset, 409)
			arm.RequestKey = uuid.New().String()
			arm.StepID = uuid.New().String()
			call(p.FailureEndpoint, run, arm, 409)
		})
	}
}
