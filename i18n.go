package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// Language is one of the panel's locales. English is the source language and
// the fallback for any key a translation has not caught up with.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var languages = []Language{
	{Code: "en", Name: "English"},
	{Code: "de", Name: "Deutsch"},
	{Code: "es", Name: "Español"},
}

func isSupportedLanguage(code string) bool {
	for _, l := range languages {
		if l.Code == code {
			return true
		}
	}
	return false
}

func languageList() []Language { return languages }

// strings are flat keys so the front end can look them up without walking a
// tree. English carries every key; the others may be partial.
var translations = map[string]map[string]string{
	"en": {
		"app.name":           "gpuless",
		"nav.image":          "Image",
		"nav.voice":          "Voice",
		"nav.settings":       "Settings",
		"kernel.stopped":     "Kernel stopped",
		"kernel.starting":    "Kernel starting",
		"kernel.ready":       "Kernel running",
		"kernel.stopping":    "Kernel stopping",
		"kernel.failed":      "Kernel failed",
		"kernel.idle":        "idle — stopping in {t}",
		"kernel.wake":        "Waking the kernel",
		"kernel.wakeNote":    "It stopped after {n} idle minutes to save your weekly hours.",
		"kernel.stopNow":     "Stop now",
		"kernel.start":       "Start now",
		"kernel.keepAlive":   "kept alive",
		"quota.week":         "This week",
		"quota.of":           "of {n} h",
		"quota.resets":       "resets {when}",
		"quota.warning":      "The weekly GPU budget is nearly used up.",
		"quota.blocked":      "The weekly GPU budget is used up. New runs are refused until it resets.",
		"quota.estimate":     "Counted by this panel — Kaggle publishes no figure.",
		"image.prompt":       "Prompt",
		"image.exclude":      "Exclude",
		"image.optional":     "optional",
		"image.model":        "Model",
		"image.size":         "Size",
		"image.steps":        "Steps",
		"image.guidance":     "Guidance",
		"image.seed":         "Seed",
		"image.random":       "Random",
		"image.generate":     "Generate",
		"image.empty":        "Nothing generated yet.",
		"image.coldNote":     "The kernel is warm — this run starts right away.",
		"image.coldNoteCold": "The kernel is asleep. The first run takes about 40 s longer.",
		"voice.text":         "Text to speak",
		"voice.voice":        "Voice",
		"voice.language":     "Language",
		"voice.speed":        "Speed",
		"voice.format":       "Format",
		"voice.speak":        "Speak",
		"voice.empty":        "Nothing spoken yet.",
		"run.running":        "running",
		"run.failed":         "failed",
		"run.done":           "done",
		"run.queued":         "Queued",
		"run.download":       "Download",
		"common.save":        "Save",
		"common.saved":       "Saved",
		"common.cancel":      "Cancel",
		"common.back":        "Back",
		"common.continue":    "Continue",
		"common.signIn":      "Sign in",
		"common.signOut":     "Sign out",
		"common.recent":      "Recent",
		"common.minutes":     "minutes",
		"common.hours":       "hours",
		"setup.title":        "Set up gpuless",
		"setup.lead":         "Four steps, then the panel is ready to use.",
		"setup.admin":        "Administrator",
		"setup.kaggle":       "Kaggle",
		"setup.tunnel":       "Tunnel",
		"setup.models":       "Models",
		"setup.step":         "Step {n} of 4",
		"settings.kaggle":    "Kaggle account",
		"settings.tunnel":    "Tunnel",
		"settings.budget":    "GPU budget",
		"settings.kernel":    "Kernel",
		"settings.models":    "Models",
		"settings.language":  "Language",
		"settings.idleStop":  "Stop when idle",
		"settings.warmVisit": "Warm up on first visit",
		"settings.keepAlive": "Keep alive while a tab is open",
		"settings.hardLimit": "Hard session limit",
		"error.generic":      "Something went wrong.",
		"error.signedOut":    "You have been signed out.",
	},
	"de": {
		"nav.image":          "Bild",
		"nav.voice":          "Stimme",
		"nav.settings":       "Einstellungen",
		"kernel.stopped":     "Kernel gestoppt",
		"kernel.starting":    "Kernel startet",
		"kernel.ready":       "Kernel läuft",
		"kernel.stopping":    "Kernel stoppt",
		"kernel.failed":      "Kernel fehlgeschlagen",
		"kernel.idle":        "unbenutzt — stoppt in {t}",
		"kernel.wake":        "Kernel wird geweckt",
		"kernel.wakeNote":    "Er wurde nach {n} Minuten ohne Anfrage gestoppt, um Wochenstunden zu sparen.",
		"kernel.stopNow":     "Jetzt stoppen",
		"kernel.start":       "Jetzt starten",
		"kernel.keepAlive":   "dauerhaft an",
		"quota.week":         "Diese Woche",
		"quota.of":           "von {n} h",
		"quota.resets":       "Reset {when}",
		"quota.warning":      "Das Wochenkontingent ist fast aufgebraucht.",
		"quota.blocked":      "Das Wochenkontingent ist aufgebraucht. Neue Läufe werden bis zum Reset abgelehnt.",
		"quota.estimate":     "Von diesem Panel gezählt — Kaggle veröffentlicht keinen Wert.",
		"image.prompt":       "Prompt",
		"image.exclude":      "Ausschließen",
		"image.optional":     "optional",
		"image.model":        "Modell",
		"image.size":         "Größe",
		"image.steps":        "Schritte",
		"image.guidance":     "Guidance",
		"image.seed":         "Seed",
		"image.random":       "Zufällig",
		"image.generate":     "Erzeugen",
		"image.empty":        "Noch nichts erzeugt.",
		"image.coldNote":     "Der Kernel läuft — dieser Lauf startet sofort.",
		"image.coldNoteCold": "Der Kernel schläft. Der erste Lauf dauert etwa 40 s länger.",
		"voice.text":         "Text zum Sprechen",
		"voice.voice":        "Stimme",
		"voice.language":     "Sprache",
		"voice.speed":        "Tempo",
		"voice.format":       "Format",
		"voice.speak":        "Sprechen",
		"voice.empty":        "Noch nichts gesprochen.",
		"run.running":        "läuft",
		"run.failed":         "fehlgeschlagen",
		"run.done":           "fertig",
		"run.queued":         "In der Warteschlange",
		"run.download":       "Herunterladen",
		"common.save":        "Speichern",
		"common.saved":       "Gespeichert",
		"common.cancel":      "Abbrechen",
		"common.back":        "Zurück",
		"common.continue":    "Weiter",
		"common.signIn":      "Anmelden",
		"common.signOut":     "Abmelden",
		"common.recent":      "Zuletzt",
		"common.minutes":     "Minuten",
		"common.hours":       "Stunden",
		"setup.title":        "gpuless einrichten",
		"setup.lead":         "Vier Schritte, dann ist das Panel einsatzbereit.",
		"setup.admin":        "Administrator",
		"setup.kaggle":       "Kaggle",
		"setup.tunnel":       "Tunnel",
		"setup.models":       "Modelle",
		"setup.step":         "Schritt {n} von 4",
		"settings.kaggle":    "Kaggle-Konto",
		"settings.tunnel":    "Tunnel",
		"settings.budget":    "GPU-Kontingent",
		"settings.kernel":    "Kernel",
		"settings.models":    "Modelle",
		"settings.language":  "Sprache",
		"settings.idleStop":  "Stoppen bei Untätigkeit",
		"settings.warmVisit": "Beim ersten Besuch vorwärmen",
		"settings.keepAlive": "Laufen lassen, solange ein Tab offen ist",
		"settings.hardLimit": "Harte Sitzungsgrenze",
		"error.generic":      "Da ist etwas schiefgelaufen.",
		"error.signedOut":    "Du wurdest abgemeldet.",
	},
	"es": {
		"nav.image":          "Imagen",
		"nav.voice":          "Voz",
		"nav.settings":       "Ajustes",
		"kernel.stopped":     "Kernel detenido",
		"kernel.starting":    "Kernel arrancando",
		"kernel.ready":       "Kernel en marcha",
		"kernel.stopping":    "Kernel deteniéndose",
		"kernel.failed":      "El kernel falló",
		"kernel.idle":        "inactivo — se detiene en {t}",
		"kernel.wake":        "Despertando el kernel",
		"kernel.wakeNote":    "Se detuvo tras {n} minutos sin uso para ahorrar horas semanales.",
		"kernel.stopNow":     "Detener ahora",
		"kernel.start":       "Arrancar ahora",
		"kernel.keepAlive":   "siempre activo",
		"quota.week":         "Esta semana",
		"quota.of":           "de {n} h",
		"quota.resets":       "se reinicia {when}",
		"quota.warning":      "El presupuesto semanal de GPU está casi agotado.",
		"quota.blocked":      "El presupuesto semanal de GPU se ha agotado. No habrá nuevas ejecuciones hasta el reinicio.",
		"quota.estimate":     "Contado por este panel — Kaggle no publica la cifra.",
		"image.prompt":       "Indicación",
		"image.exclude":      "Excluir",
		"image.optional":     "opcional",
		"image.model":        "Modelo",
		"image.size":         "Tamaño",
		"image.steps":        "Pasos",
		"image.guidance":     "Guía",
		"image.seed":         "Semilla",
		"image.random":       "Aleatoria",
		"image.generate":     "Generar",
		"image.empty":        "Todavía no hay nada.",
		"image.coldNote":     "El kernel está caliente — esta ejecución empieza ya.",
		"image.coldNoteCold": "El kernel está dormido. La primera ejecución tarda unos 40 s más.",
		"voice.text":         "Texto para leer",
		"voice.voice":        "Voz",
		"voice.language":     "Idioma",
		"voice.speed":        "Velocidad",
		"voice.format":       "Formato",
		"voice.speak":        "Leer",
		"voice.empty":        "Todavía no hay nada.",
		"run.running":        "en curso",
		"run.failed":         "falló",
		"run.done":           "listo",
		"run.queued":         "En cola",
		"run.download":       "Descargar",
		"common.save":        "Guardar",
		"common.saved":       "Guardado",
		"common.cancel":      "Cancelar",
		"common.back":        "Atrás",
		"common.continue":    "Continuar",
		"common.signIn":      "Iniciar sesión",
		"common.signOut":     "Cerrar sesión",
		"common.recent":      "Reciente",
		"common.minutes":     "minutos",
		"common.hours":       "horas",
		"setup.title":        "Configurar gpuless",
		"setup.lead":         "Cuatro pasos y el panel queda listo.",
		"setup.admin":        "Administrador",
		"setup.kaggle":       "Kaggle",
		"setup.tunnel":       "Túnel",
		"setup.models":       "Modelos",
		"setup.step":         "Paso {n} de 4",
		"settings.kaggle":    "Cuenta de Kaggle",
		"settings.tunnel":    "Túnel",
		"settings.budget":    "Presupuesto de GPU",
		"settings.kernel":    "Kernel",
		"settings.models":    "Modelos",
		"settings.language":  "Idioma",
		"settings.idleStop":  "Detener si está inactivo",
		"settings.warmVisit": "Precalentar en la primera visita",
		"settings.keepAlive": "Mantener activo mientras haya una pestaña abierta",
		"settings.hardLimit": "Límite duro de sesión",
		"error.generic":      "Algo ha salido mal.",
		"error.signedOut":    "Tu sesión se ha cerrado.",
	},
}

// bundle returns a complete set of strings for a language: the translation on
// top of English, so a missing key shows English rather than the raw key.
func bundle(code string) map[string]string {
	out := make(map[string]string, len(translations["en"]))
	for k, v := range translations["en"] {
		out[k] = v
	}
	if code != "en" {
		for k, v := range translations[code] {
			out[k] = v
		}
	}
	return out
}

// pickLanguage honours the operator's choice, or the browser's when they
// asked it to. Anything unknown lands on English.
func pickLanguage(cfg Config, r *http.Request) string {
	// An explicit ?lang= wins: it is how the language links work before a
	// visitor has an account to store a preference on.
	if q := strings.ToLower(r.URL.Query().Get("lang")); isSupportedLanguage(q) {
		return q
	}
	if !cfg.FollowBrowser {
		if isSupportedLanguage(cfg.Language) {
			return cfg.Language
		}
		return "en"
	}
	for _, tag := range parseAcceptLanguage(r.Header.Get("Accept-Language")) {
		if isSupportedLanguage(tag) {
			return tag
		}
	}
	if isSupportedLanguage(cfg.Language) {
		return cfg.Language
	}
	return "en"
}

// parseAcceptLanguage returns the primary subtags in descending q order.
func parseAcceptLanguage(header string) []string {
	type pref struct {
		tag string
		q   float64
	}
	var prefs []pref
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, q := part, 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			if v := strings.TrimSpace(part[i+1:]); strings.HasPrefix(v, "q=") {
				if f, err := parseFloat(v[2:]); err == nil {
					q = f
				}
			}
		}
		if i := strings.IndexByte(tag, '-'); i > 0 {
			tag = tag[:i]
		}
		prefs = append(prefs, pref{strings.ToLower(tag), q})
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	out := make([]string, 0, len(prefs))
	for _, p := range prefs {
		out = append(out, p.tag)
	}
	return out
}

func parseFloat(s string) (float64, error) {
	var f float64
	err := json.Unmarshal([]byte(strings.TrimSpace(s)), &f)
	return f, err
}
