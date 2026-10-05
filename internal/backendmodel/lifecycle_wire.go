package backendmodel

import "encoding/json/v2"

func (v *LifecycleValue) UnmarshalJSON(b []byte) error {
	type plain LifecycleValue
	*v = LifecycleValue{}
	return strictAPIObject(b, []string{"json"}, nil, (*plain)(v))
}
func (v *LifecycleState) UnmarshalJSON(b []byte) error {
	type plain LifecycleState
	*v = LifecycleState{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs", "initial", "terminal"}, []string{"value"}, (*plain)(v))
}
func (v *LifecycleTransition) UnmarshalJSON(b []byte) error {
	type plain LifecycleTransition
	*v = LifecycleTransition{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs", "from", "to", "triggers", "writes", "events", "guard"}, nil, (*plain)(v))
}
func (v *LifecycleRule) UnmarshalJSON(b []byte) error {
	type plain LifecycleRule
	*v = LifecycleRule{}
	return strictAPIObject(b, []string{"id", "from", "to", "trigger", "verdict", "origin"}, nil, (*plain)(v))
}
func (v *LifecyclePayload) UnmarshalJSON(b []byte) error {
	type plain LifecyclePayload
	*v = LifecyclePayload{}
	return strictAPIObject(b, []string{"entity", "stateFields", "states", "transitions", "rules", "coverage", "coverageOrigin"}, []string{"compoundMappingReason"}, (*plain)(v))
}
func (v *LifecycleGuard) UnmarshalJSON(b []byte) error {
	type plain LifecycleGuard
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	fields := []string{"kind"}
	if head.Kind == "opaque" {
		fields = append(fields, "text")
	} else if head.Kind != "none" {
		return invalid("guard", "Unsupported guard")
	}
	*v = LifecycleGuard{}
	return strictAPIObject(b, fields, nil, (*plain)(v))
}
