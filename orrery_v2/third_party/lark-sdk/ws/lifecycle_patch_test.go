package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStartReturnsAndStopsBackgroundLoopsWhenContextIsCancelled(t *testing.T) {
	originalClient := bootstrapHTTPClient
	defer func() { bootstrapHTTPClient = originalClient }()

	connected := make(chan struct{}, 1)
	disconnected := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case GenEndpointUri:
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?device_id=device&service_id=1"
			_ = json.NewEncoder(w).Encode(&EndpointResp{Code: OK, Data: &Endpoint{Url: wsURL}})
		case "/ws":
			conn, err := upgrader.Upgrade(w, req, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			connected <- struct{}{}
			defer conn.Close()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					disconnected <- struct{}{}
					return
				}
			}
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	bootstrapHTTPClient = server.Client()

	client := NewClient("app", "secret", WithDomain(server.URL), WithAutoReconnect(false))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(ctx) }()
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket did not connect")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after cancellation")
	}
	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket was not closed after cancellation")
	}
}

func TestCloseAndWaitDrainsAdmittedCallbacks(t *testing.T) {
	client := NewClient("app-id", "app-secret")
	if !client.beginCallback() {
		t.Fatal("new client rejected callback admission")
	}

	done := make(chan error, 1)
	go func() {
		done <- client.CloseAndWait(context.Background())
	}()
	select {
	case err := <-done:
		t.Fatalf("CloseAndWait returned before callback completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	client.endCallback()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CloseAndWait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseAndWait did not return after callback completed")
	}
	if client.beginCallback() {
		client.endCallback()
		t.Fatal("closed client admitted another callback")
	}
}
