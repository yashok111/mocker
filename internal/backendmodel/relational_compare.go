package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strconv"
)

type FacetPairDifference struct {
	LeftFacetKey        string   `json:"leftFacetKey"`
	RightFacetKey       string   `json:"rightFacetKey"`
	Status              string   `json:"status"`
	ChangedPaths        []string `json:"changedPaths"`
	DefinitionDifferent bool     `json:"definitionDifferent"`
}
type FacetComparison struct {
	Status string                `json:"status"`
	Pairs  []FacetPairDifference `json:"pairs"`
}

// CompareRelationalFacets compares source claims without mutating or copying
// proof vectors into the computed read summary. Paths are facet-relative.
func CompareRelationalFacets(kind string, attrs map[string]jsontext.Value, edge bool) (*FacetComparison, error) {
	return compareRelationalFacetsMode(kind, attrs, edge, true)
}
func compareRelationalFacetsMode(kind string, attrs map[string]jsontext.Value, edge, sourceAdmission bool) (*FacetComparison, error) {
	if !relationalSubject(kind, attrs, edge) {
		return nil, nil
	}
	fs, _, err := relationalFacetObject(kind, attrs)
	if err != nil {
		return nil, err
	}
	keys := slices.Sorted(maps.Keys(fs))
	objects := map[string]map[string]jsontext.Value{}
	for _, key := range keys {
		if _, err := decodeRelationalFacetMode(kind, fs[key], true, sourceAdmission); err != nil {
			return nil, err
		}
		objects[key], err = relationalObject(fs[key])
		if err != nil {
			return nil, err
		}
	}
	out := &FacetComparison{Status: "unknown", Pairs: []FacetPairDifference{}}
	for i, left := range keys {
		for _, right := range keys[i+1:] {
			a, b := objects[left], objects[right]
			p := FacetPairDifference{LeftFacetKey: left, RightFacetKey: right, Status: "consistent", ChangedPaths: []string{}}
			uncertain := facetUncertain(a) || facetUncertain(b)
			for _, property := range slices.Sorted(maps.Keys(a)) {
				if slices.Contains([]string{"sourceKind", "dialect", "analysisStatus", "gaps", "evidenceIds", "freshness", "sourceSnapshotId", "columnsStatus", "constraintsStatus", "dependenciesStatus", "bodyStatus", "derivationStatus", "targetReason"}, property) {
					continue
				}
				if property == "nativeDefinition" || property == "definition" {
					different, _ := compareRelationalValue(a[property], b[property])
					p.DefinitionDifferent = p.DefinitionDifferent || different
					continue
				}
				if property == "dependencyIds" && (!rawStringEquals(a["dependenciesStatus"], "complete") && !rawStringEquals(a["bodyStatus"], "complete") || !rawStringEquals(b["dependenciesStatus"], "complete") && !rawStringEquals(b["bodyStatus"], "complete")) {
					uncertain = true
					continue
				}
				if property == "changes" && (!rawStringEquals(a["derivationStatus"], "complete") || !rawStringEquals(b["derivationStatus"], "complete")) {
					uncertain = true
					continue
				}
				different, unknown := compareRelationalValue(a[property], b[property])
				uncertain = uncertain || unknown
				if different {
					p.ChangedPaths = append(p.ChangedPaths, "/"+escapeRelationalPointer(property))
				}
			}
			if len(p.ChangedPaths) > 0 {
				p.Status = "different"
			} else if uncertain {
				p.Status = "unknown"
			}
			out.Pairs = append(out.Pairs, p)
		}
	}
	if len(out.Pairs) > 0 {
		out.Status = "consistent"
		for _, p := range out.Pairs {
			if p.Status == "different" {
				out.Status = "different"
				break
			}
			if p.Status == "unknown" {
				out.Status = "unknown"
			}
		}
	}
	return out, nil
}
func facetUncertain(m map[string]jsontext.Value) bool {
	for _, key := range []string{"analysisStatus", "columnsStatus", "constraintsStatus", "dependenciesStatus", "bodyStatus", "derivationStatus"} {
		if raw, ok := m[key]; ok && !rawStringEquals(raw, "complete") {
			return true
		}
	}
	fresh, err := relationalObject(m["freshness"])
	return err != nil || !rawStringEquals(fresh["status"], "current")
}
func rawStringEquals(raw jsontext.Value, want string) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && s == want
}

// Unknown wrappers and nested unknown enums cannot prove either equality or
// disagreement. Independent known differences still survive the uncertainty.
func compareRelationalValue(a, b jsontext.Value) (different, unknown bool) {
	a, b = bytes.TrimSpace(a), bytes.TrimSpace(b)
	if len(a) == 0 || len(b) == 0 {
		return false, true
	}
	if a[0] == '{' && b[0] == '{' {
		am, _ := relationalObject(a)
		bm, _ := relationalObject(b)
		if am["status"] != nil || bm["status"] != nil {
			if !rawStringEquals(am["status"], "known") || !rawStringEquals(bm["status"], "known") {
				return false, true
			}
			return compareRelationalValue(am["value"], bm["value"])
		}
		keys := map[string]bool{}
		for key := range am {
			keys[key] = true
		}
		for key := range bm {
			keys[key] = true
		}
		for key := range keys {
			av, bv := am[key], bm[key]
			if slices.Contains([]string{"direction", "nulls", "operation"}, key) && (rawStringEquals(av, "unknown") || rawStringEquals(bv, "unknown")) {
				unknown = true
				continue
			}
			if av == nil || bv == nil {
				different = true
				continue
			}
			d, u := compareRelationalValue(av, bv)
			different, unknown = different || d, unknown || u
		}
		return
	}
	if a[0] == '[' && b[0] == '[' {
		var aa, ba []jsontext.Value
		_ = json.Unmarshal(a, &aa)
		_ = json.Unmarshal(b, &ba)
		different = len(aa) != len(ba)
		for i := range min(len(aa), len(ba)) {
			d, u := compareRelationalValue(aa[i], ba[i])
			different, unknown = different || d, unknown || u
		}
		return
	}
	if a[0] == '"' && b[0] == '"' {
		var as, bs string
		_ = json.Unmarshal(a, &as)
		_ = json.Unmarshal(b, &bs)
		return as != bs, false
	}
	// All numeric properties here are exact bounded integers, never floats.
	ai, ae := strconv.ParseInt(string(a), 10, 64)
	bi, be := strconv.ParseInt(string(b), 10, 64)
	if ae == nil && be == nil {
		return ai != bi, false
	}
	return !bytes.Equal(a, b), false
}
