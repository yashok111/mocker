package backendmodel

// Lifecycle records explicit static claims and authored intent. It has no evaluator.
type LifecycleValue struct {
	JSON string `json:"json"`
}
type LifecycleGuard struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}
type LifecycleState struct {
	ID       string          `json:"id"`
	Label    string          `json:"label"`
	Origin   DiagramOrigin   `json:"origin"`
	Refs     []DiagramRef    `json:"refs"`
	Initial  bool            `json:"initial"`
	Terminal bool            `json:"terminal"`
	Value    *LifecycleValue `json:"value,omitzero"`
}
type LifecycleTransition struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Origin   DiagramOrigin  `json:"origin"`
	Refs     []DiagramRef   `json:"refs"`
	From     string         `json:"from"`
	To       string         `json:"to"`
	Triggers []DiagramRef   `json:"triggers"`
	Writes   []DiagramRef   `json:"writes"`
	Events   []DiagramRef   `json:"events"`
	Guard    LifecycleGuard `json:"guard"`
}
type LifecycleRule struct {
	ID      string        `json:"id"`
	From    string        `json:"from"`
	To      string        `json:"to"`
	Trigger DiagramRef    `json:"trigger"`
	Verdict string        `json:"verdict"`
	Origin  DiagramOrigin `json:"origin"`
}
type LifecyclePayload struct {
	Entity                DiagramRef            `json:"entity"`
	StateFields           []DiagramRef          `json:"stateFields"`
	CompoundMappingReason string                `json:"compoundMappingReason,omitempty"`
	States                []LifecycleState      `json:"states"`
	Transitions           []LifecycleTransition `json:"transitions"`
	Rules                 []LifecycleRule       `json:"rules"`
	Coverage              string                `json:"coverage"`
	CoverageOrigin        DiagramOrigin         `json:"coverageOrigin"`
}
