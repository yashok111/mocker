// tools_config.go registers A9's one tool: get_server_config, the effective
// ceilings of the process the agent is talking to. Tool fifty-seven, and
// the second after get_guide whose toolRoutes row is empty — it reaches no
// handler because there is nothing in the domain to reach: the numbers are
// the process's own environment, already in the *config.Config New was
// handed, and the SAME config.Limits projection the panel receives inside
// ServerConfigView, so the two readers cannot disagree.
//
// Why a tool and not a route: a route for a dozen integers would be a
// contract entry, a coverage exemption and an allowlist line for a value
// that changes only with a restart; and the panel already has the numbers
// through login. What an agent needs is the answer to "why 413" and "how
// big may a frame be" before it sends, not after.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/config"
)

func addConfigTools(s *sdk.Server, cfg *config.Config) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_server_config",
		Description: "Reports this server's routing facts and effective limits, read from its own " +
			"configuration: adminHost, baseDomain, routing (host|path) and reservedPrefix (the " +
			"control routes live under it on every workspace host); and limits — maxBodyBytes " +
			"(any request to the admin API, including import_spec's document and upload_asset's " +
			"base64), maxResponseBytes (a generated or pinned body, and one stream frame), " +
			"maxAssetBytes / maxAssetsTotalBytes, maxEntities (rows per resource family), " +
			"trafficMaxBodyBytes and trafficRetention, checkpointRetention and " +
			"checkpointDebounceSec, streamMaxConns (per workspace; a handshake over it is refused " +
			"503), streamMaxLifetimeSec, streamMaxFrameBytes (an inbound WebSocket frame over it " +
			"closes with 1009), streamSendBudgetBytes, streamPingSec, streamFrameTimeoutSec, " +
			"streamTrafficFrames (off|first|all). Bytes are bytes, seconds are seconds. Read it once per " +
			"session, before sizing a document, a frame or a family. Calls no admin route.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetServerConfig(cfg))
}

// GetServerConfigInput is get_server_config's input: nothing.
type GetServerConfigInput struct{}

// GetServerConfigOutput is get_server_config's declared output.
type GetServerConfigOutput struct {
	AdminHost      string        `json:"adminHost"`
	BaseDomain     string        `json:"baseDomain"`
	Routing        string        `json:"routing"`
	ReservedPrefix string        `json:"reservedPrefix"`
	Limits         config.Limits `json:"limits"`
}

func handleGetServerConfig(cfg *config.Config) sdk.ToolHandlerFor[GetServerConfigInput, GetServerConfigOutput] {
	return func(_ context.Context, _ *sdk.CallToolRequest, _ GetServerConfigInput) (*sdk.CallToolResult, GetServerConfigOutput, error) {
		return nil, GetServerConfigOutput{
			AdminHost:      cfg.AdminHost,
			BaseDomain:     cfg.BaseDomain,
			Routing:        string(cfg.Routing),
			ReservedPrefix: cfg.ReservedPrefix,
			Limits:         cfg.Limits(),
		}, nil
	}
}
