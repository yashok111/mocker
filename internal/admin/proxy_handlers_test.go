package admin_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/recordproxy"
)

func TestProxyConfigurationAuthCASAndClear(t *testing.T) {
	ts := newTestServerCfg(t, func(c *config.Config) { c.ProxyAllowlist = []string{"https://api.example.com"} })
	cookie, csrf, id, slug := ts.createWorkspace(t, "proxy-admin", "proxy test")
	path := fmt.Sprintf("http://mocker.local/api/workspaces/%.0f/proxy", id)
	if got := ts.do(jsonRequest(t, "GET", path, nil, nil, "")); got.Code != 401 {
		t.Fatalf("unauth %d", got.Code)
	}
	got := ts.do(jsonRequest(t, "GET", path, nil, cookie, csrf))
	if got.Code != 200 {
		t.Fatalf("get: %d %s", got.Code, got.Body)
	}
	c := recordproxy.DefaultConfig()
	c.Mode = "record"
	c.Upstream = "https://evil.example.com"
	if got := ts.do(jsonRequest(t, "PUT", path, c, cookie, csrf)); got.Code != 400 {
		t.Fatalf("allowlist %d %s", got.Code, got.Body)
	}
	c.Upstream = "https://api.example.com"
	if got := ts.do(jsonRequest(t, "PUT", path, c, cookie, "")); got.Code != 403 {
		t.Fatalf("CSRF %d", got.Code)
	}
	got = ts.do(jsonRequest(t, "PUT", path, c, cookie, csrf))
	if got.Code != 200 {
		t.Fatalf("save %d %s", got.Code, got.Body)
	}
	var view struct{ Config recordproxy.Config }
	if err := json.Unmarshal(got.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if got := ts.do(jsonRequest(t, "PUT", path, c, cookie, csrf)); got.Code != 409 {
		t.Fatalf("CAS %d %s", got.Code, got.Body)
	}
	if got := ts.do(jsonRequest(t, "POST", path+"/recordings/clear", map[string]any{"version": view.Config.Version, "confirmSlug": "wrong"}, cookie, csrf)); got.Code != 400 {
		t.Fatalf("confirmation %d", got.Code)
	}
	if got := ts.do(jsonRequest(t, "POST", path+"/recordings/clear", map[string]any{"version": view.Config.Version, "confirmSlug": slug}, cookie, csrf)); got.Code != 200 {
		t.Fatalf("clear %d %s", got.Code, got.Body)
	}
}

func TestProxyRecordingViewPreservesExactJSONText(t *testing.T) {
	ts := newTestServer(t)
	cookie, csrf, id, _ := ts.createWorkspace(t, "proxy-reader", "proxy precision")
	repo := recordproxy.NewRepo(ts.db)
	c, err := repo.Save(t.Context(), int64(id), recordproxy.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"id":9007199254740993}`
	if _, err := repo.Record(t.Context(), int64(id), c, recordproxy.Recording{Key: "exact", Method: "GET", Path: "/users/1", Status: 200, ContentType: "application/json", Body: []byte(body)}); err != nil {
		t.Fatal(err)
	}
	r := ts.do(jsonRequest(t, "GET", fmt.Sprintf("http://mocker.local/api/workspaces/%.0f/proxy/recordings", id), nil, cookie, csrf))
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	var out struct {
		Items []struct {
			BodyText string `json:"bodyText"`
		}
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].BodyText != body {
		t.Fatalf("exact JSON text missing: %s", r.Body)
	}
}
