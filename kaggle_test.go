package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func kaggleServer(t *testing.T, handler http.HandlerFunc) *KaggleClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewKaggleClient("ada", "key")
	c.BaseURL = srv.URL
	return c
}

func TestKagglePushSendsTheRightRequest(t *testing.T) {
	var got KernelPush
	var authUser, authPass string
	var ok bool

	c := kaggleServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/kernels/push" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		authUser, authPass, ok = r.BasicAuth()
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"url": "https://kaggle.com/code/ada/gpuless-runner"})
	})

	url, err := c.Push(context.Background(), KernelPush{
		ID: "ada/gpuless-runner", Slug: "gpuless-runner", Text: "print(1)",
		Language: "python", KernelType: "script", IsPrivate: true,
		EnableGPU: true, EnableInternet: true,
		DatasetDataSources: []string{"ada/rt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if url == "" {
		t.Error("the notebook URL should come back")
	}
	if !ok || authUser != "ada" || authPass != "key" {
		t.Errorf("basic auth wrong: %q/%q ok=%v", authUser, authPass, ok)
	}
	if !got.IsPrivate {
		t.Error("the runner carries secrets and must always be pushed private")
	}
	if !got.EnableGPU || !got.EnableInternet {
		t.Error("the runner needs both the GPU and the internet")
	}
	if len(got.DatasetDataSources) != 1 {
		t.Errorf("dataset sources not forwarded: %+v", got.DatasetDataSources)
	}
}

func TestKagglePushSurfacesAnError(t *testing.T) {
	c := kaggleServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"error": "slug already in use"})
	})
	_, err := c.Push(context.Background(), KernelPush{})
	if err == nil || !strings.Contains(err.Error(), "slug already in use") {
		t.Errorf("got %v, want Kaggle's own message", err)
	}
}

func TestKaggleAuthFailure(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		c := kaggleServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		})
		if err := c.Verify(context.Background()); err != ErrKaggleAuth {
			t.Errorf("status %d: got %v, want ErrKaggleAuth", code, err)
		}
	}
}

func TestKaggleServerErrorIncludesTheBody(t *testing.T) {
	c := kaggleServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("kernel slug is not valid"))
	})
	err := c.Verify(context.Background())
	if err == nil || !strings.Contains(err.Error(), "kernel slug is not valid") {
		t.Errorf("got %v, want the server's explanation", err)
	}
}

func TestKaggleStatusAndOutput(t *testing.T) {
	c := kaggleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/kernels/status":
			if r.URL.Query().Get("kernelSlug") != "runner" {
				t.Errorf("slug not passed: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(KernelStatus{Status: "running"})
		case "/kernels/output":
			json.NewEncoder(w).Encode(map[string]string{"log": "boom"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	st, err := c.Status(context.Background(), "runner")
	if err != nil || st.Status != "running" {
		t.Errorf("status: %+v %v", st, err)
	}
	log, err := c.Output(context.Background(), "runner")
	if err != nil || log != "boom" {
		t.Errorf("output: %q %v", log, err)
	}
}

func TestKaggleRef(t *testing.T) {
	if got := NewKaggleClient("ada", "k").Ref("runner"); got != "ada/runner" {
		t.Errorf("got %q", got)
	}
}
