package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func semantic(field, message string) *FaultError {
	return &FaultError{Status: 422, Code: "backend_import_invalid", Message: message, Details: map[string]any{"path": field}}
}
func limitFault(message string) *FaultError {
	return &FaultError{Status: 413, Code: "backend_import_limit", Message: message}
}
func importConflict(code, message string, version int64) *FaultError {
	return &FaultError{Status: 409, Code: code, Message: message, CurrentVersion: version}
}
func validHash(h string) bool {
	return len(h) == 64 && !strings.ContainsFunc(h, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') })
}
func nonblank(v string) bool {
	return utf8.ValidString(v) && strings.TrimSpace(v) != "" && !strings.ContainsFunc(v, unicode.IsControl)
}
func externalKey(v string) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= 1 && utf8.RuneCountInString(v) <= MaxExternalKeyLength && !strings.ContainsFunc(v, unicode.IsControl)
}
func validPath(v string) bool {
	return nonblank(v) && !strings.Contains(v, "\\") && !strings.HasPrefix(v, "/") && path.Clean(v) == v && v != "." && !strings.ContainsFunc(v, unicode.IsControl) && !slices.Contains(strings.Split(v, "/"), "..") && !driveName(v)
}

// driveName reports a Windows drive prefix (`C:`). Only that is refused: a
// colon is legal in a POSIX file name, and refusing every colon made a
// repository holding `docs/a:b.md` unreportable — the whole manifest failed
// with 422 even when the file was listed as excluded (review 2026-10-06,
// F88).
func driveName(v string) bool {
	return len(v) >= 2 && v[1] == ':' && (v[0] >= 'a' && v[0] <= 'z' || v[0] >= 'A' && v[0] <= 'Z')
}
func secretPath(v string) bool {
	b := strings.ToLower(path.Base(v))
	return b == ".env" || strings.HasPrefix(b, ".env.") || strings.HasSuffix(b, ".key") || strings.HasSuffix(b, ".pem") || b == "id_rsa" || b == "id_ed25519" || b == "id_dsa" || b == "id_ecdsa" || strings.HasSuffix(b, ".p12") || strings.HasSuffix(b, ".pfx")
}
func validateManifest(m SourceManifest) error {
	if !nonblank(m.RepositoryName) {
		return semantic("manifest.repositoryName", "Repository name is required")
	}
	p := m.Provider
	if !nonblank(p.Name) || !nonblank(p.Version) || !externalKey(p.Namespace) || !slices.Contains(evidenceMethods, p.Method) || !slices.Contains(p.Profiles, GraphProfile) {
		return semantic("manifest.provider", "Provider name, version, namespace, method and foundation profile are required")
	}
	if m.Snapshot.CapturedAt.IsZero() || !slices.Contains([]string{"verified", "unverified"}, m.Snapshot.Consistency) {
		return semantic("manifest.snapshot", "Valid snapshot consistency and capture time are required")
	}
	if len(m.Snapshot.Files) > MaxManifestFiles {
		return limitFault("Manifest file limit exceeded")
	}
	seen := map[string]bool{}
	for i, f := range m.Snapshot.Files {
		field := fmt.Sprintf("manifest.snapshot.files/%d", i)
		if !validPath(f.Path) || seen[f.Path] {
			return semantic(field+"/path", "File path must be normalized, unique and repository-relative")
		}
		seen[f.Path] = true
		if !validHash(f.ContentHash) || !nonblank(f.FileType) {
			return semantic(field, "File needs a lowercase SHA-256 hash and fileType")
		}
		if !slices.Contains([]string{"analyzed", "excluded", "unsupported"}, f.AnalysisStatus) {
			return semantic(field+"/analysisStatus", "Unsupported analysis status")
		}
		if f.AnalysisStatus != "analyzed" && !nonblank(f.Reason) {
			return semantic(field+"/reason", "Excluded and unsupported files require a reason")
		}
		if f.AnalysisStatus == "analyzed" && secretPath(f.Path) {
			return semantic(field+"/path", "Secret files cannot be analyzed")
		}
	}
	return nil
}

var inventoryCategories = []string{"files", "endpoints", "datastores", "migrations", "producers", "consumers", "jobs", "contracts", "tests"}
var evidenceMethods = []string{"ast", "sql", "orm", "contract", "agent", "manual", "trace", "test"}

func validateInventory(items []InventoryItem) error {
	if len(items) != len(inventoryCategories) {
		return semantic("inventory", "Inventory requires exactly one item for every category")
	}
	seen := map[string]bool{}
	for i, x := range items {
		field := fmt.Sprintf("inventory/%d", i)
		if !slices.Contains(inventoryCategories, x.Category) || seen[x.Category] {
			return semantic(field+"/category", "Unknown or duplicate inventory category")
		}
		seen[x.Category] = true
		if x.KnownCount < 0 || x.Denominator != nil && (*x.Denominator < 0 || *x.Denominator < x.KnownCount) {
			return semantic(field, "Inventory counts must be nonnegative and denominator must cover knownCount")
		}
		if !nonblank(x.DiscoverySource) {
			return semantic(field+"/discoverySource", "Discovery source is required")
		}
		switch x.Status {
		case "complete":
			if x.Denominator == nil || *x.Denominator != x.KnownCount {
				return semantic(field, "Complete inventory requires matching known denominator")
			}
		case "partial":
			if len(x.Gaps) == 0 {
				return semantic(field+"/gaps", "Partial inventory needs gaps")
			}
		case "excluded", "unsupported":
			if !nonblank(x.Reason) {
				return semantic(field+"/reason", "Excluded and unsupported inventory needs a reason")
			}
		default:
			return semantic(field+"/status", "Unsupported inventory status")
		}
		for _, g := range x.Gaps {
			if strings.TrimSpace(g) == "" {
				return semantic(field+"/gaps", "Gap descriptions cannot be blank")
			}
		}
	}
	return nil
}
func validateAttributes(kind string, a map[string]jsontext.Value, edge bool) error {
	if a == nil {
		return semantic("attributes", "Attributes must be an object")
	}
	allowed := []string{"description"}
	required := []string{}
	if !edge {
		switch kind {
		case "symbol", "handler":
			allowed = append(allowed, "language", "qualifiedName")
		case "http_operation":
			allowed = append(allowed, "method", "path")
			required = []string{"method", "path"}
		case "datastore":
			allowed = append(allowed, "technology")
		case "unresolved_target":
			allowed = append(allowed, "expectedKind", "reason", "searchScope")
			required = []string{"expectedKind", "reason", "searchScope"}
		}
	}
	for k, raw := range a {
		if !slices.Contains(allowed, k) {
			return semantic("attributes/"+k, "Unknown attribute for this kind")
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if slices.Contains(required, k) {
				return semantic("attributes/"+k, "Required attribute cannot be null")
			}
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return semantic("attributes/"+k, "Attribute must be a string or null")
		}
		if slices.Contains(required, k) && strings.TrimSpace(s) == "" {
			return semantic("attributes/"+k, "Required attribute cannot be blank")
		}
		if kind == "http_operation" && k == "method" && (s != strings.ToUpper(s) || strings.ContainsFunc(s, func(r rune) bool { return r < 'A' || r > 'Z' })) {
			return semantic("attributes/method", "HTTP method must be uppercase ASCII letters")
		}
		if kind == "http_operation" && k == "path" && !strings.HasPrefix(s, "/") {
			return semantic("attributes/path", "HTTP path must start with /")
		}
	}
	for _, k := range required {
		if _, ok := a[k]; !ok {
			return semantic("attributes/"+k, "Required attribute is missing")
		}
	}
	return nil
}
func validateEvidence(e ImportEvidence, s *ImportSession) error {
	if !slices.Contains([]string{"node", "edge"}, e.SubjectType) || !externalKey(e.SubjectKey) {
		return semantic("evidence/subjectKey", "Evidence needs a node or edge subject key")
	}
	if !slices.Contains(evidenceMethods, e.Method) || !slices.Contains([]string{"explicit", "inferred", "unresolved"}, e.Status) {
		return semantic("evidence", "Unsupported evidence method or status")
	}
	if e.Status == "inferred" && strings.TrimSpace(e.Explanation) == "" {
		return semantic("evidence/explanation", "Inferred evidence requires an explanation")
	}
	src := e.Source
	if src.RepositoryID != s.RepositoryID || src.SnapshotID != s.SnapshotID || !validPath(src.File) || !validHash(src.ContentHash) {
		return semantic("evidence/source", "Source must match the session repository and snapshot")
	}
	found := false
	for _, f := range s.Manifest.Snapshot.Files {
		if f.Path == src.File && f.AnalysisStatus == "analyzed" && f.ContentHash == src.ContentHash {
			found = true
			break
		}
	}
	if !found {
		return semantic("evidence/source", "Source must match an analyzed manifest file and hash")
	}
	if (src.StartLine == nil) != (src.EndLine == nil) || src.StartLine != nil && (*src.StartLine <= 0 || *src.EndLine < *src.StartLine) {
		return semantic("evidence/source", "Both line bounds must be positive and ordered")
	}
	if e.Snippet != nil && (!utf8.ValidString(*e.Snippet) || len(*e.Snippet) > MaxEvidenceSnippetBytes) {
		return limitFault("Evidence snippet exceeds UTF-8 byte limit")
	}
	return nil
}
func commandAddress(c ImportCommand) (string, string, error) {
	count := 0
	for _, ok := range []bool{c.Node != nil, c.Edge != nil, c.Evidence != nil, c.Remove != nil, c.Identity != nil, c.Deletion != nil} {
		if ok {
			count++
		}
	}
	if count != 1 {
		return "", "", semantic("commands", "Command must contain exactly one addressed record")
	}
	switch c.Op {
	case "upsert_node":
		if c.Node != nil {
			return "node", c.Node.ExternalKey, nil
		}
	case "upsert_edge":
		if c.Edge != nil {
			return "edge", c.Edge.ExternalKey, nil
		}
	case "upsert_evidence":
		if c.Evidence != nil {
			return "evidence", c.Evidence.ExternalKey, nil
		}
	case "map_identity":
		if c.Identity != nil && slices.Contains([]string{"node", "edge"}, c.Identity.RecordType) {
			return c.Identity.RecordType, c.Identity.ToExternalKey, nil
		}
	case "delete_assertion":
		if c.Deletion != nil && slices.Contains([]string{"node", "edge", "evidence"}, c.Deletion.RecordType) {
			return c.Deletion.RecordType, c.Deletion.ExternalKey, nil
		}
	case "remove":
		if c.Remove != nil && slices.Contains([]string{"node", "edge", "evidence"}, c.Remove.RecordType) {
			return c.Remove.RecordType, c.Remove.ExternalKey, nil
		}
	}
	return "", "", semantic("commands/op", "Operation and addressed record do not agree")
}
func validateCommand(c ImportCommand, s *ImportSession) error {
	if s.Mode == "composed" {
		return validateComposedCommand(c, s)
	}
	if c.ClaimIdentity != nil || c.Resolution != nil || c.Node != nil && c.Node.ParentRef != nil || c.Edge != nil && (c.Edge.FromRef != nil || c.Edge.ToRef != nil) {
		return semantic("commands", "Source6 members require composed mode")
	}
	typ, key, err := commandAddress(c)
	if err != nil {
		return err
	}
	if !externalKey(key) {
		return semantic("commands/externalKey", "External key must contain 1–200 non-control Unicode characters")
	}
	if c.Op == "remove" {
		return nil
	}
	if c.Identity != nil {
		x := c.Identity
		if !externalKey(x.FromExternalKey) || x.FromExternalKey == x.ToExternalKey || !ValidID(x.ExpectedID) || !nonblank(x.Reason) || len(x.EvidenceKeys) == 0 {
			return semantic("identity", "Mapping requires distinct valid keys, expectedId, reason and evidence")
		}
		return validateEvidenceKeys(x.EvidenceKeys)
	}
	if c.Deletion != nil {
		if !ValidID(c.Deletion.ExpectedID) || !nonblank(c.Deletion.Reason) {
			return semantic("deletion", "Deletion requires expectedId and reason")
		}
		return nil
	}
	switch typ {
	case "node":
		n := c.Node
		if !slices.Contains(SupportedNodeKindsForProfile(s.Profile), n.Kind) {
			return semantic("node/kind", "Unsupported node kind")
		}
		if !nonblank(n.Name) {
			return semantic("node/name", "Node name is required")
		}
		if n.ParentKey != nil && !externalKey(*n.ParentKey) {
			return semantic("node/parentKey", "Invalid parent key")
		}
		validator := validateAttributes
		if hasRuntimeProfile(selectedProfile(s.Profile)) {
			validator = func(kind string, attrs map[string]jsontext.Value, edge bool) error {
				if selectedProfile(s.Profile) == EventsProfile {
					return validateEventsAttributes(kind, attrs, edge, false)
				}
				if selectedProfile(s.Profile) == LineageProfile {
					return validateLineageAttributes(kind, attrs, edge, false)
				}
				return validateRuntimeAttributes(kind, attrs, edge, false)
			}
		}
		if selectedProfile(s.Profile) == RelationalProfile {
			validator = func(kind string, attrs map[string]jsontext.Value, edge bool) error {
				return validateRelationalAttributes(kind, attrs, edge, false)
			}
		}
		if err := validator(n.Kind, n.Attributes, false); err != nil {
			return err
		}
		return validateEvidenceKeys(n.EvidenceKeys)
	case "edge":
		e := c.Edge
		if !slices.Contains(SupportedEdgeKindsForProfile(s.Profile), e.Kind) {
			return semantic("edge/kind", "Unsupported edge kind")
		}
		if !externalKey(e.FromKey) || !externalKey(e.ToKey) {
			return semantic("edge", "Valid endpoint keys are required")
		}
		validator := validateAttributes
		if hasRuntimeProfile(selectedProfile(s.Profile)) {
			validator = func(kind string, attrs map[string]jsontext.Value, edge bool) error {
				if selectedProfile(s.Profile) == EventsProfile {
					return validateEventsAttributes(kind, attrs, edge, false)
				}
				if selectedProfile(s.Profile) == LineageProfile {
					return validateLineageAttributes(kind, attrs, edge, false)
				}
				return validateRuntimeAttributes(kind, attrs, edge, false)
			}
		}
		if selectedProfile(s.Profile) == RelationalProfile {
			validator = func(kind string, attrs map[string]jsontext.Value, edge bool) error {
				return validateRelationalAttributes(kind, attrs, edge, false)
			}
		}
		if err := validator(e.Kind, e.Attributes, true); err != nil {
			return err
		}
		return validateEvidenceKeys(e.EvidenceKeys)
	case "evidence":
		return validateEvidence(*c.Evidence, s)
	}
	return nil
}
func validateEvidenceKeys(keys []string) error {
	seen := map[string]bool{}
	for _, k := range keys {
		if !externalKey(k) || seen[k] {
			return semantic("evidenceKeys", "Evidence keys must be valid and unique")
		}
		seen[k] = true
	}
	return nil
}

// canonicalJSON retains number lexemes while recursively sorting object keys.
// It follows Python ensure_ascii=False, sort_keys=True and compact separators.
func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	return canonicalValue(b)
}
func ImportBatchHash(commands []ImportCommand) (string, error) {
	b, err := canonicalJSON(commands)
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}
