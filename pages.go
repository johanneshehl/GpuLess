package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"sync"
)

//go:embed web/templates/*.html
var templateFS embed.FS

//go:embed web/static
var staticFS embed.FS

func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(template.FuncMap{
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(b), nil
		},
	}).ParseFS(templateFS, "web/templates/*.html")
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		panic("gpuless: static assets missing: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assets are embedded and change only with the binary, so a long
		// cache is safe as long as the panel is versioned.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fileServer.ServeHTTP(w, r)
	})
}

// assetTag is a short hash over the embedded static files. Templates append
// it to every asset URL, so a new build reaches the browser at once instead
// of after the cache runs out.
var assetTag = sync.OnceValue(func() string {
	h := sha256.New()
	fs.WalkDir(staticFS, "web/static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
})

// pageData is what every template receives.
type pageData struct {
	Lang      string
	Strings   map[string]string
	Languages []Language
	Version   string
	Asset     string
	Extra     map[string]any
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, extra map[string]any) {
	cfg := a.cfg()
	lang := pickLanguage(cfg, r)
	data := pageData{
		Lang:      lang,
		Strings:   bundle(lang),
		Languages: languageList(),
		Version:   version,
		Asset:     assetTag(),
		Extra:     extra,
	}
	var buf bytes.Buffer
	if err := a.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		a.log.Error("template failed", "name", name, "err", err)
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func (a *App) pageSetup(w http.ResponseWriter, r *http.Request) {
	if a.store.GetBool(kSetupDone, false) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, r, "setup.html", nil)
}

func (a *App) pageLogin(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, r, "login.html", nil)
}

func (a *App) pagePanel(w http.ResponseWriter, r *http.Request, u *User) {
	cfg := a.cfg()
	// Warming up here rather than on the first prompt hides the cold start
	// behind the time it takes to read the page.
	if cfg.WarmOnVisit && cfg.Configured() && a.kernel.State() == StateStopped {
		go func() {
			ctx, cancel := contextWithTimeout(bootTimeout)
			defer cancel()
			if err := a.kernel.EnsureReady(ctx, cfg); err != nil {
				a.log.Info("warm-up did not start the kernel", "err", err)
			}
		}()
	}
	a.render(w, r, "panel.html", map[string]any{
		"Email": u.Email,
		"Image": cfg.SDXLDataset != "",
		"Voice": cfg.XTTSDataset != "",
	})
}
