package weixin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type IlinkBotMultiClient struct {
	mu         sync.Mutex
	wg         sync.WaitGroup
	clients    map[string]*IlinkBotClient
	starting   bool
	stopping   bool
	cancel     context.CancelFunc
	generation uint64
	newClient  func() *IlinkBotClient
}

func NewIlinkBotMultiClient() *IlinkBotMultiClient {
	return &IlinkBotMultiClient{clients: map[string]*IlinkBotClient{}}
}

func (c *IlinkBotMultiClient) Start(ctx context.Context, opts Options, logger Logger, inbound InboundProcessor) error {
	opts = opts.WithDefaults()
	if len(opts.Accounts) == 0 {
		return errors.New("weixin account bindings are required")
	}
	c.mu.Lock()
	if c.starting || c.stopping || len(c.clients) > 0 {
		c.mu.Unlock()
		return errors.New("weixin ilink bot multi client already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.generation++
	generation := c.generation
	c.starting = true
	c.cancel = cancel
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()

	type accountClient struct {
		accountID string
		options   Options
		client    *IlinkBotClient
	}
	candidates := make([]accountClient, 0, len(opts.Accounts))
	accountIDs := make(map[string]struct{}, len(opts.Accounts))
	for _, account := range opts.Accounts {
		accountOpts := opts
		accountOpts.Accounts = nil
		accountOpts.BotToken = strings.TrimSpace(account.BotToken)
		accountOpts.AccountID = strings.TrimSpace(account.BotID)
		accountOpts.AccountUserID = strings.TrimSpace(account.UserID)
		accountOpts.IlinkBaseURL = firstNonBlank(account.IlinkBaseURL, opts.IlinkBaseURL)
		accountOpts.IlinkCDNBaseURL = firstNonBlank(account.IlinkCDNBaseURL, opts.IlinkCDNBaseURL)
		accountOpts.StatePath = firstNonBlank(account.StatePath, accountStatePath(opts.StatePath, account.TenantID, accountOpts.AccountUserID, accountOpts.AccountID))
		accountOpts.XWechatUIN = firstNonBlank(account.XWechatUIN, opts.XWechatUIN)
		accountID := strings.TrimSpace(accountOpts.AccountID)
		if accountID == "" {
			c.abortStart(cancel, generation)
			return errors.New("weixin account id is required")
		}
		if _, exists := accountIDs[accountID]; exists {
			c.abortStart(cancel, generation)
			return errors.New("duplicate weixin account id")
		}
		accountIDs[accountID] = struct{}{}
		client := NewIlinkBotClient()
		if c.newClient != nil {
			client = c.newClient()
		}
		if client == nil {
			c.abortStart(cancel, generation)
			return errors.New("weixin ilink account client factory returned nil")
		}
		candidates = append(candidates, accountClient{accountID: accountID, options: accountOpts, client: client})
	}

	type startResult struct {
		candidate accountClient
		err       error
	}
	results := make(chan startResult, len(candidates))
	for _, candidate := range candidates {
		candidate := candidate
		go func() {
			results <- startResult{candidate: candidate, err: candidate.client.Start(runCtx, candidate.options, logger, inbound)}
		}()
	}
	started := make(map[string]*IlinkBotClient, len(candidates))
	var firstErr error
	for range candidates {
		result := <-results
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
				cancel()
			}
			continue
		}
		started[result.candidate.accountID] = result.candidate.client
	}
	if firstErr != nil {
		stopErr := stopIlinkClients(started)
		c.abortStart(cancel, generation)
		return errors.Join(firstErr, stopErr)
	}
	c.mu.Lock()
	if !c.starting || c.cancel == nil || c.generation != generation {
		c.mu.Unlock()
		cancel()
		return errors.Join(context.Canceled, stopIlinkClients(started))
	}
	c.clients = started
	c.starting = false
	c.mu.Unlock()
	return nil
}

func (c *IlinkBotMultiClient) abortStart(cancel context.CancelFunc, generation uint64) {
	cancel()
	c.mu.Lock()
	if c.generation == generation {
		c.cancel = nil
		c.starting = false
	}
	c.mu.Unlock()
}

func stopIlinkClients(clients map[string]*IlinkBotClient) error {
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stopErrors []error
	for _, client := range clients {
		if err := client.Stop(stopCtx); err != nil {
			stopErrors = append(stopErrors, err)
		}
	}
	return errors.Join(stopErrors...)
}

func (c *IlinkBotMultiClient) SendReply(ctx context.Context, message OutboundMessage) error {
	_, err := c.SendWithReceipt(ctx, message)
	return err
}

func (c *IlinkBotMultiClient) SendWithReceipt(ctx context.Context, message OutboundMessage) (string, error) {
	accountID := strings.TrimSpace(message.AccountID)
	if accountID == "" {
		return "", errors.New("weixin outbound account id is required")
	}
	c.mu.Lock()
	client := c.clients[accountID]
	c.mu.Unlock()
	if client == nil {
		return "", errors.New("weixin outbound account is not connected")
	}
	return client.SendWithReceipt(ctx, message)
}

func (c *IlinkBotMultiClient) Stop(ctx context.Context) error {
	c.mu.Lock()
	if !c.stopping {
		c.generation++
		c.stopping = true
	}
	stopGeneration := c.generation
	cancel := c.cancel
	c.starting = false
	clients := make([]*IlinkBotClient, 0, len(c.clients))
	for _, client := range c.clients {
		clients = append(clients, client)
	}
	c.clients = map[string]*IlinkBotClient{}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var firstErr error
	for _, client := range clients {
		if err := client.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return errors.Join(firstErr, ctx.Err())
	case <-done:
	}
	c.mu.Lock()
	if c.generation == stopGeneration {
		c.cancel = nil
		c.stopping = false
	}
	c.mu.Unlock()
	return firstErr
}

func (c *IlinkBotMultiClient) GatewayConnectionHealthy() bool {
	c.mu.Lock()
	clients := make([]*IlinkBotClient, 0, len(c.clients))
	for _, client := range c.clients {
		clients = append(clients, client)
	}
	c.mu.Unlock()
	if len(clients) == 0 {
		return false
	}
	for _, client := range clients {
		if client == nil || !client.GatewayConnectionHealthy() {
			return false
		}
	}
	return true
}

func (c *IlinkBotMultiClient) GatewayAccountConnectionHealthy(accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false
	}
	c.mu.Lock()
	client := c.clients[accountID]
	c.mu.Unlock()
	return client != nil && client.GatewayConnectionHealthy()
}

func accountStatePath(base string, tenantID string, userID string, botID string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	tenantID = strings.TrimSpace(tenantID)
	userID = strings.TrimSpace(userID)
	botID = strings.TrimSpace(botID)
	if tenantID == "" && userID == "" && botID == "" {
		return base
	}
	key := tenantID + "\x00" + userID + "\x00" + botID
	sum := sha256.Sum256([]byte(key))
	suffix := hex.EncodeToString(sum[:])[:12]
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(filepath.Base(base), ext)
	return filepath.Join(filepath.Dir(base), name+"-"+suffix+ext)
}
