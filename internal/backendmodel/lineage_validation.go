package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func lineageSubject(kind string, edge bool) bool {
	return !edge && (kind == "api_field" || kind == "field_mapping")
}
func lineageText(raw jsontext.Value) error {
	if err := runtimeText(raw); err != nil {
		return err
	}
	if strings.ContainsFunc(runtimeString(raw), func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) {
		return semantic("attributes", "Text must not contain control characters")
	}
	return nil
}
func validateLineageRef(raw jsontext.Value, persisted bool) error {
	return validateLineageRefMode(raw, persisted, false)
}
func validateEventsLineageRef(raw jsontext.Value, persisted bool) error {
	return validateLineageRefMode(raw, persisted, true)
}
func validateLineageRefMode(raw jsontext.Value, persisted, events bool) error {
	return validateLineageRefProfile(raw, persisted, events, false)
}
func validateLineageRefProfile(raw jsontext.Value, persisted, events, representations bool) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	key := "nodeKey"
	if persisted {
		key = "nodeId"
	}
	required := []string{"kind", key}
	switch runtimeString(m["kind"]) {
	case "column":
		required = append(required, "facetKey")
	case "port":
		required = append(required, "collection", "portKey")
	case "api_field":
	case "representation_field":
		if !representations || !persisted {
			return semantic("kind", "Representation references require scoped source6 input or normalized UUIDs")
		}
	case "event_field":
		if !events {
			return semantic("kind", "Event references require source5")
		}
		required = append(required, runtimeReferenceName("endpointKey", persisted), runtimeReferenceName("routeKey", persisted))
	default:
		return semantic("kind", "Unknown lineage value kind")
	}
	if err := relationalFields(m, required, nil); err != nil {
		return err
	}
	v := runtimeString(m[key])
	if !externalKey(v) || persisted && !ValidID(v) {
		return semantic(key, "Value reference requires an external key or canonical UUID in the selected mode")
	}
	if err := validateLineageContextualRef(m, persisted); err != nil {
		return err
	}
	if f, ok := m["facetKey"]; ok {
		if err := lineageText(f); err != nil {
			return err
		}
	}
	return validateLineagePortRef(m)
}
func validateLineageAttributes(kind string, a map[string]jsontext.Value, edge, persisted bool) error {
	return validateLineageAttributesMode(kind, a, edge, persisted, false)
}
func validateEventsLineageAttributes(kind string, a map[string]jsontext.Value, edge, persisted bool) error {
	return validateLineageAttributesMode(kind, a, edge, persisted, true)
}
func validateLineageAttributesMode(kind string, a map[string]jsontext.Value, edge, persisted, events bool) error {
	return validateLineageAttributesProfile(kind, a, edge, persisted, events, false)
}
func validateLineageAttributesProfile(kind string, a map[string]jsontext.Value, edge, persisted, events, representations bool) error {
	if !lineageSubject(kind, edge) {
		return validateRuntimeAttributes(kind, a, edge, persisted)
	}
	required := []string{"analysisStatus", "gaps"}
	optional := []string{"description"}
	if kind == "field_mapping" {
		required = append(required, "sources", "destination", "transform")
		if events {
			optional = append(optional, "transport")
		}
	} else {
		required = append(required, "direction", "location", "selector", "nativeType")
		optional = append(optional, "responseStatus", "mediaType")
	}
	if err := relationalFields(a, required, optional); err != nil {
		return err
	}
	if err := relationalEnum(a["analysisStatus"], "complete", "partial", "unsupported"); err != nil {
		return err
	}
	gaps, err := relationalArray(a["gaps"], MaxRevisionEvidence)
	if err != nil {
		return err
	}
	for _, gap := range gaps {
		if err := lineageText(gap); err != nil {
			return err
		}
	}
	status := runtimeString(a["analysisStatus"])
	if (status == "complete") != (len(gaps) == 0) {
		return semantic("gaps", "Complete analysis requires no gaps; partial and unsupported require gaps")
	}
	if d, ok := a["description"]; ok {
		if err := lineageText(d); err != nil {
			return err
		}
	}
	if kind == "api_field" {
		return validateAPIField(a)
	}
	return validateLineageMappingValues(a, persisted, events, representations, status)
}

func validateLineageMappingValues(a map[string]jsontext.Value, persisted, events, representations bool, status string) error {
	sources, err := relationalArray(a["sources"], MaxLineageSources)
	if err != nil {
		return err
	}
	seen := map[ImportLineageValueRef]bool{}
	for _, raw := range append(slices.Clone(sources), a["destination"]) {
		if err := validateLineageRefProfile(raw, persisted, events, representations); err != nil {
			return err
		}
	}
	for _, raw := range sources {
		// Normalize only the node member name, never the address components.
		m, _ := relationalObject(raw)
		key := "nodeKey"
		if persisted {
			key = "nodeId"
		}
		ref := ImportLineageValueRef{Kind: runtimeString(m["kind"]), NodeKey: runtimeString(m[key]), FacetKey: runtimeString(m["facetKey"]), Collection: runtimeString(m["collection"]), PortKey: runtimeString(m["portKey"]), EndpointKey: runtimeString(m[runtimeReferenceName("endpointKey", persisted)]), RouteKey: runtimeString(m[runtimeReferenceName("routeKey", persisted)])}
		if seen[ref] {
			return semantic("sources", "Duplicate exact value reference")
		}
		seen[ref] = true
	}
	if err := validateLineageTransport(a, persisted); err != nil {
		return err
	}
	return validateLineageTransform(a["transform"], len(sources), status)
}

func validateLineageTransform(raw jsontext.Value, count int, status string) error {
	tr, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err = relationalFields(tr, []string{"kind", "description", "redacted"}, nil); err != nil {
		return err
	}
	if err = lineageText(tr["description"]); err != nil {
		return err
	}
	var redacted bool
	if string(tr["redacted"]) != "true" && string(tr["redacted"]) != "false" || json.Unmarshal(tr["redacted"], &redacted) != nil {
		return semantic("transform/redacted", "Required boolean")
	}
	transform := runtimeString(tr["kind"])
	valid := false
	switch transform {
	case "copy", "rename":
		valid = count == 1
	case "constant":
		valid = count == 0
	case "flatten", "enum_map", "aggregate", "compute":
		valid = count >= 1
	case "unknown_transform":
		valid = status != "complete"
	}
	if !valid || status == "unsupported" && transform != "unknown_transform" {
		return semantic("transform", "Transform source count or analysis status is incompatible")
	}
	return nil
}

var lineageHTTPToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9a-z-]+$")
var lineageResponseStatus = regexp.MustCompile(`^([1-5][0-9][0-9]|[1-5]XX|default)$`)

func lineageFieldName(raw jsontext.Value) error {
	var name string
	if json.Unmarshal(raw, &name) != nil || name == "" || !utf8.ValidString(name) {
		return semantic("selector", "Expected a nonempty UTF-8 field name")
	}
	if utf8.RuneCountInString(name) > 200 {
		return limitFault("API selector names are limited to 200 characters")
	}
	return nil
}
func validateAPIField(a map[string]jsontext.Value) error {
	if err := relationalEnum(a["direction"], "request", "response"); err != nil {
		return err
	}
	if err := relationalEnum(a["location"], "path", "query", "header", "cookie", "body"); err != nil {
		return err
	}
	direction, location := runtimeString(a["direction"]), runtimeString(a["location"])
	if direction == "request" && a["responseStatus"] != nil {
		return semantic("responseStatus", "Request fields forbid response status")
	}
	if direction == "response" && (!slices.Contains([]string{"body", "header"}, location) || !lineageResponseStatus.MatchString(runtimeString(a["responseStatus"]))) {
		return semantic("responseStatus", "Response fields require body/header location and an exact response status")
	}
	sel, err := relationalObject(a["selector"])
	if err != nil {
		return err
	}
	if location == "body" {
		if err = relationalFields(sel, []string{"kind", "path"}, nil); err != nil {
			return err
		}
		if runtimeString(sel["kind"]) != "body" {
			return semantic("selector", "Body selector required")
		}
		media := runtimeString(a["mediaType"])
		first, second, ok := strings.Cut(media, "/")
		if !ok || !lineageHTTPToken.MatchString(first) || !lineageHTTPToken.MatchString(second) {
			return semantic("mediaType", "Explicit lowercase token/token media type required")
		}
		path, err := relationalArray(sel["path"], 32)
		if err != nil {
			return err
		}
		for _, segment := range path {
			m, err := relationalObject(segment)
			if err != nil {
				return err
			}
			if len(m) != 1 {
				return semantic("selector/path", "Expected one property or items segment")
			}
			if prop, ok := m["property"]; ok {
				if err := lineageFieldName(prop); err != nil {
					return err
				}
			} else if string(m["items"]) != "true" {
				return semantic("selector/path", "Array shape segment requires items:true")
			}
		}
	} else {
		if a["mediaType"] != nil {
			return semantic("mediaType", "Non-body fields forbid media type")
		}
		if err = relationalFields(sel, []string{"kind", "name"}, nil); err != nil {
			return err
		}
		if runtimeString(sel["kind"]) != "name" {
			return semantic("selector", "Name selector required")
		}
		if err = lineageFieldName(sel["name"]); err != nil {
			return err
		}
		if location == "header" && !lineageHTTPToken.MatchString(runtimeString(sel["name"])) {
			return semantic("selector/name", "Header names must be lowercase HTTP tokens")
		}
	}
	if err := runtimeScalar(a["nativeType"], false); err != nil {
		return err
	}
	return nil
}

func validateLineageContextualRef(m map[string]jsontext.Value, persisted bool) error {
	for _, member := range []string{runtimeReferenceName("endpointKey", persisted), runtimeReferenceName("routeKey", persisted)} {
		if raw, ok := m[member]; ok {
			value := runtimeString(raw)
			if !externalKey(value) || persisted && !ValidID(value) {
				return semantic(member, "Invalid contextual reference")
			}
		}
	}
	return nil
}

func validateLineageTransport(a map[string]jsontext.Value, persisted bool) error {
	if raw, ok := a["transport"]; ok {
		tr, err := relationalObject(raw)
		if err != nil {
			return err
		}
		keys := []string{runtimeReferenceName("emitsEdgeKey", persisted), runtimeReferenceName("deliveryEdgeKey", persisted)}
		if err := relationalFields(tr, keys, nil); err != nil {
			return err
		}
		for _, key := range keys {
			v := runtimeString(tr[key])
			if !externalKey(v) || persisted && !ValidID(v) {
				return semantic(key, "Invalid transport edge reference")
			}
		}
	}
	return nil
}

func validateLineagePortRef(m map[string]jsontext.Value) error {
	if p, ok := m["portKey"]; ok {
		s := runtimeString(p)
		if len(s) < 1 || len(s) > 128 || strings.ContainsFunc(s, func(r rune) bool { return r < 33 || r > 126 }) {
			return semantic("portKey", "Invalid opaque local port key")
		}
		if err := relationalEnum(m["collection"], "inputs", "outputs", "parameters", "results"); err != nil {
			return err
		}
	}
	return nil
}
