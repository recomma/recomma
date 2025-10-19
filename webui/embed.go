package webui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
)

var (
	//go:embed app/dist/*
	content embed.FS
	distFS  = mustSubFS(content, "app/dist")
)

func FS() fs.FS {
	return distFS
}

func mustSubFS(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		slog.Default().Error("webui dir does not exist", slog.String("error", err.Error()), slog.String("dir", dir))
		return nil
	}
	return sub
}

// Handler returns an http.Handler that serves the embedded assets and a runtime config script.
func Handler(opsAPIOrigin string, tls bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/config.js", configHandler(opsAPIOrigin, tls))
	mux.Handle("/", http.FileServer(http.FS(distFS)))

	return mux
}

func configHandler(listen string, tls bool) http.Handler {
	url := fmt.Sprintf("http://%s", listen)
	if tls {
		url = fmt.Sprintf("https://%s", listen)
	}
	script := fmt.Sprintf("window.__RECOMMA_CONFIG__ = { OPS_API_ORIGIN: %q };\n", url)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, script)
	})
}
