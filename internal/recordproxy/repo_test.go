package recordproxy

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/testkit"
	"github.com/yashok111/mocker/internal/workspaces"
)

func fixture(t *testing.T) (*Repo, int64) {
	t.Helper()
	db := testkit.NewDB(t)
	ws, err := workspaces.NewRepo(db).Create(t.Context(), workspaces.CreateInput{Name: "proxy", Slug: "proxy-test"})
	if err != nil {
		t.Fatal(err)
	}
	return NewRepo(db), ws.ID
}
func TestRequestKeys(t *testing.T) {
	a := httptest.NewRequest(http.MethodPost, "http://mock/users/1?b=2&a=1", nil)
	b := httptest.NewRequest(http.MethodPost, "http://mock/users/1?a=1&b=2", nil)
	a.Header.Set("Content-Type", "application/json")
	b.Header.Set("Content-Type", "application/json")
	ka := RequestKey("http://api", a, []byte(`{"n":1,"x":2}`))
	if ka != RequestKey("http://api", b, []byte(`{ "x":2, "n":1 }`)) {
		t.Fatal("unstable JSON/query key")
	}
	b.Header.Set("Authorization", "Bearer other")
	if ka == RequestKey("http://api", b, []byte(`{"n":1,"x":2}`)) {
		t.Fatal("credentials not isolated")
	}
	if ka == RequestKey("http://other", a, []byte(`{"n":1,"x":2}`)) {
		t.Fatal("upstreams not isolated")
	}
}
func TestRecordPersistenceFirstWinsAndClearFence(t *testing.T) {
	repo, id := fixture(t)
	cfg := DefaultConfig()
	cfg.Mode = "record"
	cfg.Upstream = "http://api"
	cfg.Overwrite = "first"
	saved, err := repo.Save(t.Context(), id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rec := Recording{Key: "key", Method: "GET", Path: "/users/1", Status: 200, ContentType: "application/json", Body: []byte(`{"id":1}`)}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.Record(t.Context(), id, saved, rec); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, err := repo.List(t.Context(), id)
	if err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	rec.Body = []byte(`{"id":2}`)
	changed, err := repo.Record(t.Context(), id, saved, rec)
	if changed || err != nil {
		t.Fatalf("first wins: %v %v", changed, err)
	}
	got, err := NewRepo(repo.db).Lookup(t.Context(), id, "key", saved.Version)
	if err != nil || string(got.Body) != `{"id":1}` {
		t.Fatalf("%+v %v", got, err)
	}
	if err := repo.Clear(t.Context(), id, saved.Version, "proxy-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Record(t.Context(), id, saved, rec); !errors.Is(err, ErrConflict) {
		t.Fatalf("inflight fence: %v", err)
	}
	if _, err := repo.Save(t.Context(), id, cfg); !errors.Is(err, ErrConflict) {
		t.Fatalf("config CAS: %v", err)
	}
	list, err = repo.List(t.Context(), id)
	if err != nil || len(list) != 0 {
		t.Fatalf("clear: %+v %v", list, err)
	}
}
func TestConfigValidation(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(nil); err != nil {
		t.Fatal(err)
	}
	c.Mode = "record"
	c.Upstream = "http://api"
	if err := c.Validate(nil); err == nil {
		t.Fatal("unapproved origin")
	}
	if err := c.Validate([]string{"http://api"}); err != nil {
		t.Fatal(err)
	}
	c.Operations = map[string]string{"GET /users/{id}": "typo"}
	if err := c.Validate([]string{"http://api"}); err == nil {
		t.Fatal("invalid policy")
	}
}

func TestRequestKeyPreservesLargeNumbers(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Content-Type", "application/json")
	if RequestKey("http://api", r, []byte(`{"id":9007199254740992}`)) == RequestKey("http://api", r, []byte(`{"id":9007199254740993}`)) {
		t.Fatal("distinct integer requests collapsed")
	}
}

func TestRequestKeyIsolatesTenantAndConditionalHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/users/1", nil)
	a := RequestKey("http://api", r, nil)
	for _, header := range []string{"X-Tenant-ID", "If-None-Match", "Range"} {
		q := r.Clone(t.Context())
		q.Header.Set(header, "different")
		if a == RequestKey("http://api", q, nil) {
			t.Errorf("ignored %s", header)
		}
	}
}

func TestRecordingQuotaDeleteAndWorkspaceIsolation(t *testing.T) {
	repo, id := fixture(t)
	cfg, err := repo.Save(t.Context(), id, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	rec := Recording{Key: "first", Method: "GET", Path: "/", Status: 200, Body: []byte(`{"value":1}`)}
	if _, err := repo.Record(t.Context(), id, cfg, rec); err != nil {
		t.Fatal(err)
	}
	rows, _ := repo.List(t.Context(), id)
	if err := repo.Delete(t.Context(), id+1, rows[0].ID, cfg.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross workspace delete %v", err)
	}
	if err := repo.Delete(t.Context(), id, rows[0].ID, cfg.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Record(t.Context(), id, cfg, rec); !errors.Is(err, ErrConflict) {
		t.Fatal("delete did not fence inflight recording")
	}
	cfg, _ = repo.Get(t.Context(), id)
	if err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		for i := 0; i < MaxRecordings; i++ {
			_, err := tx.ExecContext(t.Context(), `INSERT INTO proxy_recordings(workspace_id,request_key,method,path,status,content_type,body,redacted,created_at,updated_at) VALUES(?,?,'GET','/',200,'application/json',?,0,0,0)`, id, fmt.Sprint(i), []byte(`{}`))
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Record(t.Context(), id, cfg, rec); !errors.Is(err, ErrLimit) {
		t.Fatalf("quota %v", err)
	}
	rec.Key = "0"
	rec.Body = []byte(`{"replace":true}`)
	if _, err := repo.Record(t.Context(), id, cfg, rec); err != nil {
		t.Fatalf("replacement at quota %v", err)
	}
	if err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "DELETE FROM workspaces WHERE id=?", id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.List(t.Context(), id)
	if err != nil || len(rows) != 0 {
		t.Fatalf("workspace cascade %d %v", len(rows), err)
	}
}

func TestClearChecksSlugInItsTransaction(t *testing.T) {
	repo, id := fixture(t)
	cfg, err := repo.Save(t.Context(), id, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "UPDATE workspaces SET slug='renamed' WHERE id=?", id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Clear(t.Context(), id, cfg.Version, "proxy-test"); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("stale slug %v", err)
	}
}

func TestInactiveConfigStillRejectsCredentialURLs(t *testing.T) {
	for _, mode := range []string{"off", "replay"} {
		c := DefaultConfig()
		c.Mode = mode
		c.Upstream = "https://user:password@api.example.com"
		if err := c.Validate(nil); err == nil {
			t.Errorf("%s stores credentials", mode)
		}
	}
}

func TestConfigNormalizesNullOperations(t *testing.T) {
	repo, id := fixture(t)
	cfg := DefaultConfig()
	cfg.Operations = nil
	saved, err := repo.Save(t.Context(), id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Operations == nil {
		t.Fatal("saved null policies")
	}
	got, err := repo.Get(t.Context(), id)
	if err != nil || got.Operations == nil {
		t.Fatalf("read null policies: %+v %v", got, err)
	}
}

func TestRecordingCannotCrossRecreatedWorkspace(t *testing.T) {
	repo, id := fixture(t)
	cfg, err := repo.Save(t.Context(), id, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
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
		t.Skip("workspace IDs were not reused")
	}
	if _, err := repo.Save(t.Context(), id, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Record(t.Context(), id, cfg, Recording{Key: "old", Method: "GET", Path: "/", Status: 200, Body: []byte(`{"private":true}`)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("old request reached replacement workspace: %v", err)
	}
}
