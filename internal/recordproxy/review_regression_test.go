package recordproxy

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestProxyWriteDeadlineHonorsConfiguredTimeout(t *testing.T) {
	for _, tc := range []struct {
		name  string
		delay time.Duration
		want  int
	}{
		{"successful upstream", 250 * time.Millisecond, http.StatusOK},
		{"upstream timeout", 2 * time.Second, http.StatusGatewayTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, id := fixture(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(tc.delay):
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()
			c := DefaultConfig()
			c.Mode = "passthrough"
			c.Upstream = upstream.URL
			c.TimeoutSeconds = 1
			if _, err := repo.Save(t.Context(), id, c); err != nil {
				t.Fatal(err)
			}
			service := NewService(repo, &config.Config{ProxyAllowlist: []string{upstream.URL}, MaxBody: 1024, MaxResponse: 1024})
			downstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				service.ServeProxy(w, r, &workspaces.Workspace{ID: id}, "", false, nil)
			}))
			downstream.Config.WriteTimeout = 100 * time.Millisecond
			downstream.Start()
			defer downstream.Close()
			client := downstream.Client()
			client.Timeout = 4 * time.Second
			response, err := client.Get(downstream.URL)
			if err != nil {
				t.Fatalf("response inside proxy budget lost: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != tc.want {
				t.Fatalf("status=%d body=%s err=%v", response.StatusCode, body, err)
			}
		})
	}
}

type replayRaceBody struct {
	action func()
	reader io.Reader
}

func (b *replayRaceBody) Read(p []byte) (int, error) {
	if b.action != nil {
		f := b.action
		b.action = nil
		f()
	}
	return b.reader.Read(p)
}
func (b *replayRaceBody) Close() error { return nil }

func TestReplayRefusesRecycledWorkspaceID(t *testing.T) {
	repo, id := fixture(t)
	c := DefaultConfig()
	c.Mode = "replay"
	c.Upstream = "http://api.test"
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/users", nil)
	r.Body = &replayRaceBody{reader: strings.NewReader("{}"), action: func() {
		if err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), "DELETE FROM workspaces WHERE id=?", id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		ws, err := workspaces.NewRepo(repo.db).Create(t.Context(), workspaces.CreateInput{Name: "replacement", Slug: "replacement"})
		if err != nil {
			t.Fatal(err)
		}
		if ws.ID != id {
			t.Fatalf("expected reused id %d got %d", id, ws.ID)
		}
		next, err := repo.Save(t.Context(), id, c)
		if err != nil {
			t.Fatal(err)
		}
		key := RequestKey(fmt.Sprintf("%s|auth=false|cookies=false", c.Upstream), r, []byte("{}"))
		if _, err := repo.Record(t.Context(), id, next, Recording{Key: key, Method: http.MethodGet, Path: "/users", Status: 200, ContentType: "application/json", Body: []byte(`{"private":"replacement workspace data"}`)}); err != nil {
			t.Fatal(err)
		}
	}}
	service := NewService(repo, &config.Config{MaxBody: 1024, MaxResponse: 1024})
	w := httptest.NewRecorder()
	service.ServeProxy(w, r, &workspaces.Workspace{ID: id, Slug: "proxy-test"}, "", false, nil)
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "replacement workspace data") {
		t.Fatalf("stale replay status=%d body=%s", w.Code, w.Body)
	}
}
