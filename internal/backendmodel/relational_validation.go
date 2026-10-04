package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func relationalSubject(kind string, attrs map[string]jsontext.Value, edge bool) bool {
	return edge && kind == "references" || !edge && slices.Contains([]string{"db_schema", "table", "column", "constraint", "index", "view", "migration"}, kind) || !edge && (kind == "datastore" && attrs["relational"] != nil || kind == "symbol" && attrs["databaseRoutine"] != nil)
}
func relationalFacetObject(kind string, a map[string]jsontext.Value) (map[string]jsontext.Value, string, error) {
	path := "/attributes"
	container := a
	descriptor := ""
	if kind == "datastore" {
		descriptor = "relational"
	}
	if kind == "symbol" {
		descriptor = "databaseRoutine"
	}
	if descriptor != "" {
		path += "/" + descriptor
		var err error
		container, err = relationalObject(a[descriptor])
		if err != nil {
			return nil, path, err
		}
		if len(container) != 1 || container["facets"] == nil {
			return nil, path, semantic(path, "Descriptor requires exactly facets")
		}
	}
	f, err := relationalObject(container["facets"])
	return f, path + "/facets", err
}
func relationalObject(raw jsontext.Value) (map[string]jsontext.Value, error) {
	var m map[string]jsontext.Value
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' || json.Unmarshal(raw, &m) != nil {
		return nil, semantic("attributes", "Expected a strict JSON object")
	}
	return m, nil
}
func relationalFields(m map[string]jsontext.Value, required, optional []string) error {
	for k, v := range m {
		if !slices.Contains(required, k) && !slices.Contains(optional, k) {
			return semantic(k, "Unknown relational member")
		}
		if !v.IsValid() {
			return semantic(k, "Invalid JSON value")
		}
	}
	for _, k := range required {
		if m[k] == nil {
			return semantic(k, "Required relational member is missing")
		}
	}
	return nil
}
func relationalText(raw jsontext.Value, nullable, native bool) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && nullable {
		return nil
	}
	var s string
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '"' || json.Unmarshal(raw, &s) != nil || !utf8.ValidString(s) {
		return semantic("attributes", "Expected a UTF-8 string")
	}
	if native {
		if len(s) > MaxRelationalNativeBytes {
			return limitFault("Relational native UTF-8 byte limit exceeded")
		}
	} else if !nonblank(s) {
		return semantic("attributes", "Expected a nonblank string")
	}
	return nil
}
func relationalArray(raw jsontext.Value, maxCount int) ([]jsontext.Value, error) {
	var a []jsontext.Value
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' || json.Unmarshal(raw, &a) != nil {
		return nil, semantic("attributes", "Expected a non-null array")
	}
	if len(a) > maxCount {
		return nil, limitFault("Relational array limit exceeded")
	}
	return a, nil
}
func relationalStrings(raw jsontext.Value, maxCount int, ids bool) ([]string, error) {
	a, err := relationalArray(raw, maxCount)
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range a {
		var s string
		if json.Unmarshal(v, &s) != nil || !externalKey(s) || ids && !ValidID(s) || seen[s] {
			return nil, semantic("attributes", "References must be valid and unique")
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}
func relationalEnum(raw jsontext.Value, choices ...string) error {
	var s string
	if json.Unmarshal(raw, &s) != nil || !slices.Contains(choices, s) {
		return semantic("attributes", "Invalid relational enum")
	}
	return nil
}
func relationalScalarValue(raw jsontext.Value, typ string, nullable bool, choices ...string) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	var status string
	if json.Unmarshal(m["status"], &status) != nil {
		return semantic("scalar/status", "Required scalar status")
	}
	if status == "unknown" {
		if err = relationalFields(m, []string{"status", "reason"}, nil); err != nil {
			return err
		}
		return relationalText(m["reason"], false, false)
	}
	if status != "known" {
		return semantic("scalar/status", "Unsupported scalar status")
	}
	if err = relationalFields(m, []string{"status", "value"}, nil); err != nil {
		return err
	}
	value := bytes.TrimSpace(m["value"])
	if bytes.Equal(value, []byte("null")) {
		if nullable {
			return nil
		}
		return semantic("scalar/value", "Known value cannot be null")
	}
	switch typ {
	case "string":
		if err = relationalText(value, false, true); err != nil {
			return err
		}
		if len(choices) > 0 {
			return relationalEnum(value, choices...)
		}
	case "bool":
		if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
			return semantic("scalar/value", "Expected a boolean")
		}
	case "positive", "nonnegative":
		i, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil || typ == "positive" && i <= 0 || typ == "nonnegative" && i < 0 {
			return semantic("scalar/value", "Expected an exact bounded int64")
		}
	}
	return nil
}
func validateRelationalAttributes(kind string, attrs map[string]jsontext.Value, edge, persisted bool) error {
	return validateRelationalAttributesMode(kind, attrs, edge, persisted, true)
}

func validateRelationalStructureAttributes(kind string, attrs map[string]jsontext.Value, edge bool) error {
	return validateRelationalAttributesMode(kind, attrs, edge, true, false)
}

func validateRelationalAttributesMode(kind string, attrs map[string]jsontext.Value, edge, persisted, sourceAdmission bool) error {
	if !relationalSubject(kind, attrs, edge) {
		return validateAttributes(kind, attrs, edge)
	}
	allowed := []string{"facets", "description"}
	if kind == "datastore" {
		allowed = []string{"relational", "technology", "description"}
	}
	if kind == "symbol" {
		allowed = []string{"databaseRoutine", "language", "qualifiedName", "description"}
	}
	for k, v := range attrs {
		if !slices.Contains(allowed, k) {
			return semantic("attributes/"+k, "Unknown relational attribute")
		}
		if k != "facets" && k != "relational" && k != "databaseRoutine" {
			if err := validateAttributes(kind, map[string]jsontext.Value{k: v}, edge); err != nil {
				return err
			}
		}
	}
	fs, _, err := relationalFacetObject(kind, attrs)
	if err != nil {
		return err
	}
	if len(fs) == 0 {
		return semantic("facets", "Relational subject requires facets")
	}
	if len(fs) > MaxRelationalFacets {
		return limitFault("Relational facet limit exceeded")
	}
	for key, raw := range fs {
		if !externalKey(key) {
			return semantic("facets", "Invalid facet key")
		}
		if _, err = decodeRelationalFacetMode(kind, raw, persisted, sourceAdmission); err != nil {
			return err
		}
	}
	return nil
}
func decodeRelationalFacet(kind string, raw jsontext.Value, persisted bool) (*relationalFacet, error) {
	return decodeRelationalFacetMode(kind, raw, persisted, true)
}

// Source provenance is admitted only on import paths. Desired facets use the
// same typed values and UUID references without manufacturing source proof.
func decodeRelationalFacetMode(kind string, raw jsontext.Value, persisted, sourceAdmission bool) (*relationalFacet, error) {
	m, err := relationalObject(raw)
	if err != nil {
		return nil, err
	}
	refs := func(key string) string {
		if !persisted {
			return key + "Keys"
		}
		return key + "Ids"
	}
	required := []string{"dialect", "analysisStatus", "gaps"}
	optional := []string{}
	provenance := []string{"sourceKind", refs("evidence")}
	if persisted {
		provenance = append(provenance, "freshness", "sourceSnapshotId")
	}
	if sourceAdmission {
		required = append(required, provenance...)
	} else {
		optional = append(optional, provenance...)
	}
	switch kind {
	case "datastore":
		required = append(required, "databaseName", "qualifiedName", "nativeDefinition")
	case "db_schema":
		required = append(required, "qualifiedName", "nativeDefinition")
	case "table":
		required = append(required, "qualifiedName", "nativeDefinition", "columnsStatus", "constraintsStatus")
	case "column":
		required = append(required, "nativeType", "typeFamily", "nullable", "defaultExpression", "generatedExpression", "identity", "ordinal")
	case "constraint":
		required = append(required, "constraintKind", refs("column"), "expression", "nativeDefinition", "deferrable", "initiallyDeferred")
	case "index":
		required = append(required, "terms", "unique", "predicate", "method", "nativeDefinition")
	case "view":
		required = append(required, "qualifiedName", "materialized", "definition", refs("dependency"), "dependenciesStatus")
	case "migration":
		required = append(required, "order", refs("parent"), "definition", "changes", "derivationStatus")
	case "symbol":
		required = append(required, "routineKind", "qualifiedName", "definition", refs("dependency"), "bodyStatus")
	case "references":
		required = append(required, "columnPairs", "updateAction", "deleteAction", "matchType")
		optional = append(optional, "targetReason")
	default:
		return nil, semantic("kind", "Unsupported relational facet kind")
	}
	if err = relationalFields(m, required, optional); err != nil {
		return nil, err
	}
	f := new(relationalFacet)
	if err = json.Unmarshal(raw, f, json.RejectUnknownMembers(true)); err != nil {
		return nil, semantic("facets", err.Error())
	}
	for _, x := range []struct {
		key     string
		choices []string
	}{{"sourceKind", []string{"sql", "orm", "migration"}}, {"dialect", []string{"postgresql", "sqlite"}}, {"analysisStatus", []string{"complete", "partial", "unsupported"}}, {"constraintKind", []string{"primary_key", "unique", "check", "foreign_key"}}, {"routineKind", []string{"procedure", "function", "trigger"}}} {
		if v, ok := m[x.key]; ok {
			if err = relationalEnum(v, x.choices...); err != nil {
				return nil, err
			}
		}
	}
	gaps, err := relationalArray(m["gaps"], MaxRevisionEvidence)
	if err != nil {
		return nil, err
	}
	for _, v := range gaps {
		if err = relationalText(v, false, false); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"analysisStatus", "columnsStatus", "constraintsStatus", "dependenciesStatus", "derivationStatus", "bodyStatus"} {
		if v, ok := m[key]; ok {
			if err = relationalEnum(v, "complete", "partial", "unsupported"); err != nil {
				return nil, err
			}
			var status string
			_ = json.Unmarshal(v, &status)
			if status != "complete" && len(gaps) == 0 {
				return nil, semantic(key, "Incomplete analysis requires specific gaps")
			}
		}
	}
	if sourceAdmission {
		evidence, err := relationalStrings(m[refs("evidence")], MaxRevisionEvidence, persisted)
		if err != nil {
			return nil, err
		}
		if len(evidence) == 0 {
			return nil, semantic("facets", "Facet proof is required")
		}
		if persisted {
			if !ValidID(f.SourceSnapshotID) || f.Freshness == nil || !slices.Contains([]string{"current", "stale"}, f.Freshness.Status) || f.Freshness.ConfirmedSnapshotID != f.SourceSnapshotID || f.Freshness.Reasons == nil {
				return nil, semantic("facets", "Invalid persisted facet provenance")
			}
		}
	}
	for _, key := range []string{"qualifiedName", "databaseName", "targetReason"} {
		if v, ok := m[key]; ok {
			if err = relationalText(v, false, false); err != nil {
				return nil, err
			}
		}
	}
	for _, key := range []string{"nativeDefinition", "definition"} {
		if v, ok := m[key]; ok {
			if err = relationalText(v, key == "nativeDefinition", true); err != nil {
				return nil, err
			}
		}
	}
	for _, x := range []struct {
		key, typ string
		nullable bool
		choices  []string
	}{{"nativeType", "string", false, nil}, {"typeFamily", "string", false, []string{"boolean", "integer", "decimal", "float", "string", "binary", "date", "time", "timestamp", "json", "uuid", "array", "other"}}, {"nullable", "bool", false, nil}, {"defaultExpression", "string", true, nil}, {"generatedExpression", "string", true, nil}, {"identity", "string", true, nil}, {"ordinal", "positive", false, nil}, {"expression", "string", true, nil}, {"deferrable", "bool", true, nil}, {"initiallyDeferred", "bool", true, nil}, {"unique", "bool", false, nil}, {"predicate", "string", true, nil}, {"method", "string", true, nil}, {"order", "nonnegative", true, nil}, {"updateAction", "string", false, []string{"no_action", "restrict", "cascade", "set_null", "set_default"}}, {"deleteAction", "string", false, []string{"no_action", "restrict", "cascade", "set_null", "set_default"}}, {"matchType", "string", false, []string{"simple", "full", "partial"}}} {
		if v, ok := m[x.key]; ok {
			if err = relationalScalarValue(v, x.typ, x.nullable, x.choices...); err != nil {
				return nil, err
			}
		}
	}
	for _, key := range []string{"column", "dependency", "parent"} {
		if v, ok := m[refs(key)]; ok {
			maxCount := MaxRelationalReferences
			if key == "column" {
				maxCount = MaxRelationalOrderedColumns
			}
			values, err := relationalStrings(v, maxCount, persisted)
			if err != nil {
				return nil, err
			}
			if key == "column" && len(values) == 0 && f.ConstraintKind != "check" {
				return nil, semantic("columnKeys", "Non-CHECK constraints require columns")
			}
		}
	}
	if kind == "constraint" && f.ConstraintKind == "check" {
		if bytes.Equal(bytes.TrimSpace(f.Expression.Value), []byte("null")) || f.Expression.Status == "unknown" && (f.NativeDefinition == nil || len(gaps) == 0) {
			return nil, semantic("expression", "CHECK requires expression or native definition with a gap")
		}
	}
	if kind == "view" {
		v := bytes.TrimSpace(m["materialized"])
		if !bytes.Equal(v, []byte("true")) && !bytes.Equal(v, []byte("false")) {
			return nil, semantic("materialized", "Expected boolean")
		}
	}
	if kind == "index" {
		terms, err := relationalArray(m["terms"], MaxRelationalIndexTerms)
		if err != nil {
			return nil, err
		}
		if len(terms) == 0 {
			return nil, semantic("terms", "Index requires terms")
		}
		seen := map[string]bool{}
		for _, v := range terms {
			term, err := relationalObject(v)
			if err != nil {
				return nil, err
			}
			column := "columnKey"
			if persisted {
				column = "columnId"
			}
			if err = relationalFields(term, []string{"direction", "nulls"}, []string{column, "expression"}); err != nil {
				return nil, err
			}
			if (term[column] == nil) == (term["expression"] == nil) {
				return nil, semantic("terms", "Term requires exactly column or expression")
			}
			if term[column] != nil {
				var s string
				_ = json.Unmarshal(term[column], &s)
				if !externalKey(s) || persisted && !ValidID(s) || seen[s] {
					return nil, semantic("terms", "Invalid or duplicate index column")
				}
				seen[s] = true
			} else if err = relationalText(term["expression"], false, true); err != nil {
				return nil, err
			}
			if err = relationalEnum(term["direction"], "asc", "desc", "unknown"); err != nil {
				return nil, err
			}
			if err = relationalEnum(term["nulls"], "first", "last", "unknown"); err != nil {
				return nil, err
			}
		}
	}
	if kind == "references" {
		pairs, err := relationalArray(m["columnPairs"], MaxRelationalOrderedColumns)
		if err != nil {
			return nil, err
		}
		if len(pairs) == 0 && !nonblank(f.TargetReason) {
			return nil, semantic("columnPairs", "Empty pairs require targetReason")
		}
		fromSeen, toSeen := map[string]bool{}, map[string]bool{}
		for _, v := range pairs {
			pair, err := relationalObject(v)
			if err != nil {
				return nil, err
			}
			from, to := "fromColumnKey", "toColumnKey"
			if persisted {
				from, to = "fromColumnId", "toColumnId"
			}
			if err = relationalFields(pair, []string{from, to}, nil); err != nil {
				return nil, err
			}
			var a, b string
			_ = json.Unmarshal(pair[from], &a)
			_ = json.Unmarshal(pair[to], &b)
			if !externalKey(a) || !externalKey(b) || persisted && (!ValidID(a) || !ValidID(b)) || fromSeen[a] || toSeen[b] {
				return nil, semantic("columnPairs", "Pairs require valid unique columns on both sides")
			}
			fromSeen[a], toSeen[b] = true, true
		}
	}
	if kind == "migration" {
		changes, err := relationalArray(m["changes"], MaxRelationalReferences)
		if err != nil {
			return nil, err
		}
		for i, v := range changes {
			c, err := relationalObject(v)
			if err != nil {
				return nil, err
			}
			if err = relationalFields(c, []string{"target", "operation", "description"}, nil); err != nil {
				return nil, err
			}
			if err = relationalEnum(c["operation"], "create", "alter", "drop", "unknown"); err != nil {
				return nil, err
			}
			if err = relationalText(c["description"], false, false); err != nil {
				return nil, err
			}
			target, err := relationalObject(c["target"])
			if err != nil {
				return nil, err
			}
			t := f.Changes[i].Target
			req := []string{"kind"}
			switch t.Kind {
			case "candidate":
				k := "objectKey"
				if persisted {
					k = "objectId"
				}
				req = append(req, k)
				if !externalKey(t.ObjectKey) && !persisted || persisted && !ValidID(t.ObjectID) {
					return nil, semantic("target", "Invalid candidate target")
				}
			case "historical":
				req = append(req, "revisionId", "objectId")
				if !ValidID(t.RevisionID) || !ValidID(t.ObjectID) {
					return nil, semantic("target", "Invalid historical pin")
				}
			case "source_only":
				req = append(req, "externalKey", "expectedKind", "qualifiedName", "reason")
				if !externalKey(t.ExternalKey) || !slices.Contains([]string{"datastore", "db_schema", "table", "column", "constraint", "index", "view", "migration", "symbol"}, t.ExpectedKind) || !nonblank(t.QualifiedName) || !nonblank(t.Reason) {
					return nil, semantic("target", "Source-only requires a relational kind, name and reason")
				}
			default:
				return nil, semantic("target", "Unsupported migration target variant")
			}
			if err = relationalFields(target, req, nil); err != nil {
				return nil, err
			}
			if f.DerivationStatus == "complete" && (f.Order.Status != "known" || bytes.Equal(f.Order.Value, []byte("null")) || f.Changes[i].Operation == "unknown") {
				return nil, semantic("derivationStatus", "Complete derivation requires known order and handled changes")
			}
		}
		if f.DerivationStatus == "complete" && (f.Order.Status != "known" || bytes.Equal(f.Order.Value, []byte("null"))) {
			return nil, semantic("order", "Complete derivation requires known order")
		}
	}
	return f, nil
}
func escapeRelationalPointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func relationalReferences(kind string, attrs map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	return relationalReferencesMode(kind, attrs, edge, persisted, true)
}

func relationalReferencesMode(kind string, attrs map[string]jsontext.Value, edge, persisted, sourceAdmission bool) ([]relationalReference, error) {
	out := []relationalReference{}
	if !relationalSubject(kind, attrs, edge) {
		return out, nil
	}
	fs, path, err := relationalFacetObject(kind, attrs)
	if err != nil {
		return nil, err
	}
	for fk, raw := range fs {
		f, err := decodeRelationalFacetMode(kind, raw, persisted, sourceAdmission)
		if err != nil {
			return nil, err
		}
		base := path + "/" + escapeRelationalPointer(fk)
		add := func(path, kind, key, id, historical string) {
			out = append(out, relationalReference{Path: base + path, Kind: kind, Key: key, ID: id, HistoricalRevisionID: historical})
		}
		column, evidence, dependency, parent := f.ColumnKeys, f.EvidenceKeys, f.DependencyKeys, f.ParentKeys
		if persisted {
			column, evidence, dependency, parent = f.ColumnIDs, f.EvidenceIDs, f.DependencyIDs, f.ParentIDs
		}
		for _, x := range []struct {
			values     []string
			kind, path string
		}{{column, "column", "/column"}, {evidence, "evidence", "/evidence"}, {dependency, "dependency", "/dependency"}, {parent, "migration", "/parent"}} {
			suffix := "Keys"
			if persisted {
				suffix = "Ids"
			}
			for i, v := range x.values {
				key, id := v, ""
				if persisted {
					key, id = "", v
				}
				add(fmt.Sprintf("%s%s/%d", x.path, suffix, i), x.kind, key, id, "")
			}
		}
		for i, term := range f.Terms {
			if term.ColumnKey != "" || term.ColumnID != "" {
				suffix := "Key"
				if persisted {
					suffix = "Id"
				}
				add(fmt.Sprintf("/terms/%d/column%s", i, suffix), "column", term.ColumnKey, term.ColumnID, "")
			}
		}
		for i, pair := range f.ColumnPairs {
			suffix := "Key"
			if persisted {
				suffix = "Id"
			}
			add(fmt.Sprintf("/columnPairs/%d/fromColumn%s", i, suffix), "column", pair.FromColumnKey, pair.FromColumnID, "")
			add(fmt.Sprintf("/columnPairs/%d/toColumn%s", i, suffix), "column", pair.ToColumnKey, pair.ToColumnID, "")
		}
		for i, c := range f.Changes {
			if c.Target.Kind == "candidate" || c.Target.Kind == "historical" {
				suffix := "Key"
				if persisted || c.Target.Kind == "historical" {
					suffix = "Id"
				}
				add(fmt.Sprintf("/changes/%d/target/object%s", i, suffix), "relational", c.Target.ObjectKey, c.Target.ObjectID, c.Target.RevisionID)
			}
		}
	}
	return out, nil
}
