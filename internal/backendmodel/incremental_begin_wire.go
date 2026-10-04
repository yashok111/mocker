package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func (in *BeginImportInput) UnmarshalJSON(raw []byte) error {
	type plain BeginImportInput
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	var members map[string]jsontext.Value
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	_, present := members["changeManifest"]
	incremental := value.Profile == ComposedProfile && value.Mode == "composed" && value.SyncPolicy == IncrementalSourcePolicy
	if present && !incremental {
		return semantic("changeManifest", "Only incremental composed imports accept this member")
	}
	if incremental && (!present || value.ChangeManifest == nil) {
		return semantic("changeManifest", "Incremental imports require a non-null change manifest")
	}
	*in = BeginImportInput(value)
	return nil
}
