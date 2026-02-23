package httpd

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

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
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state := cfg.Hub.CurrentState()
		_ = json.NewEncoder(w).Encode(struct {
			Panes any `json:"panes"`
		}{Panes: state.Panes})
	})
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
