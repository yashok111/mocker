package ordersprotocol

import "fmt"

// Reconcile locates a positive immutable receipt. Absence, pending work or a
// changed fence never authorizes retransmission. Caller validates the journal's
// live identity against the pinned profile before invoking this witness check.
func Reconcile(j Journal, f Fence, e Endpoint, requestHash string) (Receipt, error) {
	if err := j.Validate(); err != nil {
		return Receipt{}, err
	}
	if j.RunID != f.RunID || j.IdentityHash != f.IdentityHash || !j.Complete || len(j.PendingKeys) > 0 {
		return Receipt{}, fmt.Errorf("unverified journal")
	}
	var match *Receipt
	for _, r := range j.Receipts {
		if r.RequestKey == f.RequestKey {
			if match != nil {
				return Receipt{}, fmt.Errorf("duplicate receipt")
			}
			copy := r
			match = &copy
		}
	}
	if match == nil {
		return Receipt{}, fmt.Errorf("outcome unknown: receipt absent")
	}
	r := *match
	if r.Fence != f || r.Endpoint != e || r.RequestHash != requestHash || r.Sequence < 1 || r.Sequence > j.HighWater {
		return Receipt{}, fmt.Errorf("receipt mismatch")
	}
	epoch := f.Epoch
	if e == ResetEndpoint {
		epoch++
	}
	if j.Epoch != epoch || j.CurrentEpoch != epoch || r.ResultEpoch != epoch {
		return Receipt{}, fmt.Errorf("epoch mismatch")
	}
	return r, nil
}
