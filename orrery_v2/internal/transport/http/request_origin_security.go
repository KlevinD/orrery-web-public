package httptransport

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func cookieMutationOriginAllowed(req *http.Request) bool {
	return browserRequestOriginAllowed(req, true)
}

func browserRequestOriginAllowed(req *http.Request, requireEvidence bool) bool {
	if req == nil {
		return false
	}
	origin := strings.TrimSpace(req.Header.Get("Origin"))
	if origin != "" {
		return requestOriginMatches(req, origin)
	}
	fetchSite := strings.ToLower(strings.TrimSpace(req.Header.Get("Sec-Fetch-Site")))
	if fetchSite != "" {
		return fetchSite == "same-origin"
	}
	return !requireEvidence
}

func requestOriginMatches(req *http.Request, origin string) bool {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return false
	}
	expectedScheme := "http"
	if requestIsHTTPS(req) {
		expectedScheme = "https"
	}
	if !strings.EqualFold(parsed.Scheme, expectedScheme) {
		return false
	}
	_, allowed := requestOriginHosts(req, expectedScheme)[canonicalOriginHost(parsed.Host, expectedScheme)]
	return allowed
}

func requestOriginHosts(req *http.Request, scheme string) map[string]struct{} {
	hosts := map[string]struct{}{}
	if req == nil {
		return hosts
	}
	if trustedForwardingProxy(req, cleanClientIP(req.RemoteAddr)) {
		if forwarded := canonicalOriginHost(lastForwardedHeaderValue(req.Header.Get("X-Forwarded-Host")), scheme); forwarded != "" {
			hosts[forwarded] = struct{}{}
			return hosts
		}
	}
	if host := canonicalOriginHost(req.Host, scheme); host != "" {
		hosts[host] = struct{}{}
	}
	return hosts
}

func canonicalOriginHost(host string, scheme string) string {
	parsed, err := url.Parse("//" + strings.TrimSpace(host))
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if scheme == "http" && port == "80" || scheme == "https" && port == "443" {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(hostname, port)
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}

func websocketOriginAllowed(req *http.Request) bool {
	if req == nil {
		return false
	}
	origin := strings.TrimSpace(req.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if requestOriginMatches(req, origin) {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHostname(parsed.Hostname()) && isLoopbackHostname(effectiveRequestHostname(req))
}

func effectiveRequestHostname(req *http.Request) string {
	if req == nil {
		return ""
	}
	if trustedForwardingProxy(req, cleanClientIP(req.RemoteAddr)) {
		if forwarded := lastForwardedHeaderValue(req.Header.Get("X-Forwarded-Host")); forwarded != "" {
			if hostname := requestHostname(forwarded); hostname != "" {
				return hostname
			}
		}
	}
	return requestHostname(req.Host)
}

func lastForwardedHeaderValue(value string) string {
	parts := strings.Split(value, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if part := strings.TrimSpace(parts[i]); part != "" {
			return part
		}
	}
	return ""
}

func requestHostname(host string) string {
	parsed, err := url.Parse("//" + strings.TrimSpace(host))
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func isLoopbackHostname(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func unsafeHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}
