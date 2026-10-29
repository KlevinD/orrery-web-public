package outputpolicy

import (
	"net"
	"net/url"
	"strings"
)

func publicReplyLine(text string) string {
	text = publicMarkdownLinks(text)
	var out strings.Builder
	start := 0
	for _, span := range publicMarkdownLinkPattern.FindAllStringIndex(text, -1) {
		out.WriteString(publicLocalPathPattern.ReplaceAllString(text[start:span[0]], "[内部文件]"))
		link := text[span[0]:span[1]]
		labelEnd := strings.Index(link, "](")
		out.WriteString(publicLocalPathPattern.ReplaceAllString(link[:labelEnd], "[内部文件]"))
		out.WriteString(link[labelEnd:])
		start = span[1]
	}
	out.WriteString(publicLocalPathPattern.ReplaceAllString(text[start:], "[内部文件]"))
	return out.String()
}

// Keep usable citations and governed attachment links. Local paths, credentials
// and execution-only links must not become a new disclosure through Markdown.
func publicMarkdownLinks(text string) string {
	return publicMarkdownLinkPattern.ReplaceAllStringFunc(text, func(link string) string {
		parts := publicMarkdownLinkPattern.FindStringSubmatch(link)
		target := strings.TrimSpace(link[strings.Index(link, "](")+2 : len(link)-1])
		u, err := url.Parse(target)
		if err == nil && publicLinkURL(u) {
			return link
		}
		return parts[1]
	})
}

func publicLinkURL(u *url.URL) bool {
	if u == nil || u.User != nil {
		return false
	}
	for key := range u.Query() {
		switch strings.ToLower(key) {
		case "token", "access_token", "refresh_token", "api_key", "apikey", "password", "secret", "authorization":
			return false
		}
	}
	if u.Scheme == "" && u.Host == "" {
		return strings.HasPrefix(u.Path, "/api/attachments/") && !strings.Contains(u.Path, "..")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()) {
		return false
	}
	return true
}
