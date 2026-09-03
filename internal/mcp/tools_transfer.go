// tools_transfer.go registers P4b's three tools — export_workspace,
// import_workspace and fork_workspace — adapters over the three transfer
// routes (GET /api/workspaces/{id}/export, POST /api/workspaces/import,
// POST /api/workspaces/{id}/fork). They are the "team scenarios" of DESIGN
// §19's P4: a configured workspace handed to a teammate, a reference set
// of mocks kept next to the e2e tests in git, a copy to experiment on.
package mcp

import (
	"context"
	"fmt"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

func addTransferTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "export_workspace",
		Description: "Exports a workspace's whole configuration as one portable JSON document (the " +
			"mockerBundle v4 format): settings, operation overrides, custom endpoints (streams " +
			"included), confirmed resources and decisions. includeData:true adds the entity rows of " +
			"every confirmed family under `data`; includeSpec:true inlines the bound spec's bytes " +
			"under spec.inline so the document imports on an installation that has never seen the " +
			"spec. Assets (uploaded files) are NOT carried — re-upload them after an import, or " +
			"use fork_workspace inside the same installation, which copies them. Refuses with 413 " +
			"export_too_large when the entity rows exceed the snapshot budget; export without " +
			"includeData then. Save the document to git next to the tests it belongs to: keys are " +
			"sorted, so diffs read.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleExportWorkspace(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "import_workspace",
		Description: "Creates a NEW workspace from an export_workspace document (or any mockerBundle " +
			"v4 document), in one transaction, with a baseline checkpoint of the imported state. The " +
			"spec is resolved in this order: specId if you pass one; the document's spec.hash if a " +
			"spec of the same bytes is already imported here; the document's spec.inline, imported " +
			"now (deduplicated by hash); none if the document names no spec. A document naming a " +
			"hash this installation lacks, with no inline copy, answers 409 spec_not_found with the " +
			"hash and name — import the spec (import_spec) and retry, or pass specId. name overrides " +
			"the document's workspace name; slug is optional and uniquified from the name when " +
			"omitted, so — like create_workspace — do NOT retry blindly after a lost response: check " +
			"list_workspaces first. 400 invalid_bundle carries the validator's own words (which " +
			"entry, which field). Entity rows under `data` are restored for families the document " +
			"also confirms; assets are not part of the format.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleImportWorkspace(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "fork_workspace",
		Description: "Copies a workspace inside this installation: the whole configuration, the " +
			"scenarios (the active one stays active on the copy), the uploaded assets, and — unless " +
			"includeData:false — the entity rows of every confirmed family. The copy gets its own " +
			"slug and URL (uniquified from name, which defaults to the source's name plus a suffix), " +
			"forkedFrom set to the source's id, and a baseline checkpoint; the source is not " +
			"touched (no revision bump, no checkpoint). History (checkpoints) and traffic are not " +
			"copied. Use it to hand a teammate a configured copy or to experiment without " +
			"disturbing a workspace a frontend is wired to. Not idempotent: a retry creates a " +
			"second copy under another slug.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleForkWorkspace(lb))
}

// ExportWorkspaceInput is export_workspace's input.
type ExportWorkspaceInput struct {
	WorkspaceID int64 `json:"workspaceId" jsonschema:"the workspace id from list_workspaces"`
	IncludeData bool  `json:"includeData,omitempty" jsonschema:"also carry the entity rows of every confirmed family under data"`
	IncludeSpec bool  `json:"includeSpec,omitempty" jsonschema:"inline the bound spec's bytes under spec.inline so the document is self-contained"`
}

// ExportWorkspaceOutput is export_workspace's declared output: the document
// itself, as the route answers it.
type ExportWorkspaceOutput struct {
	Document any `json:"document"`
}

func handleExportWorkspace(lb *loopback) sdk.ToolHandlerFor[ExportWorkspaceInput, ExportWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ExportWorkspaceInput) (*sdk.CallToolResult, ExportWorkspaceOutput, error) {
		if in.WorkspaceID <= 0 {
			return nil, ExportWorkspaceOutput{}, fmt.Errorf("export_workspace: workspaceId must be a positive integer")
		}
		q := url.Values{}
		if in.IncludeData {
			q.Set("includeData", "true")
		}
		if in.IncludeSpec {
			q.Set("includeSpec", "true")
		}
		method, path := toolPath("export_workspace", "GET /api/workspaces/{id}/export", in.WorkspaceID)
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		var doc any
		if err := lb.call(ctx, method, path, nil, &doc); err != nil {
			return nil, ExportWorkspaceOutput{}, err
		}
		return nil, ExportWorkspaceOutput{Document: doc}, nil
	}
}

// ImportWorkspaceInput is import_workspace's input.
type ImportWorkspaceInput struct {
	Bundle map[string]any `json:"bundle" jsonschema:"the export document as a JSON object (mockerBundle v4, optionally with data and spec.inline)"`
	Name   string         `json:"name,omitempty" jsonschema:"the new workspace's name; defaults to the document's workspace.name"`
	Slug   string         `json:"slug,omitempty" jsonschema:"optional slug; uniquified from the name when omitted"`
	SpecID *int64         `json:"specId,omitempty" jsonschema:"bind to this already-imported spec regardless of what the document names"`
}

// ImportWorkspaceOutput is import_workspace's declared output.
type ImportWorkspaceOutput struct {
	Workspace        WorkspaceLine `json:"workspace"`
	SpecID           *int64        `json:"specId,omitempty"`
	SpecCreated      bool          `json:"specCreated"`
	EntitiesRestored int           `json:"entitiesRestored"`
}

// importWorkspaceWire is the route's 201 body.
type importWorkspaceWire struct {
	Workspace        workspaceWire `json:"workspace"`
	SpecID           *int64        `json:"specId"`
	SpecCreated      bool          `json:"specCreated"`
	EntitiesRestored int           `json:"entitiesRestored"`
}

func handleImportWorkspace(lb *loopback) sdk.ToolHandlerFor[ImportWorkspaceInput, ImportWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ImportWorkspaceInput) (*sdk.CallToolResult, ImportWorkspaceOutput, error) {
		if len(in.Bundle) == 0 {
			return nil, ImportWorkspaceOutput{}, fmt.Errorf("import_workspace: bundle is required — the export document as a JSON object")
		}
		body, err := jsonx.Marshal(map[string]any{
			"bundle": in.Bundle, "name": in.Name, "slug": in.Slug, "specId": in.SpecID,
		})
		if err != nil {
			return nil, ImportWorkspaceOutput{}, fmt.Errorf("import_workspace: encode request: %w", err)
		}
		var wire importWorkspaceWire
		method, path := toolPath("import_workspace", "POST /api/workspaces/import")
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, ImportWorkspaceOutput{}, err
		}
		return nil, ImportWorkspaceOutput{
			Workspace:        workspaceLineFromWire(wire.Workspace),
			SpecID:           wire.SpecID,
			SpecCreated:      wire.SpecCreated,
			EntitiesRestored: wire.EntitiesRestored,
		}, nil
	}
}

// ForkWorkspaceInput is fork_workspace's input.
type ForkWorkspaceInput struct {
	WorkspaceID int64  `json:"workspaceId" jsonschema:"the source workspace id"`
	Name        string `json:"name,omitempty" jsonschema:"the copy's name; defaults to the source's name plus a suffix"`
	Slug        string `json:"slug,omitempty" jsonschema:"optional slug; uniquified from the name when omitted"`
	IncludeData *bool  `json:"includeData,omitempty" jsonschema:"copy the entity rows of every confirmed family; default true"`
}

// ForkWorkspaceOutput is fork_workspace's declared output: the copy.
type ForkWorkspaceOutput struct {
	Workspace WorkspaceLine `json:"workspace"`
}

func handleForkWorkspace(lb *loopback) sdk.ToolHandlerFor[ForkWorkspaceInput, ForkWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ForkWorkspaceInput) (*sdk.CallToolResult, ForkWorkspaceOutput, error) {
		if in.WorkspaceID <= 0 {
			return nil, ForkWorkspaceOutput{}, fmt.Errorf("fork_workspace: workspaceId must be a positive integer")
		}
		req := map[string]any{"name": in.Name, "slug": in.Slug}
		if in.IncludeData != nil {
			req["includeData"] = *in.IncludeData
		}
		body, err := jsonx.Marshal(req)
		if err != nil {
			return nil, ForkWorkspaceOutput{}, fmt.Errorf("fork_workspace: encode request: %w", err)
		}
		var wire workspaceWire
		method, path := toolPath("fork_workspace", "POST /api/workspaces/{id}/fork", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, ForkWorkspaceOutput{}, err
		}
		return nil, ForkWorkspaceOutput{Workspace: workspaceLineFromWire(wire)}, nil
	}
}

// workspaceLineFromWire projects the route's WorkspaceView onto the line
// shape list_workspaces and create_workspace already answer with.
func workspaceLineFromWire(w workspaceWire) WorkspaceLine {
	return WorkspaceLine{
		ID: w.ID, Slug: w.Slug, Name: w.Name, URL: w.URL,
		BasePath: w.Settings.BasePath, SpecID: w.SpecID, Revision: w.Revision,
	}
}
