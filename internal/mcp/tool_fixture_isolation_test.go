package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureIsolationCaller struct {
	marker  string
	entered chan<- struct{}
	release <-chan struct{}
}

func (caller fixtureIsolationCaller) CallAsMCP(ctx context.Context, req *http.Request, method, path string, _ []byte) (int, []byte, error) {
	if req.Header.Get("X-Fixture") != caller.marker || method != "GET" || path != "/api/backend-projects/capabilities" {
		return 0, nil, fmt.Errorf("request crossed fixture boundary: %s %s %s", req.Header.Get("X-Fixture"), method, path)
	}
	caller.entered <- struct{}{}
	select {
	case <-caller.release:
		return 200, []byte(`{"marker":"` + caller.marker + `"}`), nil
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

// Every call must be in flight before any completes. A shared, swapped Caller
// would mix either the inbound request or response across these fixtures.
func TestToolFixtureConcurrentCallerIsolation(t *testing.T) {
	const count = 8
	entered := make(chan struct{}, count)
	release := make(chan struct{})
	// Warm registration before starting the deadline for concurrent requests.
	newToolFixture(&recordingCaller{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	go func() {
		defer close(release)
		for range count {
			select {
			case <-entered:
			case <-ctx.Done():
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for index := range count {
		wg.Go(func() {
			marker := fmt.Sprint(index)
			fixture := newToolFixture(fixtureIsolationCaller{marker, entered, release})
			req := httptest.NewRequestWithContext(ctx, "POST", "http://mocker.local/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_backend_capabilities","arguments":{}}}`))
			req.Header.Set("Authorization", "Bearer "+testKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("X-Fixture", marker)
			rec := httptest.NewRecorder()
			fixture.Handler().ServeHTTP(rec, req)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"marker":"`+marker+`"`) || strings.Contains(rec.Body.String(), `"isError":true`) {
				t.Errorf("fixture %s: status=%d body=%s", marker, rec.Code, rec.Body)
			}
		})
	}
	wg.Wait()
}
