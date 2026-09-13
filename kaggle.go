package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// KaggleClient speaks the public Kaggle REST API with the operator's own
// credentials. Deliberately small: the panel only ever pushes one notebook
// and asks how it is doing.
type KaggleClient struct {
	Username string
	Key      string
	BaseURL  string
	HTTP     *http.Client
}

const kaggleBase = "https://www.kaggle.com/api/v1"

// kaggleBaseURL is a variable so tests can point the client at a local
// server. Nothing in the running panel changes it.
var kaggleBaseURL = kaggleBase

func NewKaggleClient(user, key string) *KaggleClient {
	return &KaggleClient{
		Username: user,
		Key:      key,
		BaseURL:  kaggleBaseURL,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
	}
}

// KernelPush is the body of POST /kernels/push. Field names are the API's,
// not ours, which is why they are camelCase.
type KernelPush struct {
	ID                     string   `json:"id"`
	Slug                   string   `json:"slug"`
	NewTitle               string   `json:"newTitle"`
	Text                   string   `json:"text"`
	Language               string   `json:"language"`
	KernelType             string   `json:"kernelType"`
	IsPrivate              bool     `json:"isPrivate"`
	EnableGPU              bool     `json:"enableGpu"`
	EnableInternet         bool     `json:"enableInternet"`
	DatasetDataSources     []string `json:"datasetDataSources"`
	CompetitionDataSources []string `json:"competitionDataSources"`
	KernelDataSources      []string `json:"kernelDataSources"`
	CategoryIDs            []string `json:"categoryIds"`
}

type pushResponse struct {
	Ref         string `json:"ref"`
	URL         string `json:"url"`
	Versioner   int    `json:"versionNumber"`
	Error       string `json:"error"`
	InvalidTags string `json:"invalidTags"`
}

// KernelStatus is the reply from GET /kernels/status.
type KernelStatus struct {
	Status         string `json:"status"`
	FailureMessage string `json:"failureMessage"`
}

var ErrKaggleAuth = errors.New("kaggle rejected the credentials")

func (k *KaggleClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth(k.Username, k.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := k.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("kaggle %s: %w", path, err)
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden:
		return ErrKaggleAuth
	case res.StatusCode >= 400:
		return fmt.Errorf("kaggle %s: %s: %s", path, res.Status, strings.TrimSpace(truncate(string(payload), 300)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("kaggle %s: cannot read reply: %w", path, err)
	}
	return nil
}

// Verify checks the credentials without changing anything.
func (k *KaggleClient) Verify(ctx context.Context) error {
	q := url.Values{"user": {k.Username}, "pageSize": {"1"}}
	var out json.RawMessage
	return k.do(ctx, http.MethodGet, "/kernels/list?"+q.Encode(), nil, &out)
}

// Push uploads a new version of the runner notebook, which starts it.
// There is no separate "run" call: pushing a version is what runs it.
func (k *KaggleClient) Push(ctx context.Context, p KernelPush) (string, error) {
	var out pushResponse
	if err := k.do(ctx, http.MethodPost, "/kernels/push", p, &out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("kaggle refused the notebook: %s", out.Error)
	}
	return out.URL, nil
}

func (k *KaggleClient) Status(ctx context.Context, slug string) (KernelStatus, error) {
	q := url.Values{"userName": {k.Username}, "kernelSlug": {slug}}
	var out KernelStatus
	err := k.do(ctx, http.MethodGet, "/kernels/status?"+q.Encode(), nil, &out)
	return out, err
}

// Output returns the finished kernel's log. Only useful once a run has ended,
// which is exactly when it is worth reading: it is where a failed boot
// explains itself.
func (k *KaggleClient) Output(ctx context.Context, slug string) (string, error) {
	q := url.Values{"userName": {k.Username}, "kernelSlug": {slug}}
	var out struct {
		Log string `json:"log"`
	}
	if err := k.do(ctx, http.MethodGet, "/kernels/output?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	return out.Log, nil
}

// Ref is the "owner/slug" identifier Kaggle uses everywhere.
func (k *KaggleClient) Ref(slug string) string { return k.Username + "/" + slug }
