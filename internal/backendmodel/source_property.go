package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
)

func sourceAttributeGroups(kind string) []string {
	groups := make([]string, 1, 40)
	groups[0] = "description"
	extra := map[string]string{
		"handler": "language qualifiedName", "symbol": "language qualifiedName", "http_operation": "method path",
		"datastore": "technology", "unresolved_target": "expectedKind reason searchScope",
		"flow":        "analysisStatus gaps entryStepId exitStepIds exitStatus",
		"flow_step":   "analysisStatus gaps stepKind transactionContext nativeText nativeReason expression reason dispatchStatus dispatchReason",
		"query":       "analysisStatus gaps dialect nativeDefinition definitionReason columnScope",
		"transaction": "analysisStatus gaps datastoreId connectionScope isolationLevel boundaryStatus",
		"branch":      "label condition", "error": "label outcome", "returns": "label",
		"reads": "accessMode datastoreId facetKey columnScope scopeReason", "writes": "accessMode datastoreId facetKey columnScope scopeReason", "deletes": "accessMode datastoreId facetKey columnScope scopeReason",
		"api_field":     "analysisStatus gaps direction location selector responseStatus mediaType nativeType",
		"field_mapping": "analysisStatus gaps transform transport", "channel": "analysisStatus gaps protocol address scope",
		"message": "analysisStatus gaps fieldInventory", "consumer": "analysisStatus gaps dispatchStatus dispatchReason", "job": "analysisStatus gaps dispatchStatus dispatchReason trigger",
		"event_field": "analysisStatus gaps section path nativeType", "emits": "channelId deliveryStatus deliveryReason",
		"delivered_to": "messageId condition group deliveryStatus deliveryReason", "retries": "messageId reason delay maxAttempts", "dead_letters": "messageId reason",
		"domain_entity": "qualifiedName analysisStatus gaps", "dto": "qualifiedName analysisStatus gaps", "api_schema": "qualifiedName analysisStatus gaps",
		"representation_field": "nativeType nullable cardinality analysisStatus gaps",
	}
	return append(groups, strings.Fields(extra[kind])...)
}

func sourceFacetGroups(kind string) []string {
	common := make([]string, 3, 16)
	copy(common, []string{"dialect", "analysisStatus", "gaps"})
	extra := map[string]string{
		"datastore": "databaseName qualifiedName nativeDefinition", "db_schema": "qualifiedName nativeDefinition", "table": "qualifiedName nativeDefinition constraintsStatus columnsStatus",
		"column":     "nativeType typeFamily nullable defaultExpression generatedExpression identity ordinal",
		"constraint": "constraintKind columnIds expression nativeDefinition deferrable initiallyDeferred",
		"index":      "terms unique predicate method nativeDefinition", "view": "qualifiedName materialized definition dependencyIds dependenciesStatus",
		"migration": "order parentIds definition changes derivationStatus", "symbol": "routineKind qualifiedName definition bodyStatus dependencyIds",
		"references": "columnPairs updateAction deleteAction matchType targetReason",
	}
	if _, ok := extra[kind]; !ok {
		return nil
	}
	return append(common, strings.Fields(extra[kind])...)
}

func SemanticPropertyGroups(schemaVersion, recordType, kind string) ([]SourcePropertyGroup, error) {
	if schemaVersion != ComposedSchemaVersion || recordType != "node" && recordType != "edge" {
		return nil, semantic("property", "Unsupported property schema or record type")
	}
	kinds := SupportedNodeKindsForProfile(ComposedProfile)
	if recordType == "edge" {
		kinds = SupportedEdgeKindsForProfile(ComposedProfile)
	}
	if !slices.Contains(kinds, kind) {
		return nil, semantic("property", "Unsupported record kind")
	}
	groups := []SourcePropertyGroup{}
	add := func(s TypedSourcePropertySelector) {
		groups = append(groups, SourcePropertyGroup{Selector: s, Atomic: true})
	}
	if recordType == "node" {
		add(TypedSourcePropertySelector{Kind: "name"})
		add(TypedSourcePropertySelector{Kind: "parent"})
	} else {
		add(TypedSourcePropertySelector{Kind: "edge_endpoints"})
	}
	for _, group := range sourceAttributeGroups(kind) {
		add(TypedSourcePropertySelector{Kind: "attributes", Group: group})
	}
	for _, group := range sourceFacetGroups(kind) {
		add(TypedSourcePropertySelector{Kind: "relational_facet", Group: group})
	}
	if kind == "flow_step" {
		for _, collection := range []string{"inputs", "outputs"} {
			add(TypedSourcePropertySelector{Kind: "flow_ports", Collection: collection})
		}
	}
	if kind == "query" {
		for _, collection := range []string{"parameters", "results"} {
			add(TypedSourcePropertySelector{Kind: "flow_ports", Collection: collection})
		}
	}
	if kind == "field_mapping" {
		add(TypedSourcePropertySelector{Kind: "mapping_sources"})
		add(TypedSourcePropertySelector{Kind: "mapping_destination"})
	}
	if kind == "representation_field" {
		add(TypedSourcePropertySelector{Kind: "representation_selector"})
	}
	return groups, nil
}

func validateSourceSelector(s TypedSourcePropertySelector) error {
	valid := false
	switch s.Kind {
	case "name", "parent", "edge_endpoints", "mapping_sources", "mapping_destination", "representation_selector":
		valid = s.Group == "" && s.FacetKey == "" && s.Collection == ""
	case "attributes":
		valid = nonblank(s.Group) && s.FacetKey == "" && s.Collection == ""
	case "relational_facet":
		valid = nonblank(s.Group) && externalKey(s.FacetKey) && s.Collection == ""
	case "flow_ports":
		valid = s.Group == "" && s.FacetKey == "" && slices.Contains([]string{"inputs", "outputs", "parameters", "results"}, s.Collection)
	}
	if !valid {
		return semantic("property", "Invalid typed semantic property selector")
	}
	return nil
}

func (s *TypedSourcePropertySelector) UnmarshalJSON(b []byte) error {
	type plain TypedSourcePropertySelector
	var value plain
	if err := json.Unmarshal(b, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if err := validateSourceSelector(TypedSourcePropertySelector(value)); err != nil {
		return err
	}
	members, err := relationalObject(b)
	if err != nil {
		return err
	}
	fields := []string{"kind"}
	switch value.Kind {
	case "attributes":
		fields = append(fields, "group")
	case "relational_facet":
		fields = append(fields, "group", "facetKey")
	case "flow_ports":
		fields = append(fields, "collection")
	}
	if err := relationalFields(members, fields, nil); err != nil {
		return err
	}
	*s = TypedSourcePropertySelector(value)
	return nil
}

func sourcePropertyPath(p SourceAssertionPayload, s TypedSourcePropertySelector) ([]string, error) {
	if err := validateSourceSelector(s); err != nil {
		return nil, err
	}
	groups, err := SemanticPropertyGroups(ComposedSchemaVersion, p.RecordType, p.Kind)
	if err != nil {
		return nil, err
	}
	found := false
	for _, g := range groups {
		expected := g.Selector
		if expected.Kind == "relational_facet" {
			expected.FacetKey = s.FacetKey
		}
		if expected == s {
			found = true
			break
		}
	}
	if !found {
		return nil, semantic("property", "Property group does not apply to this kind")
	}
	switch s.Kind {
	case "name":
		return []string{"name"}, nil
	case "parent":
		return []string{"parentId"}, nil
	case "edge_endpoints":
		return nil, nil
	case "attributes":
		return []string{"attributes", s.Group}, nil
	case "flow_ports":
		return []string{"attributes", s.Collection}, nil
	case "mapping_sources":
		return []string{"attributes", "sources"}, nil
	case "mapping_destination":
		return []string{"attributes", "destination"}, nil
	case "representation_selector":
		return []string{"attributes", "selector"}, nil
	case "relational_facet":
		path := []string{"attributes"}
		if p.Kind == "datastore" {
			path = append(path, "relational")
		}
		if p.Kind == "symbol" {
			path = append(path, "databaseRoutine")
		}
		return append(path, "facets", s.FacetKey, s.Group), nil
	}
	return nil, semantic("property", "Unsupported selector")
}

func SelectSourceProperty(p SourceAssertionPayload, s TypedSourcePropertySelector) (SourcePropertyValue, error) {
	path, err := sourcePropertyPath(p, s)
	if err != nil {
		return SourcePropertyValue{}, err
	}
	if s.Kind == "edge_endpoints" {
		b, err := canonicalJSON(struct {
			From string `json:"from"`
			To   string `json:"to"`
		}{p.From, p.To})
		return SourcePropertyValue{Present: true, Value: b}, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return SourcePropertyValue{}, err
	}
	for _, key := range path {
		var object map[string]jsontext.Value
		if err := json.Unmarshal(raw, &object); err != nil {
			return SourcePropertyValue{}, err
		}
		var ok bool
		raw, ok = object[key]
		if !ok {
			return SourcePropertyValue{}, nil
		}
	}
	return SourcePropertyValue{Present: true, Value: raw}, nil
}

func ApplySourceProperty(p SourceAssertionPayload, s TypedSourcePropertySelector, v SourcePropertyValue) (SourceAssertionPayload, error) {
	path, err := sourcePropertyPath(p, s)
	if err != nil {
		return p, err
	}
	if s.Kind == "edge_endpoints" {
		var pair struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if !v.Present {
			return p, semantic("property", "Endpoints cannot be absent")
		}
		if err := json.Unmarshal(v.Value, &pair, json.RejectUnknownMembers(true)); err != nil {
			return p, err
		}
		p.From, p.To = pair.From, pair.To
		return p, nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	var replace func(jsontext.Value, []string) (jsontext.Value, error)
	replace = func(raw jsontext.Value, path []string) (jsontext.Value, error) {
		object := map[string]jsontext.Value{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &object); err != nil {
				return nil, err
			}
		}
		object = maps.Clone(object)
		if len(path) == 1 {
			if v.Present {
				object[path[0]] = v.Value
			} else {
				delete(object, path[0])
			}
		} else {
			// An absent value prunes, it never builds: creating the
			// missing intermediates on the way to a deleted leaf left
			// `facets:{K:{}}` behind when the selected contender had no
			// facet K, and deleting every group of an existing facet left
			// a metadata-only shell — both fail structure validation as
			// backend_graph_invalid, so a legitimate offered contender
			// could not be committed (review 2026-10-06, F51).
			if _, ok := object[path[0]]; !ok && !v.Present {
				return json.Marshal(object)
			}
			next, err := replace(object[path[0]], path[1:])
			if err != nil {
				return nil, err
			}
			if !v.Present && s.Kind == "relational_facet" && len(path) == 2 && !sourceFacetHasGroup(p.Kind, next) {
				delete(object, path[0])
			} else {
				object[path[0]] = next
			}
		}
		return json.Marshal(object)
	}
	raw, err = replace(raw, path)
	if err != nil {
		return p, err
	}
	var result SourceAssertionPayload
	err = json.Unmarshal(raw, &result)
	return result, err
}

// sourceFacetHasGroup reports whether a facet object still carries any
// semantic group of its kind; what remains otherwise is per-facet metadata
// (evidence, snapshot) that describes nothing (F51).
func sourceFacetHasGroup(kind string, raw jsontext.Value) bool {
	var object map[string]jsontext.Value
	if json.Unmarshal(raw, &object) != nil {
		return true
	}
	for _, group := range sourceFacetGroups(kind) {
		if _, ok := object[group]; ok {
			return true
		}
	}
	return false
}

func sourceSelectors(payloads []SourceAssertionPayload) ([]TypedSourcePropertySelector, error) {
	if len(payloads) == 0 {
		return nil, nil
	}
	groups, err := SemanticPropertyGroups(ComposedSchemaVersion, payloads[0].RecordType, payloads[0].Kind)
	if err != nil {
		return nil, err
	}
	facets := map[string]bool{}
	for _, p := range payloads {
		if fs, _, err := relationalFacetObject(p.Kind, p.Attributes); err == nil {
			for key := range fs {
				facets[key] = true
			}
		}
	}
	result := []TypedSourcePropertySelector{}
	for _, g := range groups {
		if g.Selector.Kind != "relational_facet" {
			result = append(result, g.Selector)
			continue
		}
		for key := range facets {
			s := g.Selector
			s.FacetKey = key
			result = append(result, s)
		}
	}
	slices.SortFunc(result, func(a, b TypedSourcePropertySelector) int {
		return strings.Compare(sourcePropertyKey(a), sourcePropertyKey(b))
	})
	return result, nil
}

func sourcePropertyKey(s TypedSourcePropertySelector) string {
	b, _ := canonicalJSON(s)
	return string(b)
}
