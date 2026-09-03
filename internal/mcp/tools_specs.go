// tools_specs.go registers A8's one tool: import_spec, an adapter over
// POST /api/specs — the verb that, from the MCP surface's first day through
// A7, stayed a human's alone. mocker-a4-mcp-reach D3 kept the route OUT of
// admin.mcpAllowedRoutes on the argument that the /specs screen already
// worked and stayed the only way in; the owner reversed that on
// 2026-09-02 («сделай дешевые», the first item of the ranked list), and
// the reason is the shape of the agent's own workflow: the agent in a
// frontend repository holds the spec file in that repository, and a slice
// that made every other step tool-shaped left the FIRST step as "ask a
// human to paste a file".
//
// The document travels as a JSON STRING inside the tool's arguments, the
// same way the admin route takes it (specImportBody.Document) — no
// multipart, no raw body — so the ceiling is MOCKER_MAX_BODY on the
// loopback request, which is the same ceiling the screen has. YAML is
// accepted since A8 as well (internal/yamlx renders it to JSON before the
// parser sees it), and the tool says so in its description because the
// old refusal text is what an agent reading traffic history would expect.
//
// Not idempotent in the create sense but safe to retry: the route
// deduplicates by byte hash, so the same document twice answers 200 with
// duplicate:true and the pre-existing spec, never a second row (DESIGN §7).
package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

func addSpecTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "import_spec",
		Description: "Imports an OpenAPI document (3.0 or 3.1, as JSON or YAML text) as a new spec and " +
			"returns the stored spec with its import report. `document` is the WHOLE file as one " +
			"string; `name` is the label the spec list shows. Deduplicated by byte hash: the same " +
			"document again answers the pre-existing spec with duplicate:true and creates nothing, so a " +
			"retry after a timeout is safe. Swagger 2.0 is refused by name. Read report.warnings — " +
			"they name what the parser could not use (an unresolvable $ref, an unsupported keyword) " +
			"and report.degraded counts operations that will serve a poorer body because of it. " +
			"Importing changes no workspace: bind the spec with create_workspace {specId} or " +
			"update_workspace_settings {specId}. Ceiling: MOCKER_MAX_BODY (default 10mb) on the " +
			"whole request.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleImportSpec(lb))
}

// ImportSpecInput is import_spec's input.
type ImportSpecInput struct {
	Name     string `json:"name" jsonschema:"the label the spec list shows; required"`
	Document string `json:"document" jsonschema:"the whole OpenAPI document as one string, JSON or YAML"`
}

// importedSpec is the spec half of POST /api/specs's answer, projected
// field-for-field from admin's specView.
type importedSpec struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Format    string `json:"format"`
	Source    string `json:"source"`
	BasePath  string `json:"basePath"`
	Hash      string `json:"hash"`
	CreatedAt int64  `json:"createdAt"`
}

// importWarning is one row of the import report.
type importWarning struct {
	Pointer string `json:"pointer"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// importReport is the report half, projected from admin's reportView.
type importReport struct {
	Format         string          `json:"format"`
	BasePath       string          `json:"basePath"`
	BasePathOrigin string          `json:"basePathOrigin"`
	Operations     int             `json:"operations"`
	Degraded       int             `json:"degraded"`
	Warnings       []importWarning `json:"warnings"`
}

// ImportSpecOutput is import_spec's declared output.
type ImportSpecOutput struct {
	Spec      importedSpec  `json:"spec"`
	Duplicate bool          `json:"duplicate"`
	Report    *importReport `json:"report,omitempty"`
}

// importSpecWire is the exact shape POST /api/specs writes (specImportView
// embeds specView, so the spec fields sit at the TOP level beside
// duplicate and report).
type importSpecWire struct {
	importedSpec
	Duplicate bool          `json:"duplicate"`
	Report    *importReport `json:"report"`
}

func handleImportSpec(lb *loopback) sdk.ToolHandlerFor[ImportSpecInput, ImportSpecOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ImportSpecInput) (*sdk.CallToolResult, ImportSpecOutput, error) {
		if strings.TrimSpace(in.Name) == "" {
			return nil, ImportSpecOutput{}, fmt.Errorf("import_spec: name is required")
		}
		if strings.TrimSpace(in.Document) == "" {
			return nil, ImportSpecOutput{}, fmt.Errorf("import_spec: document is required — the whole OpenAPI file as one string")
		}
		body, err := jsonx.Marshal(map[string]string{"name": in.Name, "source": "upload", "document": in.Document})
		if err != nil {
			return nil, ImportSpecOutput{}, fmt.Errorf("import_spec: encode request: %w", err)
		}
		var wire importSpecWire
		method, path := toolPath("import_spec", "POST /api/specs")
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, ImportSpecOutput{}, err
		}
		out := ImportSpecOutput{Spec: wire.importedSpec, Duplicate: wire.Duplicate, Report: wire.Report}
		if out.Report != nil && out.Report.Warnings == nil {
			out.Report.Warnings = []importWarning{}
		}
		return nil, out, nil
	}
}
