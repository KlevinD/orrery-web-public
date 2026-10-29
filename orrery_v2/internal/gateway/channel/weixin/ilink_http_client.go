package weixin

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultIlinkTLSHandshakeTimeout = 30 * time.Second

type ilinkProxyFunc func(*http.Request) (*url.URL, error)

func secureIlinkTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = ilinkProxy(transport.Proxy)
	transport.TLSHandshakeTimeout = defaultIlinkTLSHandshakeTimeout
	return transport
}

func ilinkProxy(upstream ilinkProxyFunc) ilinkProxyFunc {
	return func(req *http.Request) (*url.URL, error) {
		if isOfficialIlinkRequest(req) {
			return nil, nil
		}
		if upstream == nil {
			return nil, nil
		}
		return upstream(req)
	}
}

func isOfficialIlinkRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(req.URL.Hostname()))
	return host == "ilinkai.weixin.qq.com" || strings.HasSuffix(host, ".weixin.qq.com")
}
