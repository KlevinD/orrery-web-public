package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConnectionEndpointValidatorRejectsProviderEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"endpoint": "ws://127.0.0.1/stream", "ticket": "ticket"})
	}))
	defer server.Close()
	client := NewStreamClient(
		WithAppCredential(NewAppCredentialConfig("client", "secret")),
		WithOpenApiHost(server.URL),
		WithEndpointValidator(func(endpoint string) error {
			if endpoint != "ws://127.0.0.1/stream" {
				t.Fatalf("validator endpoint = %q", endpoint)
			}
			return errors.New("untrusted endpoint")
		}),
	)
	if _, err := client.GetConnectionEndpoint(context.Background()); err == nil || !strings.Contains(err.Error(), "untrusted endpoint") {
		t.Fatalf("endpoint validation error = %v", err)
	}
}

func TestConnectionEndpointBootstrapRejectsCrossOriginRedirect(t *testing.T) {
	var targetCalled atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		targetCalled.Store(true)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	client := NewStreamClient(WithAppCredential(NewAppCredentialConfig("client", "secret")), WithOpenApiHost(origin.URL))
	if _, err := client.GetConnectionEndpoint(context.Background()); err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("redirect error = %v", err)
	}
	if targetCalled.Load() {
		t.Fatal("cross-origin redirect target received the credential request")
	}
}

func TestConnectionEndpointBootstrapRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"endpoint":"wss://wss-open-connection.dingtalk.com/connect","ticket":"ticket"}` + strings.Repeat(" ", maxConnectionEndpointResponseBytes)))
	}))
	defer server.Close()
	client := NewStreamClient(WithAppCredential(NewAppCredentialConfig("client", "secret")), WithOpenApiHost(server.URL))
	if _, err := client.GetConnectionEndpoint(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestBuildStreamConnectionURLEncodesTicket(t *testing.T) {
	raw, err := buildStreamConnectionURL("wss://wss-open-connection.dingtalk.com/connect", "ticket&admin=true")
	if err != nil {
		t.Fatalf("buildStreamConnectionURL: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Query().Get("ticket") != "ticket&admin=true" || parsed.Query().Get("admin") != "" {
		t.Fatalf("connection URL = %q, %v", raw, err)
	}
}

func TestStreamLifecycleReportsReconnectAndStops(t *testing.T) {
	connections := make(chan *websocket.Conn, 4)
	states := make(chan bool, 8)
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1.0/gateway/connections/open":
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/stream"
			_ = json.NewEncoder(w).Encode(map[string]string{"endpoint": endpoint, "ticket": "ticket"})
		case "/stream":
			conn, err := upgrader.Upgrade(w, req, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			connections <- conn
			defer conn.Close()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	client := NewStreamClient(
		WithAppCredential(NewAppCredentialConfig("client", "secret")),
		WithOpenApiHost(server.URL),
	)
	client.SetConnectionStateHandler(func(connected bool) { states <- connected })
	startCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := receiveConnection(t, connections)
	waitForConnectionState(t, states, true)
	if err := first.Close(); err != nil {
		t.Fatalf("close first connection: %v", err)
	}
	waitForConnectionState(t, states, false)
	_ = receiveConnection(t, connections)
	waitForConnectionState(t, states, true)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	if err := client.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitForConnectionState(t, states, false)
	if err := client.Start(context.Background()); err == nil {
		t.Fatal("stopped stream client restarted")
	}
}

func receiveConnection(t *testing.T, connections <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-connections:
		return conn
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for stream connection")
		return nil
	}
}

func waitForConnectionState(t *testing.T, states <-chan bool, expected bool) {
	t.Helper()
	select {
	case state := <-states:
		if state != expected {
			t.Fatalf("connection state = %v, want %v", state, expected)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for connection state %v", expected)
	}
}
