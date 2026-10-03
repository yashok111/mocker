package backendmodel

type eventsPathSegment struct {
	Property string `json:"property,omitempty"`
	Items    bool   `json:"items,omitzero"`
}
