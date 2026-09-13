package main

import (
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettingsRoundTrip(t *testing.T) {
	s := testStore(t)

	if got := s.Get("nothing", "fallback"); got != "fallback" {
		t.Errorf("missing key: got %q, want the default", got)
	}
	if err := s.Set("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("a", "2"); err != nil {
		t.Fatalf("overwriting a setting must work: %v", err)
	}
	if got := s.Get("a", ""); got != "2" {
		t.Errorf("got %q, want 2", got)
	}

	s.SetInt("n", 42)
	if got := s.GetInt("n", 0); got != 42 {
		t.Errorf("int round trip: got %d", got)
	}
	s.Set("n", "not a number")
	if got := s.GetInt("n", 7); got != 7 {
		t.Errorf("unparsable int should fall back: got %d", got)
	}

	s.SetBool("b", true)
	if !s.GetBool("b", false) {
		t.Error("bool round trip failed")
	}
	s.Set("b", "maybe")
	if !s.GetBool("b", true) || s.GetBool("b", false) {
		t.Error("an unrecognised bool should fall back to the default")
	}
}

func TestUsersAndSessions(t *testing.T) {
	s := testStore(t)

	if s.CountUsers() != 0 {
		t.Fatal("a fresh store should have no users")
	}
	id, err := s.CreateUser("Ada@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("ada@example.com", "hash", "admin"); err == nil {
		t.Error("email uniqueness must be case-insensitive")
	}

	u, err := s.UserByEmail("ADA@example.com")
	if err != nil {
		t.Fatalf("lookup should ignore case: %v", err)
	}
	if u.ID != id {
		t.Errorf("got user %d, want %d", u.ID, id)
	}
	if _, err := s.UserByEmail("nobody@example.com"); err != ErrNoUser {
		t.Errorf("got %v, want ErrNoUser", err)
	}

	if err := s.CreateSession("tok", id, time.Hour, "10.0.0.1", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser("tok"); err != nil {
		t.Fatalf("live session should resolve: %v", err)
	}

	// An expired session must not resolve, and must clean itself up.
	if err := s.CreateSession("stale", id, -time.Hour, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser("stale"); err != ErrNoUser {
		t.Errorf("expired session resolved: %v", err)
	}
	if _, err := s.SessionUser("stale"); err != ErrNoUser {
		t.Error("expired session should stay gone")
	}
}

func TestUsedSecondsCountsOpenSessions(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	window := now.Add(-24 * time.Hour)

	// A closed session inside the window.
	id1, _ := s.StartKernelSession("run", now.Add(-3*time.Hour))
	s.FinishKernelSession(id1, now.Add(-2*time.Hour), "idle", "")

	// A session that is still running: it must count up to now, or the
	// budget would only move in jumps when a kernel stops.
	s.StartKernelSession("run", now.Add(-30*time.Minute))

	got := s.UsedSeconds(window, now)
	want := int((time.Hour + 30*time.Minute).Seconds())
	if got != want {
		t.Errorf("used %ds, want %ds", got, want)
	}
}

func TestUsedSecondsClipsToTheWindow(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	window := now.Add(-1 * time.Hour)

	// Started well before the window opened: only the part inside counts.
	s.StartKernelSession("run", now.Add(-5*time.Hour))

	got := s.UsedSeconds(window, now)
	if want := int(time.Hour.Seconds()); got != want {
		t.Errorf("used %ds, want %ds — a session older than the window must be clipped", got, want)
	}
}

func TestFinishKernelSessionIsIdempotent(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	id, _ := s.StartKernelSession("run", now.Add(-time.Hour))

	s.FinishKernelSession(id, now, "idle", "")
	s.FinishKernelSession(id, now.Add(time.Hour), "again", "")

	open, err := s.OpenKernelSession()
	if err != nil {
		t.Fatal(err)
	}
	if open != nil {
		t.Fatal("the session should be closed")
	}
	// The second call must not extend the billed time.
	if got := s.UsedSeconds(now.Add(-2*time.Hour), now.Add(2*time.Hour)); got != 3600 {
		t.Errorf("billed %ds, want 3600 — closing twice must not double-count", got)
	}
}

func TestOpenKernelSession(t *testing.T) {
	s := testStore(t)
	if open, _ := s.OpenKernelSession(); open != nil {
		t.Fatal("nothing should be open in a fresh store")
	}
	id, _ := s.StartKernelSession("slug", time.Now())
	open, err := s.OpenKernelSession()
	if err != nil || open == nil {
		t.Fatalf("expected the open session back: %v", err)
	}
	if open.ID != id || open.Slug != "slug" {
		t.Errorf("got %+v", open)
	}
}

func TestRunsLifecycle(t *testing.T) {
	s := testStore(t)

	img, err := s.CreateRun("image", "a lighthouse", `{"steps":28}`, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	voice, _ := s.CreateRun("voice", "hello", "{}", 1, 0)

	s.FinishRun(img, "1-abc.png", "image/png", 2048)
	s.FailRun(voice, "the workflow blew up")

	got, err := s.Run(img)
	if err != nil || got == nil {
		t.Fatalf("run lookup: %v", err)
	}
	if got.Status != "done" || got.File != "1-abc.png" || got.Bytes != 2048 {
		t.Errorf("unexpected finished run: %+v", got)
	}
	if got.Finished == nil {
		t.Error("a finished run needs a finish time")
	}

	failed, _ := s.Run(voice)
	if failed.Status != "failed" || failed.Error == "" {
		t.Errorf("unexpected failed run: %+v", failed)
	}

	only, err := s.Runs("image", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Kind != "image" {
		t.Errorf("kind filter returned %d rows", len(only))
	}
	if all, _ := s.Runs("", 10); len(all) != 2 {
		t.Errorf("unfiltered returned %d rows, want 2", len(all))
	}
	if missing, err := s.Run(99999); err != nil || missing != nil {
		t.Errorf("a missing run should be (nil, nil), got (%v, %v)", missing, err)
	}
}

func TestAbandonRuns(t *testing.T) {
	s := testStore(t)
	id, _ := s.CreateRun("image", "x", "{}", 1, 0)
	s.AbandonRuns()

	run, _ := s.Run(id)
	if run.Status != "failed" {
		t.Errorf("a run in flight at restart should be failed, got %q", run.Status)
	}
	if run.Error == "" {
		t.Error("the failure should say why")
	}
}
