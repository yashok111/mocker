package backendreplay

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

// review 2026-10-06, F123/F9: the run list answered 409 for good once a
// project had more than 100 runs (runs are never deleted). It now pages
// newest first behind an optional cursor (the last run id), and a request
// without one still returns a plain array: the newest page.
func TestReplayRunsPageInsteadOfFailingPastTheCap(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	runPageSize = 3
	t.Cleanup(func() { runPageSize = 100 })
	first, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	var raw string
	base := time.Now().UTC().Add(time.Hour)
	err = s.repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		for i := range 7 {
			id := newReplayID()
			// Two runs share each timestamp, so the id tie-break is paged too.
			at := base.Add(time.Duration(i/2) * time.Second).Format(time.RFC3339Nano)
			if _, e := tx.ExecContext(t.Context(), `INSERT INTO backend_replay_runs(id,project_id,target_id,status,input_hash,input_json,version,author,created_at,updated_at,terminal_report_json) SELECT ?,project_id,target_id,'cancelled',input_hash,input_json,version,author,?,?,terminal_report_json FROM backend_replay_runs WHERE id=?`, id, at, at, first.ID); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.repo.db.R.QueryRowContext(t.Context(), `SELECT json_group_array(id) FROM (SELECT id FROM backend_replay_runs WHERE project_id=? ORDER BY created_at DESC,id)`, pid).Scan(&raw)
	if err != nil || json.Unmarshal([]byte(raw), &want) != nil {
		t.Fatal(raw, err)
	}
	got := []string{}
	cursor := ""
	for range 10 {
		page, err := s.Runs(t.Context(), pid, cursor)
		if err != nil {
			t.Fatalf("page after %q: %v", cursor, err)
		}
		for _, r := range page {
			got = append(got, r.ID)
		}
		if len(page) < runPageSize {
			break
		}
		cursor = page[len(page)-1].ID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged runs %v, want %v", got, want)
	}
	if _, err = s.Runs(t.Context(), pid, newReplayID()); err == nil {
		t.Fatal("unknown cursor accepted")
	}
}

// review 2026-10-06, F124: profiles and packages decoded up to 1001 full
// documents per request and answered 409 for good past 1000. They page now,
// oldest first, behind an "<id>:<version>" cursor.
func TestReplayProfilesAndPackagesPage(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	profilePageSize, packagePageSize = 2, 2
	t.Cleanup(func() { profilePageSize, packagePageSize = 100, 25 })
	saved, err := s.GetPackage(t.Context(), pid, in.Package.ID, in.Package.Version)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.GetProfile(t.Context(), pid, in.Profile.ID, in.Profile.Version)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, err = s.Connect(t.Context(), pid, "actor", ConnectInput{ConfiguredTargetID: "test", ExpectedIdentityHash: profile.IdentityHash, AllowReset: true, IdempotencyKey: newReplayID()}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SavePackage(t.Context(), pid, "actor", SavePackageInput{ID: newReplayID(), Package: saved.Package, Provenance: saved.Provenance, IdempotencyKey: newReplayID()}); err != nil {
			t.Fatal(err)
		}
	}
	var profiles []string
	cursor := ""
	for range 10 {
		page, err := s.Profiles(t.Context(), pid, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page {
			profiles = append(profiles, fmt.Sprintf("%s:%d", v.Pin.ID, v.Pin.Version))
		}
		if len(page) < profilePageSize {
			break
		}
		cursor = profiles[len(profiles)-1]
	}
	var packages []string
	cursor = ""
	for range 10 {
		page, err := s.Packages(t.Context(), pid, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page {
			packages = append(packages, fmt.Sprintf("%s:%d", v.Pin.ID, v.Pin.Version))
		}
		if len(page) < packagePageSize {
			break
		}
		cursor = packages[len(packages)-1]
	}
	if len(profiles) != 5 || len(packages) != 5 || len(slices.Compact(slices.Sorted(slices.Values(profiles)))) != 5 || len(slices.Compact(slices.Sorted(slices.Values(packages)))) != 5 {
		t.Fatalf("profiles %v packages %v", profiles, packages)
	}
	for _, bad := range []string{"x", in.Profile.ID, in.Profile.ID + ":0", newReplayID() + ":1"} {
		if _, err = s.Profiles(t.Context(), pid, bad); err == nil {
			t.Fatalf("cursor %q accepted", bad)
		}
	}
}
