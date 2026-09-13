// Command gpuless is a self-hosted panel that runs image and voice models on
// the free weekly GPU hours of the operator's own Kaggle account.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gpuless:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr      = flag.String("addr", envOr("GPULESS_ADDR", ":8080"), "address to listen on")
		dataDir   = flag.String("data", envOr("GPULESS_DATA", "./data"), "directory for the database and generated media")
		wfDir     = flag.String("workflows", envOr("GPULESS_WORKFLOWS", ""), "directory of ComfyUI workflow overrides")
		behind    = flag.Bool("behind-proxy", os.Getenv("GPULESS_BEHIND_PROXY") == "1", "trust X-Forwarded-For and X-Forwarded-Proto")
		showVer   = flag.Bool("version", false, "print the version and exit")
		checkKag  = flag.Bool("check-kaggle", false, "exercise the Kaggle API with your credentials and report what it does")
		logFormat = flag.String("log", envOr("GPULESS_LOG", "text"), "log format: text or json")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("gpuless", version)
		return nil
	}
	if *checkKag {
		return checkKaggle(*dataDir)
	}
	trustProxy = *behind

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	log := slog.New(handler)

	if err := os.MkdirAll(*dataDir, 0o750); err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	store, err := OpenStore(filepath.Join(*dataDir, "gpuless.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	// A run that was in flight when the process died can never finish.
	store.AbandonRuns()
	store.PurgeSessions()

	workflows, err := LoadWorkflows(*wfDir)
	if err != nil {
		return fmt.Errorf("workflows: %w", err)
	}
	log.Info("workflows loaded", "names", workflows.Names(), "overrides", *wfDir)

	app, err := NewApp(store, workflows, filepath.Join(*dataDir, "media"), log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A kernel can outlive the panel; pick it back up rather than leaving it
	// running unwatched on Kaggle.
	app.kernel.Adopt(ctx, store.Config())
	go app.kernel.Supervise(ctx, app.cfg)
	go housekeeping(ctx, store)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// Generous: uploading a voice sample over a slow line is legitimate.
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("gpuless listening", "addr", *addr, "version", version,
			"setup", store.GetBool(kSetupDone, false))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		log.Warn("http shutdown", "err", err)
	}

	// Let generations finish writing before the process goes away; the kernel
	// is deliberately left running, because it bills by wall clock either way
	// and stops itself when it goes idle.
	app.Wait()
	return nil
}

// housekeeping does the small periodic chores that keep the database tidy.
func housekeeping(ctx context.Context, store *Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			store.PurgeSessions()
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
