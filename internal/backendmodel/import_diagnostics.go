package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"unicode/utf8"
)

type ImportBatchValidationInput struct {
	ResponseMode          string          `json:"responseMode,omitempty"`
	ExpectedImportVersion int64           `json:"expectedImportVersion"`
	PayloadHash           string          `json:"payloadHash"`
	Commands              []ImportCommand `json:"commands"`
	Cursor                string          `json:"cursor,omitempty"`
}
type ImportRecordDiagnostic struct {
	Truncated    bool   `json:"truncated,omitzero"`
	CommandIndex int    `json:"commandIndex"`
	Op           string `json:"op"`
	RecordType   string `json:"recordType"`
	ExternalKey  string `json:"externalKey"`
	Kind         string `json:"kind"`
	Code         string `json:"code"`
	Path         string `json:"path"`
	Message      string `json:"message"`
}
type ImportBatchValidation struct {
	DetailChunk string                   `json:"detailChunk,omitempty"`
	DetailBytes int                      `json:"detailBytes,omitzero"`
	SessionID   string                   `json:"sessionId"`
	Version     int64                    `json:"version"`
	PayloadHash string                   `json:"payloadHash"`
	Valid       bool                     `json:"valid"`
	Scope       string                   `json:"scope"`
	TotalErrors int                      `json:"totalErrors"`
	Diagnostics []ImportRecordDiagnostic `json:"diagnostics"`
	NextCursor  string                   `json:"nextCursor"`
}

func (in *ImportBatchValidationInput) UnmarshalJSON(b []byte) error {
	type plain ImportBatchValidationInput
	*in = ImportBatchValidationInput{}
	return strictAPIObject(b, []string{"expectedImportVersion", "payloadHash", "commands"}, []string{"cursor", "responseMode"}, (*plain)(in))
}

// Record validation shares the exact session/profile validators with staging.
// It neither allocates identities nor claims to replace Preview's graph closure,
// source audit, quota reservation or concurrent CAS checks during a later Put.
func (r *Repo) ValidateImportBatch(ctx context.Context, pid, sid string, in ImportBatchValidationInput) (*ImportBatchValidation, error) {
	if in.ResponseMode != "" && in.ResponseMode != "json-chunks-v1" {
		return nil, invalid("responseMode", "Use json-chunks-v1 for complete diagnostic text")
	}
	if err := validateImportBatchPayload(ImportBatchInput{ExpectedImportVersion: in.ExpectedImportVersion, PayloadHash: in.PayloadHash, Commands: in.Commands}); err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	s, err := loadSession(ctx, tx, pid, sid)
	if err != nil {
		return nil, err
	}
	if err := requireSessionVersion(s, in.ExpectedImportVersion); err != nil {
		return nil, err
	}
	if err := requireCollectible(s); err != nil {
		return nil, err
	}
	const limit = 25
	scope := fmt.Sprintf("%s:%d:%s", sid, s.Version, in.PayloadHash)
	if in.ResponseMode == "json-chunks-v1" {
		return importDiagnosticDetailPage(ctx, s, pid, scope, in)
	}
	_, after, err := decodeGraphPage(limit, in.Cursor, "import-validation", pid, scope, false)
	if err != nil {
		return nil, err
	}
	offset := 0
	if after != "" {
		offset, err = strconv.Atoi(after)
		if err != nil || offset < 0 || offset >= len(in.Commands) {
			return nil, invalid("cursor", "Invalid diagnostic cursor")
		}
	}
	out := &ImportBatchValidation{SessionID: sid, Version: s.Version, PayloadHash: in.PayloadHash, Valid: true, Scope: "record-validation-v1", Diagnostics: []ImportRecordDiagnostic{}}
	diagnostics, err := importCommandDiagnostics(ctx, s, in.Commands)
	if err != nil {
		return nil, err
	}
	out.Valid, out.TotalErrors = len(diagnostics) == 0, len(diagnostics)
	for _, diagnostic := range diagnostics {
		if diagnostic.CommandIndex < offset {
			continue
		}
		if len(out.Diagnostics) == limit {
			out.NextCursor = encodeGraphPage("import-validation", pid, scope, strconv.Itoa(diagnostic.CommandIndex))
			break
		}
		out.Diagnostics = append(out.Diagnostics, boundImportRecordDiagnostic(diagnostic))
	}

	return out, nil
}

func importCommandDiagnostics(ctx context.Context, s *ImportSession, commands []ImportCommand) ([]ImportRecordDiagnostic, error) {
	out := []ImportRecordDiagnostic{}
	seen := map[string]bool{}
	for i, c := range commands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := validateCommand(c, s)
		typ, key, _ := commandAddress(c)
		address := typ + "\x00" + key
		if typ != "" && seen[address] && err == nil {
			err = semantic("commands", "Duplicate addressed command in one batch")
		}
		if typ != "" {
			seen[address] = true
		}
		if err != nil {
			out = append(out, fullImportRecordDiagnostic(i, c, err))
		}
	}
	return out, nil
}

func importRecordDiagnostic(index int, c ImportCommand, err error) ImportRecordDiagnostic {
	return boundImportRecordDiagnostic(fullImportRecordDiagnostic(index, c, err))
}

func fullImportRecordDiagnostic(index int, c ImportCommand, err error) ImportRecordDiagnostic {
	typ, key, _ := commandAddress(c)
	if c.ClaimIdentity != nil {
		typ, key = c.ClaimIdentity.RecordType, c.ClaimIdentity.ExternalKey
	}
	if c.Resolution != nil {
		typ = c.Resolution.RecordType
	}
	kind := ""
	if c.Node != nil {
		kind = c.Node.Kind
	}
	if c.Edge != nil {
		kind = c.Edge.Kind
	}
	d := ImportRecordDiagnostic{CommandIndex: index, Op: c.Op, RecordType: typ, ExternalKey: key, Kind: kind, Code: "backend_import_invalid", Path: fmt.Sprintf("/commands/%d", index), Message: "Record validation failed"}
	if f, ok := errors.AsType[*FaultError](err); ok {
		d.Code, d.Message = f.Code, f.Message
		path, _ := f.Details["path"].(string)
		arm := map[string]string{"upsert_node": "node", "upsert_edge": "edge", "upsert_evidence": "evidence", "map_identity": "identity", "delete_assertion": "deletion", "claim_identity": "claimIdentity", "resolve_assertion": "resolution", "remove": "remove"}[c.Op]
		d.Path = importCommandPath(index, arm, path)
	}
	return d
}

func boundImportRecordDiagnostic(d ImportRecordDiagnostic) ImportRecordDiagnostic {
	for _, field := range []struct {
		value *string
		limit int
	}{{&d.Op, 64}, {&d.RecordType, 32}, {&d.ExternalKey, 800}, {&d.Kind, 80}, {&d.Code, 96}, {&d.Path, 1024}, {&d.Message, 800}} {
		bounded, truncated := boundImportDiagnostic(*field.value, field.limit)
		*field.value = bounded
		d.Truncated = d.Truncated || truncated
	}
	return d
}

func importDiagnosticDetailPage(ctx context.Context, s *ImportSession, pid, scope string, in ImportBatchValidationInput) (*ImportBatchValidation, error) {
	diagnostics, err := importCommandDiagnostics(ctx, s, in.Commands)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(diagnostics)
	if err != nil {
		return nil, err
	}
	scope += ":" + hashBytes(raw)
	_, after, err := decodeGraphPage(1, in.Cursor, "import-validation-detail", pid, scope, false)
	if err != nil {
		return nil, err
	}
	offset := 0
	if after != "" {
		offset, err = strconv.Atoi(after)
		if err != nil || offset < 0 || offset >= len(raw) || !utf8.Valid(raw[:offset]) {
			return nil, invalid("cursor", "Invalid detail cursor")
		}
	}
	end := min(offset+2048, len(raw))
	for !utf8.Valid(raw[offset:end]) {
		end--
	}
	out := &ImportBatchValidation{SessionID: s.ID, Version: s.Version, PayloadHash: in.PayloadHash, Valid: len(diagnostics) == 0, Scope: "record-validation-json-chunks-v1", TotalErrors: len(diagnostics), Diagnostics: []ImportRecordDiagnostic{}, DetailChunk: string(raw[offset:end]), DetailBytes: len(raw)}
	if end < len(raw) {
		out.NextCursor = encodeGraphPage("import-validation-detail", pid, scope, strconv.Itoa(end))
	}
	return out, ctx.Err()
}

func importCommandPath(index int, typ, path string) string {
	base := fmt.Sprintf("/commands/%d", index)
	path = strings.TrimPrefix(path, "/")
	for _, prefix := range []string{"node.", "edge.", "evidence.", "attributes.", "identity.", "deletion.", "claimIdentity.", "resolution."} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimSuffix(prefix, ".") + "/" + strings.TrimPrefix(path, prefix)
			break
		}
	}
	if path == "commands" || path == "" {
		return base
	}
	path = strings.TrimPrefix(path, "commands/")
	if path == "op" {
		return base + "/op"
	}
	if typ != "" && !strings.HasPrefix(path, typ+"/") && path != typ {
		path = typ + "/" + path
	}
	return base + "/" + path
}

func importCommandError(err error, index int, c ImportCommand) error {
	f, ok := errors.AsType[*FaultError](err)
	if !ok {
		return err
	}
	d := importRecordDiagnostic(index, c, err)
	out := *f
	out.Message = d.Message
	out.Details = maps.Clone(f.Details)
	if out.Details == nil {
		out.Details = map[string]any{}
	}
	out.Details["format"] = "import-diagnostics-v1"
	out.Details["path"], out.Details["commandIndex"], out.Details["op"] = d.Path, d.CommandIndex, d.Op
	out.Details["recordType"], out.Details["externalKey"], out.Details["kind"] = d.RecordType, d.ExternalKey, d.Kind
	out.Details["truncated"] = d.Truncated
	return &out
}

func importAttributeError(err error, key string) error {
	f, ok := errors.AsType[*FaultError](err)
	if !ok {
		return err
	}
	out := *f
	out.Details = maps.Clone(f.Details)
	if out.Details == nil {
		out.Details = map[string]any{}
	}
	path, _ := out.Details["path"].(string)
	path = strings.TrimPrefix(strings.TrimPrefix(path, "/"), "attributes")
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	out.Details["path"] = "attributes/" + jsonPointerSegment(key) + path
	return &out
}

func jsonPointerSegment(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

// Bound encoded JSON bytes as well as UTF-8, so invalid control-heavy values
// cannot expand a short-looking diagnostic into another huge error response.
func boundImportDiagnostic(value string, limit int) (string, bool) {
	original := value
	if len(value) > limit {
		value = strings.ToValidUTF8(value[:limit], "")
	}
	for {
		raw, err := json.Marshal(value)
		if err != nil {
			return "Invalid text", true
		}
		if len(raw) <= limit+2 {
			return value, value != original
		}
		value = strings.ToValidUTF8(value[:len(value)/2], "")
	}
}
