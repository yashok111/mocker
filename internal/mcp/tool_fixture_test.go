package mcp

import (
	"context"
	"net/http"
	"sync"
)

// Registration and compiled schemas are read-only after New. A fixture owns
// only its Caller, carried on each request instead of swapping shared state.
// This keeps real SDK validation/transport while avoiding ~9s of registration
// under race for every assertion. Production startup is benchmarked separately.
var sharedToolHandler = sync.OnceValue(func() http.Handler {
	return New(fixtureCaller{}, testKey, testConfig(), nil).Handler()
})

// Preserve rawCaller's optional capability: a JSON-only fixture must still
// exercise upload_asset's refusal for callers that cannot send binary bodies.
var sharedRawToolHandler = sync.OnceValue(func() http.Handler {
	return New(fixtureRawCaller{}, testKey, testConfig(), nil).Handler()
})

type fixtureCallerKey struct{}
type fixtureCaller struct{}

func (fixtureCaller) CallAsMCP(ctx context.Context, req *http.Request, method, path string, body []byte) (int, []byte, error) {
	return ctx.Value(fixtureCallerKey{}).(Caller).CallAsMCP(ctx, req, method, path, body)
}

type fixtureRawCaller struct{ fixtureCaller }

func (fixtureRawCaller) CallAsMCPRaw(ctx context.Context, req *http.Request, method, path, contentType string, body []byte) (int, []byte, error) {
	return ctx.Value(fixtureCallerKey{}).(rawCaller).CallAsMCPRaw(ctx, req, method, path, contentType, body)
}

type toolFixture struct{ handler http.Handler }

func newToolFixture(calls Caller) *toolFixture {
	handler := sharedToolHandler
	if _, ok := calls.(rawCaller); ok {
		handler = sharedRawToolHandler
	}
	shared := handler()
	return &toolFixture{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shared.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), fixtureCallerKey{}, calls)))
	})}
}

func (fixture *toolFixture) Handler() http.Handler { return fixture.handler }
