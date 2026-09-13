package main

import (
	"net/http/httptest"
	"testing"
)

func TestBundleFallsBackToEnglish(t *testing.T) {
	en := bundle("en")
	de := bundle("de")

	if len(de) != len(en) {
		t.Errorf("every bundle should carry all %d keys, German has %d", len(en), len(de))
	}
	if de["nav.image"] != "Bild" {
		t.Errorf("translated key missing: %q", de["nav.image"])
	}
	// app.name is only defined in English; German must inherit it rather than
	// render the raw key.
	if de["app.name"] != en["app.name"] {
		t.Errorf("untranslated key should fall back, got %q", de["app.name"])
	}
}

func TestTranslationsAreComplete(t *testing.T) {
	// A key only English has shows up as English text in the middle of a
	// German or Spanish page, so every visible string needs all languages.
	for _, l := range languages {
		if l.Code == "en" {
			continue
		}
		for k := range translations["en"] {
			if k == "app.name" {
				continue
			}
			if _, ok := translations[l.Code][k]; !ok {
				t.Errorf("%s is missing %s", l.Code, k)
			}
		}
	}
}

func TestEveryLanguageHasTheNavigation(t *testing.T) {
	// These are the strings a visitor sees before anything else.
	keys := []string{"nav.image", "nav.voice", "nav.settings", "common.signIn", "quota.week"}
	for _, l := range languages {
		b := bundle(l.Code)
		for _, k := range keys {
			if b[k] == "" || b[k] == k {
				t.Errorf("%s is missing %s", l.Code, k)
			}
		}
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	got := parseAcceptLanguage("de-DE,de;q=0.9,en-US;q=0.8,en;q=0.7")
	want := []string{"de", "de", "en", "en"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}

	// Quality ordering wins over document order.
	if got := parseAcceptLanguage("en;q=0.2,es;q=0.9"); got[0] != "es" {
		t.Errorf("q values ignored: %v", got)
	}
	if got := parseAcceptLanguage(""); len(got) != 0 {
		t.Errorf("an empty header should yield nothing, got %v", got)
	}
}

func TestPickLanguage(t *testing.T) {
	req := func(header, query string) *httptest.ResponseRecorder {
		return nil
	}
	_ = req

	cases := []struct {
		name   string
		cfg    Config
		accept string
		query  string
		want   string
	}{
		{"query wins", Config{Language: "de", FollowBrowser: false}, "es", "es", "es"},
		{"unknown query is ignored", Config{Language: "de", FollowBrowser: false}, "", "fr", "de"},
		{"fixed language ignores the browser", Config{Language: "de", FollowBrowser: false}, "es-ES", "", "de"},
		{"browser is honoured when asked", Config{Language: "en", FollowBrowser: true}, "es-ES,es;q=0.9", "", "es"},
		{"unsupported browser language falls back", Config{Language: "de", FollowBrowser: true}, "fr-FR", "", "de"},
		{"nothing at all is English", Config{FollowBrowser: true}, "", "", "en"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := "/"
			if c.query != "" {
				url += "?lang=" + c.query
			}
			r := httptest.NewRequest("GET", url, nil)
			if c.accept != "" {
				r.Header.Set("Accept-Language", c.accept)
			}
			if got := pickLanguage(c.cfg, r); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
