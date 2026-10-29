package weixin

import (
	"net/http"
	"net/url"
	"testing"
)

func TestIlinkProxyBypassesProxyOnlyForOfficialWeixinHosts(t *testing.T) {
	proxyURL, err := url.Parse("http://127.0.0.1:3128")
	if err != nil {
		t.Fatal(err)
	}
	delegated := 0
	proxy := ilinkProxy(func(_ *http.Request) (*url.URL, error) {
		delegated++
		return proxyURL, nil
	})

	official, err := http.NewRequest(http.MethodGet, "https://ilinkai.weixin.qq.com/ilink/bot/get_bot_qrcode", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := proxy(official)
	if err != nil || got != nil || delegated != 0 {
		t.Fatalf("official proxy = %v error = %v delegated = %d", got, err, delegated)
	}

	other, err := http.NewRequest(http.MethodGet, "https://example.com/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = proxy(other)
	if err != nil || got == nil || got.String() != proxyURL.String() || delegated != 1 {
		t.Fatalf("other proxy = %v error = %v delegated = %d", got, err, delegated)
	}
}

func TestSecureIlinkTransportExtendsTLSHandshakeTimeout(t *testing.T) {
	transport := secureIlinkTransport()
	if transport.TLSHandshakeTimeout != defaultIlinkTLSHandshakeTimeout {
		t.Fatalf("TLS handshake timeout = %s, want %s", transport.TLSHandshakeTimeout, defaultIlinkTLSHandshakeTimeout)
	}
}
