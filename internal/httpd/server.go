package httpd

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ampcode/wmux/internal/assets"
	"github.com/ampcode/wmux/internal/wshub"
)

type Config struct {
	StaticDir string
	Hub       *wshub.Hub
}

func NewServer(cfg Config) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", cfg.Hub.HandleWS)
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) { serveAPIState(w, r, cfg.Hub) })
	mux.HandleFunc("/api/state.json", func(w http.ResponseWriter, r *http.Request) { serveAPIState(w, r, cfg.Hub) })
	mux.HandleFunc("/api/state.html", func(w http.ResponseWriter, r *http.Request) { serveAPIState(w, r, cfg.Hub) })
	mux.HandleFunc("/api/contents/", func(w http.ResponseWriter, r *http.Request) { serveAPIContents(w, r, cfg.Hub) })
	mux.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w, r, cfg.StaticDir)
	})
	mux.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w, r, cfg.StaticDir)
	})

	staticHandler, err := staticHandler(cfg.StaticDir)
	if err != nil {
		return nil, err
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			redirectToFirstPane(w, r, cfg.Hub)
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

func serveAPIState(w http.ResponseWriter, r *http.Request, hub *wshub.Hub) {
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
				Href:  paneTargetHref(pane.PaneID),
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

func redirectToFirstPane(w http.ResponseWriter, r *http.Request, hub *wshub.Hub) {
	if href, ok := firstTargetPaneHref(hub); ok {
		http.Redirect(w, r, href, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/api/state.html", http.StatusFound)
}

func firstTargetPaneHref(hub *wshub.Hub) (string, bool) {
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
	return paneTargetHref(firstPane.PaneID), true
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

func paneTargetHref(paneID string) string {
	return fmt.Sprintf("/p/%s", paneID)
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
