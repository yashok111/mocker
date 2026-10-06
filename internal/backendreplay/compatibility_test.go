package backendreplay

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"go/build"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"github.com/yashok111/mocker/internal/ordersreference"
	"github.com/yashok111/mocker/internal/probe"
	"github.com/yashok111/mocker/internal/testkit"
)

// compatibilityProvenance hashes the actual selected production source roster,
// following local imports and embedded files. No ignored manifests or generated
// oracle artifacts are needed. This describes test fixture build inputs, not a
// claim that the separately linked standalone binary was executed.
func compatibilityProvenance(t *testing.T, variant string) Provenance {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx := build.Default
	ctx.BuildTags = []string{"orders_" + variant}
	paths := map[string]bool{"go.mod": true, "go.sum": true}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(dir string) {
		if seen[dir] {
			return
		}
		seen[dir] = true
		pkg, err := ctx.ImportDir(filepath.Join(root, dir), 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range append(slices.Clone(pkg.GoFiles), pkg.CgoFiles...) {
			paths[filepath.ToSlash(filepath.Join(dir, file))] = true
		}
		for _, pattern := range pkg.EmbedPatterns {
			matches, err := filepath.Glob(filepath.Join(root, dir, pattern))
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) == 0 {
				t.Fatalf("embed pattern has no files: %s", pattern)
			}
			for _, match := range matches {
				rel, err := filepath.Rel(root, match)
				if err != nil {
					t.Fatal(err)
				}
				paths[filepath.ToSlash(rel)] = true
			}
		}
		for _, importPath := range pkg.Imports {
			if local, ok := strings.CutPrefix(importPath, "github.com/yashok111/mocker/"); ok {
				visit(local)
			}
		}
	}
	visit("cmd/orders-reference")
	provenance := Provenance{SourceFiles: []p.SourceFile{}}
	for path := range paths {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Lstat(absolute)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("nonregular source %s", path)
		}
		data, err := os.ReadFile(absolute)
		if err != nil {
			t.Fatal(err)
		}
		provenance.SourceFiles = append(provenance.SourceFiles, p.SourceFile{Path: path, SHA256: p.HashBytes(data)})
	}
	slices.SortFunc(provenance.SourceFiles, func(a, b p.SourceFile) int { return strings.Compare(a.Path, b.Path) })
	source, err := p.SourceTreeHash(provenance.SourceFiles)
	if err != nil {
		t.Fatal(err)
	}
	provenance.Build = p.BuildDescriptor{ServiceVersion: "compatibility-in-process-v1", Variant: variant, SourceTreeHash: source, Toolchain: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, BuildFlags: []string{"-tags=orders_" + variant}}
	t.Logf("production source roster=%d sourceTreeHash=%s variant=%s", len(provenance.SourceFiles), source, variant)
	return provenance
}

func runOrdersCompatibility(t *testing.T, variant, drop string) *Run {
	t.Helper()
	provenance := compatibilityProvenance(t, variant)
	buildHash, err := p.BuildHash(provenance.Build)
	if err != nil {
		t.Fatal(err)
	}
	token := "compatibility-only-token-01234567890123456789"
	isolation := newReplayID()
	fixturePath := filepath.Join(t.TempDir(), "orders.sqlite")
	fixture, err := ordersreference.Open(ordersreference.Config{DBPath: fixturePath, Token: token, IsolationID: isolation, TargetID: "compatibility", ConfigVersion: 1}, ordersreference.Build{Variant: variant, ServiceVersion: provenance.Build.ServiceVersion, SourceTreeHash: provenance.Build.SourceTreeHash, BuildHash: buildHash})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Error(err)
		}
	})
	db := testkit.NewDB(t)
	var posts, resets, arms, orders atomic.Int32
	var dropped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			fixture.ServeHTTP(w, r)
			return
		}
		posts.Add(1)
		endpoint := "order"
		if strings.HasSuffix(r.URL.Path, "/reset") {
			endpoint = "reset"
			resets.Add(1)
		} else if strings.HasSuffix(r.URL.Path, "/failure") {
			endpoint = "failure"
			arms.Add(1)
		} else {
			orders.Add(1)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "read", 500)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var fence p.Fence
		if err = json.Unmarshal(raw, &fence); err != nil {
			t.Error(err)
			http.Error(w, "decode", 500)
			return
		}
		var persisted []byte
		if err = db.R.QueryRowContext(r.Context(), `SELECT request_json FROM backend_replay_steps WHERE run_id=? AND request_key=?`, fence.RunID, fence.RequestKey).Scan(&persisted); err != nil || !bytes.Equal(raw, persisted) {
			t.Errorf("mutation arrived without durable exact request: %v", err)
			http.Error(w, "not durable", 500)
			return
		}
		if endpoint == drop && dropped.CompareAndSwap(false, true) {
			recorder := httptest.NewRecorder()
			fixture.ServeHTTP(recorder, r)
			if recorder.Code != 200 && recorder.Code != 503 {
				t.Errorf("dropped response was not a committed mutation: %d %s", recorder.Code, recorder.Body.String())
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		fixture.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	transport, err := probe.NewTestProfileClient(t.Context(), probe.TestTarget{ID: "compatibility", Version: 1, Origin: server.URL, AllowedIPs: []string{"127.0.0.1"}, CredentialRef: "COMPATIBILITY_TOKEN", IsolationID: isolation}, func(string) string { return token })
	if err != nil {
		t.Fatal(err)
	}
	live, err := transport.Identity(t.Context())
	if err != nil || live.Payload == nil {
		t.Fatalf("identity: %v", err)
	}
	graphs := backendmodel.NewRepo(db)
	project, err := graphs.Create(t.Context(), backendmodel.CreateInput{Name: "Compatibility", IdempotencyKey: newReplayID()})
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(NewRepo(db), graphs, []Target{{TargetInfo: TargetInfo{ID: "compatibility", Version: 1, IsolationID: isolation}, Transport: transport, ConfigFingerprint: transport.(interface{ ConfigHash() string }).ConfigHash()}})
	s.ActorAllowed = func(_ context.Context, actor string) bool { return actor == "compatibility-actor" }
	if err = s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	profile, err := s.Connect(t.Context(), project.ID, "compatibility-actor", ConnectInput{ConfiguredTargetID: "compatibility", ExpectedIdentityHash: live.Payload.IdentityHash, AllowReset: true, IdempotencyKey: newReplayID()})
	if err != nil {
		t.Fatal(err)
	}
	target := backendmodel.BackendReadTarget{RevisionID: project.CurrentRevisionID}
	graph, err := graphs.ResolveEffectiveGraph(t.Context(), project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	pkg := Template()
	pkg.Profile = profile.Pin
	pkg.Target = target
	pkg.TargetHash = graph.Pins.TargetHash
	saved, err := s.SavePackage(t.Context(), project.ID, "compatibility-actor", SavePackageInput{ID: newReplayID(), Package: pkg, Provenance: provenance, IdempotencyKey: newReplayID()})
	if err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 0 {
		t.Fatal("connect/save caused an effect")
	}
	run, err := s.Start(t.Context(), project.ID, "compatibility-actor", StartInput{Package: saved.Pin, Profile: profile.Pin, ExpectedIdentityHash: profile.IdentityHash, ResetAuthorizationID: profile.Authorization.ID, ResetAuthorizationVersion: profile.Authorization.Version, IdempotencyKey: newReplayID()})
	if err != nil {
		t.Fatal(err)
	}
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claim: %v", err)
	}
	if err = s.execute(t.Context(), actor, claimed); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.Get(t.Context(), project.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "succeeded"
	charges := 1
	if variant == "buggy" {
		want = "failed"
		charges = 2
	}
	if terminal.Status != want || terminal.Report == nil || terminal.Report.Status != want {
		t.Fatalf("terminal status=%s report=%+v", terminal.Status, terminal.Report)
	}
	if len(terminal.Report.Assertions) != 4 {
		t.Fatal("missing assertions")
	}
	for _, assertion := range terminal.Report.Assertions {
		if assertion.Kind == "charges" {
			if assertion.Actual != charges || assertion.Scope != "mocked" || assertion.Passed != (variant == "fixed") {
				t.Fatalf("charge assertion=%+v", assertion)
			}
		} else if !assertion.Passed {
			t.Fatalf("unrelated failure=%+v", assertion)
		}
	}
	if posts.Load() != 4 || resets.Load() != 1 || arms.Load() != 1 || orders.Load() != 2 || dropped.Load() != (drop != "") {
		t.Fatalf("dispatch counts posts=%d reset=%d arms=%d orders=%d dropped=%t", posts.Load(), resets.Load(), arms.Load(), orders.Load(), dropped.Load())
	}
	journal, err := transport.Journal(t.Context(), run.ID)
	if err != nil || journal.Payload == nil {
		t.Fatalf("journal: %v", err)
	}
	j := journal.Payload
	if !j.Complete || j.Epoch != 1 || j.CurrentEpoch != 1 || len(j.Receipts) != 4 || len(j.Events) != 8 || len(j.Orders) != 1 || len(j.Charges) != charges || len(j.PendingKeys) != 0 {
		t.Fatalf("unexpected actual journal: %+v", j)
	}
	// Inspect the fixture's durable rows independently of its HTTP journal and
	// the engine checker. Opening read-only cannot create or change fixture data.
	fixtureURL := url.URL{Scheme: "file", Path: fixturePath, RawQuery: "mode=ro"}
	fixtureDB, err := sql.Open("sqlite", fixtureURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	for _, check := range []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM orders WHERE run_id=?`, 1},
		{`SELECT count(*) FROM charges WHERE run_id=?`, charges},
		{`SELECT sum(json_extract(document,'$.amountMinor')) FROM charges WHERE run_id=?`, charges * 1000},
		{`SELECT consumed FROM arms WHERE run_id=?`, 1},
		{`SELECT count(*) FROM requests WHERE run_id=?`, 4},
		{`SELECT count(*) FROM events WHERE run_id=?`, 8},
	} {
		var actual int
		if err := fixtureDB.QueryRowContext(t.Context(), check.query, run.ID).Scan(&actual); err != nil || actual != check.want {
			t.Fatalf("actual fixture DB %s: actual=%d want=%d err=%v", check.query, actual, check.want, err)
		}
	}
	var steps, evidence, leases int
	for _, check := range []struct {
		query string
		out   *int
	}{{`SELECT count(*) FROM backend_replay_steps WHERE run_id=?`, &steps}, {`SELECT count(*) FROM backend_replay_evidence WHERE run_id=?`, &evidence}, {`SELECT count(*) FROM backend_replay_target_leases WHERE run_id=?`, &leases}} {
		if err = db.R.QueryRowContext(t.Context(), check.query, run.ID).Scan(check.out); err != nil {
			t.Fatal(err)
		}
	}
	minimum := 7
	if drop != "" {
		minimum = 9
	}
	if steps != 4 || evidence < minimum || leases != 0 {
		t.Fatalf("durable state steps=%d evidence=%d leases=%d", steps, evidence, leases)
	}
	t.Logf("variant=%s lostReply=%q status=%s orders=%d mockedCharges=%d events=%d durableSteps=%d evidence=%d", variant, drop, terminal.Status, len(j.Orders), len(j.Charges), len(j.Events), steps, evidence)
	return terminal
}

func TestOrdersServiceReplayCompatibility(t *testing.T) {
	var buggy, fixed *Run
	t.Run("buggy", func(t *testing.T) { buggy = runOrdersCompatibility(t, "buggy", "") })
	t.Run("fixed", func(t *testing.T) { fixed = runOrdersCompatibility(t, "fixed", "") })
	t.Run("lost_reset_reply", func(t *testing.T) { runOrdersCompatibility(t, "fixed", "reset") })
	t.Run("lost_first_order_reply", func(t *testing.T) { runOrdersCompatibility(t, "fixed", "order") })
	if buggy != nil && fixed != nil {
		comparison, err := Compare(buggy.Input, *buggy.Report, fixed.Input, *fixed.Report)
		if err != nil || !comparison.Reproduced || !comparison.Fixed {
			t.Fatalf("actual regression compare: %+v %v", comparison, err)
		}
		if comparison.Left.Identity.BuildHash == comparison.Right.Identity.BuildHash || comparison.Left.Identity.SourceTreeHash == comparison.Right.Identity.SourceTreeHash || comparison.Left.Package == comparison.Right.Package {
			t.Fatal("comparison collapsed distinct pins")
		}
	}
}
