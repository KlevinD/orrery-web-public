package httptransport

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"orrery_v2/internal/gateway/channel/weixin"
	gwconfig "orrery_v2/internal/gateway/config"
)

type blockingGatewayWeixinLoginClient struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	err     error

	mu         sync.Mutex
	fetchCalls int
}

func (c *blockingGatewayWeixinLoginClient) FetchQRCode(ctx context.Context, _ weixin.IlinkLoginOptions, _ []string) (weixin.IlinkQRCode, error) {
	c.mu.Lock()
	c.fetchCalls++
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	select {
	case <-ctx.Done():
		return weixin.IlinkQRCode{}, ctx.Err()
	case <-c.release:
	}
	if c.err != nil {
		return weixin.IlinkQRCode{}, c.err
	}
	return weixin.IlinkQRCode{Code: "qr-code", URL: "https://weixin.qq.com/qr-code"}, nil
}

func (c *blockingGatewayWeixinLoginClient) PollQRCodeStatus(context.Context, weixin.IlinkLoginOptions, string, string) (weixin.IlinkQRCodeStatus, error) {
	return weixin.IlinkQRCodeStatus{}, errors.New("unexpected poll")
}

func (c *blockingGatewayWeixinLoginClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetchCalls
}

func TestGatewayWeixinLoginManagerLimitsConcurrentStartsPerUser(t *testing.T) {
	client := &blockingGatewayWeixinLoginClient{started: make(chan struct{}), release: make(chan struct{})}
	manager := newGatewayWeixinLoginManager(false)
	manager.client = client
	config := gwconfig.WeixinAdapterConfig{Extra: map[string]any{
		"_bindingTenantID": "tenant-1",
		"_bindingHumanID":  "human-1",
	}}

	firstResult := make(chan error, 1)
	go func() {
		_, err := manager.start(context.Background(), config)
		firstResult <- err
	}()
	<-client.started

	if _, err := manager.start(context.Background(), config); err == nil || !strings.Contains(err.Error(), "already being started") {
		t.Fatalf("concurrent start error = %v", err)
	}
	if got := client.calls(); got != 1 {
		t.Fatalf("provider FetchQRCode calls = %d, want 1", got)
	}

	close(client.release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first start: %v", err)
	}
}

func TestGatewayWeixinLoginManagerReleasesStartReservationAfterProviderFailure(t *testing.T) {
	client := &blockingGatewayWeixinLoginClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     errors.New("provider unavailable"),
	}
	close(client.release)
	manager := newGatewayWeixinLoginManager(false)
	manager.client = client
	config := gwconfig.WeixinAdapterConfig{Extra: map[string]any{
		"_bindingTenantID": "tenant-1",
		"_bindingHumanID":  "human-1",
	}}

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := manager.start(context.Background(), config); err == nil || !strings.Contains(err.Error(), "provider unavailable") {
			t.Fatalf("attempt %d error = %v", attempt+1, err)
		}
	}
	if got := client.calls(); got != 2 {
		t.Fatalf("provider FetchQRCode calls = %d, want 2", got)
	}
}
