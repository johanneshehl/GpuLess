package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupKaggleRejectedSaysToWait(t *testing.T) {
	h := newPanel(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	old := kaggleBaseURL
	kaggleBaseURL = srv.URL
	t.Cleanup(func() { kaggleBaseURL = old })

	if code, body := h.do(t, "POST", "/api/setup/admin",
		map[string]any{"email": "ada@example.com", "password": "correct horse battery"}); code != 200 {
		t.Fatalf("admin step: %d %v", code, body)
	}
	code, body := h.do(t, "POST", "/api/setup/kaggle",
		map[string]any{"username": "ada", "key": "KGAT_fresh", "accelerator": "T4x2"})
	msg, _ := body["error"].(string)
	if code != http.StatusBadGateway || !strings.Contains(msg, "5 minutes") {
		t.Fatalf("a rejected token should suggest waiting: %d %q", code, msg)
	}
}
