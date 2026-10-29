package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type productionWebRoundTripFunc func(*http.Request) (*http.Response, error)

func (f productionWebRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestProductionWebSearchParsesBoundedRSSResults(t *testing.T) {
	client := &http.Client{Transport: productionWebRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("q") != "Orrery ontology" {
			t.Fatalf("search query = %q", req.URL.Query().Get("q"))
		}
		body := `<?xml version="1.0"?><rss><channel><item><title>First &amp; useful</title><link>https://example.com/first</link><description><![CDATA[<b>First snippet</b>]]></description></item><item><title>Second</title><link>https://example.org/second</link><description>Second snippet</description></item></channel></rss>`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/xml"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	executor := &productionWebToolExecutor{client: client, searchEndpoint: "https://search.example/search?format=rss"}

	result, err := executor.ExecuteWebTool(context.Background(), string(toolstd.WebSearch), mcptransport.WebToolRequest{Query: "Orrery ontology", Limit: 1})
	if err != nil {
		t.Fatalf("ExecuteWebTool: %v", err)
	}
	results, ok := result.Output["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %#v", result.Output["results"])
	}
	first, _ := results[0].(map[string]any)
	if first["title"] != "First & useful" || first["url"] != "https://example.com/first" || first["snippet"] != "First snippet" {
		t.Fatalf("first result = %#v", first)
	}
}

func TestDecodeProductionOpenClawWebSearchNormalizesUntrustedResults(t *testing.T) {
	output := []byte(`{
  "ok": true,
  "outputs": [{"result": {
    "query": "Orrery ontology",
    "provider": "minimax",
    "results": [
      {
        "title": "\n<<<EXTERNAL_UNTRUSTED_CONTENT id=\"title\">>>\nSource: Web Search\n---\nOrrery docs\n<<<END_EXTERNAL_UNTRUSTED_CONTENT id=\"title\">>>",
        "url": "https://example.com/orrery",
        "description": "\n<<<EXTERNAL_UNTRUSTED_CONTENT id=\"description\">>>\nSource: Web Search\n---\nOntology documentation\n<<<END_EXTERNAL_UNTRUSTED_CONTENT id=\"description\">>>"
      },
      {"title": "Private", "url": "http://127.0.0.1/admin", "description": "skip"}
    ]
  }}]
}`)

	result, err := decodeProductionOpenClawWebSearch(output, "fallback", 5)
	if err != nil {
		t.Fatalf("decodeProductionOpenClawWebSearch: %v", err)
	}
	if result.Output["query"] != "Orrery ontology" || result.Output["provider"] != "minimax" {
		t.Fatalf("search output = %#v", result.Output)
	}
	results, ok := result.Output["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %#v", result.Output["results"])
	}
	first, _ := results[0].(map[string]any)
	if first["title"] != "Orrery docs" || first["snippet"] != "Ontology documentation" {
		t.Fatalf("first result = %#v", first)
	}
}

func TestProductionWebFetchExtractsTextAndIgnoresActiveHTML(t *testing.T) {
	client := &http.Client{Transport: productionWebRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `<html><head><title>Useful &amp; safe</title><style>.hidden{}</style><script>ignore()</script></head><body><main><h1>Heading</h1><p>Useful body.</p></main></body></html>`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	executor := &productionWebToolExecutor{client: client}

	result, err := executor.ExecuteWebTool(context.Background(), string(toolstd.WebFetch), mcptransport.WebToolRequest{URL: "https://example.com/article", MaxCharacters: 1000})
	if err != nil {
		t.Fatalf("ExecuteWebTool: %v", err)
	}
	content, _ := result.Output["content"].(string)
	if result.Output["title"] != "Useful & safe" || !strings.Contains(content, "Heading") || !strings.Contains(content, "Useful body.") || strings.Contains(content, "ignore()") || strings.Contains(content, ".hidden") {
		t.Fatalf("fetch output = %#v", result.Output)
	}
}

func TestValidatePublicWebURLSyntaxRejectsPrivateDestinations(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/", "http://169.254.169.254/latest/meta-data/", "http://100.64.0.1/",
		"http://10.0.0.1/", "http://localhost/", "http://service.internal/", "file:///etc/passwd",
		"https://user:password@example.com/", "https://example.com:8443/",
	} {
		t.Run(raw, func(t *testing.T) {
			target, _ := url.Parse(raw)
			if err := validatePublicWebURLSyntax(target); err == nil {
				t.Fatalf("validatePublicWebURLSyntax(%q) unexpectedly allowed", raw)
			}
		})
	}
	for _, raw := range []string{"https://example.com/article", "http://example.org:80/path", "https://example.net:443/"} {
		target, _ := url.Parse(raw)
		if err := validatePublicWebURLSyntax(target); err != nil {
			t.Fatalf("validatePublicWebURLSyntax(%q): %v", raw, err)
		}
	}
}

func TestProductionWebProxyAddressesOnlyIncludeHTTPProxyEndpoints(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:20172")
	t.Setenv("HTTPS_PROXY", "socks5://127.0.0.1:1080")

	addresses := productionWebProxyAddressesFromEnvironment()
	if _, ok := addresses["127.0.0.1:20172"]; !ok {
		t.Fatalf("proxy addresses = %#v", addresses)
	}
	if _, ok := addresses["127.0.0.1:1080"]; ok {
		t.Fatalf("unsupported proxy address was allowed: %#v", addresses)
	}
}

func TestProductionOpenClawHomePrefersDedicatedWebConfiguration(t *testing.T) {
	t.Setenv("ORRERY_WEB_OPENCLAW_HOME", "")
	t.Setenv("OPENCLAW_HOME", "")
	baseDir := filepath.Join(t.TempDir(), "orrery", "shared")
	dedicated := filepath.Join(baseDir, "openclaw-web")
	if err := os.MkdirAll(filepath.Join(dedicated, ".openclaw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dedicated, ".openclaw", "openclaw.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_BASE_DIR", baseDir)

	if got := productionOpenClawHome(); got != dedicated {
		t.Fatalf("productionOpenClawHome() = %q, want %q", got, dedicated)
	}
}

func TestProductionPublicWebRoundTripperRejectsPrivateDestination(t *testing.T) {
	called := false
	transport := productionWebRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/private", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := (productionPublicWebRoundTripper{base: transport}).RoundTrip(req); err == nil {
		t.Fatal("private destination was allowed")
	}
	if called {
		t.Fatal("base transport was called for a private destination")
	}
}

func TestProductionWebToolsLive(t *testing.T) {
	if os.Getenv("ORRERY_LIVE_WEB_TEST") != "1" {
		t.Skip("set ORRERY_LIVE_WEB_TEST=1 to call public web endpoints")
	}
	executor := newProductionWebToolExecutor()
	search, err := executor.ExecuteWebTool(context.Background(), string(toolstd.WebSearch), mcptransport.WebToolRequest{Query: "OpenAI", Limit: 2})
	if err != nil {
		t.Fatalf("live web search: %v", err)
	}
	results, ok := search.Output["results"].([]any)
	if !ok || len(results) == 0 {
		t.Fatalf("live web search results = %#v", search.Output["results"])
	}
	fetchURL := strings.TrimSpace(os.Getenv("ORRERY_LIVE_WEB_FETCH_URL"))
	if fetchURL == "" {
		fetchURL = "https://example.com/"
	}
	fetch, err := executor.ExecuteWebTool(context.Background(), string(toolstd.WebFetch), mcptransport.WebToolRequest{URL: fetchURL, MaxCharacters: 2000})
	if err != nil {
		t.Fatalf("live web fetch: %v", err)
	}
	if strings.TrimSpace(fetch.Output["content"].(string)) == "" {
		t.Fatalf("live web fetch output = %#v", fetch.Output)
	}
}
