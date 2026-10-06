package ordersprotocol

import (
	"fmt"
	"slices"
)

func (v Identity) Validate() error {
	if v.Protocol != Version || v.Service != "orders-reference" || v.ServiceVersion == "" || !v.TestOnly || !ValidID(v.IsolationID) || !ValidID(v.InstanceID) || !slices.Contains([]string{"buggy", "fixed"}, v.Variant) || !ValidHash(v.BuildHash) || !ValidHash(v.SourceTreeHash) || v.SourcePolicy != ManifestPolicy || v.FixtureHash != FixtureHash() {
		return fmt.Errorf("invalid identity")
	}
	return nil
}
func (v IdentityResponse) Validate() error {
	h, e := IdentityHash(v.Identity)
	if e != nil {
		return e
	}
	if h != v.IdentityHash || v.Epoch < 0 {
		return fmt.Errorf("identity hash/epoch mismatch")
	}
	return nil
}
func (v Fence) Validate() error {
	if !ValidID(v.RunID) || !ValidID(v.StepID) || !ValidID(v.RequestKey) || v.Epoch < 0 || !ValidHash(v.IdentityHash) {
		return fmt.Errorf("invalid fence")
	}
	return nil
}
func (v ResetAuthorization) Validate() error {
	if !ValidID(v.ID) || v.Version < 1 || v.ConfigVersion < 1 || v.TargetID == "" || !ValidHash(v.IdentityHash) || !ValidID(v.IsolationID) || !v.AllowReset {
		return fmt.Errorf("reset authorization required")
	}
	return nil
}
func (v ResetRequest) Validate() error {
	if e := v.Fence.Validate(); e != nil {
		return e
	}
	if e := v.Authorization.Validate(); e != nil {
		return e
	}
	if v.Authorization.IdentityHash != v.IdentityHash || v.FixtureHash != FixtureHash() {
		return fmt.Errorf("reset identity/fixture mismatch")
	}
	return nil
}
func (v FailureRequest) Validate() error {
	if e := v.Fence.Validate(); e != nil {
		return e
	}
	if v.Epoch < 1 || v.Point != FailurePoint || v.Count != 1 {
		return fmt.Errorf("unsupported failure")
	}
	return nil
}
func (v OrderRequest) Validate() error {
	if e := v.Fence.Validate(); e != nil {
		return e
	}
	if v.Epoch < 1 || !ValidID(v.BusinessKey) || (v.Attempt != 1 && v.Attempt != 2) || v.Order != Fixture().Order {
		return fmt.Errorf("unsupported order")
	}
	return nil
}
