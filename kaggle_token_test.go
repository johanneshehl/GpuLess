package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKaggleAuthHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	cases := []struct{ name, user, key, want string }{
		{"api token", "anna", "KGAT_0123abcd", "Bearer KGAT_0123abcd"},
		{"legacy key", "anna", "0123abcd", "Basic " + base64.StdEncoding.EncodeToString([]byte("anna:0123abcd"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := NewKaggleClient(c.user, c.key)
			k.BaseURL = srv.URL
			if err := k.Verify(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Authorization = %q, want %q", got, c.want)
			}
		})
	}
}
