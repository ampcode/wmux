package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ampcode/wmux/internal/httpd"
	"github.com/ampcode/wmux/internal/policy"
	"github.com/ampcode/wmux/internal/tmuxproc"
	"github.com/ampcode/wmux/internal/wshub"
)

type config struct {
	listen         string
	targetSession  string
	staticDir      string
	tmuxBin        string
	restartBackoff time.Duration
	restartMax     time.Duration
}

func main() {
	cfg := parseConfig()
	if err := run(cfg); err != nil {
		log.Fatalf("wmux: %v", err)
	}
}

func parseConfig() config {
	cfg := config{}
	flag.StringVar(&cfg.listen, "listen", envOr("WMUX_LISTEN", "127.0.0.1:8080"), "HTTP listen address")
	flag.StringVar(&cfg.targetSession, "target-session", envOr("WMUX_TARGET_SESSION", "webui"), "tmux session to ensure and serve")
	flag.StringVar(&cfg.staticDir, "static-dir", envOr("WMUX_STATIC_DIR", ""), "optional static assets directory")
	flag.StringVar(&cfg.tmuxBin, "tmux-bin", envOr("WMUX_TMUX_BIN", "tmux"), "path to tmux binary")
	flag.DurationVar(&cfg.restartBackoff, "restart-backoff", durationEnvOr("WMUX_RESTART_BACKOFF", 500*time.Millisecond), "restart backoff base")
	flag.DurationVar(&cfg.restartMax, "restart-max-backoff", durationEnvOr("WMUX_RESTART_MAX_BACKOFF", 10*time.Second), "restart backoff max")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	if strings.TrimSpace(cfg.targetSession) == "" {
		return errors.New("--target-session cannot be empty")
	}

	if err := tmuxproc.CheckTmux(cfg.tmuxBin); err != nil {
		return err
	}
	if err := tmuxproc.EnsureSession(cfg.tmuxBin, cfg.targetSession); err != nil {
		return fmt.Errorf("ensure target session: %w", err)
	}

	hub := wshub.New(policy.Default(), cfg.targetSession)
	manager := tmuxproc.NewManager(tmuxproc.Config{
		TmuxBin:       cfg.tmuxBin,
		TargetSession: cfg.targetSession,
		BackoffBase:   cfg.restartBackoff,
		BackoffMax:    cfg.restartMax,
		OnStdoutLine:  hub.BroadcastTmuxStdoutLine,
		OnStderrLine:  hub.BroadcastTmuxStderrLine,
		OnRestart:     hub.BroadcastRestart,
	})
	if err := hub.BindTmux(manager); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go manager.Run(ctx)
	go hub.RequestStateSyncWithRetry()

	handler, err := httpd.NewServer(httpd.Config{
		StaticDir: cfg.staticDir,
		Hub:       hub,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{Addr: cfg.listen, Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("wmux listening on %s target-session=%s", cfg.listen, cfg.targetSession)
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func durationEnvOr(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}
