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

func TestPaneTargetHrefUsesPaneIDPath(t *testing.T) {
	got := paneTargetHref("13")
	if got != "/p/13" {
		t.Fatalf("paneTargetHref(13) = %q, want %q", got, "/p/13")
	}
}

func TestRootRedirectsToFirstPane(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneID(t, hub, "13")

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/p/13" {
		t.Fatalf("location = %q, want %q", got, "/p/13")
	}
}

func TestRootRedirectsToStatePageWhenNoPanesAvailable(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/state.html" {
		t.Fatalf("location = %q, want %q", got, "/api/state.html")
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
	waitForTargetPaneID(t, hub, "13")

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/contents/13", nil)
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
	waitForTargetPaneID(t, hub, "13")

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/contents/13?escapes=1", nil)
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
	waitForTargetPaneID(t, hub, "13")

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

func TestAPIStateReturnsStablePaneIDWithoutAbsolutePaneID(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneID(t, hub, "13")

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
	if got, ok := payload.Panes[0]["pane_id"]; !ok || got != "13" {
		t.Fatalf("stable pane id missing or unexpected: %v", payload.Panes[0])
	}
	if _, ok := payload.Panes[0]["id"]; ok {
		t.Fatalf("unexpected absolute pane id field present: %v", payload.Panes[0])
	}
}

func TestAPIDebugUnicodeCapturesLatestReport(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneID(t, hub, "13")

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	body := strings.NewReader(`{"renderer":"ghostty","url":"http://localhost/p/13?term=ghostty","user_agent":"ua","source":"pane_snapshot","pane_id":"13","data_length":32,"data_sample":"bad\ufffdchars"}`)
	postReq := httptest.NewRequest(http.MethodPost, "/api/debug/unicode", body)
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	h.ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusOK {
		t.Fatalf("post status = %d, body = %s", postRec.Code, postRec.Body.String())
	}

	var postResp map[string]any
	if err := json.Unmarshal(postRec.Body.Bytes(), &postResp); err != nil {
		t.Fatalf("decode post response: %v", err)
	}
	if _, ok := postResp["report_id"]; !ok {
		t.Fatalf("missing report_id in post response: %v", postResp)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/debug/unicode", nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	var report unicodeDebugRecord
	if err := json.Unmarshal(getRec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if report.Client.PaneID != "13" {
		t.Fatalf("client pane id = %q, want %q", report.Client.PaneID, "13")
	}
	if report.Server.TmuxPaneID != "%13" {
		t.Fatalf("server tmux pane id = %q, want %q", report.Server.TmuxPaneID, "%13")
	}
	if report.Server.PlainSample != "plain-line" {
		t.Fatalf("plain sample = %q, want %q", report.Server.PlainSample, "plain-line")
	}
	if report.Server.EscapedSample != "\u001b[31mred\u001b[0m" {
		t.Fatalf("escaped sample = %q, want escape-decorated output", report.Server.EscapedSample)
	}
}

func waitForTargetPaneID(t *testing.T, hub *wshub.Hub, paneID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, pane := range hub.CurrentTargetSessionPaneInfos() {
			if pane.PaneID == paneID {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pane %s did not appear in target session state", paneID)
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
	case line == "capture-pane -p -N -t %13":
		go func() {
			s.hub.BroadcastTmuxStdoutLine("%begin 2 2 0")
			s.hub.BroadcastTmuxStdoutLine("plain-line")
			s.hub.BroadcastTmuxStdoutLine("%end 2 2 0")
		}()
	case line == "capture-pane -p -e -N -t %13":
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
