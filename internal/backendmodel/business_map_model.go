package backendmodel

// Business maps declare intent and exact implementation mappings, never execution.
type BusinessElement struct {
	ID                    string        `json:"id"`
	Label                 string        `json:"label"`
	Origin                DiagramOrigin `json:"origin"`
	Refs                  []DiagramRef  `json:"refs"`
	Role                  string        `json:"role"`
	Responsibility        string        `json:"responsibility"`
	ArchitectureElementID string        `json:"architectureElementId,omitempty"`
}
type BusinessLink struct {
	ID       string        `json:"id"`
	Label    string        `json:"label"`
	Origin   DiagramOrigin `json:"origin"`
	Refs     []DiagramRef  `json:"refs"`
	From     string        `json:"from"`
	To       string        `json:"to"`
	Relation string        `json:"relation"`
}
type BusinessMapPayload struct {
	Architecture *DiagramPin       `json:"architecture,omitzero"`
	Elements     []BusinessElement `json:"elements"`
	Links        []BusinessLink    `json:"links"`
}

func (v *BusinessElement) UnmarshalJSON(b []byte) error {
	type plain BusinessElement
	*v = BusinessElement{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs", "role", "responsibility"}, []string{"architectureElementId"}, (*plain)(v))
}
func (v *BusinessLink) UnmarshalJSON(b []byte) error {
	type plain BusinessLink
	*v = BusinessLink{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs", "from", "to", "relation"}, nil, (*plain)(v))
}
func (v *BusinessMapPayload) UnmarshalJSON(b []byte) error {
	type plain BusinessMapPayload
	*v = BusinessMapPayload{}
	return strictAPIObject(b, []string{"elements", "links"}, []string{"architecture"}, (*plain)(v))
}
