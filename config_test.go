package main

import (
	"testing"
	"time"
)

func TestQuotaWindowStart(t *testing.T) {
	// Reset Saturday at 00:00 UTC, which is the default.
	cfg := Config{ResetWeekday: time.Saturday, ResetHour: 0}

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"midweek looks back to Saturday",
			time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), // Tuesday
			time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)},  // Saturday before
		{"just after the reset",
			time.Date(2026, 3, 7, 0, 30, 0, 0, time.UTC),
			time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)},
		{"exactly on the reset",
			time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)},
		{"the Friday before belongs to the previous week",
			time.Date(2026, 3, 6, 23, 59, 0, 0, time.UTC),
			time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cfg.QuotaWindowStart(c.now); !got.Equal(c.want) {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestQuotaWindowStartRespectsTheHour(t *testing.T) {
	cfg := Config{ResetWeekday: time.Saturday, ResetHour: 6}

	// 05:00 on reset day is still the previous week: the hour has not come.
	now := time.Date(2026, 3, 7, 5, 0, 0, 0, time.UTC)
	want := time.Date(2026, 2, 28, 6, 0, 0, 0, time.UTC)
	if got := cfg.QuotaWindowStart(now); !got.Equal(want) {
		t.Errorf("before the reset hour: got %s, want %s", got, want)
	}

	now = time.Date(2026, 3, 7, 6, 0, 0, 0, time.UTC)
	want = time.Date(2026, 3, 7, 6, 0, 0, 0, time.UTC)
	if got := cfg.QuotaWindowStart(now); !got.Equal(want) {
		t.Errorf("at the reset hour: got %s, want %s", got, want)
	}
}

func TestTunnelBase(t *testing.T) {
	cases := map[string]string{
		"gpu.example.com":          "https://gpu.example.com",
		"  gpu.example.com/  ":     "https://gpu.example.com",
		"https://gpu.example.com":  "https://gpu.example.com",
		"http://127.0.0.1:8189":    "http://127.0.0.1:8189",
		"https://gpu.example.com/": "https://gpu.example.com",
	}
	for in, want := range cases {
		if got := (Config{TunnelHost: in}).TunnelBase(); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestConfigured(t *testing.T) {
	full := Config{
		KaggleUser: "u", KaggleKey: "k", TunnelHost: "h",
		TunnelToken: "t", KernelToken: "s", RuntimeDataset: "u/rt",
	}
	if !full.Configured() {
		t.Fatal("a complete config should be usable")
	}
	// Every one of these is load-bearing: without it the kernel cannot start
	// or cannot be reached.
	for name, mangle := range map[string]func(*Config){
		"kaggle user":     func(c *Config) { c.KaggleUser = "" },
		"kaggle key":      func(c *Config) { c.KaggleKey = "" },
		"tunnel host":     func(c *Config) { c.TunnelHost = "" },
		"tunnel token":    func(c *Config) { c.TunnelToken = "" },
		"kernel token":    func(c *Config) { c.KernelToken = "" },
		"runtime dataset": func(c *Config) { c.RuntimeDataset = "" },
	} {
		c := full
		mangle(&c)
		if c.Configured() {
			t.Errorf("missing %s should make the config unusable", name)
		}
	}
}

func TestNormaliseAccelerator(t *testing.T) {
	for in, want := range map[string]string{"T4x2": "T4x2", "P100": "P100", "": "T4x2", "H100": "T4x2"} {
		if got := normaliseAccelerator(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	s := testStore(t)
	cfg := s.Config()

	if cfg.IdleStop != 5*time.Minute {
		t.Errorf("idle stop default is %s, want 5m", cfg.IdleStop)
	}
	if cfg.KeepAlive {
		t.Error("keep-alive must default to off: an open tab overnight costs the week")
	}
	if !cfg.WarmOnVisit {
		t.Error("warm-on-visit should default to on")
	}
	if cfg.WeeklyQuota != 30*time.Hour {
		t.Errorf("weekly quota default is %s, want 30h", cfg.WeeklyQuota)
	}
	if cfg.Configured() {
		t.Error("a fresh store cannot be configured")
	}
}
