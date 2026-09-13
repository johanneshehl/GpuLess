package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testComfy(t *testing.T, h http.HandlerFunc) *Comfy {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Comfy{Base: srv.URL, Token: "shared", HTTP: srv.Client(), Poll: time.Millisecond}
}

func TestComfySubmitSendsThePromptAndToken(t *testing.T) {
	var body map[string]any
	var auth string
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]string{"prompt_id": "p1"})
	})

	id, err := c.Submit(context.Background(), map[string]any{"3": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if id != "p1" {
		t.Errorf("got prompt id %q", id)
	}
	if auth != "Bearer shared" {
		t.Errorf("the shared secret must ride every request, got %q", auth)
	}
	if _, ok := body["prompt"]; !ok {
		t.Errorf("the graph should be wrapped in a prompt field, got %v", body)
	}
}

func TestComfySubmitReportsARejectedGraph(t *testing.T) {
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"XTTSLoader not found"}}`))
	})
	_, err := c.Submit(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "XTTSLoader not found") {
		t.Errorf("got %v — a bad workflow override must say what ComfyUI disliked", err)
	}
}

func TestComfySubmitReportsABadToken(t *testing.T) {
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	_, err := c.Submit(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("got %v, want something about the token", err)
	}
}

func TestComfyWaitReturnsOutputs(t *testing.T) {
	calls := 0
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			// Still queued: ComfyUI answers with an empty history.
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"p1":{"status":{"completed":true},"outputs":{"9":{"images":[{"filename":"a.png","subfolder":"","type":"output"}]}}}}`))
	})

	pings := 0
	c.OnPing = func() { pings++ }

	out, err := c.Wait(context.Background(), "p1", []string{"9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Filename != "a.png" {
		t.Errorf("got %+v", out)
	}
	if pings == 0 {
		t.Error("waiting should keep the idle timer awake")
	}
}

func TestComfyWaitSurfacesAWorkflowError(t *testing.T) {
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"p1":{"status":{"status_str":"error","messages":[["execution_error",{"exception_message":"out of memory"}]]}}}`))
	})
	_, err := c.Wait(context.Background(), "p1", []string{"9"})
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("got %v, want the exception from ComfyUI", err)
	}
}

func TestComfyWaitFailsWhenNothingWasWritten(t *testing.T) {
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"p1":{"status":{"completed":true},"outputs":{"7":{"images":[{"filename":"a.png"}]}}}}`))
	})
	_, err := c.Wait(context.Background(), "p1", []string{"9"})
	if err == nil || !strings.Contains(err.Error(), "without writing anything") {
		t.Errorf("got %v — an output node that produced nothing must not look like success", err)
	}
}

func TestComfyWaitRespectsCancellation(t *testing.T) {
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.Wait(ctx, "p1", []string{"9"}); err == nil {
		t.Error("a cancelled wait must return")
	}
}

func TestComfyWaitCollectsAudio(t *testing.T) {
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"p1":{"status":{"completed":true},"outputs":{"3":{"audio":[{"filename":"a.wav","type":"output"}]}}}}`))
	})
	out, err := c.Wait(context.Background(), "p1", []string{"3"})
	if err != nil || len(out) != 1 || out[0].Filename != "a.wav" {
		t.Errorf("audio outputs should be picked up too: %+v %v", out, err)
	}
}

func TestComfyFetch(t *testing.T) {
	c := testComfy(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("filename") != "a.png" || q.Get("type") != "output" {
			t.Errorf("bad view query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	})
	body, ctype, err := c.Fetch(context.Background(), Output{Filename: "a.png"})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "PNGDATA" || ctype != "image/png" {
		t.Errorf("got %q %q", body, ctype)
	}
}
