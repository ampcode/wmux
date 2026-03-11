package tmuxproc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildTmuxArgsDefaultSocketUnchanged(t *testing.T) {
	got := buildTmuxArgs(SocketTarget{}, "attach-session", "-t", "dev")
	want := []string{"attach-session", "-t", "dev"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("buildTmuxArgs() = %v, want %v", got, want)
	}
}

func TestSocketTargetArgs(t *testing.T) {
	if got := strings.Join((SocketTarget{Name: "ovm"}).Args(), " "); got != "-L ovm" {
		t.Fatalf("name socket args = %q, want %q", got, "-L ovm")
	}
	if got := strings.Join((SocketTarget{Path: "/tmp/ovm.sock"}).Args(), " "); got != "-S /tmp/ovm.sock" {
		t.Fatalf("path socket args = %q, want %q", got, "-S /tmp/ovm.sock")
	}
}

func TestSocketTargetValidateRejectsMixedSocketSelectors(t *testing.T) {
	err := (SocketTarget{Name: "name", Path: "/tmp/path"}).Validate()
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestCheckTmuxUsesSocketFlags(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "tmux-args.log")
	script := writeFakeTmuxScript(t, `
	echo "$@" > "$WMUX_ARGS_LOG"
	exit 0
	`)
	t.Setenv("WMUX_ARGS_LOG", logPath)

	if err := CheckTmux(script, SocketTarget{Name: "ovm"}); err != nil {
		t.Fatalf("CheckTmux: %v", err)
	}

	got := strings.TrimSpace(readFile(t, logPath))
	if got != "-L ovm -V" {
		t.Fatalf("CheckTmux args = %q, want %q", got, "-L ovm -V")
	}
}

func TestEnsureSessionUsesSocketFlagsForHasAndCreate(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "tmux-args.log")
	script := writeFakeTmuxScript(t, `
	echo "$@" >> "$WMUX_ARGS_LOG"
	cmd="$1"
	if [ "$cmd" = "-L" ] || [ "$cmd" = "-S" ]; then
	  shift 2
	  cmd="$1"
	fi
	if [ "$cmd" = "has-session" ]; then
	  exit 1
	fi
	exit 0
	`)
	t.Setenv("WMUX_ARGS_LOG", logPath)

	if err := EnsureSession(script, SocketTarget{Path: "/tmp/ovm.sock"}, "dev"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(readFile(t, logPath)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 tmux calls, got %d (%q)", len(lines), lines)
	}
	if lines[0] != "-S /tmp/ovm.sock has-session -t dev" {
		t.Fatalf("has-session args = %q", lines[0])
	}
	if lines[1] != "-S /tmp/ovm.sock new-session -d -s dev" {
		t.Fatalf("new-session args = %q", lines[1])
	}
}

func TestRunOnceUsesSocketFlagsForAttach(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "tmux-args.log")
	script := writeFakeTmuxScript(t, `
	echo "$@" >> "$WMUX_ARGS_LOG"
	exit 0
	`)
	t.Setenv("WMUX_ARGS_LOG", logPath)

	m := NewManager(Config{
		TmuxBin:       script,
		TargetSession: "dev",
		Socket:        SocketTarget{Name: "ovm"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = m.runOnce(ctx)

	got := strings.TrimSpace(readFile(t, logPath))
	if got != "-L ovm -CC attach-session -t dev" {
		t.Fatalf("attach args = %q, want %q", got, "-L ovm -CC attach-session -t dev")
	}
}

func writeFakeTmuxScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-tmux.sh")
	script := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux script: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
