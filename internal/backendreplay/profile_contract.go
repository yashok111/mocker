package backendreplay

import (
	"fmt"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func (v Pin) Validate() error {
	if !p.ValidID(v.ID) || v.Version < 1 || !p.ValidHash(v.ContentHash) {
		return fmt.Errorf("invalid pin")
	}
	return nil
}
func (v Profile) Validate() error {
	if e := v.Pin.Validate(); e != nil {
		return e
	}
	h, e := p.IdentityHash(v.Identity)
	if e != nil {
		return e
	}
	if e := v.Authorization.Validate(); e != nil {
		return e
	}
	a := v.Authorization
	if v.TargetID != a.TargetID || v.ConfigVersion != a.ConfigVersion || v.IdentityHash != h || a.IdentityHash != h || a.IsolationID != v.Identity.IsolationID {
		return fmt.Errorf("profile authorization mismatch")
	}
	return nil
}
func (v StartInput) Validate() error {
	if e := v.Package.Validate(); e != nil {
		return e
	}
	if e := v.Profile.Validate(); e != nil {
		return e
	}
	if !p.ValidHash(v.ExpectedIdentityHash) || !p.ValidID(v.ResetAuthorizationID) || v.ResetAuthorizationVersion < 1 || !p.ValidID(v.IdempotencyKey) || (v.AcknowledgedPreviousRunID != "" && !p.ValidID(v.AcknowledgedPreviousRunID)) {
		return fmt.Errorf("invalid start authorization")
	}
	return nil
}
