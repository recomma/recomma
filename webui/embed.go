package webui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
)

// content stores the compiled web UI assets.
//
//go:embed index.html assets/*
var content embed.FS

// FS exposes the embedded assets as an fs.FS.
func FS() fs.FS {
	return content
}

// Handler returns an http.Handler that serves the embedded assets and a runtime config script.
func Handler(opsAPIOrigin string, tls bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/config.js", configHandler(opsAPIOrigin, tls))
	mux.Handle("/", http.FileServer(http.FS(content)))

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
