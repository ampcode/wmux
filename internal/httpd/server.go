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
	mux.HandleFunc("/t", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w, r, cfg.StaticDir)
	})
	mux.HandleFunc("/t/", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w, r, cfg.StaticDir)
	})

	staticHandler, err := staticHandler(cfg.StaticDir)
	if err != nil {
		return nil, err
	}
	mux.Handle("/", staticHandler)
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
				title = pane.ID
			}
			rows = append(rows, struct {
				Title string
				Size  string
				Href  string
			}{
				Title: title,
				Size:  fmt.Sprintf("%dx%d", pane.Width, pane.Height),
				Href:  paneTargetHref(pane.ID),
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

func paneTargetHref(paneID string) string {
	// tmux pane IDs are tokens like %13; keep % raw so links don't become /t/%2513.
	return "/t/" + paneID
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
