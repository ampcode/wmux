package httpd

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ampcode/wmux/internal/assets"
	"github.com/ampcode/wmux/internal/wshub"
)

type Config struct {
	StaticDir   string
	Hub         *wshub.Hub
	DefaultTerm string
}

func NewServer(cfg Config) (http.Handler, error) {
	defaultTerm := normalizeDefaultTerm(cfg.DefaultTerm)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", cfg.Hub.HandleWS)
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) { serveAPIState(w, r, cfg.Hub, defaultTerm) })
	mux.HandleFunc("/api/state.json", func(w http.ResponseWriter, r *http.Request) { serveAPIState(w, r, cfg.Hub, defaultTerm) })
	mux.HandleFunc("/api/state.html", func(w http.ResponseWriter, r *http.Request) { serveAPIState(w, r, cfg.Hub, defaultTerm) })
	mux.HandleFunc("/api/contents/", func(w http.ResponseWriter, r *http.Request) { serveAPIContents(w, r, cfg.Hub) })
	mux.HandleFunc("/api/panes", func(w http.ResponseWriter, r *http.Request) { serveAPIPanes(w, r, cfg.Hub, defaultTerm) })
	mux.HandleFunc("/api/debug/unicode", func(w http.ResponseWriter, r *http.Request) { serveAPIDebugUnicode(w, r, cfg.Hub) })
	mux.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		if redirectURL, ok := ensureTermQuery(r, defaultTerm); ok {
			http.Redirect(w, r, redirectURL, http.StatusFound)
			return
		}
		serveIndex(w, r, cfg.StaticDir)
	})
	mux.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) {
		if redirectURL, ok := ensureTermQuery(r, defaultTerm); ok {
			http.Redirect(w, r, redirectURL, http.StatusFound)
			return
		}
		serveIndex(w, r, cfg.StaticDir)
	})

	staticHandler, err := staticHandler(cfg.StaticDir)
	if err != nil {
		return nil, err
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			redirectToFirstPane(w, r, cfg.Hub, defaultTerm)
			return
		}
		staticHandler.ServeHTTP(w, r)
	}))
	return mux, nil
}

func staticHandler(staticDir string) (http.Handler, error) {
	if staticDir != "" {
		return http.FileServer(http.Dir(staticDir)), nil
	}
	sub, err := fs.Sub(assets.Web, "web")
	if err != nil {
		return nil, err
	}
	return http.FileServerFS(sub), nil
}

func serveIndex(w http.ResponseWriter, _ *http.Request, staticDir string) {
	if staticDir != "" {
		f, err := os.Open(filepath.Join(staticDir, "index.html"))
		if err != nil {
			http.Error(w, "index.html not found", http.StatusNotFound)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(w, f)
		return
	}
	b, err := fs.ReadFile(assets.Web, "web/index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func serveAPIState(w http.ResponseWriter, r *http.Request, hub *wshub.Hub, defaultTerm string) {
	panes := hub.CurrentTargetSessionPaneInfos()
	format := negotiateStateFormat(r)

	if format == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		rows := make([]struct {
			Title string
			Size  string
			Href  string
		}, 0, len(panes))
		for _, pane := range panes {
			title := pane.Name
			if strings.TrimSpace(title) == "" {
				title = fmt.Sprintf("pane %s", pane.PaneID)
			}
			rows = append(rows, struct {
				Title string
				Size  string
				Href  string
			}{
				Title: title,
				Size:  fmt.Sprintf("%dx%d", pane.Width, pane.Height),
				Href:  paneTargetHref(pane.PaneID, defaultTerm),
			})
		}
		_ = stateHTMLTemplate.Execute(w, struct {
			Panes []struct {
				Title string
				Size  string
				Href  string
			}
		}{Panes: rows})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Panes any `json:"panes"`
	}{Panes: panes})
}

func serveAPIContents(w http.ResponseWriter, r *http.Request, hub *wshub.Hub) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	paneID, ok := parsePanePathID(r.URL.EscapedPath(), "/api/contents/")
	if !ok {
		http.NotFound(w, r)
		return
	}

	tmuxPaneID, found := hub.TargetSessionPaneIDByPublicID(paneID)
	if !found {
		http.Error(w, "pane not found", http.StatusNotFound)
		return
	}

	withEscapes := parseEscapesFlag(r)
	content, err := hub.CapturePaneContent(tmuxPaneID, withEscapes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, content)
}

type createPaneRequest struct {
	Env map[string]string `json:"env"`
	Cwd string            `json:"cwd"`
	Cmd []string          `json:"cmd"`
}

func serveAPIPanes(w http.ResponseWriter, r *http.Request, hub *wshub.Hub, defaultTerm string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req createPaneRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if err != io.EOF {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	} else if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := validateCreatePaneRequest(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pane, err := hub.CreatePane(wshub.CreatePaneOptions{
		Env: req.Env,
		Cwd: req.Cwd,
		Cmd: req.Cmd,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", paneTargetHref(pane.PaneID, defaultTerm))
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(struct {
		PaneID string `json:"pane_id"`
	}{
		PaneID: pane.PaneID,
	})
}

func validateCreatePaneRequest(req createPaneRequest) error {
	if req.Cwd != "" && strings.TrimSpace(req.Cwd) == "" {
		return fmt.Errorf("cwd cannot be blank")
	}
	for key := range req.Env {
		if !isValidEnvKey(key) {
			return fmt.Errorf("invalid env key: %q", key)
		}
	}
	return nil
}

func isValidEnvKey(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		ch := v[i]
		isLetter := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
		isDigit := ch >= '0' && ch <= '9'
		if i == 0 {
			if !isLetter && ch != '_' {
				return false
			}
			continue
		}
		if !isLetter && !isDigit && ch != '_' {
			return false
		}
	}
	return true
}

func redirectToFirstPane(w http.ResponseWriter, r *http.Request, hub *wshub.Hub, defaultTerm string) {
	if href, ok := firstTargetPaneHref(hub, defaultTerm); ok {
		http.Redirect(w, r, href, http.StatusFound)
		return
	}
	http.Redirect(w, r, statePageHref(defaultTerm), http.StatusFound)
}

func firstTargetPaneHref(hub *wshub.Hub, defaultTerm string) (string, bool) {
	panes := hub.CurrentTargetSessionPaneInfos()
	if len(panes) == 0 {
		return "", false
	}
	firstPane := panes[0]
	for _, pane := range panes[1:] {
		if pane.PaneIndex < firstPane.PaneIndex {
			firstPane = pane
		}
	}
	return paneTargetHref(firstPane.PaneID, defaultTerm), true
}

func statePageHref(defaultTerm string) string {
	v := url.Values{}
	v.Set("term", normalizeDefaultTerm(defaultTerm))
	return "/api/state.html?" + v.Encode()
}

func ensureTermQuery(r *http.Request, defaultTerm string) (string, bool) {
	query := r.URL.Query()
	current := strings.ToLower(strings.TrimSpace(query.Get("term")))
	desired := normalizeDefaultTerm(current)
	if current != "ghostty" && current != "xterm" {
		desired = normalizeDefaultTerm(defaultTerm)
	}
	if current == desired {
		return "", false
	}
	query.Set("term", desired)
	return r.URL.Path + "?" + query.Encode(), true
}

func parsePanePathID(escapedPath, prefix string) (string, bool) {
	if !strings.HasPrefix(escapedPath, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(escapedPath, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	id := strings.TrimSpace(raw)
	if id == "" || strings.HasPrefix(id, "%") {
		return "", false
	}
	return id, true
}

func parseEscapesFlag(r *http.Request) bool {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("escapes")))
	return v == "1" || v == "true" || v == "yes"
}

type unicodeDebugClientMessage struct {
	At      string `json:"at"`
	Type    string `json:"type"`
	PaneID  string `json:"pane_id,omitempty"`
	Length  int    `json:"length,omitempty"`
	Preview string `json:"preview,omitempty"`
}

type unicodeDebugClientReport struct {
	Renderer       string                      `json:"renderer"`
	Reason         string                      `json:"reason,omitempty"`
	URL            string                      `json:"url"`
	UserAgent      string                      `json:"user_agent"`
	Source         string                      `json:"source"`
	PaneID         string                      `json:"pane_id"`
	CurrentPaneID  string                      `json:"current_pane_id,omitempty"`
	TargetPaneID   string                      `json:"target_pane_id,omitempty"`
	DataLength     int                         `json:"data_length,omitempty"`
	DataSample     string                      `json:"data_sample,omitempty"`
	RecentMessages []unicodeDebugClientMessage `json:"recent_messages,omitempty"`
}

type unicodeDebugServerCapture struct {
	TmuxPaneID          string `json:"tmux_pane_id,omitempty"`
	PlainSample         string `json:"plain_sample,omitempty"`
	PlainHexPreview     string `json:"plain_hex_preview,omitempty"`
	PlainCaptureError   string `json:"plain_capture_error,omitempty"`
	EscapedSample       string `json:"escaped_sample,omitempty"`
	EscapedHexPreview   string `json:"escaped_hex_preview,omitempty"`
	EscapedCaptureError string `json:"escaped_capture_error,omitempty"`
}

type unicodeDebugRecord struct {
	ID         int64                     `json:"id"`
	ReceivedAt string                    `json:"received_at"`
	Client     unicodeDebugClientReport  `json:"client"`
	Server     unicodeDebugServerCapture `json:"server"`
}

type unicodeDebugStore struct {
	mu      sync.Mutex
	nextID  int64
	reports []unicodeDebugRecord
}

func (s *unicodeDebugStore) add(r unicodeDebugRecord) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	r.ID = s.nextID
	s.reports = append(s.reports, r)
	if len(s.reports) > 50 {
		s.reports = append([]unicodeDebugRecord{}, s.reports[len(s.reports)-50:]...)
	}
	return r.ID
}

func (s *unicodeDebugStore) latest() (unicodeDebugRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reports) == 0 {
		return unicodeDebugRecord{}, false
	}
	return s.reports[len(s.reports)-1], true
}

var unicodeReports = &unicodeDebugStore{}

func serveAPIDebugUnicode(w http.ResponseWriter, r *http.Request, hub *wshub.Hub) {
	switch r.Method {
	case http.MethodPost:
		var clientReport unicodeDebugClientReport
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&clientReport); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		record := unicodeDebugRecord{
			ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Client:     clientReport,
		}

		if tmuxPaneID, ok := hub.TargetSessionPaneIDByPublicID(clientReport.PaneID); ok {
			record.Server.TmuxPaneID = tmuxPaneID
			if plain, err := hub.CapturePaneContent(tmuxPaneID, false); err != nil {
				record.Server.PlainCaptureError = err.Error()
			} else {
				record.Server.PlainSample = truncateRunes(plain, 2048)
				record.Server.PlainHexPreview = hexPreview(plain, 128)
			}
			if escaped, err := hub.CapturePaneContent(tmuxPaneID, true); err != nil {
				record.Server.EscapedCaptureError = err.Error()
			} else {
				record.Server.EscapedSample = truncateRunes(escaped, 2048)
				record.Server.EscapedHexPreview = hexPreview(escaped, 128)
			}
		} else {
			record.Server.PlainCaptureError = "pane not found"
		}

		id := unicodeReports.add(record)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          true,
			"report_id":   id,
			"latest_path": "/api/debug/unicode",
		})
		return
	case http.MethodGet:
		report, ok := unicodeReports.latest()
		if !ok {
			http.Error(w, "no unicode debug report captured yet", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func hexPreview(s string, maxBytes int) string {
	b := []byte(s)
	if len(b) > maxBytes {
		b = b[:maxBytes]
	}
	if len(b) == 0 {
		return ""
	}
	parts := make([]string, 0, len(b))
	for _, v := range b {
		parts = append(parts, fmt.Sprintf("%02x", v))
	}
	return strings.Join(parts, " ")
}

func paneTargetHref(paneID, defaultTerm string) string {
	v := url.Values{}
	v.Set("term", normalizeDefaultTerm(defaultTerm))
	return fmt.Sprintf("/p/%s?%s", paneID, v.Encode())
}

func normalizeDefaultTerm(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "xterm" {
		return "xterm"
	}
	return "ghostty"
}

func negotiateStateFormat(r *http.Request) string {
	path := r.URL.Path
	if strings.HasSuffix(path, ".html") {
		return "html"
	}
	if strings.HasSuffix(path, ".json") {
		return "json"
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/html") {
		return "html"
	}
	if strings.Contains(accept, "application/json") {
		return "json"
	}
	return "json"
}

var stateHTMLTemplate = template.Must(template.New("state").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>wmux panes</title>
  <style>
    body { font-family: ui-sans-serif, -apple-system, sans-serif; margin: 2rem; }
    ul { padding-left: 1.2rem; }
    li { margin: 0.45rem 0; }
    small { color: #555; margin-left: 0.45rem; }
  </style>
</head>
<body>
  <h1>Available Panes</h1>
  <ul>
  {{range .Panes}}
    <li><a href="{{.Href}}">{{.Title}}</a><small>{{.Size}}</small></li>
  {{else}}
    <li>No panes available.</li>
  {{end}}
  </ul>
</body>
</html>`))
