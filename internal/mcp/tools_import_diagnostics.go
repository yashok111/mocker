package mcp

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

func addImportDiagnosticTool(s *sdk.Server, catalog *toolCatalog) {
	tool := &sdk.Tool{Name: "diagnose_backend_import_request", Description: "Read-only complete wire diagnostics for an original import request, paged as json-chunks-v1. Concatenate detailChunk strings before parsing JSON. Cursor binds arguments and current schema. Selects the relevant union arms, does not echo invalid values or execute/stage the request. Semantic diagnostics use validate_backend_import_batch with responseMode json-chunks-v1.", InputSchema: designScenarioSchemaObject([]string{"name", "arguments"}, map[string]any{
		"name":      map[string]any{"type": "string", "enum": []string{"begin_backend_import", "put_backend_import_batch", "validate_backend_import_batch"}},
		"arguments": map[string]any{"type": "object"}, "cursor": map[string]any{"type": "string"},
	}), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	schema, err := compileBackendImportToolSchema(tool)
	if err != nil {
		panic(err)
	}
	s.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var in struct {
			Name      string         `json:"name"`
			Arguments jsontext.Value `json:"arguments"`
			Cursor    string         `json:"cursor"`
		}
		if len(req.Params.Arguments) > backendmodel.MaxImportBatchBytes {
			return designScenarioToolErrorResult(fmt.Errorf("diagnostic request exceeds import batch byte limit")), nil
		}
		if err := decodeDesignScenarioToolInput(req.Params.Arguments, &in, schema); err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		registered, err := catalog.registered(ctx, req.Session)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		for _, candidate := range registered {
			if candidate.Name != in.Name {
				continue
			}
			result, err := importSchemaDetail(ctx, candidate, in.Arguments, in.Cursor)
			if err != nil {
				return designScenarioToolErrorResult(err), nil
			}
			return &sdk.CallToolResult{StructuredContent: jsonx.RawMessage(result), Content: []sdk.Content{&sdk.TextContent{Text: string(result)}}}, nil
		}
		return designScenarioToolErrorResult(fmt.Errorf("unknown import tool")), nil
	})
}

func importSchemaDetail(ctx context.Context, tool *sdk.Tool, arguments jsontext.Value, cursor string) ([]byte, error) {
	schema, err := compileBackendImportToolSchema(tool)
	if err != nil {
		return nil, err
	}
	var input any
	decoder := jsonx.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	diagnostics := &importSchemaDiagnostics{full: true, scores: map[*jsonschema.ValidationError]int{}, seen: map[string]bool{}, items: []importSchemaDiagnostic{}}
	if err := schema.Validate(input); err != nil {
		root, ok := errors.AsType[*jsonschema.ValidationError](err)
		if !ok {
			return nil, err
		}
		diagnostics.score(root)
		diagnostics.visit(root)
	}
	slices.SortFunc(diagnostics.items, func(a, b importSchemaDiagnostic) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Constraint, b.Constraint), cmp.Compare(a.Message, b.Message))
	})
	raw, err := json.Marshal(diagnostics.items)
	if err != nil {
		return nil, err
	}
	binding, err := json.Marshal([]any{tool.Name, tool.InputSchema, arguments, jsontext.Value(raw)}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(binding)
	hash := hex.EncodeToString(digest[:])
	offset := 0
	if cursor != "" {
		pin, start, ok := strings.Cut(cursor, ":")
		offset, err = strconv.Atoi(start)
		if !ok || pin != hash || err != nil || offset < 0 || offset >= len(raw) || !utf8.Valid(raw[:offset]) {
			return nil, fmt.Errorf("diagnostic cursor does not match exact arguments/schema")
		}
	}
	end := min(offset+2048, len(raw))
	for !utf8.Valid(raw[offset:end]) {
		end--
	}
	next := ""
	if end < len(raw) {
		next = hash + ":" + strconv.Itoa(end)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Format      string `json:"format"`
		RequestHash string `json:"requestHash"`
		Valid       bool   `json:"valid"`
		TotalErrors int    `json:"totalErrors"`
		DetailChunk string `json:"detailChunk"`
		DetailBytes int    `json:"detailBytes"`
		NextCursor  string `json:"nextCursor"`
	}{"json-chunks-v1", hash, len(diagnostics.items) == 0, len(diagnostics.items), string(raw[offset:end]), len(raw), next})
}
