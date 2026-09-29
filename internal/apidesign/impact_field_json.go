package apidesign

import "github.com/yashok111/mocker/internal/jsonx"

// The empty pointer selects a whole JSON body; keep it explicit while named
// parameters only carry their name.
func (f ImpactFieldSelector) MarshalJSON() ([]byte, error) {
	var pointer *string
	if f.Kind == "response" || f.Kind == "body" {
		pointer = &f.Pointer
	}
	return jsonx.Marshal(struct {
		Kind    string  `json:"kind"`
		Pointer *string `json:"pointer,omitempty"`
		Name    string  `json:"name,omitempty"`
	}{Kind: f.Kind, Pointer: pointer, Name: f.Name})
}
