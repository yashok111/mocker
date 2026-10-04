package backendmodel

import "encoding/json/v2"

// Effective pins retain the exact baseline legacy or tagged artifact context.
// ArtifactContext's ordinary decoder remains closed to its tagged source arm.
func (pins *EffectiveGraphPins) UnmarshalJSON(raw []byte) error {
	type plain EffectiveGraphPins
	type contextValue ArtifactContext
	decoded := struct {
		*plain
		ArtifactContext *contextValue `json:"artifactContext"`
	}{plain: (*plain)(pins)}
	if err := json.Unmarshal(raw, &decoded, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if decoded.ArtifactContext != nil {
		context := ArtifactContext(*decoded.ArtifactContext)
		if _, err := EncodeArtifactContext(context, pins.ArtifactPins); err != nil {
			return err
		}
		pins.ArtifactContext = new(context)
	} else {
		pins.ArtifactContext = nil
	}
	return nil
}
