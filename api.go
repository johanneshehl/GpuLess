package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("could not read the request: %w", err)
	}
	return nil
}

// -------------------------------------------------------------- sign in/out

func (a *App) apiLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	key := clientIP(r)
	if locked, wait := a.logins.Locked(key); locked {
		writeError(w, http.StatusTooManyRequests,
			fmt.Sprintf("too many attempts — try again in %s", wait.Round(time.Minute)))
		return
	}

	u, err := a.store.UserByEmail(in.Email)
	if err != nil || !checkPassword(u.Password, in.Password) {
		a.logins.Fail(key)
		// One message for both cases: a different one for an unknown address
		// tells an attacker which addresses exist.
		writeError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	a.logins.Reset(key)
	if err := a.startSession(w, r, u.ID, in.Remember); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) apiLogout(w http.ResponseWriter, r *http.Request) {
	a.endSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------------------- setup

func (a *App) apiSetupState(w http.ResponseWriter, r *http.Request) {
	cfg := a.cfg()
	writeJSON(w, http.StatusOK, map[string]any{
		"complete":    cfg.SetupComplete,
		"has_admin":   a.store.CountUsers() > 0,
		"has_kaggle":  cfg.KaggleUser != "" && cfg.KaggleKey != "",
		"has_tunnel":  cfg.TunnelHost != "" && cfg.TunnelToken != "",
		"has_runtime": cfg.RuntimeDataset != "",
		"languages":   languageList(),
	})
}

// guardSetup keeps the wizard endpoints closed once setup is done — otherwise
// they would be an unauthenticated way to rewrite the configuration.
func (a *App) guardSetup(w http.ResponseWriter, r *http.Request) bool {
	if !a.store.GetBool(kSetupDone, false) {
		return true
	}
	if a.currentUser(r) != nil {
		return true
	}
	writeError(w, http.StatusForbidden, "setup is already finished")
	return false
}

func (a *App) apiSetupAdmin(w http.ResponseWriter, r *http.Request) {
	if !a.guardSetup(w, r) {
		return
	}
	if a.store.CountUsers() > 0 && a.currentUser(r) == nil {
		writeError(w, http.StatusForbidden, "an administrator already exists")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.Contains(in.Email, "@") {
		writeError(w, http.StatusBadRequest, "that does not look like an email address")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := a.store.CreateUser(in.Email, hash, "admin"); err != nil {
		writeError(w, http.StatusBadRequest, "could not create the administrator: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) apiSetupKaggle(w http.ResponseWriter, r *http.Request) {
	if !a.guardSetup(w, r) {
		return
	}
	var in struct {
		Username    string `json:"username"`
		Key         string `json:"key"`
		Accelerator string `json:"accelerator"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Username, in.Key = strings.TrimSpace(in.Username), strings.TrimSpace(in.Key)
	if in.Username == "" || in.Key == "" {
		writeError(w, http.StatusBadRequest, "both the username and the API token are needed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := NewKaggleClient(in.Username, in.Key).Verify(ctx); err != nil {
		writeError(w, http.StatusBadGateway, "Kaggle did not accept those credentials: "+err.Error())
		return
	}

	a.store.Set(kKaggleUser, in.Username)
	a.store.Set(kKaggleKey, in.Key)
	a.store.Set(kAccelerator, normaliseAccelerator(in.Accelerator))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": in.Username})
}

func (a *App) apiSetupTunnel(w http.ResponseWriter, r *http.Request) {
	if !a.guardSetup(w, r) {
		return
	}
	var in struct {
		Host  string `json:"host"`
		Token string `json:"token"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Host, in.Token = strings.TrimSpace(in.Host), strings.TrimSpace(in.Token)
	if in.Host == "" || in.Token == "" {
		writeError(w, http.StatusBadRequest, "both the hostname and the tunnel token are needed")
		return
	}
	if strings.ContainsAny(strings.TrimPrefix(strings.TrimPrefix(in.Host, "https://"), "http://"), " /\\") {
		writeError(w, http.StatusBadRequest, "the hostname should be bare, like gpu.example.com")
		return
	}
	a.store.Set(kTunnelHost, in.Host)
	a.store.Set(kTunnelToken, in.Token)
	if a.store.Get(kKernelToken, "") == "" {
		// The shared secret the kernel checks. Generated here so it never has
		// to be typed, and rotated only when the operator asks.
		a.store.Set(kKernelToken, randomToken(32))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) apiSetupModels(w http.ResponseWriter, r *http.Request) {
	if !a.guardSetup(w, r) {
		return
	}
	var in struct {
		Runtime string `json:"runtime"`
		SDXL    string `json:"sdxl"`
		XTTS    string `json:"xtts"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for name, ref := range map[string]string{"runtime": in.Runtime, "image model": in.SDXL, "voice model": in.XTTS} {
		if ref == "" {
			continue
		}
		if !strings.Contains(strings.Trim(ref, "/"), "/") {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("the %s dataset should look like owner/slug", name))
			return
		}
	}
	if strings.TrimSpace(in.Runtime) == "" {
		writeError(w, http.StatusBadRequest, "the runtime dataset is required — it carries ComfyUI and cloudflared")
		return
	}
	a.store.Set(kDatasetRun, strings.TrimSpace(in.Runtime))
	a.store.Set(kDatasetSDXL, strings.TrimSpace(in.SDXL))
	a.store.Set(kDatasetXTTS, strings.TrimSpace(in.XTTS))

	if a.store.CountUsers() == 0 {
		writeError(w, http.StatusBadRequest, "create the administrator first")
		return
	}
	a.store.SetBool(kSetupDone, true)
	a.log.Info("setup finished")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------------------ status

func (a *App) apiStatus(w http.ResponseWriter, r *http.Request, u *User) {
	cfg := a.cfg()
	writeJSON(w, http.StatusOK, map[string]any{
		"kernel": a.kernel.Info(cfg),
		"user":   map[string]any{"email": u.Email, "role": u.Role},
		"panel": map[string]any{
			"language":   cfg.Language,
			"languages":  languageList(),
			"configured": cfg.Configured(),
			"image":      cfg.SDXLDataset != "",
			"voice":      cfg.XTTSDataset != "",
			"keep_alive": cfg.KeepAlive,
			"idle_stop":  int(cfg.IdleStop.Seconds()),
		},
	})
}

func (a *App) apiKernelStart(w http.ResponseWriter, r *http.Request, _ *User) {
	cfg := a.cfg()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bootTimeout+time.Minute)
		defer cancel()
		if err := a.kernel.EnsureReady(ctx, cfg); err != nil {
			a.log.Warn("manual start failed", "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *App) apiKernelStop(w http.ResponseWriter, r *http.Request, _ *User) {
	cfg := a.cfg()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	if err := a.kernel.Stop(ctx, cfg, "asked from the panel"); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// -------------------------------------------------------------------- runs

func (a *App) apiRuns(w http.ResponseWriter, r *http.Request, _ *User) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "image" && kind != "voice" {
		writeError(w, http.StatusBadRequest, "kind must be image or voice")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := a.store.Runs(kind, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (a *App) apiRun(w http.ResponseWriter, r *http.Request, _ *User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad run id")
		return
	}
	run, err := a.store.Run(id)
	if err != nil || run == nil {
		writeError(w, http.StatusNotFound, "no such run")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (a *App) apiGenerate(w http.ResponseWriter, r *http.Request, u *User) {
	kind := r.PathValue("kind")
	if kind != "image" && kind != "voice" {
		writeError(w, http.StatusNotFound, "no such generator")
		return
	}
	var in generateRequest
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := a.startGeneration(u, kind, in)
	switch {
	case err == nil:
	case errorsIs(err, ErrQuotaBlocked):
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	case errorsIs(err, ErrNotConfigured):
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	default:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": id})
}

// ---------------------------------------------------------------- settings

func (a *App) apiGetSettings(w http.ResponseWriter, r *http.Request, _ *User) {
	cfg := a.cfg()
	writeJSON(w, http.StatusOK, map[string]any{
		"kaggle_username":     cfg.KaggleUser,
		"kaggle_key_set":      cfg.KaggleKey != "",
		"tunnel_host":         cfg.TunnelHost,
		"tunnel_token_set":    cfg.TunnelToken != "",
		"accelerator":         cfg.Accelerator,
		"idle_stop_minutes":   int(cfg.IdleStop.Minutes()),
		"warm_on_visit":       cfg.WarmOnVisit,
		"keep_alive":          cfg.KeepAlive,
		"session_limit_hours": int(cfg.SessionCap.Hours()),
		"weekly_quota_hours":  int(cfg.WeeklyQuota.Hours()),
		"quota_warn_pct":      cfg.WarnPct,
		"quota_block_pct":     cfg.BlockPct,
		"language":            cfg.Language,
		"follow_browser":      cfg.FollowBrowser,
		"dataset_runtime":     cfg.RuntimeDataset,
		"dataset_sdxl":        cfg.SDXLDataset,
		"dataset_xtts":        cfg.XTTSDataset,
		"workflows":           a.workflows.Names(),
	})
}

type settingsPatch struct {
	KaggleUsername *string `json:"kaggle_username"`
	KaggleKey      *string `json:"kaggle_key"`
	TunnelHost     *string `json:"tunnel_host"`
	TunnelToken    *string `json:"tunnel_token"`
	Accelerator    *string `json:"accelerator"`
	IdleStopMin    *int    `json:"idle_stop_minutes"`
	WarmOnVisit    *bool   `json:"warm_on_visit"`
	KeepAlive      *bool   `json:"keep_alive"`
	SessionLimitH  *int    `json:"session_limit_hours"`
	WeeklyQuotaH   *int    `json:"weekly_quota_hours"`
	WarnPct        *int    `json:"quota_warn_pct"`
	BlockPct       *int    `json:"quota_block_pct"`
	Language       *string `json:"language"`
	FollowBrowser  *bool   `json:"follow_browser"`
	DatasetRuntime *string `json:"dataset_runtime"`
	DatasetSDXL    *string `json:"dataset_sdxl"`
	DatasetXTTS    *string `json:"dataset_xtts"`
}

func (a *App) apiSaveSettings(w http.ResponseWriter, r *http.Request, _ *User) {
	var in settingsPatch
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	setStr := func(key string, v *string, clean func(string) string) {
		if v == nil {
			return
		}
		s := strings.TrimSpace(*v)
		if clean != nil {
			s = clean(s)
		}
		a.store.Set(key, s)
	}
	setInt := func(key string, v *int, lo, hi int) {
		if v == nil {
			return
		}
		a.store.SetInt(key, clampRange(*v, lo, hi))
	}
	setBool := func(key string, v *bool) {
		if v != nil {
			a.store.SetBool(key, *v)
		}
	}

	setStr(kKaggleUser, in.KaggleUsername, nil)
	// An empty secret means "leave it alone", so a form can round-trip
	// without ever holding the real value.
	if in.KaggleKey != nil && strings.TrimSpace(*in.KaggleKey) != "" {
		a.store.Set(kKaggleKey, strings.TrimSpace(*in.KaggleKey))
	}
	setStr(kTunnelHost, in.TunnelHost, nil)
	if in.TunnelToken != nil && strings.TrimSpace(*in.TunnelToken) != "" {
		a.store.Set(kTunnelToken, strings.TrimSpace(*in.TunnelToken))
	}
	setStr(kAccelerator, in.Accelerator, normaliseAccelerator)
	setInt(kIdleStopMin, in.IdleStopMin, 1, 120)
	setBool(kWarmOnVisit, in.WarmOnVisit)
	setBool(kKeepAlive, in.KeepAlive)
	setInt(kSessionCapH, in.SessionLimitH, 1, 12)
	setInt(kQuotaHours, in.WeeklyQuotaH, 1, 200)
	setInt(kWarnPct, in.WarnPct, 1, 100)
	setInt(kBlockPct, in.BlockPct, 1, 100)
	setStr(kLanguage, in.Language, func(s string) string {
		if isSupportedLanguage(s) {
			return s
		}
		return "en"
	})
	setBool(kFollowBrowse, in.FollowBrowser)
	setStr(kDatasetRun, in.DatasetRuntime, nil)
	setStr(kDatasetSDXL, in.DatasetSDXL, nil)
	setStr(kDatasetXTTS, in.DatasetXTTS, nil)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// errorsIs keeps the switch above readable without importing errors here.
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
