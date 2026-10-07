// Package backendreplay currently supplies the 53.0 foundation only.
package backendreplay

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

const PackageFormat = "backend-replay-v1"
const CheckerVersion = "orders-checker-v1"
const MigrationReplay = 25
const ExecutionSeconds = 60
const Workers = 2
const QueueLimit = 20
const TargetConcurrency = 1
const HTTPSeconds = 3

type Pin struct {
	ID          string `json:"id"`
	Version     int64  `json:"version"`
	ContentHash string `json:"contentHash"`
}
type Step struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
}
type Assertion struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	StepID   string `json:"stepId"`
	Expected int    `json:"expected"`
}
type DiagramBinding struct {
	Diagram      backendmodel.DiagramPin `json:"diagram"`
	ElementID    string                  `json:"elementId"`
	StepID       string                  `json:"stepId"`
	AssertionIDs []string                `json:"assertionIds"`
}
type Package struct {
	DiagramScopeHash   string                          `json:"diagramScopeHash,omitempty"`
	Format             string                          `json:"format"`
	Target             backendmodel.BackendReadTarget  `json:"target"`
	TargetHash         string                          `json:"targetHash"`
	ArtifactPins       []backendmodel.ArtifactPin      `json:"artifactPins"`
	FindingFingerprint string                          `json:"findingFingerprint,omitempty"`
	Profile            Pin                             `json:"profile"`
	FixtureHash        string                          `json:"fixtureHash"`
	FailurePoint       string                          `json:"failurePoint"`
	Steps              []Step                          `json:"steps"`
	Assertions         []Assertion                     `json:"assertions"`
	DiagramScope       *backendmodel.DiagramScopeInput `json:"diagramScope,omitzero"`
	DiagramBindings    []DiagramBinding                `json:"diagramBindings,omitempty"`
	ExcludedIDs        []string                        `json:"excludedIds"`
}
type Profile struct {
	Pin           Pin                  `json:"pin"`
	TargetID      string               `json:"targetId"`
	ConfigVersion int64                `json:"configVersion"`
	Identity      p.Identity           `json:"identity"`
	IdentityHash  string               `json:"identityHash"`
	Authorization p.ResetAuthorization `json:"authorization"`
}
type StartInput struct {
	Package                   Pin    `json:"package"`
	Profile                   Pin    `json:"profile"`
	ExpectedIdentityHash      string `json:"expectedIdentityHash"`
	ResetAuthorizationID      string `json:"resetAuthorizationId"`
	ResetAuthorizationVersion int64  `json:"resetAuthorizationVersion"`
	IdempotencyKey            string `json:"idempotencyKey"`
	AcknowledgedPreviousRunID string `json:"acknowledgedPreviousRunId,omitempty"`
}
type RunInput struct {
	RunID       string     `json:"runId"`
	Start       StartInput `json:"start"`
	Package     Package    `json:"package"`
	Profile     Profile    `json:"profile"`
	BusinessKey string     `json:"businessKey"`
	Requests    []p.Fence  `json:"requests"`
}

// Save resolves diagram membership through this existing read-only seam. It
// cannot connect, reset or start a run. *backendmodel.Repo implements it today.
type DiagramReader interface {
	ResolveDiagramScope(context.Context, string, backendmodel.DiagramScopeInput) (*backendmodel.DiagramScope, error)
}

var _ DiagramReader = (*backendmodel.Repo)(nil)

type BindingEvidence struct {
	Package      Pin            `json:"package"`
	Binding      DiagramBinding `json:"binding"`
	Status       string         `json:"status"` // executed, mocked, skipped, unverified, failed
	AssertionIDs []string       `json:"assertionIds"`
}

func Template() Package {
	steps := []Step{
		{"01900000-0053-7000-8000-000000000001", "reset", "actual_fixture"},
		{"01900000-0053-7000-8000-000000000002", "arm", "actual_fixture"},
		{"01900000-0053-7000-8000-000000000003", "order_first", "actual_fixture"},
		{"01900000-0053-7000-8000-000000000004", "order_retry", "actual_fixture"},
		{"01900000-0053-7000-8000-000000000005", "journal", "actual_fixture"},
	}
	kinds := []string{"orders", "charges", "attempts", "triggers"}
	expected := []int{1, 1, 2, 1}
	a := make([]Assertion, 4)
	for i, k := range kinds {
		a[i] = Assertion{fmt.Sprintf("01900000-0053-7000-8000-%012d", 101+i), k, steps[4].ID, expected[i]}
	}
	return Package{Format: PackageFormat, FixtureHash: p.FixtureHash(), FailurePoint: p.FailurePoint, Steps: steps, Assertions: a, ArtifactPins: []backendmodel.ArtifactPin{}, ExcludedIDs: []string{}}
}
func (v Package) Validate() error {
	if v.Format != PackageFormat || v.FixtureHash != p.FixtureHash() || v.FailurePoint != p.FailurePoint || !p.ValidHash(v.TargetHash) {
		return fmt.Errorf("invalid package")
	}
	t := v.Target
	if t.Proposal != nil || t.ImportCandidate != nil || (t.RevisionID == "") == (t.ChangeProposal == nil) {
		return fmt.Errorf("unsupported target")
	}
	if t.RevisionID != "" && !p.ValidID(t.RevisionID) {
		return fmt.Errorf("invalid revision")
	}
	if t.ChangeProposal != nil && (!p.ValidID(t.ChangeProposal.ProposalID) || !p.ValidID(t.ChangeProposal.ProposalRevisionID)) {
		return fmt.Errorf("invalid proposal")
	}
	if e := v.Profile.Validate(); e != nil {
		return e
	}
	template := Template()
	if !reflect.DeepEqual(v.Steps, template.Steps) || !reflect.DeepEqual(v.Assertions, template.Assertions) {
		return fmt.Errorf("only fixed Orders program supported")
	}
	if len(v.DiagramBindings) > 1000 {
		return fmt.Errorf("binding limit")
	}
	if (v.DiagramScope == nil && v.DiagramScopeHash != "") || (v.DiagramScope != nil && !p.ValidHash(v.DiagramScopeHash)) {
		return fmt.Errorf("exact scope hash required")
	}
	if v.DiagramScope != nil {
		if e := v.DiagramScope.Validate(); e != nil {
			return e
		}
	}
	seen := map[string]bool{}
	for _, b := range v.DiagramBindings {
		if e := b.Diagram.Validate(); e != nil {
			return e
		}
		if !p.ValidID(b.ElementID) || !slices.ContainsFunc(v.Steps, func(s Step) bool { return s.ID == b.StepID }) || len(b.AssertionIDs) > 4 {
			return fmt.Errorf("invalid binding membership")
		}
		k := fmt.Sprint(b.Diagram, b.ElementID, b.StepID)
		if seen[k] {
			return fmt.Errorf("duplicate binding")
		}
		seen[k] = true
		ids := map[string]bool{}
		for _, id := range b.AssertionIDs {
			if ids[id] || !slices.ContainsFunc(v.Assertions, func(a Assertion) bool { return a.ID == id }) {
				return fmt.Errorf("invalid assertion membership")
			}
			ids[id] = true
		}
	}
	return nil
}
func PackageHash(v Package) (string, error) {
	if e := v.Validate(); e != nil {
		return "", e
	}
	return p.Hash(PackageFormat, v)
}
func ValidateDiagramBindings(ctx context.Context, r DiagramReader, pid string, v Package) error {
	if e := v.Validate(); e != nil {
		return e
	}
	check := func(in backendmodel.DiagramScopeInput, expectedScopeHash string) error {
		s, e := r.ResolveDiagramScope(ctx, pid, in)
		if e != nil {
			return e
		}
		if s == nil || s.Pin != in.Pin || s.TargetHash != v.TargetHash || !reflect.DeepEqual(s.Target, v.Target) || s.Truncated || len(s.Gaps) > 0 || (expectedScopeHash != "" && s.ScopeHash != expectedScopeHash) {
			// Review 2026-10-06, F1: a plain error here was answered as a
			// logged 500 backend_internal although narrowing the scope is the
			// caller's fix; it is the same class as "Target pin mismatch".
			return conflictReplay("Incompatible or incomplete diagram scope")
		}
		return nil
	}
	if v.DiagramScope != nil {
		if e := check(*v.DiagramScope, v.DiagramScopeHash); e != nil {
			return e
		}
	}
	for _, b := range v.DiagramBindings {
		in := backendmodel.DiagramScopeInput{Pin: b.Diagram, Selectors: []backendmodel.DiagramScopeSelector{{Kind: "semantic", ID: b.ElementID}}}
		if e := check(in, ""); e != nil {
			return e
		}
	}
	return nil
}
