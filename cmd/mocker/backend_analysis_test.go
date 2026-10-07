package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func analysisApp(t *testing.T) *app {
	t.Helper()
	a := &app{cfg: appTestConfig(t), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.openStore(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	return a
}
func seedAnalysis(t *testing.T, a *app) (string, string) {
	t.Helper()
	graphs := backendmodel.NewRepo(a.db)
	p, err := graphs.Create(t.Context(), backendmodel.CreateInput{Name: "Startup analysis", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	service := backendanalysis.NewService(backendanalysis.NewRepo(a.db), graphs, backendanalysis.NewEngine(graphs, nil))
	job, err := service.Start(t.Context(), p.ID, backendanalysis.StartInput{Kind: "impact", FromRevisionID: p.CurrentRevisionID, Target: backendanalysis.AnalysisTarget{RevisionID: p.CurrentRevisionID}, ObservationMode: "none", IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID, job.ID
}
func TestAnalysisStartupInterruptsBeforeTraffic(t *testing.T) {
	a := analysisApp(t)
	pid, id := seedAnalysis(t, a)
	if err := a.buildPlanes(t.Context()); err != nil {
		t.Fatal(err)
	}
	job, err := a.analysisRepo.Get(t.Context(), pid, id)
	if err != nil || job.Status != "interrupted" || job.ResultVersion == nil {
		t.Fatalf("startup recovery %v %v", job, err)
	}
	if a.analysisService.Running() {
		t.Fatal("workers started during construction")
	}
	a.adminSrv.SetBackendAnalysis(nil, nil)
	if !strings.Contains(strings.Join(a.adminSrv.Ready(), ","), "SetBackendAnalysis") {
		t.Fatal("missing analysis hidden")
	}
}
func TestAnalysisStartupRecoveryFailureRefusesConstruction(t *testing.T) {
	a := analysisApp(t)
	seedAnalysis(t, a)
	if _, err := a.db.W.ExecContext(t.Context(), `CREATE TRIGGER analysis_startup_fail BEFORE INSERT ON backend_analysis_manifests BEGIN SELECT RAISE(ABORT,'recovery failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.buildPlanes(t.Context()); err == nil {
		t.Fatal("production ignored recovery failure")
	}
}
func TestAnalysisApplicationShutdownJoinsWorkers(t *testing.T) {
	for _, mode := range []string{"listener_error", "normal"} {
		t.Run(mode, func(t *testing.T) {
			a := analysisApp(t)
			if err := a.buildPlanes(t.Context()); err != nil {
				t.Fatal(err)
			}
			a.wireMockPlane()
			a.wireStreaming()
			if mode == "listener_error" {
				a.cfg.Addr = "invalid-listen-address"
			} else {
				a.cfg.Addr = "127.0.0.1:0"
			}
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			done := make(chan error, 1)
			go func() { done <- a.startAndDrain(ctx, stop) }()
			if mode == "normal" {
				if err := a.analysisService.WaitRunning(t.Context()); err != nil {
					t.Fatal(err)
				}
				// "normal" means a shutdown after startup finished. Since
				// be06f56 (B5.3) startAndDrain starts the replay workers
				// AFTER the analysis ones; stopping on analysis alone landed
				// in replay's WaitRunning, so startAndDrain returned a
				// startup cancellation ("context canceled" joined with the
				// replay Run that lost the race to Close) — a startup abort,
				// not the drain this mode exists to prove.
				if err := a.replayService.WaitRunning(t.Context()); err != nil {
					t.Fatal(err)
				}
				stop()
			}
			select {
			case err := <-done:
				if mode == "listener_error" && err == nil {
					t.Fatal("listener failure lost")
				}
				if mode == "normal" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("app did not join workers")
			}
			if a.analysisService.Running() {
				t.Fatal("workers survived application drain")
			}
			if err := a.db.R.PingContext(t.Context()); err != nil {
				t.Fatal("DB closed before caller owns shutdown", err)
			}
		})
	}
}

func TestAnalysisProductionEngineCompletesJob(t *testing.T) {
	a := analysisApp(t)
	if err := a.buildPlanes(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.wireMockPlane()
	a.wireStreaming()
	a.cfg.Addr = "127.0.0.1:0"
	graphs := backendmodel.NewRepo(a.db)
	p, err := graphs.Create(t.Context(), backendmodel.CreateInput{Name: "Production engine", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.analysisService.Start(t.Context(), p.ID, backendanalysis.StartInput{Kind: "impact", FromRevisionID: p.CurrentRevisionID, Target: backendanalysis.AnalysisTarget{RevisionID: p.CurrentRevisionID}, ObservationMode: "none", IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- a.startAndDrain(ctx, stop) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		job, err = a.analysisRepo.Get(t.Context(), p.ID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" {
			break
		}
		if job.Status == "failed" {
			t.Fatalf("production engine failed: %+v", job.Diagnostic)
		}
		select {
		case err := <-done:
			t.Fatalf("app stopped before completion (job=%+v): %v", job, err)
		case <-deadline.C:
			t.Fatalf("production job did not complete: %+v", job)
		case <-time.After(10 * time.Millisecond):
		}
	}
	page, err := a.analysisRepo.Results(t.Context(), p.ID, job.ID, backendanalysis.ResultQuery{ResultVersion: *job.ResultVersion, Section: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Manifest.AnalysisInputHash != job.AnalysisInputHash || page.Manifest.RuntimeVerified || page.Manifest.Verdict != "unknown" {
		t.Fatalf("empty source coverage became verified: %+v", page.Manifest)
	}
	stop()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
