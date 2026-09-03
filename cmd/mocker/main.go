// Command mocker is the entry point: a "hash-password" subcommand that needs
// no configuration at all, a "healthcheck" subcommand that reads the same
// configuration the server did and dials its own /readyz (the container's
// HEALTHCHECK — see healthcheck.go), and the normal path that loads config,
// opens and migrates the database, wires every package together through the
// dispatcher in internal/server, and serves until told to stop.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mcp"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/server"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// shutdownDrain bounds how long in-flight requests get to finish once a
// shutdown signal arrives before the process gives up and exits anyway.
const shutdownDrain = 15 * time.Second

// janitorInterval is how often expired sessions are swept from the database.
// Hourly is frequent enough that a leaked cookie's session row does not sit
// around for days, and cheap enough not to matter next to real traffic.
const janitorInterval = time.Hour

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		if err := runHashPassword(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "mocker hash-password:", err)
			os.Exit(1)
		}
		return
	}
	// setup runs on a colleague's machine, BEFORE any .env exists, so like
	// hash-password it must not go through config.Load.
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := runSetup(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
			if !errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(os.Stderr, "mocker setup:", err)
			}
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		// errors.Join's Error() puts one problem per line, so this alone
		// satisfies "print every validation error" — a closed contour is
		// exactly the place where fixing a misconfiguration one restart per
		// variable is worse than reading the whole list once.
		fmt.Fprintln(os.Stderr, "mocker: invalid configuration:")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// config only checks the hash is non-empty (internal/config may not
	// import internal/auth); decoding it is auth's, and a hash argon2 would
	// panic on must refuse the start, not the first login.
	if cfg.AuthMode == config.AuthShared {
		if err := auth.ValidatePasswordHash(cfg.SharedPasswordHash); err != nil {
			fmt.Fprintln(os.Stderr, "mocker: invalid configuration:")
			fmt.Fprintln(os.Stderr, "MOCKER_SHARED_PASSWORD_HASH:", err)
			os.Exit(2)
		}
	}

	// After config.Load on purpose: a healthcheck that ran against an
	// environment the server itself would refuse to start from should fail
	// the same way, with the same list, rather than probing a listener that
	// cannot exist.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(cfg, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "mocker healthcheck:", err)
			os.Exit(1)
		}
		return
	}

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "mocker:", err)
		os.Exit(1)
	}
}

// runHashPassword implements the "hash-password" subcommand: print an
// argon2id hash for MOCKER_SHARED_PASSWORD_HASH from a password given as the
// first argument, or read one line from stdin when no argument is given.
// It touches no config and no database, so it works in a container with
// nothing configured yet — the whole point of shipping the hasher inside the
// same binary that will eventually read the hash back out.
func runHashPassword(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var plain string
	if len(args) > 0 && args[0] != "" {
		plain = args[0]
	} else {
		_, _ = fmt.Fprint(stderr, "Password: ")
		sc := bufio.NewScanner(stdin)
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return fmt.Errorf("read password from stdin: %w", err)
			}
			return errors.New("no password given (pass it as an argument or type one on stdin)")
		}
		plain = strings.TrimSpace(sc.Text())
	}
	if plain == "" {
		return errors.New("password must not be empty")
	}

	hash, err := auth.HashPassword(plain)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, hash)
	return nil
}

// run wires every package together and serves until a shutdown signal or a
// fatal error, in either case returning once the drain is complete.
func run(cfg *config.Config) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))

	// One root context for the process. Canceling it on SIGINT/SIGTERM is
	// what tells the janitor goroutine (and, transitively, the code below
	// that waits on it) to stop; store.Open/Migrate below run against it too
	// so a signal during startup aborts them instead of finishing a migration
	// nobody is going to use.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Migrate(ctx, log); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)

	adminSrv := admin.New(cfg, sessions, ws, db, log)
	specRepo := specs.NewRepo(db, cfg)

	// MOCKER_DEFAULT_SPEC names a spec by id, and only config.Load's syntax
	// check (positive integer) ran before the database existed — whether
	// that id actually names a spec can only be answered now. Checking it
	// HERE, before the listener opens, turns a typo'd id into a refused
	// startup instead of a silent no-op discovered by a confused operator
	// staring at a user's empty workspace list after their first login (the
	// DEBT this auto-create feature was written to close in the first
	// place — "a documented environment variable that silently does
	// nothing is worse than an absent one").
	if cfg.DefaultSpecID != 0 {
		if _, err := specRepo.ByID(ctx, cfg.DefaultSpecID); err != nil {
			return fmt.Errorf("MOCKER_DEFAULT_SPEC=%d: %w", cfg.DefaultSpecID, err)
		}
	}

	mockPlane := mockplane.New(cfg, ws, specRepo, log)
	// Wired here, at startup, before the server begins serving: skip this and
	// mockPlane keeps the nil OverrideSource [mockplane.Plane.New] gives it,
	// and every op_overrides row an operator ever writes silently misses in
	// production while every Go test (which wires this explicitly) keeps
	// passing — the near-miss this file has produced before, and the reason
	// every setter below is wired here rather than defaulted somewhere.
	//
	// Hoisted into a variable rather than inlined into the call: the
	// scenarios repo below needs the SAME *overrides.Repo, because
	// CreateFromCurrentState snapshots a workspace's rows through
	// overrides.Repo.ForWorkspace — the identical query the mock plane's
	// runtime build reads them with. Two instances over the same *store.DB
	// would behave identically today, and that is exactly why sharing one
	// is worth spelling out: it is a property nothing would go red over if
	// it silently stopped holding.
	overridesRepo := overrides.NewRepo(db)
	mockPlane.SetOverrides(overridesRepo)

	// The SLICE 2 sources: live-state directives (RAM, never SQLite — see
	// internal/livestate's own doc comment), traffic recording, and custom
	// endpoints. Same near-miss risk as SetOverrides above, times three: a
	// nil left here ships a fully green test suite with the feature dead in
	// production, because every Go test wires these explicitly and only
	// main.go can forget to.
	liveState := livestate.NewStore(livestate.DefaultTTL, nil)
	trafficRec := traffic.NewRecorder(db, log, traffic.Options{
		MaxBody:   cfg.TrafficMaxBody,
		Retention: cfg.TrafficRetention,
	})
	customRepo := customep.NewRepo(db)
	customRepo.MaxFrameBytes = cfg.MaxResponse // P6b D5: the mock plane's reader never writes, set anyway so one number governs both

	// liveState and trafficRec are handed to BOTH planes as the SAME
	// instance on purpose: the live-state store is RAM shared between the
	// router (which calls Apply on every request) and the admin API (which
	// calls Set/List/Clear from {id}/session); two Stores would mean the
	// admin UI shows directives the router never sees. Likewise one
	// Recorder — two would mean the admin DELETE flushes a queue that is
	// not the one filling up.
	mockPlane.SetLiveState(liveState)
	mockPlane.SetTraffic(trafficRec)
	mockPlane.SetCustomEndpoints(customRepo)

	// The P3a source: resources and entities (D13 clause 23's "a
	// ResourceSource that is never wired into buildRuntime" — the exact
	// near-miss risk every setter above already carries, except this one is
	// what proves it: scripts/smoke.sh's whole "created it - saw it in the
	// list" walk answers 404/empty on every route with SetResources or
	// SetEntities left nil, because internal/mockplane's own tests wire a
	// fake ResourceSource/EntityStore directly and never exercise this
	// file. A second *resources.Repo instance, not adminSrv's own
	// resourcesRepo: two *resources.Repo over the same *store.DB behave
	// identically (the package holds no state of its own beyond the DB
	// handle and the two byte caps, both process-level config read once),
	// same reasoning as SetScenarios above — sharing one across the planes
	// would only avoid an allocation, not a behavior.
	resourcesRepo := resources.NewRepo(db, specRepo, cfg.MaxResponse, cfg.TrafficMaxBody, int64(cfg.MaxEntities))
	mockPlane.SetResources(resourcesRepo)
	mockPlane.SetEntities(resourcesRepo)
	// A6 (DESIGN §32): the mock plane's read-only view of uploaded assets —
	// its own *assets.Repo over the one *store.DB, the same two caps
	// internal/admin's instance enforces on write. Left unwired, the asset
	// route 404s and every bodyRef answers asset_missing with every test
	// still green — scripts/smoke.sh's A6 block is what proves this line.
	mockPlane.SetAssets(assets.NewRepo(db, cfg.MaxAsset, cfg.MaxAssetsTotal))
	adminSrv.SetLiveState(liveState)
	adminSrv.SetTraffic(trafficRec)

	// P6a (decisions.md mocker-p6a-sse D4, D5, D12): the ONE stream
	// registry of the process, handed to two owners — the admin plane
	// serves connections through it, and the traffic recorder nudges it
	// after every committed batch. Two registries would mean nudges that
	// reach nobody and a cap that is a fiction, which is the same
	// same-instance rule liveState and trafficRec above already follow.
	// D12's three variables are converted from integer seconds to durations
	// HERE, at the package boundary: internal/config parses no Go duration
	// strings, and internal/stream never reads the environment. The same
	// near-miss risk as every setter above: skip SetStream and both routes
	// answer 503 service_unavailable in production while every Go test,
	// which wires it explicitly, stays green; skip SetNotifier and the
	// stream opens, pings, and never delivers a row.
	streamRegistry := stream.NewRegistry()
	trafficRec.SetNotifier(streamRegistry)
	// P6b (decisions.md mocker-p6b-sse-mock D8, D9): the mock plane's OWN
	// registry, capped per workspace by MOCKER_STREAM_MAX_CONNS — a second
	// instance on purpose, so an unauthenticated plane cannot exhaust the
	// admin feed's 64 by sharing a counter. The admin plane holds it only
	// to report it (GET /api/stream/stats's "mock" object, D10). The ping
	// and frame deadline are P6a's two, shared by both planes as D12 said
	// they would be; the lifetime is the mock plane's own variable.
	mockStreams := stream.NewWorkspaceRegistry(cfg.StreamMaxConns)
	mockPlane.SetStreams(mockStreams, mockplane.StreamOptions{
		Ping:          time.Duration(cfg.StreamPing) * time.Second,
		FrameTimeout:  time.Duration(cfg.StreamFrameTimeout) * time.Second,
		Lifetime:      time.Duration(cfg.StreamMaxLifetime) * time.Second,
		TrafficFrames: cfg.StreamTrafficFrames,
		// A14: "all"'s per-row budget, read nowhere else.
		TrafficMaxFrames: cfg.StreamTrafficMaxFrames,
		TrafficMaxBytes:  cfg.StreamTrafficMaxBytes,
		// P6d (decisions.md mocker-p6d-websocket D5): WebSocket's three,
		// converted here and read nowhere else.
		MaxFrame:   cfg.StreamMaxFrame,
		SendBudget: cfg.StreamSendBudget,
		Origins:    cfg.StreamOrigins,
	})
	adminSrv.SetStream(streamRegistry, mockStreams, admin.StreamOptions{
		Ping:           time.Duration(cfg.StreamPing) * time.Second,
		FrameTimeout:   time.Duration(cfg.StreamFrameTimeout) * time.Second,
		SessionRecheck: time.Duration(cfg.StreamSessionRecheck) * time.Second,
	})

	// P2f: adminSrv's POST .../preview route renders through the SAME
	// runtime-building code real traffic does, so it takes the SAME
	// *mockplane.Plane instance, not a second one built just for this
	// route (mockPlane already implements admin.Previewer's one method,
	// [mockplane.Plane.Preview] — see that method's own doc comment). The
	// near-miss risk is identical to every setter above: skip this line
	// and the route ships wired to nothing, answering 503 forever, while
	// every Go test (which wires it explicitly) keeps passing.
	adminSrv.SetPreviewer(mockPlane)

	// The SLICE P2b source: scenarios. Same near-miss risk as every setter
	// above — and a sharper one, because the feature is INVISIBLE without
	// it rather than broken: a workspace whose scenario_id is set serves
	// its own layer perfectly happily with a nil ScenarioSource, so a
	// forgotten line here ships a switch that returns 200, moves the
	// revision, and changes not one served byte. Every Go test in
	// internal/mockplane wires this explicitly; only main.go can forget to.
	//
	// The admin plane builds its own repositories inside admin.New (specs,
	// overrides, traffic, customep all do), so there is no second setter
	// here to pair with this one — unlike liveState and trafficRec above,
	// which are RAM/queue singletons that MUST be the same instance on both
	// planes. A scenarios repo holds no state of its own: it is a thin
	// reader/writer over *store.DB, and two of them see the same rows.
	mockPlane.SetScenarios(scenarios.NewRepo(db, overridesRepo))

	// MOCKER_MCP_KEY unset (config.Load's own default) means nothing in this
	// block runs at all and SetMCP is never called — the "no surface"
	// state the MCP slice's context document (§A2) requires: an operator
	// who never sets the key gets no /mcp route to attack, not a route
	// guarded by an empty lock. A non-empty key already cleared
	// config.Load's own 32-byte floor, so there is nothing left to
	// validate here.
	//
	// mcp.New takes adminSrv itself as its Caller: admin.Server.CallAsMCP
	// is the ONLY way the MCP tools reach the domain (§A6), so — unlike
	// liveState/trafficRec/customRepo above — this needs no repository of
	// its own wired in from here.
	//
	// SetMCP sits here, right after SetLiveState/SetTraffic and before
	// adminSrv.Handler() is ever called (inside server.New below): the
	// same near-miss risk as those two setters, wired at the same point
	// for the same reason. Order relative to Handler() does not actually
	// matter — internal/admin/server.go's own Handler doc comment is
	// explicit that s.mcp is read PER REQUEST, never captured at build
	// time — but this is where a reader already looks for "did main wire
	// this dependency", so it is where this one lives too.
	//
	// Nothing here needs a Close/Shutdown call on either exit path below
	// (contrast liveState.Close(), called on both): the Endpoint the SDK
	// builds is stateless by construction (StreamableHTTPOptions.Stateless
	// — no session map, no per-request goroutine of its own) and starts
	// none of its own background work, so there is nothing for either exit
	// path to release.
	if cfg.MCPKey != "" {
		mcpEndpoint := mcp.New(adminSrv, cfg.MCPKey, cfg, log)
		adminSrv.SetMCP(mcpEndpoint.Handler())
		// Never the key, never its length — this line is an operator's
		// ONLY way to know the key was picked up at all, and printing
		// either would defeat the point of having a secret.
		log.Info("mcp endpoint mounted", "path", "/mcp")
	}

	dispatcher := server.New(cfg, adminSrv.Handler(), mockPlane, log)
	// Recover, RequestLog and MaxBody wrap the WHOLE dispatcher exactly once
	// here — both planes build their own handler with only what is specific
	// to them (see internal/admin.Server.Handler's doc comment), because
	// adding these again per-plane would double every log line and enforce
	// the body limit twice for no benefit.
	handler := httpx.Chain(dispatcher, httpx.Recover(log), httpx.RequestLog(log), httpx.MaxBody(cfg.MaxBody))

	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// ReadHeaderTimeout bounds a slow-headers client (accidental or not)
		// from parking a connection indefinitely before the request even
		// starts.
		ReadHeaderTimeout: 5 * time.Second,
		// IdleTimeout reclaims a keep-alive connection nobody is using.
		IdleTimeout: 120 * time.Second,
		// WriteTimeout bounds how long writing a response may take; P0's
		// responses are all small JSON, so this only guards against a stuck
		// client on the receiving end. It is GLOBAL over both planes and
		// stays exactly this: P6a's SSE handler exempts ITSELF per
		// connection through http.ResponseController.SetWriteDeadline
		// (internal/stream, DESIGN §30.6) rather than this value being
		// raised for everyone.
		WriteTimeout: 30 * time.Second,
		// MaxHeaderBytes: Go's default is 1 MiB, and the mock plane records
		// every request's headers AND path into a traffic row without a
		// cap of their own (only bodies are cut at MOCKER_TRAFFIC_MAX_BODY)
		// — an anonymous client could put ~2 MiB into every row of a
		// 1000-row retention. 64 KiB is far above any real cookie or
		// bearer header and is what the recorder's own cap below assumes.
		MaxHeaderBytes: 64 << 10,
	}

	janitorDone := make(chan struct{})
	go func() {
		defer close(janitorDone)
		runJanitor(ctx, sessions, liveState, log)
	}()

	// The recorder gets its OWN cancellation, deliberately not ctx: ctx is
	// Done at the START of shutdown (the signal itself), while
	// httpServer.Shutdown below spends up to shutdownDrain still answering
	// in-flight requests. A recorder bound to ctx would stop (and run its
	// final flush) before those responses are recorded — the "last few
	// requests never appear in traffic" bug. recorderCancel is called
	// explicitly on both of this function's exit paths below: after
	// httpServer.Shutdown returns on the signal path, and immediately on the
	// listener-error path, where Shutdown is never called at all. Without
	// the second call, a listener error would hang <-recorderDone forever,
	// waiting for a goroutine nothing had told to stop.
	recorderCtx, recorderCancel := context.WithCancel(context.Background())
	recorderDone := make(chan struct{})
	go func() {
		defer close(recorderDone)
		trafficRec.Run(recorderCtx)
	}()

	serveErr := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	log.Info("mocker starting",
		"addr", cfg.Addr,
		"admin_host", cfg.AdminHost,
		"workspace_host_pattern", workspaceHostPattern(cfg),
		"routing", string(cfg.Routing),
	)

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received, draining", "timeout", shutdownDrain)
		// BEFORE the drain, not after it: a request parked on a live-state
		// pause directive (§14's "pause") is holding its connection open
		// waiting for an operator who is never going to come now, and
		// httpServer.Shutdown below is waiting for exactly those
		// connections to go idle. Close releases every parked request and
		// stops Apply handing out new pauses, so the drain has something
		// finite to wait for. Skip it and shutdown takes the full
		// livestate.MaxPauseHold per parked request instead — under the
		// 15s drain, so the process still exits and no bar ever says so.
		liveState.Close()
		// AFTER the live state and BEFORE the drain, on both exit paths
		// (P6a D13, DESIGN §30.7): Shutdown waits for every live SSE
		// connection for the whole drain window, because to it a stream
		// is just a response that has not finished. Close sets the
		// registry's closed flag (a handshake racing shutdown is refused,
		// not registered), cancels every connection, and WAITS for their
		// handlers to return — so the drain below has only ordinary
		// requests left to wait for. Live state first because a handler
		// parked on a pause must be released before its connection is
		// cancelled; recorder last (below) because a mock request answered
		// during the drain leaves an event in the recorder's queue that a
		// cancel before the drain ends would discard.
		streamRegistry.Close()
		mockStreams.Close() // the mock plane's streams, same step, same reason
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownDrain)
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			runErr = fmt.Errorf("graceful shutdown: %w", err)
		}
		cancel()
		<-serveErr // ListenAndServe always returns once Shutdown completes
		// Only now, with the drain finished and every in-flight request
		// answered, is it safe to let the recorder stop taking new events
		// and run its final flush.
		recorderCancel()
	case err := <-serveErr:
		// The server stopped on its own (e.g. the listener failed) with no
		// signal received, so ctx is not Done yet. stop() cancels it
		// explicitly — it is the same context runJanitor waits on, and
		// leaving it live here would make the <-janitorDone below block
		// forever instead of letting this process exit on its own error.
		stop()
		// The SAME order as the signal path — live state, registry,
		// recorder — brought into line by P6a (D13): before it this path
		// cancelled the recorder FIRST and released the live state after,
		// so the recorder died before parked pause handlers were released,
		// the reverse of the other path. Same reason liveState.Close() is
		// on the other path, and the same trap the recorder's cancel
		// documents: this path never calls Shutdown, so nothing else here
		// would ever release a request parked on a pause. It would hold
		// its connection (and its goroutine) until livestate.MaxPauseHold
		// expired, past the error this function is trying to return.
		liveState.Close()
		// The registries' own Close joins their connection goroutines; there
		// is no drain on this path, so this is the only thing that does.
		streamRegistry.Close()
		mockStreams.Close()
		// No Shutdown was called and nothing is draining, so there is no
		// reason to keep the recorder waiting either — cancel it here too,
		// or <-recorderDone below blocks forever on this path.
		recorderCancel()
		runErr = err
	}

	<-janitorDone  // ctx is now Done on both paths above, so this returns promptly
	<-recorderDone // recorderCancel was called on both paths above too
	return runErr
}

// workspaceHostPattern describes, for the startup log line, how a request
// gets attributed to a workspace under the configured routing mode.
func workspaceHostPattern(cfg *config.Config) string {
	if cfg.Routing == config.RoutingPath {
		return "/w/{slug}/... (MOCKER_ROUTING=path)"
	}
	return "<slug>." + cfg.BaseDomain
}

// runJanitor purges expired sessions and sweeps abandoned live-state
// directives on janitorInterval until ctx is done. It runs on its own
// goroutine and never returns an error to main: a failed purge just means
// expired rows (or stale directives) linger a bit longer, not a reason to
// bring the server down.
func runJanitor(ctx context.Context, sessions *auth.Manager, liveState *livestate.Store, log *slog.Logger) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := sessions.PurgeExpired(ctx)
			if err != nil {
				log.Error("purge expired sessions", "err", err)
			} else if n > 0 {
				log.Info("purged expired sessions", "count", n)
			}

			// livestate.Store is pure RAM (see its own doc comment: it must
			// never reach SQLite), so an abandoned workspace's directives
			// would otherwise live for the lifetime of the process instead
			// of just DefaultTTL past their last Set.
			if dropped := liveState.Sweep(time.Now()); dropped > 0 {
				log.Info("swept expired live-state directives", "count", dropped)
			}
		}
	}
}

// logLevel maps a config.Config.LogLevel string, already validated by
// [config.Load], to a slog.Level.
func logLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
