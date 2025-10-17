package webui

import (
	"embed"
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

// Handler returns an http.Handler that serves the embedded assets.
func Handler() http.Handler {
	return http.FileServer(http.FS(content))
}
