package main

import (
	"flag"
	"io"
	"testing"
)

func TestParseConfigFromParsesSocketNameFlag(t *testing.T) {
	fs := flag.NewFlagSet("wmux-test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	cfg, err := parseConfigFrom(fs, []string{"--tmux-socket-name", "overmind-sock", "--target-session", "dev"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parseConfigFrom: %v", err)
	}
	if cfg.tmuxSocketName != "overmind-sock" {
		t.Fatalf("tmuxSocketName = %q, want %q", cfg.tmuxSocketName, "overmind-sock")
	}
	if cfg.tmuxSocketPath != "" {
		t.Fatalf("tmuxSocketPath = %q, want empty", cfg.tmuxSocketPath)
	}
}

func TestParseConfigFromReadsSocketPathFromEnv(t *testing.T) {
	fs := flag.NewFlagSet("wmux-test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	env := map[string]string{
		"WMUX_TMUX_SOCKET_PATH": "/tmp/overmind.sock",
	}
	cfg, err := parseConfigFrom(fs, nil, func(name string) string { return env[name] })
	if err != nil {
		t.Fatalf("parseConfigFrom: %v", err)
	}
	if cfg.tmuxSocketPath != "/tmp/overmind.sock" {
		t.Fatalf("tmuxSocketPath = %q, want %q", cfg.tmuxSocketPath, "/tmp/overmind.sock")
	}
}

func TestNormalizeAndValidateConfigRejectsBothSocketFlags(t *testing.T) {
	_, err := normalizeAndValidateConfig(config{
		targetSession:  "dev",
		term:           "ghostty",
		tmuxSocketName: "sock-a",
		tmuxSocketPath: "/tmp/sock-b",
	})
	if err == nil {
		t.Fatalf("expected mutually-exclusive socket flag validation error")
	}
}

func TestNormalizeAndValidateConfigAllowsDefaultSocket(t *testing.T) {
	cfg, err := normalizeAndValidateConfig(config{
		targetSession: "dev",
		term:          "xterm",
	})
	if err != nil {
		t.Fatalf("normalizeAndValidateConfig: %v", err)
	}
	if cfg.tmuxSocketName != "" || cfg.tmuxSocketPath != "" {
		t.Fatalf("expected empty socket targeting in default mode: %#v", cfg)
	}
}
