package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// trustProxy is set from a flag: honouring X-Forwarded-* without a proxy in
// front lets any client claim any address.
var trustProxy bool

type App struct {
	store     *Store
	kernel    *Kernel
	workflows *Workflows
	log       *slog.Logger
	tpl       *template.Template
	mediaDir  string
	logins    *throttle

	jobs sync.WaitGroup // in-flight generations, so shutdown can wait for them
}

func NewApp(store *Store, wf *Workflows, mediaDir string, log *slog.Logger) (*App, error) {
	tpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(mediaDir, 0o750); err != nil {
		return nil, fmt.Errorf("media directory: %w", err)
	}
	return &App{
		store:     store,
		kernel:    NewKernel(store, log),
		workflows: wf,
		log:       log,
		tpl:       tpl,
		mediaDir:  mediaDir,
		logins:    newThrottle(6, 15*time.Minute),
	}, nil
}

func (a *App) cfg() Config { return a.store.Config() }

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))

	// Onboarding and sign-in are the only pages a stranger may reach.
	mux.HandleFunc("GET /setup", a.pageSetup)
	mux.HandleFunc("GET /login", a.pageLogin)
	mux.HandleFunc("POST /api/login", a.apiLogin)
	mux.HandleFunc("POST /api/logout", a.apiLogout)
	mux.HandleFunc("GET /api/setup", a.apiSetupState)
	mux.HandleFunc("POST /api/setup/admin", a.apiSetupAdmin)
	mux.HandleFunc("POST /api/setup/kaggle", a.apiSetupKaggle)
	mux.HandleFunc("POST /api/setup/tunnel", a.apiSetupTunnel)
	mux.HandleFunc("POST /api/setup/models", a.apiSetupModels)

	mux.HandleFunc("GET /{$}", a.requireUser(a.pagePanel))
	mux.HandleFunc("GET /api/status", a.requireUser(a.apiStatus))
	mux.HandleFunc("POST /api/kernel/start", a.requireUser(a.apiKernelStart))
	mux.HandleFunc("POST /api/kernel/stop", a.requireUser(a.apiKernelStop))
	mux.HandleFunc("GET /api/runs", a.requireUser(a.apiRuns))
	mux.HandleFunc("GET /api/runs/{id}", a.requireUser(a.apiRun))
	mux.HandleFunc("POST /api/generate/{kind}", a.requireUser(a.apiGenerate))
	mux.HandleFunc("GET /media/{id}", a.requireUser(a.serveMedia))
	mux.HandleFunc("GET /api/settings", a.requireUser(a.apiGetSettings))
	mux.HandleFunc("POST /api/settings", a.requireUser(a.apiSaveSettings))

	return a.middleware(mux)
}

// middleware applies the guards that belong on every response, and sends
// anyone who arrives before setup to the wizard.
func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		// Everything the panel loads is served from the panel itself.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; "+
				"style-src 'self'; script-src 'self'; connect-src 'self'; "+
				"form-action 'self'; frame-ancestors 'none'; base-uri 'none'")

		path := r.URL.Path
		open := path == "/setup" || path == "/healthz" ||
			strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/api/setup")
		if !a.store.GetBool(kSetupDone, false) && !open {
			if strings.HasPrefix(path, "/api/") {
				writeError(w, http.StatusServiceUnavailable, "gpuless is not set up yet")
				return
			}
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ------------------------------------------------------------- generation

type generateRequest struct {
	Prompt   string  `json:"prompt"`
	Negative string  `json:"negative"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Steps    int     `json:"steps"`
	Guidance float64 `json:"guidance"`
	Seed     int64   `json:"seed"`
	Language string  `json:"language"`
	Speaker  string  `json:"speaker"`
	Speed    float64 `json:"speed"`
}

// params turns a validated request into workflow bindings. Bounds are the
// panel's, not ComfyUI's: a 4096-pixel side or 500 steps on a T4 is a wedged
// kernel and a chunk of the weekly budget.
func (g generateRequest) params(kind string) (map[string]any, error) {
	prompt := strings.TrimSpace(g.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("say what to generate")
	}
	if len(prompt) > 5000 {
		return nil, fmt.Errorf("the prompt is longer than 5000 characters")
	}

	switch kind {
	case "image":
		p := map[string]any{
			"prompt":   prompt,
			"negative": truncate(strings.TrimSpace(g.Negative), 2000),
			"width":    clampInt(g.Width, 256, 1536, 1024),
			"height":   clampInt(g.Height, 256, 1536, 1024),
			"steps":    clampInt(g.Steps, 1, 80, 28),
			"guidance": clampFloat(g.Guidance, 0.5, 20, 6.5),
			"seed":     g.Seed,
		}
		if g.Seed <= 0 {
			p["seed"] = time.Now().UnixNano() % (1 << 32)
		}
		return p, nil
	case "voice":
		lang := strings.ToLower(strings.TrimSpace(g.Language))
		if !isSupportedLanguage(lang) {
			lang = "en"
		}
		return map[string]any{
			"text":     prompt,
			"language": lang,
			"speaker":  orDefault(filepath.Base(strings.TrimSpace(g.Speaker)), "amelie.wav"),
			"speed":    clampFloat(g.Speed, 0.5, 2.0, 1.0),
		}, nil
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// clampRange is a plain clamp with no magic value, for input that is always
// meaningful — including zero.
func clampRange(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampInt additionally reads 0 as "the caller did not say", which is what a
// generation request means by an omitted field.
func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi, def float64) float64 {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// startGeneration records the run and does the slow part in the background.
// A cold start plus a diffusion pass is minutes; holding the HTTP request open
// that long is how you collect proxy timeouts.
func (a *App) startGeneration(u *User, kind string, req generateRequest) (int64, error) {
	wf, err := a.workflows.Get(kind)
	if err != nil {
		return 0, err
	}
	params, err := req.params(kind)
	if err != nil {
		return 0, err
	}
	cfg := a.cfg()
	if !cfg.Configured() {
		return 0, ErrNotConfigured
	}
	// Check the budget here rather than letting EnsureReady discover it: the
	// caller gets a straight refusal instead of a run that exists only to fail.
	if info := a.kernel.Info(cfg); info.Blocked {
		return 0, ErrQuotaBlocked
	}

	paramJSON, _ := json.Marshal(params)
	runID, err := a.store.CreateRun(kind, strings.TrimSpace(req.Prompt), string(paramJSON), u.ID, 0)
	if err != nil {
		return 0, err
	}

	a.jobs.Add(1)
	go func() {
		defer a.jobs.Done()
		// Detached from the request: the browser may well have gone away.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := a.runGeneration(ctx, runID, kind, wf, params, cfg); err != nil {
			a.log.Warn("generation failed", "run", runID, "kind", kind, "err", err)
			a.store.FailRun(runID, err.Error())
		}
	}()
	return runID, nil
}

func (a *App) runGeneration(ctx context.Context, runID int64, kind string, wf *Workflow, params map[string]any, cfg Config) error {
	if err := a.kernel.EnsureReady(ctx, cfg); err != nil {
		return err
	}
	graph, err := wf.Build(params)
	if err != nil {
		return err
	}

	comfy := NewComfy(cfg)
	comfy.OnPing = a.kernel.Touch

	promptID, err := comfy.Submit(ctx, graph)
	if err != nil {
		return err
	}
	files, err := comfy.Wait(ctx, promptID, wf.Meta.OutputNodes)
	if err != nil {
		return err
	}

	body, ctype, err := comfy.Fetch(ctx, files[0])
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s%s", runID, randomToken(6), extensionFor(kind, ctype, files[0].Filename))
	if err := os.WriteFile(filepath.Join(a.mediaDir, name), body, 0o640); err != nil {
		return fmt.Errorf("could not save the result: %w", err)
	}
	a.kernel.Touch()
	return a.store.FinishRun(runID, name, ctype, int64(len(body)))
}

func extensionFor(kind, ctype, filename string) string {
	if ext := filepath.Ext(filename); ext != "" && len(ext) <= 6 {
		return strings.ToLower(ext)
	}
	switch {
	case strings.Contains(ctype, "png"):
		return ".png"
	case strings.Contains(ctype, "jpeg"):
		return ".jpg"
	case strings.Contains(ctype, "wav"):
		return ".wav"
	case strings.Contains(ctype, "mpeg"):
		return ".mp3"
	}
	if kind == "voice" {
		return ".wav"
	}
	return ".png"
}

func (a *App) serveMedia(w http.ResponseWriter, r *http.Request, _ *User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	run, err := a.store.Run(id)
	if err != nil || run == nil || run.File == "" {
		http.NotFound(w, r)
		return
	}
	// The filename comes from our own row, but joining a stored string to a
	// path deserves the check anyway.
	name := filepath.Base(run.File)
	if name != run.File {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", orDefault(run.Mime, "application/octet-stream"))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, filepath.Join(a.mediaDir, name))
}

// Wait blocks until in-flight generations finish, so a restart does not leave
// half-written media behind.
func (a *App) Wait() { a.jobs.Wait() }
