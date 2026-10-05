package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
)

func lifecycleComparisonMetadataID(a, b *LifecyclePayload) string {
	occupied := lifecycleRows(a)
	for id, v := range lifecycleRows(b) {
		occupied[id] = v
	}
	for salt := 0; salt <= len(occupied); salt++ {
		id := diagramIdentity("lifecycle-document-metadata-v1", strconv.Itoa(salt))
		if _, ok := occupied[id]; !ok {
			return id
		}
	}
	return ""
}
func lifecycleComparisonRows(p *LifecyclePayload, metadataID string) (map[string]map[string]jsontext.Value, error) {
	rows := map[string]map[string]jsontext.Value{}
	for id, v := range lifecycleRows(p) {
		raw, err := canonicalJSON(v)
		if err != nil {
			return nil, err
		}
		var fields map[string]jsontext.Value
		if err = json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		rows[id] = fields
	}
	if !ValidID(metadataID) {
		return nil, invalid("comparison", "Exact free metadata identity required")
	}
	fields := map[string]jsontext.Value{}
	for key, value := range map[string]any{"entity": p.Entity, "stateFields": p.StateFields, "compoundMappingReason": p.CompoundMappingReason, "coverage": p.Coverage, "coverageOrigin": p.CoverageOrigin} {
		raw, err := canonicalJSON(value)
		if err != nil {
			return nil, err
		}
		fields[key] = raw
	}
	rows[metadataID] = fields
	return rows, nil
}
