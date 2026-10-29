package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductionRealtimeWebsocketCheckOriginAllowsSameHostAndLocalDev(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
	}{
		{name: "missing browser origin", host: "127.0.0.1:8080", origin: ""},
		{name: "same host", host: "app.example.test", origin: "https://app.example.test"},
		{name: "local vite port", host: "127.0.0.1:8080", origin: "http://127.0.0.1:5178"},
		{name: "localhost to loopback api", host: "127.0.0.1:8080", origin: "http://localhost:5178"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := realtimeOriginRequest(tc.host, tc.origin)
			if !productionRealtimeWebsocketCheckOrigin(req) {
				t.Fatalf("origin %q for host %q was rejected", tc.origin, tc.host)
			}
		})
	}
}

func TestProductionRealtimeWebsocketCheckOriginRejectsExternalOrigins(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
	}{
		{name: "external page to local api", host: "127.0.0.1:8080", origin: "https://evil.example.test"},
		{name: "local page to production host", host: "app.example.test", origin: "http://127.0.0.1:5178"},
		{name: "different production host", host: "app.example.test", origin: "https://other.example.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := realtimeOriginRequest(tc.host, tc.origin)
			if productionRealtimeWebsocketCheckOrigin(req) {
				t.Fatalf("origin %q for host %q was allowed", tc.origin, tc.host)
			}
		})
	}
}

func realtimeOriginRequest(host string, origin string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/realtime/connect", nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}
