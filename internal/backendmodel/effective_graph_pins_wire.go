package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func (pins EffectiveGraphPins) MarshalJSON() ([]byte, error) {
	type plain EffectiveGraphPins
	if pins.ArtifactContextV3 == nil {
		return json.Marshal(plain(pins))
	}
	if pins.ArtifactContext != nil || len(pins.ArtifactPins) != 0 {
		return nil, invalid("context", "Mixed legacy and namespaced contexts")
	}
	raw, err := EncodeArtifactContextV3(*pins.ArtifactContextV3)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		plain
		ArtifactContext jsontext.Value `json:"artifactContext"`
	}{plain(pins), raw})
}

func (pins *EffectiveGraphPins) UnmarshalJSON(raw []byte) error {
	type plain EffectiveGraphPins
	var value plain
	decoded := struct {
		*plain
		ArtifactContext jsontext.Value `json:"artifactContext"`
	}{plain: &value}
	if err := json.Unmarshal(raw, &decoded, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if len(decoded.ArtifactContext) > 0 && string(decoded.ArtifactContext) != "null" {
		// Historical effective pins encode the legacy in-memory shape, not the storage codec.
		var tag struct {
			DocumentVersion string `json:"documentVersion"`
		}
		if err := json.Unmarshal(decoded.ArtifactContext, &tag); err != nil {
			return err
		}
		if tag.DocumentVersion == ArtifactContextV3Version {
			c, err := DecodeVersionedArtifactContext(decoded.ArtifactContext, value.ArtifactPins)
			if err != nil {
				return err
			}
			value.ArtifactContextV3 = c.V3
		} else {
			type contextValue ArtifactContext
			var c contextValue
			if err := json.Unmarshal(decoded.ArtifactContext, &c, json.RejectUnknownMembers(true)); err != nil {
				return err
			}
			if _, err := EncodeArtifactContext(ArtifactContext(c), value.ArtifactPins); err != nil {
				return err
			}
			value.ArtifactContext = new(ArtifactContext(c))
		}
	}
	*pins = EffectiveGraphPins(value)
	return nil
}
