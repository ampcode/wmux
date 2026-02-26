package httpd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ampcode/wmux/internal/policy"
	"github.com/ampcode/wmux/internal/wshub"
)

func TestPaneTargetHrefUsesPaneIDPath(t *testing.T) {
	got := paneTargetHref("13", "ghostty")
	if got != "/p/13?term=ghostty" {
		t.Fatalf("paneTargetHref(13, ghostty) = %q, want %q", got, "/p/13?term=ghostty")
	}
}

func TestRootReturnsJSONHypermediaWithFollowUpLinks(t *testing.T) {
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
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type = %q, want application/json", got)
	}

	var payload struct {
		Resource string `json:"resource"`
		Links    []struct {
			Rel       string `json:"rel"`
			Href      string `json:"href"`
			Method    string `json:"method"`
			Templated bool   `json:"templated"`
		} `json:"links"`
		Actions []struct {
			Name   string `json:"name"`
			Method string `json:"method"`
			Href   string `json:"href"`
		} `json:"actions"`
		Panes []struct {
			PaneID string `json:"pane_id"`
			Links  []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Resource != "wmux" {
		t.Fatalf("resource = %q, want %q", payload.Resource, "wmux")
	}
	if !hasDocLink(payload.Links, "create-pane", "/api/panes", "POST") {
		t.Fatalf("missing create-pane link: %#v", payload.Links)
	}
	if !hasDocLink(payload.Links, "pane-contents", "/api/contents/{pane_id}{?escapes}", "GET") {
		t.Fatalf("missing pane contents template link: %#v", payload.Links)
	}
	if !hasDocLink(payload.Links, "pane-resource", "/api/panes/{pane_id}", "GET") {
		t.Fatalf("missing pane-resource template link: %#v", payload.Links)
	}
	if !hasDocAction(payload.Actions, "create-pane", "/api/panes", "POST") {
		t.Fatalf("missing create-pane action: %#v", payload.Actions)
	}
	if len(payload.Panes) == 0 || payload.Panes[0].PaneID != "13" {
		t.Fatalf("unexpected panes payload: %#v", payload.Panes)
	}
	if !hasPaneLink(payload.Panes[0].Links, "contents", "/api/contents/13") {
		t.Fatalf("missing per-pane contents link: %#v", payload.Panes[0].Links)
	}
	if !hasPaneLink(payload.Panes[0].Links, "self", "/api/panes/13") {
		t.Fatalf("missing per-pane self link: %#v", payload.Panes[0].Links)
	}
	if !hasPaneLink(payload.Panes[0].Links, "terminal", "/p/13?term=ghostty") {
		t.Fatalf("missing per-pane terminal link: %#v", payload.Panes[0].Links)
	}
}

func TestRootReturnsHTMLHypermediaWhenRequested(t *testing.T) {
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
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("content-type = %q, want text/html", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "POST") || !strings.Contains(body, "/api/panes") {
		t.Fatalf("html missing create-pane affordance: %s", body)
	}
	if !strings.Contains(body, `id="create-pane-form"`) {
		t.Fatalf("html missing create-pane form: %s", body)
	}
	if !strings.Contains(body, `id="create-pane-result"`) {
		t.Fatalf("html missing create-pane form result region: %s", body)
	}
	if !strings.Contains(body, "/api/contents/{pane_id}{?escapes}") {
		t.Fatalf("html missing pane contents template: %s", body)
	}
	if !strings.Contains(body, "example:") {
		t.Fatalf("html missing concrete link examples: %s", body)
	}
	if !strings.Contains(body, "/p/13?term=ghostty") {
		t.Fatalf("html missing pane terminal link: %s", body)
	}
}

func TestRootUsesConfiguredDefaultTermInHypermediaLinks(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}
	if err := hub.RequestStateSync(); err != nil {
		t.Fatalf("RequestStateSync: %v", err)
	}
	waitForTargetPaneID(t, hub, "13")

	h, err := NewServer(Config{Hub: hub, DefaultTerm: "xterm"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		DefaultTerm string `json:"default_term"`
		Panes       []struct {
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.DefaultTerm != "xterm" {
		t.Fatalf("default_term = %q, want %q", payload.DefaultTerm, "xterm")
	}
	if len(payload.Panes) == 0 || !hasPaneLink(payload.Panes[0].Links, "terminal", "/p/13?term=xterm") {
		t.Fatalf("pane terminal link did not use xterm: %#v", payload.Panes)
	}
}

func TestPaneRouteAddsMissingTermQueryUsingDefault(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	h, err := NewServer(Config{Hub: hub, DefaultTerm: "xterm"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/p/13", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/p/13?term=xterm" {
		t.Fatalf("location = %q, want %q", got, "/p/13?term=xterm")
	}
}

func TestPaneRouteNormalizesInvalidTermQueryUsingDefault(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	h, err := NewServer(Config{Hub: hub, DefaultTerm: "xterm"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/p/13?term=unknown&foo=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/p/13?foo=1&term=xterm" {
		t.Fatalf("location = %q, want %q", got, "/p/13?foo=1&term=xterm")
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

func TestAPIPaneReturnsSinglePaneResource(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/panes/13", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Resource string `json:"resource"`
		Panes    []struct {
			PaneID string `json:"pane_id"`
			Links  []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Resource != "wmux-pane" {
		t.Fatalf("resource = %q, want %q", payload.Resource, "wmux-pane")
	}
	if len(payload.Panes) != 1 || payload.Panes[0].PaneID != "13" {
		t.Fatalf("unexpected panes payload: %#v", payload.Panes)
	}
	if !hasPaneLink(payload.Panes[0].Links, "self", "/api/panes/13") {
		t.Fatalf("pane self link missing: %#v", payload.Panes[0].Links)
	}
}

func TestAPIPaneReturnsNotFoundForUnknownPane(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/panes/404", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIPanesCreatesPaneWithOptions(t *testing.T) {
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

	body := strings.NewReader(`{"env":{"FOO":"bar","ALPHA":"1"},"cwd":"/tmp/work dir","cmd":["bash","-lc","echo hi"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/panes", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/panes/14" {
		t.Fatalf("location = %q, want %q", got, "/api/panes/14")
	}

	var payload struct {
		Resource string `json:"resource"`
		Actions  []struct {
			Name   string         `json:"name"`
			Schema map[string]any `json:"schema"`
		} `json:"actions"`
		Panes []struct {
			PaneID string `json:"pane_id"`
			Links  []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Resource != "wmux-pane" {
		t.Fatalf("resource = %q, want %q", payload.Resource, "wmux-pane")
	}
	if len(payload.Panes) != 1 || payload.Panes[0].PaneID != "14" {
		t.Fatalf("unexpected panes payload: %#v", payload.Panes)
	}
	if !hasPaneLink(payload.Panes[0].Links, "self", "/api/panes/14") {
		t.Fatalf("missing self link in create response: %#v", payload.Panes[0].Links)
	}
	if len(payload.Actions) == 0 || payload.Actions[0].Name != "create-pane" {
		t.Fatalf("missing create-pane action: %#v", payload.Actions)
	}
	if _, ok := payload.Actions[0].Schema["properties"]; !ok {
		t.Fatalf("missing machine-readable schema in action: %#v", payload.Actions[0].Schema)
	}

	line := tmux.LastCommandWithPrefix("split-window ")
	if line == "" {
		t.Fatalf("missing split-window command")
	}
	if !strings.Contains(line, "split-window -P -F '#{pane_id}' -t webui") {
		t.Fatalf("unexpected split-window command: %q", line)
	}
	if !strings.Contains(line, "-c '/tmp/work dir'") {
		t.Fatalf("split-window missing cwd: %q", line)
	}
	if !strings.Contains(line, "-e 'ALPHA=1' -e 'FOO=bar'") {
		t.Fatalf("split-window missing env vars: %q", line)
	}
	if !strings.Contains(line, "'bash -lc '\\''echo hi'\\'''") {
		t.Fatalf("split-window missing cmd argv: %q", line)
	}
}

func TestAPIPanesRejectsInvalidEnvKey(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	tmux := &scriptedTmuxSender{hub: hub}
	if err := hub.BindTmux(tmux); err != nil {
		t.Fatalf("BindTmux: %v", err)
	}

	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	body := strings.NewReader(`{"env":{"BAD-KEY":"v"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/panes", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if line := tmux.LastCommandWithPrefix("split-window "); line != "" {
		t.Fatalf("unexpected split-window command: %q", line)
	}
}

func TestAPIPanesRejectsNonPost(t *testing.T) {
	hub := wshub.New(policy.Default(), "webui")
	h, err := NewServer(Config{Hub: hub})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/panes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
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

func hasDocLink(links []struct {
	Rel       string "json:\"rel\""
	Href      string "json:\"href\""
	Method    string "json:\"method\""
	Templated bool   "json:\"templated\""
}, rel, href, method string) bool {
	for _, link := range links {
		if link.Rel == rel && link.Href == href && link.Method == method {
			return true
		}
	}
	return false
}

func hasDocAction(actions []struct {
	Name   string "json:\"name\""
	Method string "json:\"method\""
	Href   string "json:\"href\""
}, name, href, method string) bool {
	for _, action := range actions {
		if action.Name == name && action.Href == href && action.Method == method {
			return true
		}
	}
	return false
}

func hasPaneLink(links []struct {
	Rel  string "json:\"rel\""
	Href string "json:\"href\""
}, rel, href string) bool {
	for _, link := range links {
		if link.Rel == rel && link.Href == href {
			return true
		}
	}
	return false
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
	mu  sync.Mutex

	lines []string
}

func (s *scriptedTmuxSender) Send(line string) error {
	s.mu.Lock()
	s.lines = append(s.lines, line)
	s.mu.Unlock()

	switch {
	case strings.HasPrefix(line, "list-panes "):
		go func() {
			s.hub.BroadcastTmuxStdoutLine("%begin 1 1 0")
			s.hub.BroadcastTmuxStdoutLine("__WMUX___pane\twebui\t%13\t@1\t0\t1\t0\t0\t120\t40\tbash\tbash")
			s.hub.BroadcastTmuxStdoutLine("%end 1 1 0")
		}()
	case strings.HasPrefix(line, "split-window "):
		go func() {
			s.hub.BroadcastTmuxStdoutLine("%begin 5 5 0")
			s.hub.BroadcastTmuxStdoutLine("%14")
			s.hub.BroadcastTmuxStdoutLine("%end 5 5 0")
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

func (s *scriptedTmuxSender) LastCommandWithPrefix(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(s.lines[i], prefix) {
			return s.lines[i]
		}
	}
	return ""
}
