package origin

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

var DefaultOrigin = "http://localhost:8080"

func BuildAllowedOrigins(listenAddr, publicOrigin string) []string {
	origins := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)

	addNormalized := func(origin string) {
		if origin == "" {
			return
		}
		if _, ok := seen[origin]; ok {
			return
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}

	var addedPublic bool
	for _, origin := range parseOriginList(publicOrigin) {
		normalized := normalizeOrigin(origin)
		if normalized == "" {
			continue
		}
		addNormalized(normalized)
		addedPublic = true
	}

	if addedPublic {
		return origins
	}

	addNormalized(DefaultOrigin)

	for _, origin := range originsFromListen(listenAddr) {
		normalized := normalizeOrigin(origin)
		if normalized == "" {
			continue
		}
		addNormalized(normalized)
	}

	if len(origins) == 0 {
		addNormalized(DefaultOrigin)
	}

	return origins
}

func DeriveRPID(listenAddr, publicOrigin string) string {
	publicOrigins := parseOriginList(publicOrigin)
	for _, origin := range publicOrigins {
		host := hostFromOrigin(origin)
		if host != "" && host != "localhost" {
			return host
		}
	}

	for _, origin := range publicOrigins {
		host := hostFromOrigin(origin)
		if host != "" {
			return host
		}
	}

	host := hostFromListen(listenAddr)
	if host == "" {
		return "localhost"
	}
	return host
}

func parseOriginList(origin string) []string {
	trimmed := strings.TrimSpace(origin)
	if trimmed == "" {
		return nil
	}

	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	normalized := fmt.Sprintf("%s://%s", strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Host))
	return strings.TrimSuffix(normalized, "/")
}

func originsFromListen(listenAddr string) []string {
	host := strings.TrimSpace(listenAddr)
	if host == "" {
		return nil
	}

	addr := host
	if !strings.Contains(host, ":") {
		addr = host + ":"
	}
	if strings.HasPrefix(host, ":") {
		addr = "127.0.0.1" + host
	}

	parsedHost, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return nil
	}

	candidates := []string{"localhost", "127.0.0.1"}
	if parsedHost != "" && parsedHost != "0.0.0.0" && parsedHost != "::" {
		candidates = append(candidates, parsedHost)
	}

	origins := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		hostLabel := candidate
		if strings.Contains(candidate, ":") && !strings.HasPrefix(candidate, "[") {
			hostLabel = "[" + candidate + "]"
		}

		origins = append(origins, fmt.Sprintf("http://%s:%s", hostLabel, port))
	}

	return origins
}

func hostFromOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}

	return normalizeRPIDHost(parsed.Hostname())
}

func hostFromListen(listenAddr string) string {
	host := strings.TrimSpace(listenAddr)
	if host == "" {
		return "localhost"
	}

	addr := host
	if !strings.Contains(host, ":") {
		addr = host + ":"
	}
	if strings.HasPrefix(host, ":") {
		addr = "127.0.0.1" + host
	}

	parsedHost, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "localhost"
	}

	return normalizeRPIDHost(parsedHost)
}

func normalizeRPIDHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}

	if host == "0.0.0.0" || host == "::" {
		return "localhost"
	}

	if ip := net.ParseIP(host); ip != nil {
		return "localhost"
	}

	return host
}
