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
	tmuxSocketName string
	tmuxSocketPath string
	term           string
	restartBackoff time.Duration
	restartMax     time.Duration
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatalf("wmux: %v", err)
	}

	if err := run(cfg); err != nil {
		log.Fatalf("wmux: %v", err)
	}
}

type envLookup func(string) string

func parseConfig() (config, error) {
	return parseConfigFrom(flag.CommandLine, os.Args[1:], os.Getenv)
}

func parseConfigFrom(fs *flag.FlagSet, args []string, getenv envLookup) (config, error) {
	cfg := config{}
	fs.StringVar(&cfg.listen, "listen", envOrLookup(getenv, "WMUX_LISTEN", "127.0.0.1:8080"), "HTTP listen address")
	fs.StringVar(&cfg.targetSession, "target-session", envOrLookup(getenv, "WMUX_TARGET_SESSION", "webui"), "tmux session to serve")
	fs.StringVar(&cfg.staticDir, "static-dir", envOrLookup(getenv, "WMUX_STATIC_DIR", ""), "optional static assets directory")
	fs.StringVar(&cfg.tmuxBin, "tmux-bin", envOrLookup(getenv, "WMUX_TMUX_BIN", "tmux"), "path to tmux binary")
	fs.StringVar(&cfg.tmuxSocketName, "tmux-socket-name", envOrLookup(getenv, "WMUX_TMUX_SOCKET_NAME", ""), "tmux socket name (maps to tmux -L)")
	fs.StringVar(&cfg.tmuxSocketPath, "tmux-socket-path", envOrLookup(getenv, "WMUX_TMUX_SOCKET_PATH", ""), "tmux socket path (maps to tmux -S)")
	fs.StringVar(&cfg.term, "term", envOrLookup(getenv, "WMUX_TERM", "ghostty"), "default terminal renderer for generated pane links (ghostty or xterm)")
	fs.DurationVar(&cfg.restartBackoff, "restart-backoff", durationEnvOrLookup(getenv, "WMUX_RESTART_BACKOFF", 500*time.Millisecond), "restart backoff base")
	fs.DurationVar(&cfg.restartMax, "restart-max-backoff", durationEnvOrLookup(getenv, "WMUX_RESTART_MAX_BACKOFF", 10*time.Second), "restart backoff max")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func normalizeAndValidateConfig(cfg config) (config, error) {
	cfg.targetSession = strings.TrimSpace(cfg.targetSession)
	if cfg.targetSession == "" {
		return cfg, errors.New("--target-session cannot be empty")
	}

	cfg.tmuxSocketName = strings.TrimSpace(cfg.tmuxSocketName)
	cfg.tmuxSocketPath = strings.TrimSpace(cfg.tmuxSocketPath)
	socket := tmuxproc.SocketTarget{Name: cfg.tmuxSocketName, Path: cfg.tmuxSocketPath}
	if err := socket.Validate(); err != nil {
		return cfg, errors.New("--tmux-socket-name and --tmux-socket-path are mutually exclusive")
	}

	cfg.term = normalizeDefaultTerm(cfg.term)
	if cfg.term == "" {
		return cfg, errors.New("--term must be one of: ghostty, xterm")
	}

	return cfg, nil
}

func run(cfg config) error {
	var err error
	cfg, err = normalizeAndValidateConfig(cfg)
	if err != nil {
		return err
	}

	socket := tmuxproc.SocketTarget{Name: cfg.tmuxSocketName, Path: cfg.tmuxSocketPath}
	autoCreateSession := len(socket.Args()) == 0

	if err := tmuxproc.CheckTmux(cfg.tmuxBin, socket); err != nil {
		return err
	}
	if autoCreateSession {
		if err := tmuxproc.EnsureSession(cfg.tmuxBin, socket, cfg.targetSession); err != nil {
			log.Printf("wmux: initial ensure target session failed: %v", err)
		}
	}

	hub := wshub.New(policy.Default(), cfg.targetSession)
	manager := tmuxproc.NewManager(tmuxproc.Config{
		TmuxBin:           cfg.tmuxBin,
		TargetSession:     cfg.targetSession,
		Socket:            socket,
		AutoCreateSession: autoCreateSession,
		BackoffBase:       cfg.restartBackoff,
		BackoffMax:        cfg.restartMax,
		OnStdoutLine:      hub.BroadcastTmuxStdoutLine,
		OnStderrLine:      hub.BroadcastTmuxStderrLine,
		OnConnected:       hub.BroadcastConnected,
		OnDisconnect:      hub.BroadcastDisconnected,
	})
	if err := hub.BindTmux(manager); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go manager.Run(ctx)

	handler, err := httpd.NewServer(httpd.Config{
		StaticDir:   cfg.staticDir,
		Hub:         hub,
		DefaultTerm: cfg.term,
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

	log.Printf("wmux listening on %s target-session=%s socket=%s", cfg.listen, cfg.targetSession, describeSocket(socket))
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func describeSocket(socket tmuxproc.SocketTarget) string {
	if socket.Name != "" {
		return fmt.Sprintf("name:%s", socket.Name)
	}
	if socket.Path != "" {
		return fmt.Sprintf("path:%s", socket.Path)
	}
	return "default"
}

func normalizeDefaultTerm(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "ghostty" || v == "xterm" {
		return v
	}
	return ""
}

func envOr(name, fallback string) string {
	return envOrLookup(os.Getenv, name, fallback)
}

func envOrLookup(getenv envLookup, name, fallback string) string {
	if v := strings.TrimSpace(getenv(name)); v != "" {
		return v
	}
	return fallback
}

func durationEnvOr(name string, fallback time.Duration) time.Duration {
	return durationEnvOrLookup(os.Getenv, name, fallback)
}

func durationEnvOrLookup(getenv envLookup, name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}
