package backendreplay

import (
	"fmt"
	"reflect"
	"slices"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

// Check derives assertions from linked journal records; remote counters alone
// never establish a verdict. Any inconsistent or incomplete evidence is unverified.
func Check(in RunInput, provenance Provenance, j p.Journal, live p.IdentityResponse) (report Report, err error) {
	report = initialReport(in, provenance)
	defer func() {
		if err != nil {
			report.Status = "unverified"
			report.Reason = err.Error()
		}
	}()
	if err = validateRun(in, provenance); err != nil {
		return
	}
	if err = j.Validate(); err != nil {
		return
	}
	if err = live.Validate(); err != nil {
		return
	}
	if !journalFenceIntact(in, j, live) {
		return report, fmt.Errorf("incomplete or changed journal fence")
	}
	receipts, err := reconcileReceipts(in, j, &report)
	if err != nil {
		return
	}
	chargeEvents, err := checkEventProgram(in, j, receipts)
	if err != nil {
		return
	}
	actual, err := checkRecords(in, j, receipts, chargeEvents)
	if err != nil {
		return
	}
	report.assert(in, actual)
	return report, nil
}

// journalFenceIntact reports whether the journal is complete, belongs to this
// run one epoch after its reset fence, and both it and the live identity still
// name the pinned profile.
func journalFenceIntact(in RunInput, j p.Journal, live p.IdentityResponse) bool {
	epoch := in.Requests[0].Epoch + 1
	if !j.Complete || len(j.PendingKeys) != 0 || j.RunID != in.RunID || len(j.Receipts) != 4 {
		return false
	}
	if j.Epoch != epoch || j.CurrentEpoch != epoch || live.Epoch != epoch {
		return false
	}
	return live.Identity == in.Profile.Identity && j.Identity == live.Identity && j.IdentityHash == in.Profile.IdentityHash && live.IdentityHash == in.Profile.IdentityHash
}

// reconcileReceipts finds the journal's receipt for each of the four
// mutations, recording each on the report as it is verified.
func reconcileReceipts(in RunInput, j p.Journal, report *Report) ([]p.Receipt, error) {
	receipts := make([]p.Receipt, 4)
	for i := range 4 {
		endpoint, request := mutation(in, i)
		hash, err := p.RequestHash(endpoint, request)
		if err != nil {
			return nil, err
		}
		r, err := p.Reconcile(j, in.Requests[i], endpoint, hash)
		if err != nil {
			return nil, err
		}
		if err = validateReceipt(in, i, r, hash); err != nil {
			return nil, err
		}
		receipts[i] = r
		report.Receipts = append(report.Receipts, r)
	}
	return receipts, nil
}

// checkEventProgram matches the journal's events against the closed program
// and returns the charge ids its payment events witness.
func checkEventProgram(in RunInput, j p.Journal, receipts []p.Receipt) (map[string]bool, error) {
	// This closed program has exactly eight events. Both legitimate payment
	// strategies have this shape; the charge assertion distinguishes the bug.
	if len(j.Events) != 8 {
		return nil, fmt.Errorf("unexpected event program")
	}
	kinds := []string{"reset", "armed", "attempt", "payment_charged", "failure_triggered", "attempt", "", "order_persisted"}
	owners := []int{0, 1, 2, 2, 2, 3, 3, 3}
	chargeEvents := map[string]bool{}
	for n, event := range j.Events {
		owner := owners[n]
		r := receipts[owner]
		if event.StepID != r.StepID || event.RequestKey != r.RequestKey || (n > 0 && event.Sequence != j.Events[n-1].Sequence+1) || (kinds[n] != "" && event.Kind != kinds[n]) {
			return nil, fmt.Errorf("event order or ownership mismatch")
		}
		if owner >= 2 && event.BusinessKey != in.BusinessKey {
			return nil, fmt.Errorf("event business key mismatch")
		}
		if err := checkEventObject(n, event, r, chargeEvents); err != nil {
			return nil, err
		}
		if n == 6 && event.Kind != "payment_charged" && event.Kind != "payment_reused" {
			return nil, fmt.Errorf("retry payment witness missing")
		}
	}
	return chargeEvents, nil
}

// checkEventObject checks the object an event names against its receipt; a
// payment may be charged once and reused only by the retry, event 6.
func checkEventObject(n int, event p.Event, r p.Receipt, chargeEvents map[string]bool) error {
	switch event.Kind {
	case "payment_charged":
		if event.ObjectID != r.ChargeID || event.AmountMinor != p.Fixture().Order.AmountMinor || chargeEvents[event.ObjectID] {
			return fmt.Errorf("charge event mismatch")
		}
		chargeEvents[event.ObjectID] = true
	case "payment_reused":
		if n != 6 || event.ObjectID != r.ChargeID || !chargeEvents[event.ObjectID] || event.AmountMinor != p.Fixture().Order.AmountMinor {
			return fmt.Errorf("payment reuse mismatch")
		}
	case "order_persisted":
		if event.ObjectID != r.OrderID {
			return fmt.Errorf("order event mismatch")
		}
	}
	return nil
}

// checkRecords requires the stored order and charges to be exactly what the
// events witness, and the final receipt's counters to agree with them.
func checkRecords(in RunInput, j p.Journal, receipts []p.Receipt, chargeEvents map[string]bool) (p.Counters, error) {
	if len(j.Orders) != 1 || j.Orders[0].ID != receipts[3].OrderID || j.Orders[0].BusinessKey != in.BusinessKey || len(j.Charges) != len(chargeEvents) {
		return p.Counters{}, fmt.Errorf("record/event mismatch")
	}
	for _, charge := range j.Charges {
		if charge.BusinessKey != in.BusinessKey || !chargeEvents[charge.ID] {
			return p.Counters{}, fmt.Errorf("unwitnessed charge")
		}
	}
	actual := p.Counters{Orders: len(j.Orders), Charges: len(j.Charges), Attempts: 2, Triggers: 1}
	if receipts[3].Counters != actual {
		return p.Counters{}, fmt.Errorf("final counters contradict records")
	}
	return actual, nil
}

// assert evaluates the package's assertions against verified counters and
// derives each diagram binding's status from the assertions it names.
func (report *Report) assert(in RunInput, actual p.Counters) {
	counts := map[string]int{"orders": actual.Orders, "charges": actual.Charges, "attempts": actual.Attempts, "triggers": actual.Triggers}
	report.Status = "succeeded"
	for _, a := range in.Package.Assertions {
		scope := "actual_fixture"
		if a.Kind == "charges" {
			scope = "mocked"
		}
		result := AssertionResult{ID: a.ID, Kind: a.Kind, Expected: a.Expected, Actual: counts[a.Kind], Passed: counts[a.Kind] == a.Expected, Scope: scope}
		report.Assertions = append(report.Assertions, result)
		if !result.Passed {
			report.Status = "failed"
		}
	}
	for i := range report.Bindings {
		b := &report.Bindings[i]
		b.Status = "executed"
		for _, a := range report.Assertions {
			if slices.Contains(b.AssertionIDs, a.ID) {
				if !a.Passed {
					b.Status = "failed"
					break
				}
				if a.Scope == "mocked" {
					b.Status = "mocked"
				}
			}
		}
	}
}

type Comparison struct {
	Left       Report `json:"left"`
	Right      Report `json:"right"`
	Reproduced bool   `json:"reproduced"`
	Fixed      bool   `json:"fixed"`
}

// Compare requires identical program meaning, but deliberately retains distinct
// package/profile/source/build pins for the two immutable runs.
func Compare(left RunInput, leftReport Report, right RunInput, rightReport Report) (Comparison, error) {
	result := Comparison{Left: leftReport, Right: rightReport}
	for _, v := range []struct {
		in     RunInput
		report Report
	}{{left, leftReport}, {right, rightReport}} {
		if err := validateRun(v.in, v.report.Provenance); err != nil {
			return result, err
		}
		if v.report.RunID != v.in.RunID || v.report.Package != v.in.Start.Package || v.report.Profile != v.in.Start.Profile || v.report.Identity != v.in.Profile.Identity || v.report.CheckerVersion != CheckerVersion {
			return result, fmt.Errorf("report provenance mismatch")
		}
	}
	a, b := left.Package, right.Package
	if a.Format != b.Format || a.FixtureHash != b.FixtureHash || a.FailurePoint != b.FailurePoint || !reflect.DeepEqual(a.Steps, b.Steps) || !reflect.DeepEqual(a.Assertions, b.Assertions) || !reflect.DeepEqual(a.DiagramBindings, b.DiagramBindings) || !reflect.DeepEqual(a.DiagramScope, b.DiagramScope) || a.DiagramScopeHash != b.DiagramScopeHash || !reflect.DeepEqual(a.ExcludedIDs, b.ExcludedIDs) {
		return result, fmt.Errorf("unrelated replay programs")
	}
	result.Reproduced = leftReport.Status == "failed"
	result.Fixed = result.Reproduced && rightReport.Status == "succeeded"
	return result, nil
}
