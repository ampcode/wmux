package httpd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ampcode/wmux/internal/policy"
	"github.com/ampcode/wmux/internal/wshub"
)

func TestPaneTargetHrefUsesPaneNumberPath(t *testing.T) {
	got := paneTargetHref(13)
	if got != "/p/13" {
		t.Fatalf("paneTargetHref(13) = %q, want %q", got, "/p/13")
	}
}

func TestAPIContentsReturnsRawPlainPaneContents(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneNumber(t, hub, 0)

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/contents/0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("content-type = %q, want text/plain", got)
	}
	if got := rec.Body.String(); got != "plain-line" {
		t.Fatalf("body = %q, want %q", got, "plain-line")
	}
}

func TestAPIContentsReturnsRawEscapedPaneContents(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneNumber(t, hub, 0)

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/contents/0?escapes=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("content-type = %q, want text/plain", got)
	}
	if got := rec.Body.String(); got != "\u001b[31mred\u001b[0m" {
		t.Fatalf("body = %q, want escape-decorated output", got)
	}
}

func TestAPIContentsReturnsNotFoundForUnknownPane(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneNumber(t, hub, 0)

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/contents/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIStateReturnsPaneNumberNotAbsolutePaneID(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneNumber(t, hub, 0)

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/state.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Panes []map[string]any `json:"panes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Panes) == 0 {
		t.Fatalf("expected at least one pane")
	}
	if _, ok := payload.Panes[0]["pane"]; !ok {
		t.Fatalf("pane number field missing: %v", payload.Panes[0])
	}
	if _, ok := payload.Panes[0]["id"]; ok {
		t.Fatalf("unexpected absolute pane id field present: %v", payload.Panes[0])
	}
}

func waitForTargetPaneNumber(t *testing.T, hub *wshub.Hub, paneNumber int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, pane := range hub.CurrentTargetSessionPaneInfos() {
			if pane.Pane == paneNumber {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pane %d did not appear in target session state", paneNumber)
}

type scriptedTmuxSender struct {
	hub *wshub.Hub
}

func (s *scriptedTmuxSender) Send(line string) error {
	switch {
	case strings.HasPrefix(line, "list-panes "):
		go func() {
			s.hub.BroadcastTmuxStdoutLine("%begin 1 1 0")
			s.hub.BroadcastTmuxStdoutLine("__WMUX___pane\twebui\t%13\t@1\t0\t1\t0\t0\t120\t40\tbash\tbash")
			s.hub.BroadcastTmuxStdoutLine("%end 1 1 0")
		}()
	case line == "capture-pane -p -t %13":
		go func() {
			s.hub.BroadcastTmuxStdoutLine("%begin 2 2 0")
			s.hub.BroadcastTmuxStdoutLine("plain-line")
			s.hub.BroadcastTmuxStdoutLine("%end 2 2 0")
		}()
	case line == "capture-pane -p -e -t %13":
		go func() {
			s.hub.BroadcastTmuxStdoutLine("%begin 3 3 0")
			s.hub.BroadcastTmuxStdoutLine("\u001b[31mred\u001b[0m")
			s.hub.BroadcastTmuxStdoutLine("%end 3 3 0")
		}()
	default:
		go func() {
			s.hub.BroadcastTmuxStdoutLine("%begin 4 4 0")
			s.hub.BroadcastTmuxStdoutLine("%error 4 4 0")
		}()
	}
	return nil
}
