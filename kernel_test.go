package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeKernel stands in for the runner inside a Kaggle kernel: the edge that
// answers /gpuless/* and proxies ComfyUI.
type fakeKernel struct {
	srv      *httptest.Server
	mu       sync.Mutex
	up       bool
	gpu      string
	idle     int
	shutdown int
	token    string
}

func newFakeKernel(t *testing.T) *fakeKernel {
	t.Helper()
	f := &fakeKernel{up: true, gpu: "Tesla T4, Tesla T4", token: "shared"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/gpuless/health":
			if !f.up {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(health{OK: true, GPU: f.gpu, Idle: f.idle, IdleLimit: 300})
		case "/gpuless/shutdown":
			f.shutdown++
			f.up = false
			json.NewEncoder(w).Encode(map[string]bool{"stopping": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeKernel) setUp(v bool) { f.mu.Lock(); f.up = v; f.mu.Unlock() }

// fakeKaggle counts pushes and always accepts.
func fakeKaggle(t *testing.T, pushes *atomic.Int32) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/kernels/push" {
			pushes.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"url": "https://kaggle.com/x"})
	}))
	old := kaggleBaseURL
	kaggleBaseURL = srv.URL
	t.Cleanup(func() { kaggleBaseURL = old; srv.Close() })
}

func testKernel(t *testing.T, edge *fakeKernel) (*Kernel, *Store, Config) {
	t.Helper()
	bootPoll, stopPoll = 5*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { bootPoll, stopPoll = 4*time.Second, 3*time.Second })

	store := testStore(t)
	k := NewKernel(store, slog.New(slog.DiscardHandler))
	cfg := Config{
		KaggleUser: "ada", KaggleKey: "key",
		TunnelHost: edge.srv.URL, TunnelToken: "cf", KernelToken: edge.token,
		RuntimeDataset: "ada/rt", KernelSlug: "runner",
		IdleStop: 5 * time.Minute, SessionCap: 9 * time.Hour,
		WeeklyQuota: 30 * time.Hour, ResetWeekday: time.Saturday,
		WarnPct: 80, BlockPct: 98,
	}
	return k, store, cfg
}

func TestEnsureReadyStartsTheKernel(t *testing.T) {
	edge := newFakeKernel(t)
	var pushes atomic.Int32
	fakeKaggle(t, &pushes)
	k, store, cfg := testKernel(t, edge)

	if err := k.EnsureReady(context.Background(), cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	if k.State() != StateReady {
		t.Errorf("state is %s, want ready", k.State())
	}
	if pushes.Load() != 1 {
		t.Errorf("pushed %d times, want 1", pushes.Load())
	}
	if info := k.Info(cfg); info.GPU != "Tesla T4, Tesla T4" {
		t.Errorf("the GPU the kernel reported should be shown, got %q", info.GPU)
	}
	open, _ := store.OpenKernelSession()
	if open == nil {
		t.Error("starting should open a billing session")
	}

	// A second call must not push again.
	if err := k.EnsureReady(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if pushes.Load() != 1 {
		t.Errorf("a warm kernel was restarted: %d pushes", pushes.Load())
	}
}

func TestEnsureReadyIsSharedBetweenCallers(t *testing.T) {
	edge := newFakeKernel(t)
	var pushes atomic.Int32
	fakeKaggle(t, &pushes)
	k, _, cfg := testKernel(t, edge)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = k.EnsureReady(context.Background(), cfg) }(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if got := pushes.Load(); got != 1 {
		t.Errorf("eight concurrent callers caused %d pushes, want 1", got)
	}
}

func TestEnsureReadyRefusesWhenUnconfigured(t *testing.T) {
	edge := newFakeKernel(t)
	k, _, cfg := testKernel(t, edge)
	cfg.RuntimeDataset = ""

	if err := k.EnsureReady(context.Background(), cfg); err != ErrNotConfigured {
		t.Errorf("got %v, want ErrNotConfigured", err)
	}
}

func TestEnsureReadyRefusesWhenTheBudgetIsSpent(t *testing.T) {
	edge := newFakeKernel(t)
	var pushes atomic.Int32
	fakeKaggle(t, &pushes)
	k, store, cfg := testKernel(t, edge)

	// Burn the whole weekly allowance inside the current window.
	start := cfg.QuotaWindowStart(time.Now().UTC())
	id, _ := store.StartKernelSession("old", start.Add(time.Minute))
	store.FinishKernelSession(id, start.Add(time.Minute+30*time.Hour), "test", "")

	if err := k.EnsureReady(context.Background(), cfg); err != ErrQuotaBlocked {
		t.Errorf("got %v, want ErrQuotaBlocked", err)
	}
	if pushes.Load() != 0 {
		t.Error("a blocked start must not reach Kaggle")
	}
	if info := k.Info(cfg); !info.Blocked {
		t.Error("the snapshot should say the budget is blocked")
	}
}

func TestStopClosesTheSession(t *testing.T) {
	edge := newFakeKernel(t)
	var pushes atomic.Int32
	fakeKaggle(t, &pushes)
	k, store, cfg := testKernel(t, edge)

	if err := k.EnsureReady(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := k.Stop(context.Background(), cfg, "test"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if k.State() != StateStopped {
		t.Errorf("state is %s, want stopped", k.State())
	}
	edge.mu.Lock()
	calls := edge.shutdown
	edge.mu.Unlock()
	if calls != 1 {
		t.Errorf("the kernel was asked to stop %d times, want 1", calls)
	}
	if open, _ := store.OpenKernelSession(); open != nil {
		t.Error("stopping must close the billing session")
	}
}

func TestStopRefusesWhenNotRunning(t *testing.T) {
	edge := newFakeKernel(t)
	k, _, cfg := testKernel(t, edge)
	if err := k.Stop(context.Background(), cfg, "test"); err == nil {
		t.Error("stopping a stopped kernel should be an error, not a no-op that bills")
	}
}

func TestAdoptReconnectsToARunningKernel(t *testing.T) {
	edge := newFakeKernel(t)
	k, store, cfg := testKernel(t, edge)

	started := time.Now().Add(-20 * time.Minute)
	id, _ := store.StartKernelSession("runner", started)

	k.Adopt(context.Background(), cfg)

	if k.State() != StateReady {
		t.Fatalf("state is %s, want ready — a surviving kernel must be picked back up", k.State())
	}
	open, _ := store.OpenKernelSession()
	if open == nil || open.ID != id {
		t.Error("the original session should stay open so its time keeps counting")
	}
	if info := k.Info(cfg); info.Since == nil || !info.Since.Equal(started.Truncate(time.Second)) {
		t.Errorf("the adopted session should keep its original start time, got %v", info.Since)
	}
}

func TestAdoptClosesALostSession(t *testing.T) {
	edge := newFakeKernel(t)
	edge.setUp(false) // the kernel died while the panel was down
	k, store, cfg := testKernel(t, edge)
	store.StartKernelSession("runner", time.Now().Add(-time.Hour))

	k.Adopt(context.Background(), cfg)

	if k.State() != StateStopped {
		t.Errorf("state is %s, want stopped", k.State())
	}
	if open, _ := store.OpenKernelSession(); open != nil {
		t.Error("a session whose kernel is gone must be closed, or it bills for ever")
	}
}

func TestInfoCountsDownToTheIdleStop(t *testing.T) {
	edge := newFakeKernel(t)
	var pushes atomic.Int32
	fakeKaggle(t, &pushes)
	k, _, cfg := testKernel(t, edge)
	if err := k.EnsureReady(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	// Pretend the last request was four minutes ago.
	k.mu.Lock()
	k.activity = time.Now().Add(-4 * time.Minute)
	k.mu.Unlock()

	info := k.Info(cfg)
	if info.IdleFor < 235 || info.IdleFor > 245 {
		t.Errorf("idle for %ds, want about 240", info.IdleFor)
	}
	if info.StopsIn < 55 || info.StopsIn > 65 {
		t.Errorf("stops in %ds, want about 60", info.StopsIn)
	}

	// Keep-alive turns the countdown off entirely.
	cfg.KeepAlive = true
	if got := k.Info(cfg).StopsIn; got != 0 {
		t.Errorf("keep-alive should not count down, got %ds", got)
	}
}

func TestIdleSecondsForHonoursKeepAlive(t *testing.T) {
	cfg := Config{IdleStop: 5 * time.Minute}
	if got := idleSecondsFor(cfg); got != 300 {
		t.Errorf("got %d, want 300", got)
	}
	cfg.KeepAlive = true
	if got := idleSecondsFor(cfg); got != 0 {
		t.Errorf("keep-alive must disable the kernel's own idle timer, got %d", got)
	}
}

func TestModelMountsSkipEmptyDatasets(t *testing.T) {
	got := modelMounts(Config{SDXLDataset: "o/sdxl"})
	if len(got) != 1 || got[0].Folder != "checkpoints" {
		t.Fatalf("got %+v", got)
	}
	if len(modelMounts(Config{})) != 0 {
		t.Error("no datasets should mean no mounts")
	}
}

func TestAppendUnique(t *testing.T) {
	got := appendUnique(appendUnique(appendUnique(nil, "a"), "a"), "")
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("got %v, want [a]", got)
	}
}
