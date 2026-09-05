package admin

import (
	"errors"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/overrides"
)

// refusalCode is the envelope code for a 400 that came out of a row's
// validation. Every such refusal is wrapped in customep.ErrInvalidRow or
// overrides.ErrInvalidRow and USED to be answered as plain bad_request with
// the message as the only distinction; A18's documents (the gate document,
// the embedded guide, api/openapi.json) promised seven named codes and the
// server delivered none of them — an agent branching on
// `error.code == "tick_lua_and_schema"` never matched. The validators now
// wrap the named sentinel alongside ErrInvalidRow, and this is the one place
// the name is read, through errors.Is and never by string, so the three
// mappers that answer a refusal (create endpoint, update endpoint, PUT
// override) cannot drift from one another. Anything unnamed stays
// bad_request. A18 review finding 10.
func refusalCode(err error) string {
	for _, c := range namedRefusals {
		if errors.Is(err, c.sentinel) {
			return c.code
		}
	}
	return httpx.CodeBadRequest
}

// namedRefusals is the whole list. The code is the sentinel's own text — the
// two are kept side by side here so a reader sees that they agree, and a
// test pins it.
var namedRefusals = []struct {
	sentinel error
	code     string
}{
	{overrides.ErrBadFunction, "bad_function"},
	{overrides.ErrFunctionAndBody, "function_and_body"},
	{customep.ErrFunctionOnStream, "function_on_stream"},
	{customep.ErrTickLuaAndSchema, "tick_lua_and_schema"},
	{customep.ErrOnFrameOnSSE, "on_frame_on_sse"},
	{customep.ErrOnFrameAndReactive, "on_frame_and_reactive"},
	{customep.ErrOnFrameAndEcho, "on_frame_and_echo"},
}
