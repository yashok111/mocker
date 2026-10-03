package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"strings"
)

func contextualLineageMapping(kind string, a map[string]jsontext.Value, edge bool) bool {
	if edge || kind != "field_mapping" {
		return false
	}
	if _, ok := a["transport"]; ok {
		return true
	}
	sources, _ := relationalArray(a["sources"], MaxLineageSources)
	for _, raw := range append(sources, a["destination"]) {
		m, _ := relationalObject(raw)
		if runtimeString(m["kind"]) == "event_field" {
			return true
		}
	}
	return false
}
func eventsLineageReferences(kind string, a map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	if !contextualLineageMapping(kind, a, edge) {
		return lineageReferences(kind, a, edge, persisted)
	}
	if err := validateEventsLineageAttributes(kind, a, edge, persisted); err != nil {
		return nil, err
	}
	refs := []relationalReference{}
	add := func(path, kind, typ, value string) {
		r := relationalReference{Path: "/attributes/" + path, Kind: kind, RecordType: typ, Key: value}
		if persisted {
			r.ID, r.Key = value, ""
		}
		refs = append(refs, r)
	}
	key := runtimeReferenceName("nodeKey", persisted)
	sources, _ := relationalArray(a["sources"], MaxLineageSources)
	for i, raw := range append(sources, a["destination"]) {
		m, _ := relationalObject(raw)
		path := "destination/"
		if i < len(sources) {
			path = fmt.Sprintf("sources/%d/", i)
		}
		kind := runtimeString(m["kind"])
		add(path+key, kind, "node", runtimeString(m[key]))
		if kind == "event_field" {
			ep := runtimeReferenceName("endpointKey", persisted)
			route := runtimeReferenceName("routeKey", persisted)
			add(path+ep, "event_endpoint", "node", runtimeString(m[ep]))
			add(path+route, "event_route", "edge", runtimeString(m[route]))
		}
	}
	if raw, ok := a["transport"]; ok {
		m, _ := relationalObject(raw)
		for _, x := range []struct{ key, kind string }{{"emitsEdgeKey", "emits"}, {"deliveryEdgeKey", "delivered_to"}} {
			key := runtimeReferenceName(x.key, persisted)
			add("transport/"+key, x.kind, "edge", runtimeString(m[key]))
		}
	}
	return refs, nil
}
func resolveEventsLineageAttributes(kind string, a map[string]jsontext.Value, resolve func(string, string, string) string) (map[string]jsontext.Value, error) {
	if !contextualLineageMapping(kind, a, false) {
		return resolveLineageAttributes(kind, a, resolve)
	}
	refs, err := eventsLineageReferences(kind, a, false, false)
	if err != nil {
		return nil, err
	}
	replacements := map[string]jsontext.Value{}
	for _, ref := range refs {
		replacements[ref.Path], err = json.Marshal(resolve(ref.RecordType, ref.Key, ref.Path))
		if err != nil {
			return nil, err
		}
	}
	var walk func(jsontext.Value, string) (jsontext.Value, error)
	walk = func(raw jsontext.Value, path string) (jsontext.Value, error) {
		if value, ok := replacements[path]; ok {
			return value, nil
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(string(raw)), "{"):
			m, err := relationalObject(raw)
			if err != nil {
				return nil, err
			}
			out := map[string]jsontext.Value{}
			for key, value := range m {
				v, err := walk(value, path+"/"+key)
				if err != nil {
					return nil, err
				}
				target := key
				if _, ok := replacements[path+"/"+key]; ok {
					target = runtimeReferenceName(key, true)
				}
				out[target] = v
			}
			return json.Marshal(out)
		case strings.HasPrefix(strings.TrimSpace(string(raw)), "["):
			var values []jsontext.Value
			if err := json.Unmarshal(raw, &values); err != nil {
				return nil, err
			}
			for i := range values {
				var err error
				values[i], err = walk(values[i], fmt.Sprintf("%s/%d", path, i))
				if err != nil {
					return nil, err
				}
			}
			return json.Marshal(values)
		default:
			return raw, nil
		}
	}
	out := maps.Clone(a)
	for key, value := range out {
		out[key], err = walk(value, "/attributes/"+key)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
