package httpd

import (
	"io/fs"
	"net/http"

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
