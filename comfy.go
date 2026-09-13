package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Comfy talks to the ComfyUI instance inside the Kaggle kernel, through the
// tunnel. Every request carries the shared secret: the tunnel hostname is
// public, and a GPU someone else can drive is the whole budget gone.
type Comfy struct {
	Base   string
	Token  string
	HTTP   *http.Client
	Poll   time.Duration
	OnPing func() // called on each poll so the idle timer sees the work
}

func NewComfy(cfg Config) *Comfy {
	return &Comfy{
		Base:  cfg.TunnelBase(),
		Token: cfg.KernelToken,
		HTTP:  &http.Client{Timeout: 5 * time.Minute},
		Poll:  time.Second,
	}
}

// Output is one file a run produced.
type Output struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

func (c *Comfy) do(ctx context.Context, method, path string, body io.Reader, ctype string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	return c.HTTP.Do(req)
}

// Submit queues a graph and returns ComfyUI's prompt id.
func (c *Comfy) Submit(ctx context.Context, graph map[string]any) (string, error) {
	payload, err := json.Marshal(map[string]any{"prompt": graph, "client_id": "gpuless"})
	if err != nil {
		return "", err
	}
	res, err := c.do(ctx, http.MethodPost, "/prompt", bytes.NewReader(payload), "application/json")
	if err != nil {
		return "", fmt.Errorf("could not reach the kernel: %w", err)
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("the kernel rejected the panel's token")
	}
	if res.StatusCode >= 400 {
		// ComfyUI reports a bad graph in detail; it is the single most useful
		// thing to show when a workflow override is wrong.
		return "", fmt.Errorf("comfyui refused the graph: %s", truncate(string(raw), 600))
	}
	var out struct {
		PromptID string `json:"prompt_id"`
		Error    any    `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("comfyui sent an unreadable reply: %w", err)
	}
	if out.PromptID == "" {
		return "", fmt.Errorf("comfyui accepted nothing: %s", truncate(string(raw), 300))
	}
	return out.PromptID, nil
}

// Wait polls the history until the prompt has produced its outputs.
func (c *Comfy) Wait(ctx context.Context, promptID string, outputNodes []string) ([]Output, error) {
	ticker := time.NewTicker(c.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		if c.OnPing != nil {
			c.OnPing()
		}

		res, err := c.do(ctx, http.MethodGet, "/history/"+url.PathEscape(promptID), nil, "")
		if err != nil {
			return nil, fmt.Errorf("lost the kernel while waiting: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		res.Body.Close()
		if res.StatusCode >= 400 {
			return nil, fmt.Errorf("history: %s", res.Status)
		}

		var hist map[string]struct {
			Status struct {
				Completed bool    `json:"completed"`
				StatusStr string  `json:"status_str"`
				Messages  [][]any `json:"messages"`
			} `json:"status"`
			Outputs map[string]struct {
				Images []Output `json:"images"`
				Audio  []Output `json:"audio"`
				GIFs   []Output `json:"gifs"`
			} `json:"outputs"`
		}
		if err := json.Unmarshal(raw, &hist); err != nil {
			return nil, fmt.Errorf("history is not readable: %w", err)
		}
		entry, ok := hist[promptID]
		if !ok {
			continue // still queued
		}
		if entry.Status.StatusStr == "error" {
			return nil, fmt.Errorf("the workflow failed inside ComfyUI: %s", firstMessage(entry.Status.Messages))
		}

		var files []Output
		for _, node := range outputNodes {
			out, ok := entry.Outputs[node]
			if !ok {
				continue
			}
			files = append(files, out.Images...)
			files = append(files, out.Audio...)
			files = append(files, out.GIFs...)
		}
		if len(files) > 0 {
			return files, nil
		}
		if entry.Status.Completed {
			return nil, fmt.Errorf("the workflow finished without writing anything to node %v", outputNodes)
		}
	}
}

func firstMessage(msgs [][]any) string {
	for _, m := range msgs {
		if len(m) >= 2 {
			if b, err := json.Marshal(m[1]); err == nil {
				return truncate(string(b), 400)
			}
		}
	}
	return "no detail given"
}

// Fetch downloads one output file.
func (c *Comfy) Fetch(ctx context.Context, o Output) ([]byte, string, error) {
	q := url.Values{
		"filename":  {o.Filename},
		"subfolder": {o.Subfolder},
		"type":      {orDefault(o.Type, "output")},
	}
	res, err := c.do(ctx, http.MethodGet, "/view?"+q.Encode(), nil, "")
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return nil, "", fmt.Errorf("could not download %s: %s", o.Filename, res.Status)
	}
	// 64 MiB is far above any single image or speech clip and keeps a
	// misbehaving kernel from filling the panel's memory.
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	ctype := res.Header.Get("Content-Type")
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	return body, ctype, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
