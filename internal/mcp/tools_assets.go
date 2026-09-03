package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// A6 (DESIGN §32.5, decisions.md mocker-a6-assets D8): the three asset
// tools — fifty-second through fifty-fourth. upload_asset carries the file
// as base64, which is the shape an agent actually produces (a generated
// placeholder, a small icon); a real photograph is a `curl -T` job and the
// description says so, with the REAL ceiling: the base64 text travels
// under MOCKER_MAX_BODY (10 mb by default), so the tool carries about 7 MB
// of file at the defaults, never the 8 MB MOCKER_MAX_ASSET. No get_asset:
// an agent that needs the bytes GETs the mock route the list reports.

// rawCaller is the one-method interface upload_asset needs beyond Caller:
// a body whose content type is not JSON. Declared here and asserted only
// by this file, so mcp.Caller — the seam every other tool and every test
// double dispatch through — is not widened for one tool (D8).
type rawCaller interface {
	CallAsMCPRaw(ctx context.Context, src *http.Request, method, path, contentType string, body []byte) (status int, respBody []byte, err error)
}

func addAssetTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "upload_asset",
		Description: "Stores a file in a workspace so a mock can serve it (DESIGN §32): the bytes come as " +
			"dataBase64 with their media type, under a name of [A-Za-z0-9._-] (at most 128 characters). " +
			"A second upload under the same name REPLACES the file. The response's url is the mock-plane " +
			"address a frontend fetches — put it into a pinned body, or reference the file by name from a " +
			"pinned variant's bodyRef (\"asset:<name>\", the variant then serves the bytes verbatim) or an " +
			"asset_url recipe (the generated field gets this url). Media types a browser executes " +
			"(text/html, image/svg+xml, application/xhtml+xml) are refused. Size: one file up to " +
			"MOCKER_MAX_ASSET (8mb by default) and a workspace up to MOCKER_MAX_ASSETS_TOTAL (64mb), but " +
			"this tool's base64 payload must fit MOCKER_MAX_BODY (10mb) — about 7 MB of file at the " +
			"defaults; upload anything larger with `curl -T file -H 'Content-Type: image/jpeg' " +
			"PUT /api/workspaces/{id}/assets/{name}` instead. Bumps the workspace revision.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleUploadAsset(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_assets",
		Description: "Lists a workspace's uploaded assets — name, media type, size, sha256 and the " +
			"mock-plane url each is served at — beside the workspace's stored total and the two caps " +
			"(per file, per workspace). Never the bytes: GET an asset's url for those.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListAssets(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "delete_asset",
		Description: "Permanently deletes one uploaded asset. No checkpoint carries asset bytes " +
			"(DESIGN §32.4), so nothing brings it back but a new upload under the same name — and " +
			"that IS the repair: a pinned variant's bodyRef or an asset_url recipe naming the deleted " +
			"asset is neither refused nor changed, it answers an empty body noted asset_missing in the " +
			"traffic (or a 404 url) until the name exists again. Requires confirmSlug naming the exact " +
			"workspace, checked live inside the delete's own transaction; a mismatch refuses the call " +
			"and nothing is touched.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleDeleteAsset(lb))
}

// assetWire is the admin API's assetView, decoded and re-emitted verbatim.
type assetWire struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	URL       string `json:"url"`
}

// ---- upload_asset ----

// UploadAssetInput is upload_asset's input.
type UploadAssetInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Name        string `json:"name" jsonschema:"The asset's name: one path segment of [A-Za-z0-9._-], at most 128 characters, e.g. avatar-1.jpg. Uploading again under the same name replaces the file."`
	MediaType   string `json:"mediaType" jsonschema:"The file's media type, e.g. image/jpeg, image/webp, application/pdf. Types a browser executes are refused."`
	DataBase64  string `json:"dataBase64" jsonschema:"The file's bytes, standard base64 (RFC 4648, with padding)."`
}

// UploadAssetOutput is upload_asset's output: the stored asset as the admin
// API reports it, plus whether this call created or replaced it.
type UploadAssetOutput struct {
	Asset   assetWire `json:"asset"`
	Created bool      `json:"created"`
}

func handleUploadAsset(lb *loopback) sdk.ToolHandlerFor[UploadAssetInput, UploadAssetOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in UploadAssetInput) (*sdk.CallToolResult, UploadAssetOutput, error) {
		raw, ok := lb.calls.(rawCaller)
		if !ok {
			return nil, UploadAssetOutput{}, errors.New("upload_asset: the admin caller cannot carry a non-JSON body")
		}
		mediaType, _, err := mime.ParseMediaType(in.MediaType)
		if err != nil || mediaType == "" {
			return nil, UploadAssetOutput{}, fmt.Errorf("upload_asset: mediaType %q is not a media type", in.MediaType)
		}
		data, err := base64.StdEncoding.DecodeString(in.DataBase64)
		if err != nil {
			// Decoded HERE, before any request is made: a body the tool
			// cannot decode is the caller's mistake, and the admin plane
			// would only see garbage bytes it has no way to refuse.
			return nil, UploadAssetOutput{}, fmt.Errorf("upload_asset: dataBase64 does not decode: %w", err)
		}

		req, ok := inboundRequest(ctx)
		if !ok {
			return nil, UploadAssetOutput{}, errors.New("mcp: loopback: no inbound request on context")
		}
		// url.PathEscape, as tools_entities does for {key}: a name carrying
		// "?" or "/" would otherwise be parsed by the loopback as a query or
		// a second segment, and the admin plane would act on the TRUNCATED
		// name and report success for it.
		method, path := toolPath("upload_asset", "PUT /api/workspaces/{id}/assets/{name}", in.WorkspaceID, url.PathEscape(in.Name))
		status, respBody, err := raw.CallAsMCPRaw(ctx, req, method, path, mediaType, data)
		if err != nil {
			return nil, UploadAssetOutput{}, err
		}
		if status < 200 || status >= 300 {
			return nil, UploadAssetOutput{}, toolErr(status, respBody)
		}
		var wire assetWire
		if err := jsonx.Unmarshal(respBody, &wire); err != nil {
			return nil, UploadAssetOutput{}, fmt.Errorf("upload_asset: decode response: %w", err)
		}
		return nil, UploadAssetOutput{Asset: wire, Created: status == http.StatusCreated}, nil
	}
}

// ---- list_assets ----

// ListAssetsInput is list_assets's input.
type ListAssetsInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// ListAssetsOutput is the admin API's list envelope verbatim.
type ListAssetsOutput struct {
	Assets        []assetWire `json:"assets"`
	TotalBytes    int64       `json:"totalBytes"`
	MaxAssetBytes int64       `json:"maxAssetBytes"`
	MaxTotalBytes int64       `json:"maxTotalBytes"`
}

func handleListAssets(lb *loopback) sdk.ToolHandlerFor[ListAssetsInput, ListAssetsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListAssetsInput) (*sdk.CallToolResult, ListAssetsOutput, error) {
		method, path := toolPath("list_assets", "GET /api/workspaces/{id}/assets", in.WorkspaceID)
		var out ListAssetsOutput
		if err := lb.call(ctx, method, path, nil, &out); err != nil {
			return nil, ListAssetsOutput{}, err
		}
		if out.Assets == nil {
			out.Assets = []assetWire{}
		}
		return nil, out, nil
	}
}

// ---- delete_asset ----

// DeleteAssetInput is delete_asset's input.
type DeleteAssetInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Name        string `json:"name"`
	ConfirmSlug string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// DeleteAssetOutput is delete_asset's output.
type DeleteAssetOutput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Name        string `json:"name"`
	Deleted     bool   `json:"deleted"`
}

func handleDeleteAsset(lb *loopback) sdk.ToolHandlerFor[DeleteAssetInput, DeleteAssetOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DeleteAssetInput) (*sdk.CallToolResult, DeleteAssetOutput, error) {
		// The admin route checks confirmSlug inside its own transaction,
		// like reset-data and decide_resource: no pre-read here, the slug
		// travels in the body.
		body, err := jsonx.Marshal(struct {
			ConfirmSlug string `json:"confirmSlug"`
		}{ConfirmSlug: in.ConfirmSlug})
		if err != nil {
			return nil, DeleteAssetOutput{}, err
		}
		method, path := toolPath("delete_asset", "DELETE /api/workspaces/{id}/assets/{name}", in.WorkspaceID, url.PathEscape(in.Name))
		if err := lb.call(ctx, method, path, body, nil); err != nil {
			return nil, DeleteAssetOutput{}, err
		}
		return nil, DeleteAssetOutput{WorkspaceID: in.WorkspaceID, Name: in.Name, Deleted: true}, nil
	}
}
