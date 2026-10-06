package backendreplay

import p "github.com/yashok111/mocker/internal/ordersprotocol"

// Public requests use the frozen strict codec: reject missing/unknown fields,
// duplicate names, explicit nulls and trailing values, including nested objects.
func (v *ConnectInput) UnmarshalJSON(raw []byte) error {
	type wire ConnectInput
	return p.Decode(raw, (*wire)(v), p.BodyLimit)
}
func (v *RevokeInput) UnmarshalJSON(raw []byte) error {
	type wire RevokeInput
	return p.Decode(raw, (*wire)(v), p.BodyLimit)
}
func (v *SavePackageInput) UnmarshalJSON(raw []byte) error {
	type wire SavePackageInput
	return p.Decode(raw, (*wire)(v), p.ReportLimit)
}
func (v *StartInput) UnmarshalJSON(raw []byte) error {
	type wire StartInput
	if err := p.Decode(raw, (*wire)(v), p.BodyLimit); err != nil {
		return err
	}
	return v.Validate()
}
