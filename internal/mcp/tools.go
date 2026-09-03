package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools adds every tool this package publishes to srv, through lb:
// the nine §C of the MCP slice's context document names, the twenty-nine
// slice A2 of the mocker-a-mcp gate document adds (D5), and the four slice
// P3b of the mocker-p3b-resources gate document adds (D7) — thirty-eight
// plus four is forty-two — plus the one each that P3f (rederive_suggestions,
// registered inside the resource group) and P4a (get_workspace_drift, its
// own group) add: forty-four; plus the two slice A4 adds (decisions.md
// mocker-a4-mcp-reach D1, D9: probe_workspace and list_resource_entities,
// each its own group), plus the one P6a adds (decisions.md mocker-p6a-sse
// D16: get_stream_stats, its own group), plus the one P6b adds (decisions.md
// mocker-p6b-sse-mock D13: preview_endpoint) — forty-eight; plus the three
// P6c adds (list_stream_connections, close_stream_connection,
// push_stream_frame) — fifty-one; plus the three A6 adds (decisions.md
// mocker-a6-assets D8: upload_asset, list_assets, delete_asset, their own
// group) — fifty-four. This is the ONLY place any
// tool is registered — New (mcp.go) calls it exactly once, before ever
// returning an Endpoint, so sdk.AddTool's panic on a schema it cannot infer
// (mcp/server.go:561) surfaces at process startup, on the ground, rather
// than turning into a 500 the first time a client happens to trigger it
// (see New's own comment on why getServer must never build a server or call
// AddTool itself).
//
// One call per file, and each file has exactly one writer: the four A2
// groups below are built by four different agents (P3b's own resource group
// joins them as a fifth, P4a's drift group as a sixth, A4's probe and entity
// groups as a seventh and eighth), so the registration list
// lives here and nowhere else — four agents appending to one list is a lost
// write, not a merge. No earlier draft of the P3b orchestration script named
// this file, and the four resource tools would have had nowhere to be
// registered without it.
func registerTools(srv *sdk.Server, lb *loopback) {
	addOperationTools(srv, lb)
	addTrafficTools(srv, lb)
	addReadTools(srv, lb)
	addEditTools(srv, lb)
	addEndpointTools(srv, lb)
	addHistoryTools(srv, lb)
	addResourceTools(srv, lb)
	addDriftTools(srv, lb)
	addProbeTools(srv, lb)
	addEntityTools(srv, lb)
	addStreamTools(srv, lb)
	addStreamPreviewTools(srv, lb)
	addStreamConnectionTools(srv, lb)
	addAssetTools(srv, lb)
	addSpecTools(srv, lb)
	addTransferTools(srv, lb)
	addDesignTools(srv, lb)
	// The guide takes no loopback: it calls no admin route (tools_guide.go).
	addGuideTools(srv)
}
