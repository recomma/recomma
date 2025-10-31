package webui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/vearutop/statigz"
	"github.com/vearutop/statigz/brotli"
	"github.com/vearutop/statigz/zstd"
)

var (
	//go:embed app/dist/*
	content embed.FS
	distFS  = mustSubFS(content, "app/dist")
	debug   atomic.Bool
)

// SetDebug toggles debug mode for runtime config exposure.
func SetDebug(enabled bool) {
	debug.Store(enabled)
}

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
func Handler(listen string, publicOrigin string, tls bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/config.js", configHandler(resolveOpsAPIOrigin(listen, publicOrigin, tls)))
	mux.Handle("/", statigz.FileServer(content,
		statigz.EncodeOnInit,
		brotli.AddEncoding,
		zstd.AddEncoding,
		statigz.FSPrefix("app/dist")))

	return mux
}

func configHandler(origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enabled := debug.Load()
		script := fmt.Sprintf("window.__RECOMMA_CONFIG__ = { OPS_API_ORIGIN: %q, DEBUG_LOGS: %t, DEBUG_MODE: %t };\n", origin, enabled, enabled)
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, script)
	})
}

func resolveOpsAPIOrigin(listen, publicOrigin string, tls bool) string {
	trimmed := strings.TrimSpace(publicOrigin)
	if trimmed != "" {
		if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
		}
		return strings.TrimRight(trimmed, "/")
	}

	scheme := "http"
	defaultPort := "80"
	if tls {
		scheme = "https"
		defaultPort = "443"
	}

	host := strings.TrimSpace(listen)
	if host == "" {
		return fmt.Sprintf("%s://localhost:8080", scheme)
	}

	addr := host
	if strings.HasPrefix(host, ":") {
		addr = "localhost" + host
	}
	if !strings.Contains(addr, ":") {
		addr = addr + ":" + defaultPort
	}

	h, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("%s://%s", scheme, host)
	}

	if h == "" || h == "0.0.0.0" || h == "::" || net.ParseIP(h) != nil {
		h = "localhost"
	}

	hostLabel := h
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		hostLabel = "[" + h + "]"
	}

	if port == defaultPort {
		return fmt.Sprintf("%s://%s", scheme, hostLabel)
	}

	return fmt.Sprintf("%s://%s:%s", scheme, hostLabel, port)
}
