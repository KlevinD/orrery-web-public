package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

const (
	productionWebSearchEndpoint       = "https://www.bing.com/search?format=rss"
	productionOpenClawSearchTimeout   = 30 * time.Second
	productionWebRequestTimeout       = 15 * time.Second
	productionWebMaximumDownloadBytes = 2 << 20
	productionWebDefaultSearchLimit   = 5
	productionWebMaximumSearchLimit   = 10
	productionWebDefaultCharacters    = 20000
	productionWebMinimumCharacters    = 1000
	productionWebMaximumCharacters    = 50000
)

var (
	webHTMLTitlePattern    = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title\s*>`)
	webHTMLScriptPattern   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	webHTMLStylePattern    = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	webHTMLNoScriptPattern = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript\s*>`)
	webHTMLSVGPattern      = regexp.MustCompile(`(?is)<svg\b[^>]*>.*?</svg\s*>`)
	webHTMLCommentPattern  = regexp.MustCompile(`(?is)<!--.*?-->`)
	webHTMLBreakPattern    = regexp.MustCompile(`(?i)</?(?:article|aside|blockquote|br|dd|div|dl|dt|figcaption|figure|footer|h[1-6]|header|hr|li|main|nav|ol|p|pre|section|table|tbody|td|tfoot|th|thead|tr|ul)\b[^>]*>`)
	webHTMLTagPattern      = regexp.MustCompile(`(?s)<[^>]+>`)
	webHorizontalSpace     = regexp.MustCompile(`[\t\f\v ]+`)
	webLineSpace           = regexp.MustCompile(`(?m)^ +| +$`)
	webBlankLines          = regexp.MustCompile(`\n{3,}`)
)

type productionWebToolExecutor struct {
	client         *http.Client
	searchEndpoint string
	searchRunner   productionWebSearchRunner
}

type productionWebSearchRunner interface {
	Search(context.Context, string, int) (mcptransport.WebToolResult, error)
}

type productionOpenClawWebSearchRunner struct {
	executable string
	home       string
	provider   string
}

type productionWebSearchFeed struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

func newProductionWebToolExecutor() *productionWebToolExecutor {
	executor := &productionWebToolExecutor{
		client:         newProductionPublicWebHTTPClient(),
		searchEndpoint: productionWebSearchEndpoint,
	}
	if runner := newProductionOpenClawWebSearchRunner(); runner != nil {
		executor.searchRunner = runner
	}
	return executor
}

func (e *productionWebToolExecutor) ExecuteWebTool(ctx context.Context, toolID string, req mcptransport.WebToolRequest) (mcptransport.WebToolResult, error) {
	if e == nil || e.client == nil {
		return mcptransport.WebToolResult{}, mcptransport.ErrServiceDependency
	}
	switch strings.TrimSpace(toolID) {
	case string(toolstd.WebSearch):
		return e.search(ctx, req)
	case string(toolstd.WebFetch):
		return e.fetch(ctx, req)
	default:
		return mcptransport.WebToolResult{}, fmt.Errorf("unsupported web tool %q", toolID)
	}
}

func (e *productionWebToolExecutor) search(ctx context.Context, req mcptransport.WebToolRequest) (mcptransport.WebToolResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return mcptransport.WebToolResult{}, fmt.Errorf("web search query is required")
	}
	if utf8.RuneCountInString(query) > 512 {
		return mcptransport.WebToolResult{}, fmt.Errorf("web search query must not exceed 512 characters")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = productionWebDefaultSearchLimit
	}
	if limit > productionWebMaximumSearchLimit {
		limit = productionWebMaximumSearchLimit
	}
	if e.searchRunner != nil {
		return e.searchRunner.Search(ctx, query, limit)
	}

	endpoint, err := url.Parse(strings.TrimSpace(e.searchEndpoint))
	if err != nil || endpoint == nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("web search endpoint is invalid")
	}
	if err := validatePublicWebURLSyntax(endpoint); err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("web search endpoint: %w", err)
	}
	params := endpoint.Query()
	params.Set("q", query)
	endpoint.RawQuery = params.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return mcptransport.WebToolResult{}, err
	}
	setProductionWebRequestHeaders(httpReq, "application/rss+xml, application/xml;q=0.9, text/xml;q=0.8")
	response, err := e.client.Do(httpReq)
	if err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("web search request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return mcptransport.WebToolResult{}, fmt.Errorf("web search returned HTTP %d", response.StatusCode)
	}
	body, err := readProductionWebBody(response.Body)
	if err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("read web search response: %w", err)
	}
	var feed productionWebSearchFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("decode web search response: %w", err)
	}

	results := make([]any, 0, limit)
	for _, item := range feed.Channel.Items {
		if len(results) >= limit {
			break
		}
		resultURL, err := url.Parse(strings.TrimSpace(item.Link))
		if err != nil || validatePublicWebURLSyntax(resultURL) != nil {
			continue
		}
		title := normalizeProductionWebText(stripProductionHTML(item.Title))
		snippet := normalizeProductionWebText(stripProductionHTML(item.Description))
		if title == "" || strings.TrimSpace(resultURL.String()) == "" {
			continue
		}
		results = append(results, map[string]any{
			"title": title, "url": resultURL.String(), "snippet": truncateProductionWebRunes(snippet, 1000),
		})
	}
	return mcptransport.WebToolResult{Output: map[string]any{
		"query": query, "provider": "bing_rss", "results": results,
	}}, nil
}

func newProductionOpenClawWebSearchRunner() *productionOpenClawWebSearchRunner {
	executable := strings.TrimSpace(os.Getenv("ORRERY_WEB_SEARCH_EXECUTABLE"))
	if executable == "" {
		executable = "openclaw"
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return nil
	}
	return &productionOpenClawWebSearchRunner{
		executable: resolved,
		home:       productionOpenClawHome(),
		provider:   strings.TrimSpace(os.Getenv("ORRERY_WEB_SEARCH_PROVIDER")),
	}
}

func productionOpenClawHome() string {
	if home := strings.TrimSpace(os.Getenv("ORRERY_WEB_OPENCLAW_HOME")); home != "" {
		return home
	}
	if home := strings.TrimSpace(os.Getenv("OPENCLAW_HOME")); home != "" {
		return home
	}
	baseDir := strings.TrimSpace(os.Getenv("ORRERY_BASE_DIR"))
	if baseDir == "" {
		return ""
	}
	dedicated := filepath.Join(filepath.Clean(baseDir), "openclaw-web")
	if _, err := os.Stat(filepath.Join(dedicated, ".openclaw", "openclaw.json")); err == nil {
		return dedicated
	}
	candidate := filepath.Dir(filepath.Dir(filepath.Clean(baseDir)))
	if _, err := os.Stat(filepath.Join(candidate, ".openclaw", "openclaw.json")); err == nil {
		return candidate
	}
	return ""
}

func (r *productionOpenClawWebSearchRunner) Search(ctx context.Context, query string, limit int) (mcptransport.WebToolResult, error) {
	if r == nil || strings.TrimSpace(r.executable) == "" {
		return mcptransport.WebToolResult{}, mcptransport.ErrServiceDependency
	}
	searchCtx, cancel := context.WithTimeout(ctx, productionOpenClawSearchTimeout)
	defer cancel()
	args := []string{"infer", "web", "search", "--query", query, "--limit", fmt.Sprintf("%d", limit), "--json"}
	if r.provider != "" {
		args = append(args, "--provider", r.provider)
	}
	cmd := exec.CommandContext(searchCtx, r.executable, args...)
	cmd.Env = os.Environ()
	if r.home != "" {
		cmd.Env = append(cmd.Env, "OPENCLAW_HOME="+r.home)
	}
	output, err := cmd.Output()
	if err != nil {
		message := err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if stderr := truncateProductionWebRunes(normalizeProductionWebText(string(exitErr.Stderr)), 1000); stderr != "" {
				message += ": " + stderr
			}
		}
		return mcptransport.WebToolResult{}, fmt.Errorf("OpenClaw web search failed: %s", message)
	}
	return decodeProductionOpenClawWebSearch(output, query, limit)
}

type productionOpenClawWebSearchOutput struct {
	OK      bool `json:"ok"`
	Outputs []struct {
		Result struct {
			Query    string `json:"query"`
			Provider string `json:"provider"`
			Results  []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"result"`
	} `json:"outputs"`
}

func decodeProductionOpenClawWebSearch(output []byte, fallbackQuery string, limit int) (mcptransport.WebToolResult, error) {
	var response productionOpenClawWebSearchOutput
	if err := json.Unmarshal(output, &response); err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("decode OpenClaw web search response: %w", err)
	}
	if !response.OK || len(response.Outputs) == 0 {
		return mcptransport.WebToolResult{}, fmt.Errorf("OpenClaw web search returned no result payload")
	}
	payload := response.Outputs[0].Result
	results := make([]any, 0, min(limit, len(payload.Results)))
	for _, item := range payload.Results {
		if len(results) >= limit {
			break
		}
		resultURL, err := url.Parse(strings.TrimSpace(item.URL))
		if err != nil || validatePublicWebURLSyntax(resultURL) != nil {
			continue
		}
		title := normalizeOpenClawExternalWebText(item.Title)
		if title == "" {
			continue
		}
		results = append(results, map[string]any{
			"title":   title,
			"url":     resultURL.String(),
			"snippet": truncateProductionWebRunes(normalizeOpenClawExternalWebText(item.Description), 1000),
		})
	}
	query := strings.TrimSpace(payload.Query)
	if query == "" {
		query = fallbackQuery
	}
	return mcptransport.WebToolResult{Output: map[string]any{
		"query": query, "provider": strings.TrimSpace(payload.Provider), "results": results,
	}}, nil
}

func normalizeOpenClawExternalWebText(content string) string {
	lines := strings.Split(content, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" || strings.HasPrefix(line, "<<<") || strings.HasPrefix(line, "Source:") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return normalizeProductionWebText(strings.Join(cleaned, "\n"))
}

func (e *productionWebToolExecutor) fetch(ctx context.Context, req mcptransport.WebToolRequest) (mcptransport.WebToolResult, error) {
	target, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || target == nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("web fetch URL is invalid")
	}
	if err := validatePublicWebURLSyntax(target); err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("web fetch URL: %w", err)
	}
	maxCharacters := req.MaxCharacters
	if maxCharacters <= 0 {
		maxCharacters = productionWebDefaultCharacters
	}
	if maxCharacters < productionWebMinimumCharacters {
		maxCharacters = productionWebMinimumCharacters
	}
	if maxCharacters > productionWebMaximumCharacters {
		maxCharacters = productionWebMaximumCharacters
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return mcptransport.WebToolResult{}, err
	}
	setProductionWebRequestHeaders(httpReq, "text/html, text/plain;q=0.9, application/xhtml+xml;q=0.8, application/json;q=0.7, application/xml;q=0.6")
	response, err := e.client.Do(httpReq)
	if err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("web fetch request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return mcptransport.WebToolResult{}, fmt.Errorf("web fetch returned HTTP %d", response.StatusCode)
	}
	body, err := readProductionWebBody(response.Body)
	if err != nil {
		return mcptransport.WebToolResult{}, fmt.Errorf("read web fetch response: %w", err)
	}
	contentType := productionWebContentType(response.Header.Get("Content-Type"), body)
	if !productionWebTextualContentType(contentType) {
		return mcptransport.WebToolResult{}, fmt.Errorf("web fetch content type %q is not readable text", contentType)
	}

	title := ""
	content := string(body)
	if strings.Contains(contentType, "html") {
		title = productionWebHTMLTitle(content)
		content = stripProductionHTML(content)
	}
	content = normalizeProductionWebText(content)
	content, truncated := truncateProductionWebText(content, maxCharacters)
	finalURL := target.String()
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return mcptransport.WebToolResult{Output: map[string]any{
		"url": finalURL, "status_code": response.StatusCode, "content_type": contentType,
		"title": title, "content": content, "truncated": truncated,
	}}, nil
}

func newProductionPublicWebHTTPClient() *http.Client {
	proxyAddresses := productionWebProxyAddressesFromEnvironment()
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           productionPublicWebDialContextWithProxies(proxyAddresses),
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   2,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: productionPublicWebRoundTripper{base: transport},
		Timeout:   productionWebRequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("web request stopped after 5 redirects")
			}
			return validatePublicWebURLSyntax(req.URL)
		},
	}
}

type productionPublicWebRoundTripper struct {
	base http.RoundTripper
}

func (t productionPublicWebRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("web request URL is required")
	}
	if err := validatePublicWebURLResolution(req.Context(), req.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

func productionWebProxyAddressesFromEnvironment() map[string]struct{} {
	addresses := map[string]struct{}{}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		raw := strings.TrimSpace(os.Getenv(name))
		proxyURL, err := url.Parse(raw)
		if err != nil || proxyURL == nil || proxyURL.Hostname() == "" || (proxyURL.Scheme != "http" && proxyURL.Scheme != "https") {
			continue
		}
		port := proxyURL.Port()
		if port == "" {
			if proxyURL.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		addresses[net.JoinHostPort(proxyURL.Hostname(), port)] = struct{}{}
	}
	return addresses
}

func productionPublicWebDialContextWithProxies(proxyAddresses map[string]struct{}) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if _, allowed := proxyAddresses[address]; allowed {
			dialer := net.Dialer{Timeout: 8 * time.Second, KeepAlive: 20 * time.Second}
			return dialer.DialContext(ctx, network, address)
		}
		return productionPublicWebDialContext(ctx, network, address)
	}
}

func validatePublicWebURLResolution(ctx context.Context, target *url.URL) error {
	if err := validatePublicWebURLSyntax(target); err != nil {
		return err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, target.Hostname())
	if err != nil {
		return fmt.Errorf("resolve web destination: %w", err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("web destination resolved without an address")
	}
	for _, address := range addresses {
		if prohibitedProductionWebIP(address.IP) {
			return fmt.Errorf("web destination resolves to a non-public address")
		}
	}
	return nil
}

func productionPublicWebDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid web destination: %w", err)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve web destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("web destination resolved without an address")
	}
	for _, address := range addresses {
		if prohibitedProductionWebIP(address.IP) {
			return nil, fmt.Errorf("web destination resolves to a non-public address")
		}
	}
	dialer := net.Dialer{Timeout: 8 * time.Second, KeepAlive: 20 * time.Second}
	var lastErr error
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("connect to web destination: %w", lastErr)
}

func validatePublicWebURLSyntax(target *url.URL) error {
	if target == nil {
		return fmt.Errorf("URL is required")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are allowed")
	}
	if target.User != nil {
		return fmt.Errorf("URLs containing credentials are not allowed")
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target.Hostname()), "."))
	if host == "" {
		return fmt.Errorf("URL host is required")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
		return fmt.Errorf("local network hosts are not allowed")
	}
	if port := target.Port(); port != "" && port != "80" && port != "443" {
		return fmt.Errorf("only standard HTTP and HTTPS ports are allowed")
	}
	if ip := net.ParseIP(host); ip != nil && prohibitedProductionWebIP(ip) {
		return fmt.Errorf("non-public IP addresses are not allowed")
	}
	return nil
}

func prohibitedProductionWebIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4[0] == 0 || (ipv4[0] == 100 && ipv4[1] >= 64 && ipv4[1] <= 127)
	}
	return false
}

func setProductionWebRequestHeaders(req *http.Request, accept string) {
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	req.Header.Set("User-Agent", "Orrery-WebReader/1.0 (+https://minvia.ai)")
}

func readProductionWebBody(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, productionWebMaximumDownloadBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > productionWebMaximumDownloadBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", productionWebMaximumDownloadBytes)
	}
	return body, nil
}

func productionWebContentType(header string, body []byte) string {
	value, _, err := mime.ParseMediaType(strings.TrimSpace(header))
	if err == nil && value != "" {
		return strings.ToLower(value)
	}
	return strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(body), ";")[0]))
}

func productionWebTextualContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "json") || strings.Contains(contentType, "xml") || strings.Contains(contentType, "javascript")
}

func productionWebHTMLTitle(content string) string {
	match := webHTMLTitlePattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return truncateProductionWebRunes(normalizeProductionWebText(stripProductionHTML(match[1])), 500)
}

func stripProductionHTML(content string) string {
	for _, pattern := range []*regexp.Regexp{webHTMLScriptPattern, webHTMLStylePattern, webHTMLNoScriptPattern, webHTMLSVGPattern, webHTMLCommentPattern} {
		content = pattern.ReplaceAllString(content, " ")
	}
	content = webHTMLBreakPattern.ReplaceAllString(content, "\n")
	content = webHTMLTagPattern.ReplaceAllString(content, " ")
	return html.UnescapeString(content)
}

func normalizeProductionWebText(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = html.UnescapeString(content)
	content = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, content)
	content = webHorizontalSpace.ReplaceAllString(content, " ")
	content = webLineSpace.ReplaceAllString(content, "")
	content = webBlankLines.ReplaceAllString(content, "\n\n")
	return strings.TrimSpace(content)
}

func truncateProductionWebText(content string, limit int) (string, bool) {
	if utf8.RuneCountInString(content) <= limit {
		return content, false
	}
	return truncateProductionWebRunes(content, limit), true
}

func truncateProductionWebRunes(content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return strings.TrimSpace(string(runes[:limit]))
}

var _ mcptransport.WebToolExecutor = (*productionWebToolExecutor)(nil)
