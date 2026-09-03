package mcp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

func TestGetServerConfig_reportsTheProcessOwnLimits(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{AdminHost: "mocker.local", BaseDomain: "mock.local", ReservedPrefix: "/__m", Routing: "host",
		MaxBody: 10 << 20, MaxResponse: 4 << 20, StreamMaxConns: 200, StreamMaxFrame: 64 << 10, StreamTrafficFrames: "off"}
	h := New(&fakeCaller{status: http.StatusOK, body: []byte(`{}`)}, testKey, cfg, nil).Handler()
	rec := doMCP(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_server_config","arguments":{}}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result struct {
			IsError           bool                  `json:"isError"`
			StructuredContent GetServerConfigOutput `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := env.Result.StructuredContent
	if env.Result.IsError || out.AdminHost != "mocker.local" || out.ReservedPrefix != "/__m" || out.Routing != "host" ||
		out.Limits.MaxBodyBytes != 10<<20 || out.Limits.StreamMaxConns != 200 || out.Limits.StreamMaxFrameBytes != 64<<10 ||
		out.Limits.StreamTrafficFrames != "off" {
		t.Errorf("out = %+v; body=%s", out, rec.Body.String())
	}
}
