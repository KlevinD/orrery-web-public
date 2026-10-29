package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

type wsMockClientAssertionProvider struct {
	mu     sync.Mutex
	tokens []*larkcore.Token
	index  int
	calls  int
	auds   []string
}

func TestCallbackConcurrencyOptionIsBounded(t *testing.T) {
	client := NewClient("app", "secret", WithCallbackConcurrency(9))
	if cap(client.callbackSlots) != 9 {
		t.Fatalf("callback concurrency = %d, want 9", cap(client.callbackSlots))
	}
	defaultClient := NewClient("app", "secret", WithCallbackConcurrency(0))
	if cap(defaultClient.callbackSlots) != 64 {
		t.Fatalf("default callback concurrency = %d, want 64", cap(defaultClient.callbackSlots))
	}
}

func TestBootstrapRedirectPolicyRejectsCrossOriginAndLoops(t *testing.T) {
	origin, _ := http.NewRequest(http.MethodPost, "https://open.feishu.cn/callback/ws/endpoint", nil)
	sameOrigin, _ := http.NewRequest(http.MethodPost, "https://open.feishu.cn/callback/ws/next", nil)
	if err := bootstrapRedirectPolicy(sameOrigin, []*http.Request{origin}); err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	crossOrigin, _ := http.NewRequest(http.MethodPost, "https://attacker.example/callback", nil)
	if err := bootstrapRedirectPolicy(crossOrigin, []*http.Request{origin}); err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("cross-origin redirect error = %v", err)
	}
	via := []*http.Request{origin, origin, origin, origin, origin}
	if err := bootstrapRedirectPolicy(sameOrigin, via); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("redirect loop error = %v", err)
	}
	if err := bootstrapRedirectPolicy(sameOrigin, nil); err == nil || !strings.Contains(err.Error(), "no original") {
		t.Fatalf("missing origin redirect error = %v", err)
	}
}

func TestCombineConcurrentFragmentsProducesOneCompletePayload(t *testing.T) {
	client := NewClient("app", "secret")
	const fragments = 64
	payloads := make([][]byte, fragments)
	for index := range payloads {
		payloads[index] = []byte{byte(index)}
	}

	var wg sync.WaitGroup
	results := make(chan []byte, fragments)
	for index := range payloads {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			if combined := client.combine("message-a", fragments, index, payloads[index]); combined != nil {
				results <- combined
			}
		}()
	}
	wg.Wait()
	close(results)

	want := bytes.Join(payloads, nil)
	var completed int
	for combined := range results {
		completed++
		if !bytes.Equal(combined, want) {
			t.Fatalf("combined payload = %v, want %v", combined, want)
		}
	}
	if completed != 1 {
		t.Fatalf("complete payloads = %d, want 1", completed)
	}
	if combined := client.combine("message-a", fragments, 0, payloads[0]); combined != nil {
		t.Fatal("completed fragment set remained replayable")
	}
}

func TestCombineRejectsMalformedFragmentMetadata(t *testing.T) {
	client := NewClient("app", "secret")
	for _, test := range []struct {
		sum int
		seq int
	}{
		{sum: 2, seq: -1},
		{sum: 2, seq: 2},
		{sum: maxFrameFragments + 1, seq: 0},
	} {
		if combined := client.combine("message-a", test.sum, test.seq, []byte("part")); combined != nil {
			t.Fatalf("combine(%d, %d) returned a payload", test.sum, test.seq)
		}
	}
}

func TestCombineRejectsPayloadAboveProductionLimit(t *testing.T) {
	client := NewClient("app", "secret")
	first := bytes.Repeat([]byte("a"), maxCombinedPayloadBytes/2+1)
	second := bytes.Repeat([]byte("b"), maxCombinedPayloadBytes/2+1)
	if combined := client.combine("message-large", 2, 0, first); combined != nil {
		t.Fatal("first oversized aggregate fragment completed unexpectedly")
	}
	if combined := client.combine("message-large", 2, 1, second); combined != nil {
		t.Fatal("oversized aggregate payload was accepted")
	}
}

func (p *wsMockClientAssertionProvider) RetrieveToken(ctx context.Context, aud string) (*larkcore.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.auds = append(p.auds, aud)
	token := p.tokens[p.index]
	if p.index < len(p.tokens)-1 {
		p.index++
	}
	return token, nil
}

func TestGetConnURLWithAppSecret(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != GenEndpointUri {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req BootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request failed: %v", err)
		}
		if req.AppSecret == "" || req.ClientAssertion != "" {
			t.Fatalf("unexpected bootstrap request: %#v", req)
		}
		_ = json.NewEncoder(w).Encode(&EndpointResp{Code: OK, Data: &Endpoint{Url: "wss://example.com/ws"}})
	}))
	defer server.Close()

	bootstrapHTTPClient = server.Client()
	client := NewClient("app-id", "app-secret", WithDomain(server.URL))
	connURL, err := client.getConnURL(context.Background())
	if err != nil {
		t.Fatalf("get conn url failed: %v", err)
	}
	if connURL != "wss://example.com/ws" {
		t.Fatalf("unexpected conn url: %s", connURL)
	}
}

func TestGetConnURLAppliesEndpointValidator(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&EndpointResp{Code: OK, Data: &Endpoint{Url: "ws://127.0.0.1/ws"}})
	}))
	defer server.Close()

	bootstrapHTTPClient = server.Client()
	validatorCalls := 0
	client := NewClient("app-id", "app-secret",
		WithDomain(server.URL),
		WithEndpointValidator(func(endpoint string) error {
			validatorCalls++
			if endpoint != "ws://127.0.0.1/ws" {
				t.Fatalf("validator endpoint = %q", endpoint)
			}
			return errors.New("untrusted endpoint")
		}),
	)
	if _, err := client.getConnURL(context.Background()); err == nil || !strings.Contains(err.Error(), "untrusted endpoint") {
		t.Fatalf("endpoint validation error = %v", err)
	}
	if validatorCalls != 1 {
		t.Fatalf("validator calls = %d, want 1", validatorCalls)
	}
}

func TestGetConnURLWithCustomHeadersAndUserAgent(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tt-Env") != "ppe_test" {
			t.Fatalf("missing X-Tt-Env header: %s", r.Header.Get("X-Tt-Env"))
		}
		if r.Header.Get("X-Use-Ppe") != "1" {
			t.Fatalf("missing X-Use-Ppe header: %s", r.Header.Get("X-Use-Ppe"))
		}
		userAgent := r.Header.Get("User-Agent")
		if !strings.HasPrefix(userAgent, "oapi-sdk-go/") {
			t.Fatalf("unexpected user agent: %s", userAgent)
		}
		if !strings.Contains(userAgent, " source/ws-test") {
			t.Fatalf("missing source in user agent: %s", userAgent)
		}
		_ = json.NewEncoder(w).Encode(&EndpointResp{Code: OK, Data: &Endpoint{Url: "wss://example.com/ws"}})
	}))
	defer server.Close()

	headers := make(http.Header)
	headers.Set("X-Tt-Env", "ppe_test")
	headers.Set("X-Use-Ppe", "1")

	bootstrapHTTPClient = server.Client()
	client := NewClient("app-id", "app-secret",
		WithDomain(server.URL),
		WithHeaders(headers),
		WithSource("ws-test"),
	)
	if _, err := client.getConnURL(context.Background()); err != nil {
		t.Fatalf("get conn url failed: %v", err)
	}
}

func TestGetConnURLRejectsOversizedBootstrapResponse(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"URL":"wss://example.com/ws"}}` + strings.Repeat(" ", maxBootstrapResponse)))
	}))
	defer server.Close()
	bootstrapHTTPClient = server.Client()
	client := NewClient("app-id", "app-secret", WithDomain(server.URL))
	if _, err := client.getConnURL(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized bootstrap response error = %v", err)
	}
}

func TestGetConnURLWithClientAssertionProxy(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy"+GenEndpointUri {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get(larkcore.HeaderXTargetService) != "open.feishu.cn" {
			t.Fatalf("unexpected target service: %s", r.Header.Get(larkcore.HeaderXTargetService))
		}
		var req BootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request failed: %v", err)
		}
		if req.ClientAssertion != "assertion" || req.AppSecret != "" {
			t.Fatalf("unexpected bootstrap request: %#v", req)
		}
		_ = json.NewEncoder(w).Encode(&EndpointResp{Code: OK, Data: &Endpoint{Url: "wss://example.com/ws"}})
	}))
	defer proxyServer.Close()

	bootstrapHTTPClient = proxyServer.Client()
	provider := &wsMockClientAssertionProvider{tokens: []*larkcore.Token{{Value: "assertion", TargetInfo: &larkcore.TargetInfo{TargetService: proxyServer.URL, TargetPrefix: "/proxy"}}}}
	client := NewClient("app-id", "", WithDomain("https://open.feishu.cn"), WithClientAssertionProvider(provider))
	if _, err := client.getConnURL(context.Background()); err != nil {
		t.Fatalf("get conn url failed: %v", err)
	}
}

func TestGetConnURLWithClientAssertionProxyHTTPErrorMsg(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}{
			Code: 20050,
			Msg:  "target service unavailable",
		})
	}))
	defer proxyServer.Close()

	bootstrapHTTPClient = proxyServer.Client()
	provider := &wsMockClientAssertionProvider{tokens: []*larkcore.Token{{Value: "assertion", TargetInfo: &larkcore.TargetInfo{TargetService: proxyServer.URL, TargetPrefix: "/proxy"}}}}
	client := NewClient("app-id", "", WithDomain("https://open.feishu.cn"), WithClientAssertionProvider(provider))
	_, err := client.getConnURL(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	serverErr, ok := err.(*ServerError)
	if !ok {
		t.Fatalf("unexpected error type: %#v", err)
	}
	if serverErr.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected server error code: %d", serverErr.Code)
	}
	if serverErr.Msg != "target service unavailable" {
		t.Fatalf("unexpected server error msg: %s", serverErr.Msg)
	}
}

func TestGetConnURLRetrieveTokenEachTime(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	bodyAssertions := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req BootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request failed: %v", err)
		}
		bodyAssertions = append(bodyAssertions, req.ClientAssertion)
		_ = json.NewEncoder(w).Encode(&EndpointResp{Code: OK, Data: &Endpoint{Url: "wss://example.com/ws"}})
	}))
	defer server.Close()

	bootstrapHTTPClient = server.Client()
	provider := &wsMockClientAssertionProvider{tokens: []*larkcore.Token{{Value: "assertion-1"}, {Value: "assertion-2"}}}
	client := NewClient("app-id", "", WithDomain(server.URL), WithClientAssertionProvider(provider))
	if _, err := client.getConnURL(context.Background()); err != nil {
		t.Fatalf("first get conn url failed: %v", err)
	}
	if _, err := client.getConnURL(context.Background()); err != nil {
		t.Fatalf("second get conn url failed: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("expected provider called twice, got %d", provider.calls)
	}
	if len(bodyAssertions) != 2 || bodyAssertions[0] != "assertion-1" || bodyAssertions[1] != "assertion-2" {
		t.Fatalf("unexpected assertions: %#v", bodyAssertions)
	}
}

func TestBuildWSProxyURL(t *testing.T) {
	testCases := map[string]string{
		"proxy.example.com":         "https://proxy.example.com/v1" + GenEndpointUri,
		"https://proxy.example.com": "https://proxy.example.com/v1" + GenEndpointUri,
		"http://proxy.example.com":  "http://proxy.example.com/v1" + GenEndpointUri,
	}

	for targetService, expected := range testCases {
		proxyURL := buildWSProxyURL(targetService, "/v1", GenEndpointUri)
		if proxyURL != expected {
			t.Fatalf("unexpected proxy url for %s: %s", targetService, proxyURL)
		}
	}
}
