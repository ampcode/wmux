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
	mux.HandleFunc("/api/panes/", func(w http.ResponseWriter, r *http.Request) { serveAPIPane(w, r, cfg.Hub, defaultTerm) })
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
			serveAPIRoot(w, r, cfg.Hub, defaultTerm)
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

func serveAPIRoot(w http.ResponseWriter, r *http.Request, hub *wshub.Hub, defaultTerm string) {
	doc := buildHypermediaDocument("/", hub.CurrentTargetSessionPaneInfos(), defaultTerm)
	serveHypermediaDocument(w, r, doc)
}

func serveAPIState(w http.ResponseWriter, r *http.Request, hub *wshub.Hub, defaultTerm string) {
	doc := buildHypermediaDocument(r.URL.Path, hub.CurrentTargetSessionPaneInfos(), defaultTerm)
	serveHypermediaDocument(w, r, doc)
}

type hypermediaLink struct {
	Rel       string `json:"rel"`
	Href      string `json:"href"`
	Method    string `json:"method,omitempty"`
	Type      string `json:"type,omitempty"`
	Templated bool   `json:"templated,omitempty"`
	Example   string `json:"example,omitempty"`
}

type hypermediaActionField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

type hypermediaAction struct {
	Name        string                  `json:"name"`
	Title       string                  `json:"title,omitempty"`
	Method      string                  `json:"method"`
	Href        string                  `json:"href"`
	Type        string                  `json:"type,omitempty"`
	Description string                  `json:"description,omitempty"`
	Fields      []hypermediaActionField `json:"fields,omitempty"`
	Schema      any                     `json:"schema,omitempty"`
}

type paneDocument struct {
	PaneID      string           `json:"pane_id"`
	PaneIndex   int              `json:"pane_index"`
	Name        string           `json:"name"`
	SessionName string           `json:"session_name"`
	Width       int              `json:"width"`
	Height      int              `json:"height"`
	Links       []hypermediaLink `json:"links,omitempty"`
}

type hypermediaDocument struct {
	Resource    string             `json:"resource"`
	DefaultTerm string             `json:"default_term"`
	Links       []hypermediaLink   `json:"links"`
	Actions     []hypermediaAction `json:"actions,omitempty"`
	Panes       []paneDocument     `json:"panes"`
}

func serveHypermediaDocument(w http.ResponseWriter, r *http.Request, doc hypermediaDocument) {
	format := negotiateStateFormat(r)
	if format == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = stateHTMLTemplate.Execute(w, struct {
			Doc                   hypermediaDocument
			CreatePaneRequestBody string
		}{
			Doc:                   doc,
			CreatePaneRequestBody: "{\n  \"env\": {\"NAME\": \"value\"},\n  \"cwd\": \"/absolute/path\",\n  \"cmd\": [\"bash\", \"-lc\", \"echo hello\"]\n}",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func buildHypermediaDocument(selfPath string, panes []wshub.PaneInfo, defaultTerm string) hypermediaDocument {
	examplePaneID := "0"
	if len(panes) > 0 {
		examplePaneID = panes[0].PaneID
	}

	doc := hypermediaDocument{
		Resource:    "wmux",
		DefaultTerm: normalizeDefaultTerm(defaultTerm),
		Links: []hypermediaLink{
			{Rel: "self", Href: selfPath, Method: "GET", Type: "application/json"},
			{Rel: "root", Href: "/", Method: "GET"},
			{Rel: "state", Href: "/api/state.json", Method: "GET", Type: "application/json"},
			{Rel: "state-html", Href: "/api/state.html", Method: "GET", Type: "text/html"},
			{Rel: "pane", Href: "/p/{pane_id}{?term}", Method: "GET", Type: "text/html", Templated: true, Example: paneTargetHref(examplePaneID, defaultTerm)},
			{Rel: "pane-resource", Href: "/api/panes/{pane_id}", Method: "GET", Type: "application/json", Templated: true, Example: paneAPIHref(examplePaneID)},
			{Rel: "pane-contents", Href: "/api/contents/{pane_id}{?escapes}", Method: "GET", Type: "text/plain; charset=utf-8", Templated: true, Example: "/api/contents/" + examplePaneID},
			{Rel: "create-pane", Href: "/api/panes", Method: "POST", Type: "application/json"},
			{Rel: "ws", Href: "/ws", Method: "GET"},
		},
		Actions: []hypermediaAction{createPaneAction()},
		Panes:   make([]paneDocument, 0, len(panes)),
	}
	for _, pane := range panes {
		doc.Panes = append(doc.Panes, paneResource(pane, defaultTerm))
	}
	return doc
}

func createPaneAction() hypermediaAction {
	return hypermediaAction{
		Name:        "create-pane",
		Title:       "Create Pane",
		Method:      "POST",
		Href:        "/api/panes",
		Type:        "application/json",
		Description: "Create a new pane in the target tmux session.",
		Fields: []hypermediaActionField{
			{Name: "env", Type: "object", Description: "Optional environment variables map; keys must match [A-Za-z_][A-Za-z0-9_]*."},
			{Name: "cwd", Type: "string", Description: "Optional working directory path."},
			{Name: "cmd", Type: "array[string]", Description: "Optional command argv executed in the new pane."},
		},
		Schema: map[string]any{
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"cwd": map[string]any{
					"type":      "string",
					"minLength": 1,
				},
				"cmd": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func paneResource(pane wshub.PaneInfo, defaultTerm string) paneDocument {
	return paneDocument{
		PaneID:      pane.PaneID,
		PaneIndex:   pane.PaneIndex,
		Name:        pane.Name,
		SessionName: pane.SessionName,
		Width:       pane.Width,
		Height:      pane.Height,
		Links: []hypermediaLink{
			{Rel: "self", Href: paneAPIHref(pane.PaneID), Method: "GET", Type: "application/json"},
			{Rel: "terminal", Href: paneTargetHref(pane.PaneID, defaultTerm), Method: "GET", Type: "text/html"},
			{Rel: "contents", Href: "/api/contents/" + pane.PaneID, Method: "GET", Type: "text/plain; charset=utf-8"},
			{Rel: "contents-escaped", Href: "/api/contents/" + pane.PaneID + "?escapes=1", Method: "GET", Type: "text/plain; charset=utf-8"},
		},
	}
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

func serveAPIPane(w http.ResponseWriter, r *http.Request, hub *wshub.Hub, defaultTerm string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	paneID, ok := parsePanePathID(r.URL.EscapedPath(), "/api/panes/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	pane, found := targetSessionPaneByPublicID(hub, paneID)
	if !found {
		http.Error(w, "pane not found", http.StatusNotFound)
		return
	}

	doc := hypermediaDocument{
		Resource:    "wmux-pane",
		DefaultTerm: normalizeDefaultTerm(defaultTerm),
		Links: []hypermediaLink{
			{Rel: "self", Href: paneAPIHref(pane.PaneID), Method: "GET", Type: "application/json"},
			{Rel: "collection", Href: "/api/state.json", Method: "GET", Type: "application/json"},
			{Rel: "root", Href: "/", Method: "GET"},
		},
		Actions: []hypermediaAction{createPaneAction()},
		Panes:   []paneDocument{paneResource(pane, defaultTerm)},
	}
	serveHypermediaDocument(w, r, doc)
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

	if resolved, found := targetSessionPaneByPublicID(hub, pane.PaneID); found {
		pane = resolved
	}
	location := paneAPIHref(pane.PaneID)
	doc := hypermediaDocument{
		Resource:    "wmux-pane",
		DefaultTerm: normalizeDefaultTerm(defaultTerm),
		Links: []hypermediaLink{
			{Rel: "self", Href: location, Method: "GET", Type: "application/json"},
			{Rel: "collection", Href: "/api/state.json", Method: "GET", Type: "application/json"},
			{Rel: "root", Href: "/", Method: "GET"},
		},
		Actions: []hypermediaAction{createPaneAction()},
		Panes:   []paneDocument{paneResource(pane, defaultTerm)},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(doc)
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

func targetSessionPaneByPublicID(hub *wshub.Hub, paneID string) (wshub.PaneInfo, bool) {
	for _, pane := range hub.CurrentTargetSessionPaneInfos() {
		if pane.PaneID == paneID {
			return pane, true
		}
	}
	return wshub.PaneInfo{}, false
}

func paneAPIHref(paneID string) string {
	return "/api/panes/" + url.PathEscape(paneID)
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
  <title>wmux API</title>
  <style>
    body { font-family: ui-sans-serif, -apple-system, sans-serif; margin: 2rem; }
    h1, h2, h3 { margin-bottom: 0.5rem; }
    section { margin-bottom: 1.5rem; }
    ul { padding-left: 1.2rem; }
    li { margin: 0.45rem 0; }
    form { border: 1px solid #d0d7de; border-radius: 0.5rem; padding: 1rem; max-width: 48rem; }
    .field { margin: 0.75rem 0; }
    label { display: block; font-weight: 600; margin-bottom: 0.25rem; }
    input, textarea { width: 100%; box-sizing: border-box; padding: 0.5rem; font: inherit; }
    button { margin-top: 0.5rem; padding: 0.55rem 0.9rem; font: inherit; cursor: pointer; }
    .meta { color: #555; margin-left: 0.45rem; }
    pre { background: #f6f8fa; padding: 0.8rem; border-radius: 0.4rem; overflow-x: auto; }
    code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  </style>
</head>
<body>
  <h1>wmux API</h1>
  <p>Default terminal renderer: <code>{{.Doc.DefaultTerm}}</code></p>

  <section>
    <h2>Links</h2>
    <ul>
    {{range .Doc.Links}}
      <li>
        <code>{{.Method}}</code>
        {{if .Templated}}
          <code>{{.Href}}</code>
        {{else}}
          <a href="{{.Href}}">{{.Href}}</a>
        {{end}}
        <span class="meta">rel={{.Rel}}{{if .Type}}, type={{.Type}}{{end}}{{if .Templated}}, templated=true{{end}}</span>
        {{if .Example}}<span class="meta">example: <a href="{{.Example}}">{{.Example}}</a></span>{{end}}
      </li>
    {{end}}
    </ul>
  </section>

  <section>
    <h2>Actions</h2>
    {{range .Doc.Actions}}
      <h3>{{.Title}}</h3>
      <p><code>{{.Method}}</code> <code>{{.Href}}</code>{{if .Type}} ({{.Type}}){{end}}</p>
      <p>{{.Description}}</p>
      <ul>
      {{range .Fields}}
        <li><code>{{.Name}}</code>: <code>{{.Type}}</code> <span class="meta">{{.Description}}</span></li>
      {{end}}
      </ul>
      {{if eq .Name "create-pane"}}
      <form id="create-pane-form" action="{{.Href}}" method="post">
        <div class="field">
          <label for="create-pane-cwd">cwd (optional)</label>
          <input id="create-pane-cwd" name="cwd" type="text" placeholder="/absolute/path" />
        </div>
        <div class="field">
          <label for="create-pane-env">env (optional JSON object)</label>
          <textarea id="create-pane-env" name="env" rows="4" placeholder='{"FOO":"bar"}'></textarea>
        </div>
        <div class="field">
          <label for="create-pane-cmd">cmd (optional JSON array)</label>
          <textarea id="create-pane-cmd" name="cmd" rows="4" placeholder='["bash","-lc","echo hello"]'></textarea>
        </div>
        <button type="submit">Create Pane</button>
      </form>
      <pre id="create-pane-result" aria-live="polite"><code>Submit the form to create a pane.</code></pre>
      {{end}}
    {{end}}
    <p>Example request body:</p>
    <pre><code>{{.CreatePaneRequestBody}}</code></pre>
  </section>

  <section>
    <h2>Available Panes</h2>
    <ul>
    {{range .Doc.Panes}}
      <li>
        <strong>{{if .Name}}{{.Name}}{{else}}pane {{.PaneID}}{{end}}</strong>
        <span class="meta">{{.Width}}x{{.Height}} (pane_id={{.PaneID}})</span>
        <ul>
        {{range .Links}}
          <li><code>{{.Method}}</code> <a href="{{.Href}}">{{.Href}}</a> <span class="meta">rel={{.Rel}}</span></li>
        {{end}}
        </ul>
      </li>
    {{else}}
      <li>No panes available.</li>
    {{end}}
    </ul>
  </section>
  <script>
    (function () {
      const form = document.getElementById("create-pane-form");
      const result = document.getElementById("create-pane-result");
      if (!form || !result) return;

      const setResult = (value) => {
        result.textContent = value;
      };

      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        const payload = {};

        const cwd = String((document.getElementById("create-pane-cwd") || {}).value || "").trim();
        const envText = String((document.getElementById("create-pane-env") || {}).value || "").trim();
        const cmdText = String((document.getElementById("create-pane-cmd") || {}).value || "").trim();

        if (cwd) payload.cwd = cwd;

        try {
          if (envText) {
            const env = JSON.parse(envText);
            if (!env || typeof env !== "object" || Array.isArray(env)) {
              throw new Error("env must be a JSON object");
            }
            payload.env = env;
          }
          if (cmdText) {
            const cmd = JSON.parse(cmdText);
            if (!Array.isArray(cmd) || cmd.some((v) => typeof v !== "string")) {
              throw new Error("cmd must be a JSON array of strings");
            }
            payload.cmd = cmd;
          }
        } catch (err) {
          setResult("Invalid form data: " + (err && err.message ? err.message : String(err)));
          return;
        }

        setResult("Creating pane...");
        try {
          const res = await fetch(form.action, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
          const text = await res.text();
          if (!res.ok) {
            setResult("Request failed (" + res.status + "): " + text);
            return;
          }
          let paneHref = "";
          try {
            const body = JSON.parse(text);
            const pane = Array.isArray(body.panes) && body.panes.length > 0 ? body.panes[0] : null;
            if (pane && Array.isArray(pane.links)) {
              const terminal = pane.links.find((l) => l && l.rel === "terminal" && typeof l.href === "string");
              if (terminal) paneHref = terminal.href;
            }
          } catch {
            // Keep raw response fallback below.
          }
          if (paneHref) {
            setResult("Created pane. Open: " + paneHref + "\n\n" + text);
            return;
          }
          setResult(text);
        } catch (err) {
          setResult("Request error: " + (err && err.message ? err.message : String(err)));
        }
      });
    })();
  </script>
</body>
</html>`))
