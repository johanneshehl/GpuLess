package main

import (
	"strings"
	"time"
)

// Setting keys. Everything the operator can change lives in the settings
// table, so a fresh container with the same volume comes back identical.
const (
	kKaggleUser   = "kaggle_username"
	kKaggleKey    = "kaggle_key"
	kTunnelHost   = "tunnel_host"
	kTunnelToken  = "tunnel_token"
	kKernelToken  = "kernel_token"
	kKernelSlug   = "kernel_slug"
	kAccelerator  = "accelerator"
	kIdleStopMin  = "idle_stop_minutes"
	kWarmOnVisit  = "warm_on_visit"
	kKeepAlive    = "keep_alive"
	kSessionCapH  = "session_limit_hours"
	kQuotaHours   = "weekly_quota_hours"
	kQuotaResetWd = "quota_reset_weekday"
	kQuotaResetHr = "quota_reset_hour"
	kWarnPct      = "quota_warn_pct"
	kBlockPct     = "quota_block_pct"
	kLanguage     = "language"
	kFollowBrowse = "follow_browser_language"
	kSetupDone    = "setup_complete"
	kDatasetRun   = "dataset_runtime"
	kDatasetSDXL  = "dataset_sdxl"
	kDatasetXTTS  = "dataset_xtts"
)

// Config is a settings snapshot, read once per request rather than field by
// field, so a handler can never see a half-applied change.
type Config struct {
	KaggleUser  string
	KaggleKey   string
	TunnelHost  string
	TunnelToken string
	KernelToken string
	KernelSlug  string
	Accelerator string

	IdleStop     time.Duration
	WarmOnVisit  bool
	KeepAlive    bool
	SessionCap   time.Duration
	WeeklyQuota  time.Duration
	ResetWeekday time.Weekday
	ResetHour    int
	WarnPct      int
	BlockPct     int

	Language       string
	FollowBrowser  bool
	SetupComplete  bool
	RuntimeDataset string
	SDXLDataset    string
	XTTSDataset    string
}

func (s *Store) Config() Config {
	c := Config{
		KaggleUser:  s.Get(kKaggleUser, ""),
		KaggleKey:   s.Get(kKaggleKey, ""),
		TunnelHost:  s.Get(kTunnelHost, ""),
		TunnelToken: s.Get(kTunnelToken, ""),
		KernelToken: s.Get(kKernelToken, ""),
		KernelSlug:  s.Get(kKernelSlug, "gpuless-runner"),
		Accelerator: s.Get(kAccelerator, "T4x2"),

		IdleStop:     time.Duration(s.GetInt(kIdleStopMin, 5)) * time.Minute,
		WarmOnVisit:  s.GetBool(kWarmOnVisit, true),
		KeepAlive:    s.GetBool(kKeepAlive, false),
		SessionCap:   time.Duration(s.GetInt(kSessionCapH, 9)) * time.Hour,
		WeeklyQuota:  time.Duration(s.GetInt(kQuotaHours, 30)) * time.Hour,
		ResetWeekday: time.Weekday(s.GetInt(kQuotaResetWd, int(time.Saturday))),
		ResetHour:    s.GetInt(kQuotaResetHr, 0),
		WarnPct:      s.GetInt(kWarnPct, 80),
		BlockPct:     s.GetInt(kBlockPct, 98),

		Language:       s.Get(kLanguage, "en"),
		FollowBrowser:  s.GetBool(kFollowBrowse, true),
		SetupComplete:  s.GetBool(kSetupDone, false),
		RuntimeDataset: s.Get(kDatasetRun, ""),
		SDXLDataset:    s.Get(kDatasetSDXL, ""),
		XTTSDataset:    s.Get(kDatasetXTTS, ""),
	}
	if c.ResetHour < 0 || c.ResetHour > 23 {
		c.ResetHour = 0
	}
	return c
}

// TunnelBase is the origin the panel talks to. Always HTTPS: the tunnel
// hostname is public, and the kernel token travels on it.
func (c Config) TunnelBase() string {
	h := strings.TrimSuffix(strings.TrimSpace(c.TunnelHost), "/")
	// A bare hostname means HTTPS, which is what a Cloudflare tunnel gives
	// you. An explicit http:// is honoured for a tunnel that terminates on
	// the same host, where there is nothing to protect in transit.
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		return h
	}
	return "https://" + h
}

// Configured reports whether the panel has everything it needs to start a
// kernel at all.
func (c Config) Configured() bool {
	return c.KaggleUser != "" && c.KaggleKey != "" && c.TunnelHost != "" &&
		c.TunnelToken != "" && c.KernelToken != "" && c.RuntimeDataset != ""
}

// QuotaWindowStart is the most recent weekly reset at or before `now`.
// Kaggle's quota is a rolling weekly allowance; the operator sets the weekday
// and hour that matches what their account actually does.
func (c Config) QuotaWindowStart(now time.Time) time.Time {
	now = now.UTC()
	reset := time.Date(now.Year(), now.Month(), now.Day(), c.ResetHour, 0, 0, 0, time.UTC)
	back := (int(now.Weekday()) - int(c.ResetWeekday) + 7) % 7
	reset = reset.AddDate(0, 0, -back)
	if reset.After(now) {
		reset = reset.AddDate(0, 0, -7)
	}
	return reset
}

// Accelerators the panel offers. Kaggle exposes these as notebook settings,
// not as free-form machine types.
var accelerators = map[string]string{
	"T4x2": "GPU T4 x2",
	"P100": "GPU P100",
}

func normaliseAccelerator(v string) string {
	if _, ok := accelerators[v]; ok {
		return v
	}
	return "T4x2"
}
