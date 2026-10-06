package ordersprotocol

import (
	"fmt"
	"slices"
)

func (v Receipt) Validate() error {
	if e := v.Fence.Validate(); e != nil {
		return e
	}
	if !ValidHash(v.RequestHash) || v.Sequence < 1 || v.Counters.Orders < 0 || v.Counters.Charges < 0 || v.Counters.Attempts < 0 || v.Counters.Triggers < 0 {
		return fmt.Errorf("invalid receipt")
	}
	epoch := v.Epoch
	switch v.Endpoint {
	case ResetEndpoint:
		epoch++
		if v.Outcome != "reset" || v.HTTPStatus != 200 || v.Counters != (Counters{}) {
			return fmt.Errorf("invalid reset receipt")
		}
	case FailureEndpoint:
		if v.Outcome != "armed" || v.HTTPStatus != 200 {
			return fmt.Errorf("invalid arm receipt")
		}
	case OrderEndpoint:
		if !ValidID(v.BusinessKey) || (v.Attempt != 1 && v.Attempt != 2) || !ValidID(v.ChargeID) {
			return fmt.Errorf("invalid order receipt")
		}
		if v.Outcome == "persistence_failed" {
			if v.HTTPStatus != 503 || v.OrderID != "" {
				return fmt.Errorf("invalid failure receipt")
			}
		} else if v.Outcome == "persisted" {
			if v.HTTPStatus != 201 || !ValidID(v.OrderID) {
				return fmt.Errorf("invalid persisted receipt")
			}
		} else {
			return fmt.Errorf("unknown order outcome")
		}
	default:
		return fmt.Errorf("unknown receipt endpoint")
	}
	if v.ResultEpoch != epoch || epoch < 1 {
		return fmt.Errorf("invalid receipt epoch")
	}
	return nil
}
func (v ErrorResponse) Validate() error {
	if v.Protocol != Version || v.Message == "" || !slices.Contains([]string{"invalid_request", "unauthorized", "forbidden", "identity_mismatch", "epoch_mismatch", "idempotency_conflict", "run_conflict", "not_found", "in_progress", "journal_limit", "internal_error"}, v.Code) {
		return fmt.Errorf("invalid error response")
	}
	return nil
}
func (v Journal) Validate() error {
	h, e := IdentityHash(v.Identity)
	if e != nil {
		return e
	}
	if h != v.IdentityHash || !ValidID(v.RunID) || v.Epoch < 1 || v.CurrentEpoch < v.Epoch || v.FixtureHash != FixtureHash() || v.HighWater < 1 {
		return fmt.Errorf("invalid journal identity")
	}
	keys := map[string]bool{}
	seq := map[int64]bool{}
	for _, r := range v.Receipts {
		if e := r.Validate(); e != nil {
			return e
		}
		if keys[r.RequestKey] || r.RunID != v.RunID || r.IdentityHash != v.IdentityHash || r.ResultEpoch != v.Epoch || r.Sequence > v.HighWater {
			return fmt.Errorf("invalid journal receipt")
		}
		keys[r.RequestKey] = true
	}
	for _, k := range v.PendingKeys {
		if !ValidID(k) || keys[k] {
			return fmt.Errorf("invalid pending key")
		}
		keys[k] = true
	}
	last := int64(0)
	lastByKey := map[string]Event{}
	for _, e := range v.Events {
		if e.Sequence <= last || e.Sequence > v.HighWater || seq[e.Sequence] || !ValidID(e.StepID) || !ValidID(e.RequestKey) || !slices.Contains([]string{"reset", "armed", "attempt", "payment_charged", "payment_reused", "failure_triggered", "order_persisted"}, e.Kind) {
			return fmt.Errorf("invalid journal event")
		}
		seq[e.Sequence] = true
		last = e.Sequence
		lastByKey[e.RequestKey] = e
	}
	if last != v.HighWater {
		return fmt.Errorf("incomplete journal events")
	}
	for _, r := range v.Receipts {
		event, ok := lastByKey[r.RequestKey]
		if !ok || event.Sequence != r.Sequence || event.StepID != r.StepID {
			return fmt.Errorf("receipt event witness mismatch")
		}
	}
	ids := map[string]bool{}
	business := map[string]bool{}
	for _, r := range v.Orders {
		if !ValidID(r.ID) || !ValidID(r.BusinessKey) || ids[r.ID] || business[r.BusinessKey] || r.Order != Fixture().Order {
			return fmt.Errorf("invalid order record")
		}
		ids[r.ID] = true
		business[r.BusinessKey] = true
	}
	ids = map[string]bool{}
	for _, r := range v.Charges {
		if !ValidID(r.ID) || !ValidID(r.BusinessKey) || ids[r.ID] || r.AmountMinor != Fixture().Order.AmountMinor || r.Currency != Fixture().Order.Currency || r.Scope != "mocked" {
			return fmt.Errorf("invalid charge record")
		}
		ids[r.ID] = true
	}
	return nil
}
