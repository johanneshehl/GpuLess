package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type KernelState string

const (
	StateStopped  KernelState = "stopped"
	StateStarting KernelState = "starting"
	StateReady    KernelState = "ready"
	StateStopping KernelState = "stopping"
	StateFailed   KernelState = "failed"
)

var (
	ErrNotConfigured = errors.New("gpuless is not configured yet")
	ErrQuotaBlocked  = errors.New("the weekly GPU budget is used up")
)

// bootTimeout is how long a cold start may take before we call it failed.
// Mounting a few gigabytes and waking cloudflared is usually well under a
// minute; the generous ceiling is for a busy Kaggle queue.
const bootTimeout = 10 * time.Minute

// Poll intervals are variables so tests do not have to wait out real seconds.
var (
	bootPoll = 4 * time.Second
	stopPoll = 3 * time.Second
)

// health is what the kernel's edge reports about itself.
type health struct {
	OK        bool   `json:"ok"`
	GPU       string `json:"gpu"`
	Wanted    string `json:"wanted"`
	Uptime    int    `json:"uptime"`
	Idle      int    `json:"idle"`
	IdleLimit int    `json:"idle_limit"`
	Cap       int    `json:"cap"`
}

// Kernel owns the remote session's lifecycle. Everything that starts, stops
// or observes the Kaggle kernel goes through here.
type Kernel struct {
	store *Store
	log   *slog.Logger
	http  *http.Client
	now   func() time.Time

	mu        sync.Mutex
	state     KernelState
	sessionID int64
	since     time.Time
	activity  time.Time
	gpu       string
	failure   string
	inflight  chan struct{} // non-nil while a start is running; closed when it ends
}

func NewKernel(store *Store, log *slog.Logger) *Kernel {
	return &Kernel{
		store: store,
		log:   log,
		http:  &http.Client{Timeout: 30 * time.Second},
		now:   time.Now,
		state: StateStopped,
	}
}

// KernelInfo is the snapshot the UI renders.
type KernelInfo struct {
	State     KernelState `json:"state"`
	GPU       string      `json:"gpu,omitempty"`
	Since     *time.Time  `json:"since,omitempty"`
	IdleFor   int         `json:"idle_for"`
	StopsIn   int         `json:"stops_in"`
	Failure   string      `json:"failure,omitempty"`
	UsedHours float64     `json:"used_hours"`
	QuotaHrs  float64     `json:"quota_hours"`
	ResetsAt  time.Time   `json:"resets_at"`
	Warning   bool        `json:"warning"`
	Blocked   bool        `json:"blocked"`
}

func (k *Kernel) Info(cfg Config) KernelInfo {
	k.mu.Lock()
	info := KernelInfo{State: k.state, GPU: k.gpu, Failure: k.failure}
	if !k.since.IsZero() {
		t := k.since
		info.Since = &t
	}
	if k.state == StateReady && !k.activity.IsZero() {
		idle := k.now().Sub(k.activity)
		info.IdleFor = int(idle.Seconds())
		if cfg.IdleStop > 0 && !cfg.KeepAlive {
			if left := int((cfg.IdleStop - idle).Seconds()); left > 0 {
				info.StopsIn = left
			}
		}
	}
	k.mu.Unlock()

	used, quota, reset := k.quota(cfg)
	info.UsedHours = used.Hours()
	info.QuotaHrs = quota.Hours()
	info.ResetsAt = reset
	info.Warning = pctOf(used, quota) >= float64(cfg.WarnPct)
	info.Blocked = pctOf(used, quota) >= float64(cfg.BlockPct)
	return info
}

func pctOf(used, quota time.Duration) float64 {
	if quota <= 0 {
		return 0
	}
	return used.Seconds() / quota.Seconds() * 100
}

func (k *Kernel) quota(cfg Config) (used, quota time.Duration, reset time.Time) {
	now := k.now().UTC()
	start := cfg.QuotaWindowStart(now)
	used = time.Duration(k.store.UsedSeconds(start, now)) * time.Second
	return used, cfg.WeeklyQuota, start.AddDate(0, 0, 7)
}

// Touch records that someone is using the kernel right now.
func (k *Kernel) Touch() {
	k.mu.Lock()
	k.activity = k.now()
	k.mu.Unlock()
}

func (k *Kernel) State() KernelState {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.state
}

// EnsureReady returns once the kernel can serve a request, starting it if
// necessary. Concurrent callers share one start rather than pushing the
// notebook several times.
func (k *Kernel) EnsureReady(ctx context.Context, cfg Config) error {
	for {
		k.mu.Lock()
		switch k.state {
		case StateReady:
			k.activity = k.now()
			k.mu.Unlock()
			return nil
		case StateStarting:
			wait := k.inflight
			k.mu.Unlock()
			select {
			case <-wait:
				continue // re-read the state: ready, or failed with a reason
			case <-ctx.Done():
				return ctx.Err()
			}
		case StateStopping:
			k.mu.Unlock()
			select {
			case <-time.After(time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Stopped or failed: this caller performs the start.
		if !cfg.Configured() {
			k.mu.Unlock()
			return ErrNotConfigured
		}
		used, quota, _ := k.quota(cfg)
		if pctOf(used, quota) >= float64(cfg.BlockPct) {
			k.mu.Unlock()
			return ErrQuotaBlocked
		}
		done := make(chan struct{})
		k.state, k.inflight, k.failure = StateStarting, done, ""
		k.since = k.now()
		k.activity = k.now()
		k.mu.Unlock()

		err := k.start(ctx, cfg)

		k.mu.Lock()
		close(done)
		k.inflight = nil
		if err != nil {
			k.state, k.failure = StateFailed, err.Error()
			id := k.sessionID
			k.sessionID = 0
			k.mu.Unlock()
			if id != 0 {
				k.store.FinishKernelSession(id, k.now(), "failed", err.Error())
			}
			return err
		}
		k.state = StateReady
		k.activity = k.now()
		k.mu.Unlock()
		return nil
	}
}

func (k *Kernel) start(ctx context.Context, cfg Config) error {
	script, err := RenderNotebook(NotebookParams{
		KernelToken: cfg.KernelToken,
		TunnelToken: cfg.TunnelToken,
		IdleSeconds: idleSecondsFor(cfg),
		CapSeconds:  int(cfg.SessionCap.Seconds()),
		Runtime:     cfg.RuntimeDataset,
		Models:      modelMounts(cfg),
		Accelerator: cfg.Accelerator,
	})
	if err != nil {
		return err
	}

	kc := NewKaggleClient(cfg.KaggleUser, cfg.KaggleKey)
	sources := []string{cfg.RuntimeDataset}
	for _, m := range modelMounts(cfg) {
		sources = appendUnique(sources, m.Dataset)
	}

	k.log.Info("starting kernel", "slug", cfg.KernelSlug, "datasets", len(sources))
	if _, err := kc.Push(ctx, KernelPush{
		ID:                     kc.Ref(cfg.KernelSlug),
		Slug:                   cfg.KernelSlug,
		NewTitle:               "gpuless runner",
		Text:                   script,
		Language:               "python",
		KernelType:             "script",
		IsPrivate:              true,
		EnableGPU:              true,
		EnableInternet:         true,
		DatasetDataSources:     sources,
		CompetitionDataSources: []string{},
		KernelDataSources:      []string{},
		CategoryIDs:            []string{},
	}); err != nil {
		return fmt.Errorf("could not start the notebook: %w", err)
	}

	// The billing clock starts when Kaggle allocates the session, not when
	// ComfyUI answers, so the session is recorded before we wait.
	id, err := k.store.StartKernelSession(cfg.KernelSlug, k.now())
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.sessionID = id
	k.mu.Unlock()

	deadline := k.now().Add(bootTimeout)
	for k.now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bootPoll):
		}
		h, err := k.health(ctx, cfg)
		if err == nil && h.OK {
			k.mu.Lock()
			k.gpu = h.GPU
			k.mu.Unlock()
			k.store.MarkKernelReady(id, k.now())
			k.log.Info("kernel ready", "gpu", h.GPU, "after", k.now().Sub(k.since).Round(time.Second))
			return nil
		}
	}

	// A boot that never answered usually explains itself in the Kaggle log.
	if logText, lerr := kc.Output(context.WithoutCancel(ctx), cfg.KernelSlug); lerr == nil && logText != "" {
		k.log.Warn("kernel boot log", "tail", truncate(logText, 2000))
	}
	return fmt.Errorf("the kernel did not answer within %s", bootTimeout)
}

// idleSecondsFor is what the kernel enforces on itself. Keep-alive turns the
// idle stop off entirely, leaving only the hard session cap.
func idleSecondsFor(cfg Config) int {
	if cfg.KeepAlive {
		return 0
	}
	return int(cfg.IdleStop.Seconds())
}

func modelMounts(cfg Config) []ModelMount {
	var out []ModelMount
	if cfg.SDXLDataset != "" {
		out = append(out, ModelMount{Dataset: cfg.SDXLDataset, Folder: "checkpoints"})
	}
	if cfg.XTTSDataset != "" {
		out = append(out, ModelMount{Dataset: cfg.XTTSDataset, Folder: "tts"})
	}
	return out
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// Stop asks the kernel to end its own session. Kaggle's API cannot kill a
// running kernel, so this is a request, not a command; the supervisor
// confirms it actually went away.
func (k *Kernel) Stop(ctx context.Context, cfg Config, reason string) error {
	k.mu.Lock()
	if k.state != StateReady && k.state != StateFailed {
		state := k.state
		k.mu.Unlock()
		return fmt.Errorf("cannot stop a kernel that is %s", state)
	}
	k.state = StateStopping
	id := k.sessionID
	k.mu.Unlock()

	req, err := k.request(ctx, cfg, http.MethodPost, "/gpuless/shutdown", nil)
	if err == nil {
		if res, derr := k.http.Do(req); derr == nil {
			io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
			res.Body.Close()
		}
	}

	// Wait for the edge to actually go quiet before we close the books.
	deadline := k.now().Add(2 * time.Minute)
	for k.now().Before(deadline) {
		if _, herr := k.health(ctx, cfg); herr != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(stopPoll):
		}
	}

	k.mu.Lock()
	k.state, k.sessionID, k.gpu = StateStopped, 0, ""
	k.since = time.Time{}
	k.mu.Unlock()
	if id != 0 {
		k.store.FinishKernelSession(id, k.now(), reason, "")
	}
	k.log.Info("kernel stopped", "reason", reason)
	return nil
}

func (k *Kernel) request(ctx context.Context, cfg Config, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, cfg.TunnelBase()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.KernelToken)
	return req, nil
}

func (k *Kernel) health(ctx context.Context, cfg Config) (health, error) {
	var h health
	req, err := k.request(ctx, cfg, http.MethodGet, "/gpuless/health", nil)
	if err != nil {
		return h, err
	}
	res, err := k.http.Do(req)
	if err != nil {
		return h, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return h, fmt.Errorf("health: %s", res.Status)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&h); err != nil {
		return h, err
	}
	return h, nil
}

// Adopt reconnects to a kernel that outlived the panel. Without it a restart
// would leave a session running on Kaggle, spending the budget unwatched.
func (k *Kernel) Adopt(ctx context.Context, cfg Config) {
	open, err := k.store.OpenKernelSession()
	if err != nil || open == nil {
		return
	}
	if !cfg.Configured() {
		k.store.FinishKernelSession(open.ID, k.now(), "panel restarted unconfigured", "")
		return
	}
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	h, herr := k.health(probe, cfg)
	if herr != nil || !h.OK {
		k.log.Info("previous kernel is gone, closing its session", "session", open.ID)
		k.store.FinishKernelSession(open.ID, k.now(), "lost when the panel restarted", "")
		return
	}
	k.mu.Lock()
	k.state, k.sessionID, k.gpu = StateReady, open.ID, h.GPU
	k.since = open.Started
	k.activity = k.now().Add(-time.Duration(h.Idle) * time.Second)
	k.mu.Unlock()
	k.log.Info("adopted the running kernel", "session", open.ID, "gpu", h.GPU)
}

// Supervise runs for the lifetime of the panel: it notices a kernel that died
// on its own, and enforces the idle stop and the budget from this side too.
func (k *Kernel) Supervise(ctx context.Context, cfgFn func() Config) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cfg := cfgFn()
		if !cfg.Configured() || k.State() != StateReady {
			continue
		}

		probe, cancel := context.WithTimeout(ctx, 20*time.Second)
		h, err := k.health(probe, cfg)
		cancel()
		if err != nil {
			// The kernel stopped itself, or the tunnel went down. Either way
			// the session is over and the budget must stop counting.
			k.mu.Lock()
			id := k.sessionID
			k.state, k.sessionID, k.gpu = StateStopped, 0, ""
			k.since = time.Time{}
			k.mu.Unlock()
			if id != 0 {
				k.store.FinishKernelSession(id, k.now(), "ended on the kernel side", "")
			}
			k.log.Info("kernel went away", "err", err)
			continue
		}

		k.mu.Lock()
		k.gpu = h.GPU
		idle := k.now().Sub(k.activity)
		k.mu.Unlock()

		used, quota, _ := k.quota(cfg)
		switch {
		case pctOf(used, quota) >= float64(cfg.BlockPct):
			k.log.Info("budget exhausted, stopping the kernel")
			k.Stop(ctx, cfg, "weekly budget reached")
		case !cfg.KeepAlive && cfg.IdleStop > 0 && idle > cfg.IdleStop+30*time.Second:
			// The kernel enforces this itself; this is the backstop for when
			// its own timer is wedged.
			k.log.Info("idle past the limit, stopping the kernel", "idle", idle.Round(time.Second))
			k.Stop(ctx, cfg, "idle")
		}
	}
}
