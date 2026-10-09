package backendmodel

import "encoding/json/v2"

func InteractionStepKinds() []string {
	return []string{"request", "response", "send", "receive", "error", "boundary", "action"}
}

type InteractionParticipant struct {
	ID                    string        `json:"id"`
	Label                 string        `json:"label"`
	Origin                DiagramOrigin `json:"origin"`
	Refs                  []DiagramRef  `json:"refs"`
	ArchitectureElementID string        `json:"architectureElementId,omitempty"`
}
type InteractionStep struct {
	ID         string        `json:"id"`
	Label      string        `json:"label"`
	Origin     DiagramOrigin `json:"origin"`
	Refs       []DiagramRef  `json:"refs"`
	From       string        `json:"from"`
	To         string        `json:"to,omitempty"`
	Kind       string        `json:"kind"`
	BranchPath []string      `json:"branchPath"`
	ReplyTo    string        `json:"replyTo,omitempty"`
}
type InteractionOrder struct {
	ID     string        `json:"id"`
	From   string        `json:"from"`
	To     string        `json:"to"`
	Origin DiagramOrigin `json:"origin"`
}
type InteractionBranch struct {
	ID        string        `json:"id"`
	ParentID  string        `json:"parentId,omitempty"`
	GroupID   string        `json:"groupId"`
	Label     string        `json:"label"`
	Kind      string        `json:"kind"`
	GuardText string        `json:"guardText"`
	Origin    DiagramOrigin `json:"origin"`
}
type InteractionPayload struct {
	Architecture *DiagramPin              `json:"architecture,omitzero"`
	ScopeRefs    []DiagramRef             `json:"scopeRefs"`
	Participants []InteractionParticipant `json:"participants"`
	Steps        []InteractionStep        `json:"steps"`
	Order        []InteractionOrder       `json:"order"`
	Branches     []InteractionBranch      `json:"branches"`
}

func (v *InteractionParticipant) UnmarshalJSON(b []byte) error {
	type plain InteractionParticipant
	*v = InteractionParticipant{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs"}, []string{"architectureElementId"}, (*plain)(v))
}
func (v *InteractionStep) UnmarshalJSON(b []byte) error {
	type plain InteractionStep
	*v = InteractionStep{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs", "from", "kind", "branchPath"}, []string{"to", "replyTo"}, (*plain)(v))
}
func (v *InteractionOrder) UnmarshalJSON(b []byte) error {
	type plain InteractionOrder
	*v = InteractionOrder{}
	return strictAPIObject(b, []string{"id", "from", "to", "origin"}, nil, (*plain)(v))
}
func (v *InteractionBranch) UnmarshalJSON(b []byte) error {
	type plain InteractionBranch
	*v = InteractionBranch{}
	return strictAPIObject(b, []string{"id", "groupId", "label", "kind", "guardText", "origin"}, []string{"parentId"}, (*plain)(v))
}
func (v *InteractionPayload) UnmarshalJSON(b []byte) error {
	type plain InteractionPayload
	*v = InteractionPayload{}
	return strictAPIObject(b, []string{"scopeRefs", "participants", "steps", "order", "branches"}, []string{"architecture"}, (*plain)(v))
}

// Keep the architecture wire byte shape unchanged while admitting a second typed arm.
func (d DiagramDocument) MarshalJSON() ([]byte, error) {
	var payload any = d.Payload
	if d.Kind == "business_map" {
		payload = d.BusinessMap
	}
	if d.Kind == "lifecycle" {
		payload = d.Lifecycle
	}
	if d.Kind == "interactions" {
		payload = d.Interactions
	}
	return json.Marshal(struct {
		Format  string            `json:"format"`
		Kind    string            `json:"kind"`
		Target  BackendReadTarget `json:"target"`
		Payload any               `json:"payload"`
	}{d.Format, d.Kind, d.Target, payload})
}
