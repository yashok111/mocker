package mcp

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// rawFakeCaller is fakeCaller plus CallAsMCPRaw, recording the content type
// and body the tool sent — A6 (A9): upload_asset must reach the PUT with the
// file's own type and the DECODED bytes, and must refuse bad base64 before
// any call is made.
type rawFakeCaller struct {
	fakeCaller
	calls       int
	contentType string
	sent        []byte
	method      string
	path        string
}

func (f *rawFakeCaller) CallAsMCPRaw(_ context.Context, _ *http.Request, method, path, contentType string, body []byte) (int, []byte, error) {
	f.calls++
	f.method, f.path, f.contentType, f.sent = method, path, contentType, body
	return f.status, f.body, nil
}

func TestUploadAsset_sendsTheFileUnderItsOwnType(t *testing.T) {
	caller := &rawFakeCaller{fakeCaller: fakeCaller{status: http.StatusCreated,
		body: []byte(`{"name":"pic.jpg","mediaType":"image/jpeg","sizeBytes":3,"sha256":"abc","createdAt":1,"updatedAt":1,"url":"http://alex.mock.local/__mocker/assets/pic.jpg"}`)}}
	ep := New(caller, testKey, testConfig(), nil)

	data := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF})
	rec := doMCP(t, ep.Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upload_asset","arguments":{"workspaceId":7,"name":"pic.jpg","mediaType":"image/jpeg; charset=binary","dataBase64":"`+data+`"}}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if caller.calls != 1 || caller.method != http.MethodPut || caller.path != "/api/workspaces/7/assets/pic.jpg" {
		t.Fatalf("call = %d %s %s", caller.calls, caller.method, caller.path)
	}
	if caller.contentType != "image/jpeg" || string(caller.sent) != "\xff\xd8\xff" {
		t.Fatalf("sent type=%q body=%q, want image/jpeg and the decoded bytes", caller.contentType, caller.sent)
	}
	if !strings.Contains(rec.Body.String(), `"created":true`) || !strings.Contains(rec.Body.String(), "__mocker/assets/pic.jpg") {
		t.Fatalf("output = %s", rec.Body)
	}

	// Bad base64: the tool's own error, no call made.
	caller.calls = 0
	rec = doMCP(t, ep.Handler(), `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"upload_asset","arguments":{"workspaceId":7,"name":"pic.jpg","mediaType":"image/jpeg","dataBase64":"not base64!"}}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if caller.calls != 0 || !strings.Contains(rec.Body.String(), "does not decode") {
		t.Fatalf("bad base64: calls=%d body=%s", caller.calls, rec.Body)
	}
}

// TestUploadAsset_refusesACallerWithoutRaw pins D8's shape: a Caller that
// cannot carry a non-JSON body makes upload_asset an error, never a JSON
// PUT of raw bytes.
func TestUploadAsset_refusesACallerWithoutRaw(t *testing.T) {
	ep := newTestEndpoint(t)
	rec := doMCP(t, ep.Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upload_asset","arguments":{"workspaceId":7,"name":"pic.jpg","mediaType":"image/jpeg","dataBase64":"AA=="}}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if !strings.Contains(rec.Body.String(), "cannot carry a non-JSON body") {
		t.Fatalf("body = %s", rec.Body)
	}
}
