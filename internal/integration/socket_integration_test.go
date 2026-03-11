package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ampcode/wmux/internal/httpd"
	"github.com/ampcode/wmux/internal/policy"
	"github.com/ampcode/wmux/internal/tmuxproc"
	"github.com/ampcode/wmux/internal/wshub"
)

type stateDocument struct {
	Panes []struct {
		PaneID      string `json:"pane_id"`
		SessionName string `json:"session_name"`
		WindowIndex int    `json:"window_index"`
		WindowName  string `json:"window_name"`
	} `json:"panes"`
	Unavailable *struct {
		Reason string `json:"reason"`
	} `json:"unavailable,omitempty"`
}

type wmuxHarness struct {
	server  *httptest.Server
	cancel  context.CancelFunc
	session string
	socket  tmuxproc.SocketTarget
}

func TestNonDefaultSocketServesExternallyCreatedSession(t *testing.T) {
	requireTmux(t)

	socket := tmuxproc.SocketTarget{Path: fmt.Sprintf("%s/non-default.sock", t.TempDir())}
	session := uniqueSessionName()

	tmuxMust(t, socket, "new-session", "-d", "-s", session, "-n", "web")
	tmuxMust(t, socket, "split-window", "-t", session)
	defer tmuxTry(socket, "kill-session", "-t", session)
	defer tmuxTry(socket, "kill-server")

	h := startHarness(t, session, socket, false)
	defer h.Close()

	state := waitForState(t, h.server.URL, 15*time.Second, func(s stateDocument) bool {
		return len(s.Panes) >= 2
	})

	if state.Unavailable != nil {
		t.Fatalf("unexpected unavailable state: %+v", state.Unavailable)
	}
	for _, pane := range state.Panes {
		if pane.SessionName != session {
			t.Fatalf("pane from unexpected session: %+v", pane)
		}
		if strings.TrimSpace(pane.WindowName) == "" {
			t.Fatalf("window_name should be populated: %+v", pane)
		}
	}
}

func TestExternalWindowLifecycleIsReflectedInState(t *testing.T) {
	requireTmux(t)

	socket := tmuxproc.SocketTarget{Path: fmt.Sprintf("%s/lifecycle.sock", t.TempDir())}
	session := uniqueSessionName()
	tmuxMust(t, socket, "new-session", "-d", "-s", session, "-n", "web")
	defer tmuxTry(socket, "kill-session", "-t", session)
	defer tmuxTry(socket, "kill-server")

	h := startHarness(t, session, socket, false)
	defer h.Close()

	waitForState(t, h.server.URL, 15*time.Second, func(s stateDocument) bool {
		return len(s.Panes) >= 1
	})

	tmuxMust(t, socket, "new-window", "-t", session, "-n", "api")
	tmuxMust(t, socket, "split-window", "-t", session+":api")
	waitForState(t, h.server.URL, 15*time.Second, func(s stateDocument) bool {
		for _, pane := range s.Panes {
			if pane.WindowName == "api" {
				return true
			}
		}
		return false
	})

	tmuxMust(t, socket, "kill-window", "-t", session+":1")
	waitForState(t, h.server.URL, 15*time.Second, func(s stateDocument) bool {
		for _, pane := range s.Panes {
			if pane.WindowIndex == 1 {
				return false
			}
		}
		return true
	})
}

func TestUnavailableStateRecoversWhenSessionAppears(t *testing.T) {
	requireTmux(t)

	socket := tmuxproc.SocketTarget{Path: fmt.Sprintf("%s/recovery.sock", t.TempDir())}
	session := uniqueSessionName()
	defer tmuxTry(socket, "kill-session", "-t", session)
	defer tmuxTry(socket, "kill-server")

	h := startHarness(t, session, socket, false)
	defer h.Close()

	waitForState(t, h.server.URL, 15*time.Second, func(s stateDocument) bool {
		return s.Unavailable != nil && strings.TrimSpace(s.Unavailable.Reason) != ""
	})

	tmuxMust(t, socket, "new-session", "-d", "-s", session, "-n", "web")
	state := waitForState(t, h.server.URL, 20*time.Second, func(s stateDocument) bool {
		return s.Unavailable == nil && len(s.Panes) > 0
	})
	if len(state.Panes) == 0 {
		t.Fatalf("expected panes after recovery")
	}
}

func TestDefaultSocketModeStillAutoCreatesTargetSession(t *testing.T) {
	requireTmux(t)

	session := uniqueSessionName()
	tmuxTry(tmuxproc.SocketTarget{}, "kill-session", "-t", session)
	defer tmuxTry(tmuxproc.SocketTarget{}, "kill-session", "-t", session)

	h := startHarness(t, session, tmuxproc.SocketTarget{}, true)
	defer h.Close()

	state := waitForState(t, h.server.URL, 15*time.Second, func(s stateDocument) bool {
		if len(s.Panes) == 0 {
			return false
		}
		for _, pane := range s.Panes {
			if pane.SessionName == session {
				return true
			}
		}
		return false
	})

	if len(state.Panes) == 0 {
		t.Fatalf("expected default mode to expose created session panes")
	}
}

func startHarness(t *testing.T, session string, socket tmuxproc.SocketTarget, autoCreate bool) *wmuxHarness {
	t.Helper()

	hub := wshub.New(policy.Default(), session)
	manager := tmuxproc.NewManager(tmuxproc.Config{
		TmuxBin:           "tmux",
		TargetSession:     session,
		Socket:            socket,
		AutoCreateSession: autoCreate,
		BackoffBase:       100 * time.Millisecond,
		BackoffMax:        time.Second,
		OnStdoutLine:      hub.BroadcastTmuxStdoutLine,
		OnStderrLine:      hub.BroadcastTmuxStderrLine,
		OnConnected:       hub.BroadcastConnected,
		OnDisconnect:      hub.BroadcastDisconnected,
	})
	if err := hub.BindTmux(manager); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}

	handler, err := httpd.NewServer(httpd.Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go manager.Run(ctx)

	return &wmuxHarness{
		server:  httptest.NewServer(handler),
		cancel:  cancel,
		session: session,
		socket:  socket,
	}
}

func (h *wmuxHarness) Close() {
	h.cancel()
	h.server.Close()
}

func waitForState(t *testing.T, baseURL string, timeout time.Duration, condition func(stateDocument) bool) stateDocument {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last stateDocument
	for time.Now().Before(deadline) {
		state, err := fetchState(baseURL)
		if err == nil {
			last = state
			if condition(state) {
				return state
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for wmux state condition; last state=%+v", last)
	return stateDocument{}
}

func fetchState(baseURL string) (stateDocument, error) {
	resp, err := http.Get(baseURL + "/api/state.json")
	if err != nil {
		return stateDocument{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return stateDocument{}, fmt.Errorf("state status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var state stateDocument
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return stateDocument{}, err
	}
	return state, nil
}

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}
}

func tmuxMust(t *testing.T, socket tmuxproc.SocketTarget, args ...string) string {
	t.Helper()
	cmd := exec.Command("tmux", append(socket.Args(), args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s failed: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func tmuxTry(socket tmuxproc.SocketTarget, args ...string) {
	cmd := exec.Command("tmux", append(socket.Args(), args...)...)
	_, _ = cmd.CombinedOutput()
}

func uniqueSessionName() string {
	return fmt.Sprintf("wmux-it-%d", time.Now().UnixNano())
}
