package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// checkSlug is deliberately its own notebook: the check must never disturb a
// runner that might be live.
const checkSlug = "gpuless-apicheck"

// checkKaggle exercises the real Kaggle API with the operator's credentials
// and reports what it found. It exists because the client in this repository
// was written against the documented endpoints and tested against fakes; this
// is what turns that into evidence.
//
// It pushes a CPU-only notebook with no internet and no datasets, so it costs
// nothing from the weekly GPU allowance.
func checkKaggle(dataDir string) error {
	user, key, source, err := kaggleCredentials(dataDir)
	if err != nil {
		return err
	}

	fmt.Println("gpuless — Kaggle API check")
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("credentials  %s (user %q, token %s)\n", source, user, redact(key))
	fmt.Printf("endpoint     %s\n\n", kaggleBaseURL)

	c := NewKaggleClient(user, key)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ok := true

	// 1. Do the credentials work at all?
	step("authentication", func() error { return c.Verify(ctx) }, &ok)

	// 2. Does a push with our exact field names get accepted? This is the
	//    call the panel depends on and the one most likely to have drifted.
	var pushURL string
	step("kernels/push", func() error {
		script := "print('gpuless api check ok')\n"
		u, err := c.Push(ctx, KernelPush{
			ID: c.Ref(checkSlug), Slug: checkSlug,
			NewTitle: "gpuless api check", Text: script,
			Language: "python", KernelType: "script", IsPrivate: true,
			EnableGPU: false, EnableInternet: false,
			DatasetDataSources: []string{}, CompetitionDataSources: []string{},
			KernelDataSources: []string{}, CategoryIDs: []string{},
		})
		pushURL = u
		return err
	}, &ok)
	if pushURL != "" {
		fmt.Printf("             notebook: %s\n", pushURL)
	}

	// 3. Does status report the run, and does it reach a terminal state?
	var last string
	step("kernels/status", func() error {
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			st, err := c.Status(ctx, checkSlug)
			if err != nil {
				return err
			}
			if st.Status != last {
				fmt.Printf("             status: %s\n", st.Status)
				last = st.Status
			}
			switch strings.ToLower(st.Status) {
			case "complete", "error", "cancelacknowledged":
				if st.FailureMessage != "" {
					fmt.Printf("             message: %s\n", st.FailureMessage)
				}
				return nil
			}
			time.Sleep(5 * time.Second)
		}
		return fmt.Errorf("still %q after four minutes — not a failure, just slow", last)
	}, &ok)

	// 4. Can we read the log back? This is how a failed boot explains itself.
	step("kernels/output", func() error {
		log, err := c.Output(ctx, checkSlug)
		if err != nil {
			return err
		}
		if !strings.Contains(log, "gpuless api check ok") {
			return fmt.Errorf("the log came back but without the expected line; got %d bytes", len(log))
		}
		return nil
	}, &ok)

	fmt.Println(strings.Repeat("-", 60))
	if ok {
		fmt.Println("All four calls behave as the panel expects.")
	} else {
		fmt.Println("Something differs from what the panel expects — the lines above say what.")
	}
	fmt.Printf("\nNothing here contains your token. Delete %s/%s on Kaggle when you are done.\n",
		user, checkSlug)
	if !ok {
		return fmt.Errorf("the API check did not pass cleanly")
	}
	return nil
}

func step(name string, fn func() error, ok *bool) {
	fmt.Printf("%-13s ", name)
	start := time.Now()
	if err := fn(); err != nil {
		*ok = false
		fmt.Printf("FAILED  (%s)\n             %v\n", time.Since(start).Round(time.Millisecond), err)
		return
	}
	fmt.Printf("ok      (%s)\n", time.Since(start).Round(time.Millisecond))
}

// kaggleCredentials looks in the three places an operator might have put them,
// in the order that causes the least surprise.
func kaggleCredentials(dataDir string) (user, key, source string, err error) {
	if u, k := os.Getenv("KAGGLE_USERNAME"), os.Getenv("KAGGLE_KEY"); u != "" && k != "" {
		return u, k, "environment", nil
	}

	home, herr := os.UserHomeDir()
	if herr == nil {
		path := filepath.Join(home, ".kaggle", "kaggle.json")
		if b, rerr := os.ReadFile(path); rerr == nil {
			var creds struct {
				Username string `json:"username"`
				Key      string `json:"key"`
			}
			if json.Unmarshal(b, &creds) == nil && creds.Username != "" && creds.Key != "" {
				return creds.Username, creds.Key, path, nil
			}
		}
	}

	if store, serr := OpenStore(filepath.Join(dataDir, "gpuless.db")); serr == nil {
		defer store.Close()
		cfg := store.Config()
		if cfg.KaggleUser != "" && cfg.KaggleKey != "" {
			return cfg.KaggleUser, cfg.KaggleKey, "the panel's database", nil
		}
	}

	return "", "", "", fmt.Errorf("no credentials found — set KAGGLE_USERNAME and KAGGLE_KEY, " +
		"or put kaggle.json in ~/.kaggle, or run this against a panel that is already set up")
}

func redact(s string) string {
	if len(s) < 8 {
		return "set"
	}
	return s[:3] + strings.Repeat("*", 8) + s[len(s)-2:]
}
