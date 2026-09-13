package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// panelHarness is a running panel plus the fakes it talks to.
type panelHarness struct {
	app    *App
	store  *Store
	srv    *httptest.Server
	client *http.Client
	edge   *fullFakeKernel
}

// fullFakeKernel is the edge plus a ComfyUI that always produces one PNG.
type fullFakeKernel struct {
	srv   *httptest.Server
	token string
	up    atomic.Bool
}

func newFullFakeKernel(t *testing.T) *fullFakeKernel {
	f := &fullFakeKernel{token: "shared"}
	f.up.Store(true)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/gpuless/health":
			if !f.up.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(health{OK: true, GPU: "Tesla T4", IdleLimit: 300})
		case r.URL.Path == "/gpuless/shutdown":
			f.up.Store(false)
			w.Write([]byte(`{"stopping":true}`))
		case r.URL.Path == "/prompt":
			w.Write([]byte(`{"prompt_id":"p1"}`))
		case strings.HasPrefix(r.URL.Path, "/history/"):
			w.Write([]byte(`{"p1":{"status":{"completed":true},"outputs":{"9":{"images":[{"filename":"a.png","type":"output"}]}}}}`))
		case r.URL.Path == "/view":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("PNGDATA"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newPanel(t *testing.T) *panelHarness {
	t.Helper()
	bootPoll, stopPoll = 5*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { bootPoll, stopPoll = 4*time.Second, 3*time.Second })

	store := testStore(t)
	wf, err := LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(store, wf, t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Routes())
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &panelHarness{app: app, store: store, srv: srv, client: client, edge: newFullFakeKernel(t)}
}

func (h *panelHarness) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// completeSetup walks the wizard, pointing the panel at the fakes.
func (h *panelHarness) completeSetup(t *testing.T) {
	t.Helper()
	var pushes atomic.Int32
	fakeKaggle(t, &pushes)

	if code, body := h.do(t, "POST", "/api/setup/admin",
		map[string]any{"email": "ada@example.com", "password": "correct horse battery"}); code != 200 {
		t.Fatalf("admin step: %d %v", code, body)
	}
	if code, body := h.do(t, "POST", "/api/setup/kaggle",
		map[string]any{"username": "ada", "key": "kaggle-secret-abc123", "accelerator": "T4x2"}); code != 200 {
		t.Fatalf("kaggle step: %d %v", code, body)
	}
	if code, body := h.do(t, "POST", "/api/setup/tunnel",
		map[string]any{"host": h.edge.srv.URL, "token": "tunnel-secret-def456"}); code != 200 {
		t.Fatalf("tunnel step: %d %v", code, body)
	}
	// The wizard generates the shared secret; point the fake at the real one.
	h.edge.token = h.store.Get(kKernelToken, "")
	if h.edge.token == "" {
		t.Fatal("the tunnel step should have generated a kernel token")
	}
	if code, body := h.do(t, "POST", "/api/setup/models",
		map[string]any{"runtime": "ada/rt", "sdxl": "ada/sdxl", "xtts": "ada/xtts"}); code != 200 {
		t.Fatalf("models step: %d %v", code, body)
	}
}

func (h *panelHarness) signIn(t *testing.T) {
	t.Helper()
	code, body := h.do(t, "POST", "/api/login",
		map[string]any{"email": "ada@example.com", "password": "correct horse battery", "remember": true})
	if code != 200 {
		t.Fatalf("sign in: %d %v", code, body)
	}
}

func TestUnconfiguredPanelSendsYouToSetup(t *testing.T) {
	h := newPanel(t)
	res, err := h.client.Get(h.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/setup" {
		t.Errorf("got %d -> %q, want a redirect to /setup", res.StatusCode, res.Header.Get("Location"))
	}
	if code, _ := h.do(t, "GET", "/api/status", nil); code != http.StatusServiceUnavailable {
		t.Errorf("API before setup returned %d, want 503", code)
	}
}

func TestSetupFlow(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)

	cfg := h.store.Config()
	if !cfg.SetupComplete || !cfg.Configured() {
		t.Fatalf("setup did not complete: %+v", cfg)
	}
	if h.store.CountUsers() != 1 {
		t.Error("the wizard should have created exactly one administrator")
	}

	// The wizard must close behind itself, or it is an unauthenticated way to
	// rewrite the credentials.
	fresh := &http.Client{}
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/setup/kaggle",
		strings.NewReader(`{"username":"mallory","key":"x","accelerator":"T4x2"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := fresh.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("setup endpoint after completion returned %d, want 403", res.StatusCode)
	}
	if h.store.Get(kKaggleUser, "") != "ada" {
		t.Error("a stranger rewrote the Kaggle credentials after setup")
	}
}

func TestSetupRejectsAWeakPassword(t *testing.T) {
	h := newPanel(t)
	code, body := h.do(t, "POST", "/api/setup/admin",
		map[string]any{"email": "ada@example.com", "password": "short"})
	if code != http.StatusBadRequest {
		t.Fatalf("got %d %v", code, body)
	}
	if h.store.CountUsers() != 0 {
		t.Error("no user should exist after a rejected password")
	}
}

func TestSetupRejectsABadDatasetReference(t *testing.T) {
	h := newPanel(t)
	code, _ := h.do(t, "POST", "/api/setup/models", map[string]any{"runtime": "just-a-slug"})
	if code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 for a reference without an owner", code)
	}
	code, _ = h.do(t, "POST", "/api/setup/models", map[string]any{"runtime": ""})
	if code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 — the runtime dataset is required", code)
	}
}

func TestSignInAndOut(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)

	if code, _ := h.do(t, "GET", "/api/status", nil); code != http.StatusUnauthorized {
		t.Errorf("status before sign-in returned %d, want 401", code)
	}
	if code, body := h.do(t, "POST", "/api/login",
		map[string]any{"email": "ada@example.com", "password": "wrong"}); code != http.StatusUnauthorized {
		t.Errorf("wrong password returned %d %v", code, body)
	}

	h.signIn(t)
	code, body := h.do(t, "GET", "/api/status", nil)
	if code != 200 {
		t.Fatalf("status after sign-in: %d %v", code, body)
	}
	user := body["user"].(map[string]any)
	if user["email"] != "ada@example.com" {
		t.Errorf("got %v", user)
	}

	h.do(t, "POST", "/api/logout", nil)
	if code, _ := h.do(t, "GET", "/api/status", nil); code != http.StatusUnauthorized {
		t.Errorf("status after sign-out returned %d, want 401", code)
	}
}

func TestLoginIsThrottled(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)

	var last int
	for i := 0; i < 8; i++ {
		last, _ = h.do(t, "POST", "/api/login",
			map[string]any{"email": "ada@example.com", "password": "wrong"})
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after eight failures the address should be locked, got %d", last)
	}
	// The lock must hold even once the password is right.
	if code, _ := h.do(t, "POST", "/api/login",
		map[string]any{"email": "ada@example.com", "password": "correct horse battery"}); code != http.StatusTooManyRequests {
		t.Errorf("a locked address got in with the right password: %d", code)
	}
}

func TestSettingsNeverLeakSecrets(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)
	h.signIn(t)

	code, body := h.do(t, "GET", "/api/settings", nil)
	if code != 200 {
		t.Fatal(code)
	}
	raw, _ := json.Marshal(body)
	for _, secret := range []string{"kaggle-secret-abc123", "tunnel-secret-def456", h.store.Get(kKernelToken, "")} {
		if secret != "" && strings.Contains(string(raw), secret) {
			t.Errorf("settings leaked a secret: %s", raw)
		}
	}
	if body["kaggle_key_set"] != true || body["tunnel_token_set"] != true {
		t.Errorf("the panel should say a secret is stored without showing it: %v", body)
	}
}

func TestSettingsKeepStoredSecretWhenBlank(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)
	h.signIn(t)

	if code, body := h.do(t, "POST", "/api/settings",
		map[string]any{"kaggle_username": "ada2", "kaggle_key": ""}); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if got := h.store.Get(kKaggleKey, ""); got != "kaggle-secret-abc123" {
		t.Errorf("an empty token field wiped the stored key: %q", got)
	}
	if got := h.store.Get(kKaggleUser, ""); got != "ada2" {
		t.Errorf("the username should have changed, got %q", got)
	}
}

func TestSettingsClampOutOfRangeValues(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)
	h.signIn(t)

	h.do(t, "POST", "/api/settings", map[string]any{
		"idle_stop_minutes": 9999, "session_limit_hours": 0, "quota_warn_pct": 400,
	})
	cfg := h.store.Config()
	if cfg.IdleStop != 120*time.Minute {
		t.Errorf("idle stop is %s, want it clamped to 120m", cfg.IdleStop)
	}
	if cfg.SessionCap != time.Hour {
		t.Errorf("session cap is %s, want it clamped to 1h", cfg.SessionCap)
	}
	if cfg.WarnPct != 100 {
		t.Errorf("warn pct is %d, want 100", cfg.WarnPct)
	}
}

func TestGenerateImageEndToEnd(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)
	h.signIn(t)

	code, body := h.do(t, "POST", "/api/generate/image", map[string]any{
		"prompt": "a lighthouse", "width": 1024, "height": 1024, "steps": 20, "guidance": 6, "seed": 7,
	})
	if code != http.StatusAccepted {
		t.Fatalf("generate: %d %v", code, body)
	}
	runID := int64(body["run"].(float64))

	var run map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_, run = h.do(t, "GET", "/api/runs/"+itoa(runID), nil)
		if run["status"] != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if run["status"] != "done" {
		t.Fatalf("run did not finish: %v", run)
	}

	res, err := h.client.Get(h.srv.URL + "/media/" + itoa(runID))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if string(data) != "PNGDATA" {
		t.Errorf("media served %q", data)
	}
	if got := res.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("content type %q", got)
	}
	if h.app.kernel.State() != StateReady {
		t.Errorf("the kernel should be ready after a run, is %s", h.app.kernel.State())
	}
}

func TestGenerateRejectsAnEmptyPrompt(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)
	h.signIn(t)

	if code, _ := h.do(t, "POST", "/api/generate/image", map[string]any{"prompt": "   "}); code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", code)
	}
	if code, _ := h.do(t, "POST", "/api/generate/nonsense", map[string]any{"prompt": "x"}); code != http.StatusNotFound {
		t.Errorf("unknown generator returned %d, want 404", code)
	}
}

func TestGenerateRefusedWhenTheBudgetIsSpent(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)
	h.signIn(t)

	cfg := h.store.Config()
	start := cfg.QuotaWindowStart(time.Now().UTC())
	id, _ := h.store.StartKernelSession("old", start.Add(time.Minute))
	h.store.FinishKernelSession(id, start.Add(time.Minute+30*time.Hour), "test", "")

	code, body := h.do(t, "POST", "/api/generate/image", map[string]any{"prompt": "x"})
	if code != http.StatusTooManyRequests {
		t.Fatalf("got %d %v, want 429", code, body)
	}
}

func TestMediaIsNotAWayIntoTheFilesystem(t *testing.T) {
	h := newPanel(t)
	h.completeSetup(t)
	h.signIn(t)

	id, _ := h.store.CreateRun("image", "x", "{}", 1, 0)
	h.store.FinishRun(id, "../../../etc/passwd", "text/plain", 1)

	res, err := h.client.Get(h.srv.URL + "/media/" + itoa(id))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("a traversing filename returned %d, want 404", res.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newPanel(t)
	res, err := h.client.Get(h.srv.URL + "/setup")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	} {
		if got := res.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP is %q", csp)
	}
}

func TestHealthzIsAlwaysOpen(t *testing.T) {
	h := newPanel(t)
	res, err := h.client.Get(h.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("healthz returned %d before setup, want 200", res.StatusCode)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
