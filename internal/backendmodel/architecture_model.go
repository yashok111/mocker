package backendmodel

type ArchitectureElement struct {
	Navigation     []ArchitectureNavigation `json:"navigation,omitempty"`
	Membership     *ArchitectureMembership  `json:"membership,omitzero"`
	ID             string                   `json:"id"`
	Label          string                   `json:"label"`
	Origin         DiagramOrigin            `json:"origin"`
	Refs           []DiagramRef             `json:"refs"`
	Role           string                   `json:"role"`
	ParentID       string                   `json:"parentId,omitempty"`
	Responsibility string                   `json:"responsibility"`
	Technology     string                   `json:"technology"`
}
type ArchitectureLink struct {
	ID       string        `json:"id"`
	Label    string        `json:"label"`
	Origin   DiagramOrigin `json:"origin"`
	Refs     []DiagramRef  `json:"refs"`
	From     string        `json:"from"`
	To       string        `json:"to"`
	Relation string        `json:"relation"`
}
type ArchitecturePayload struct {
	Elements        []ArchitectureElement `json:"elements"`
	Links           []ArchitectureLink    `json:"links"`
	PrimarySystemID string                `json:"primarySystemId"`
}
