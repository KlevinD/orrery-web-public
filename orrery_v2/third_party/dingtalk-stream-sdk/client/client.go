package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/handler"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/logger"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/payload"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/plugin"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/utils"
)

/**
 * @Author linya.jj
 * @Date 2023/3/22 14:23
 */

type StreamClient struct {
	AppCredential *AppCredentialConfig
	UserAgent     *UserAgentConfig
	AutoReconnect bool

	subscriptions map[string]map[string]handler.IFrameHandler

	conn              *websocket.Conn
	sessionId         string
	mutex             sync.Mutex
	writeMutex        sync.Mutex
	lifecycleWG       sync.WaitGroup
	lifecycleCtx      context.Context
	lifecycleStop     context.CancelFunc
	stopCh            chan struct{}
	stopOnce          sync.Once
	stopped           bool
	stateHandler      func(bool)
	endpointValidator func(string) error
	extras            map[string]string
	openApiHost       string
	proxy             string
	keepAliveIdle     time.Duration
	callbackSlots     chan struct{}
}

const maxWebSocketMessageBytes = 4 << 20

func NewStreamClient(options ...ClientOption) *StreamClient {
	lifecycleCtx, lifecycleStop := context.WithCancel(context.Background())
	cli := &StreamClient{
		keepAliveIdle: 120 * time.Second,
		callbackSlots: make(chan struct{}, 64),
		stopCh:        make(chan struct{}),
		lifecycleCtx:  lifecycleCtx,
		lifecycleStop: lifecycleStop,
	}

	defaultOptions := []ClientOption{
		WithSubscription(utils.SubscriptionTypeKSystem, "disconnect", cli.OnDisconnect),
		WithSubscription(utils.SubscriptionTypeKSystem, "ping", cli.OnPing),
		WithUserAgent(NewDingtalkGoSDKUserAgent()),
		WithAutoReconnect(true),
	}

	for _, option := range defaultOptions {
		option(cli)
	}

	for _, option := range options {
		if option == nil {
			continue
		}

		option(cli)
	}

	return cli
}

func (cli *StreamClient) Start(ctx context.Context) error {
	startCtx, cancel := context.WithCancel(ctx)
	stopCancellation := context.AfterFunc(cli.lifecycleCtx, cancel)
	defer func() {
		stopCancellation()
		cancel()
	}()
	cli.mutex.Lock()
	defer cli.mutex.Unlock()

	if cli.stopped {
		return errors.New("stream client is stopped")
	}
	if cli.callbackSlots == nil {
		cli.callbackSlots = make(chan struct{}, 64)
	}
	if cli.conn != nil {
		return nil
	}

	endpoint, err := cli.GetConnectionEndpoint(startCtx)
	if err != nil {
		return err
	}

	wssUrl, err := buildStreamConnectionURL(endpoint.Endpoint, endpoint.Ticket)
	if err != nil {
		return err
	}

	header := make(http.Header)

	var dialer *websocket.Dialer

	if len(cli.proxy) == 0 {
		dialer = websocket.DefaultDialer
	} else {
		proxyURL, err := url.Parse(cli.proxy)
		if err != nil {
			return err
		}
		dialer = &websocket.Dialer{
			Proxy: http.ProxyURL(proxyURL),
		}
	}

	conn, resp, err := dialer.DialContext(startCtx, wssUrl, header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return err
	}

	// 建连失败
	if resp.StatusCode >= http.StatusBadRequest {
		if resp.Body != nil {
			defer resp.Body.Close()
		}
		return utils.ErrorFromHttpResponseBody(resp)
	}
	conn.SetReadLimit(maxWebSocketMessageBytes)

	cli.conn = conn
	cli.sessionId = endpoint.Ticket

	logger.GetLogger().Infof("connect success")

	cli.lifecycleWG.Add(1)
	go cli.processLoop(conn)
	go cli.notifyConnectionState(true)

	return nil
}

func (cli *StreamClient) processLoop(conn *websocket.Conn) {
	defer func() {
		if err := recover(); err != nil {
			logger.GetLogger().Errorf("connection process panic due to unknown reason, error=[%s]", err)
		}
		cli.disconnect(conn)
		cli.notifyConnectionState(false)
		if cli.shouldReconnect() {
			cli.lifecycleWG.Add(1)
			go cli.reconnect()
		}
		cli.lifecycleWG.Done()
	}()

	if conn == nil {
		logger.GetLogger().Errorf("connection process connect nil, maybe disconnected.")
		return
	}

	readChan := make(chan []byte, 1)
	pongChan := make(chan struct{}, 1)
	closeChan := make(chan struct{}, 1)

	conn.SetPongHandler(func(appData string) error {
		select {
		case pongChan <- struct{}{}:
		default:
		}
		return nil
	})
	//开始启动协程读数据
	cli.lifecycleWG.Add(1)
	go func() {
		defer cli.lifecycleWG.Done()
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				logger.GetLogger().Errorf("connection process read message error: messageType=[%d] payloadBytes=[%d] error=[%s]", messageType, len(message), err)
				select {
				case closeChan <- struct{}{}:
				default:
				}
				return
			}
			if messageType == websocket.TextMessage {
				select {
				case readChan <- message:
				case <-cli.stopCh:
					return
				}
			}
		}
	}()

	//循环处理事件
	for {
		timer := time.NewTimer(cli.keepAliveIdle)
		select {
		case <-cli.stopCh:
			timer.Stop()
			return
		case msg := <-readChan:
			timer.Stop()
			select {
			case cli.callbackSlots <- struct{}{}:
			case <-cli.stopCh:
				return
			}
			cli.lifecycleWG.Add(1)
			go func() {
				defer cli.lifecycleWG.Done()
				defer func() { <-cli.callbackSlots }()
				cli.processDataFrame(msg)
			}()
		case <-timer.C:
			cli.writeMutex.Lock()
			e := conn.WriteMessage(websocket.PingMessage, nil)
			cli.writeMutex.Unlock()
			if e != nil {
				logger.GetLogger().Errorf("connection write ping message error: error=[%s]", e)
				return
			}
			cli.lifecycleWG.Add(1)
			go func() {
				defer cli.lifecycleWG.Done()
				select {
				case <-cli.stopCh:
					return
				case <-pongChan:
					return
				case <-time.After(5 * time.Second):
					logger.GetLogger().Errorf("ping time out, connection is closing")
					select {
					case closeChan <- struct{}{}:
					default:
					}
					return
				}
			}()
		case <-closeChan:
			return
		}
	}
}

func (cli *StreamClient) processDataFrame(rawData []byte) {
	defer func() {
		if err := recover(); err != nil {
			logger.GetLogger().Errorf("connection processDataFrame panic, error=[%s]", err)
		}
	}()

	dataFrame, err := payload.DecodeDataFrame(rawData)
	if err != nil {
		logger.GetLogger().Errorf("connection process decode data frame error: length=[%d] error=[%s]", len(rawData), err)
		return
	}

	if dataFrame == nil || dataFrame.Headers == nil {
		logger.GetLogger().Errorf("connection processDataFrame dataFrame nil.")
		return
	}

	var dataAck *payload.DataFrameResponse
	frameHandler, err := cli.GetHandler(dataFrame.Type, dataFrame.GetTopic())
	if err != nil || frameHandler == nil {
		// 没有注册handler，返回404
		dataAck = payload.NewDataFrameResponse(payload.DataFrameResponseStatusCodeKHandlerNotFound)
	} else {
		dataAck, err = frameHandler(cli.lifecycleCtx, dataFrame)

		if err != nil && dataAck == nil {
			dataAck = payload.NewErrorDataFrameResponse(err)
		}
	}

	if dataAck == nil {
		dataAck = payload.NewSuccessDataFrameResponse()
	}

	if dataAck.GetHeader(payload.DataFrameHeaderKMessageId) == "" {
		dataAck.SetHeader(payload.DataFrameHeaderKMessageId, dataFrame.GetMessageId())
	}

	if dataAck.GetHeader(payload.DataFrameHeaderKContentType) == "" {
		dataAck.SetHeader(payload.DataFrameHeaderKContentType, payload.DataFrameContentTypeKJson)
	}

	errSend := cli.SendDataFrameResponse(cli.lifecycleCtx, dataAck)
	sentBytes, _ := json.Marshal(dataAck)
	logger.GetLogger().Debugf("[wire] [websocket] local => remote:\n%s", string(sentBytes))

	if errSend != nil {
		logger.GetLogger().Errorf("connection processDataFrame send response error: error=[%s]", errSend)
	}
}

func (cli *StreamClient) Close() {
	cli.mutex.Lock()
	conn := cli.conn
	if conn != nil {
		cli.conn = nil
		cli.sessionId = ""
	}
	cli.mutex.Unlock()
	if conn != nil {
		cli.writeMutex.Lock()
		defer cli.writeMutex.Unlock()
		if err := conn.Close(); err != nil {
			logger.GetLogger().Errorf("StreamClient close. error=[%s]", err)
		}
	}
}

func (cli *StreamClient) reconnect() {
	defer func() {
		cli.lifecycleWG.Done()
		if err := recover(); err != nil {
			logger.GetLogger().Errorf("reconect panic due to unknown reason. error=[%s]", err)
		}
	}()

	for {
		if !cli.shouldReconnect() {
			return
		}
		startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := cli.Start(startCtx)
		cancel()
		if err != nil {
			logger.GetLogger().Errorf("StreamClient reconnect error. error=[%s]", err)
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-cli.stopCh:
				timer.Stop()
				return
			case <-timer.C:
			}
		} else {
			logger.GetLogger().Infof("StreamClient reconnect success")
			return
		}
	}

}

func (cli *StreamClient) Shutdown(ctx context.Context) error {
	if cli.lifecycleStop != nil {
		cli.lifecycleStop()
	}
	cli.mutex.Lock()
	cli.stopped = true
	cli.AutoReconnect = false
	cli.mutex.Unlock()
	cli.stopOnce.Do(func() { close(cli.stopCh) })
	cli.Close()
	done := make(chan struct{})
	go func() {
		cli.lifecycleWG.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (cli *StreamClient) SetConnectionStateHandler(handler func(bool)) {
	cli.mutex.Lock()
	cli.stateHandler = handler
	cli.mutex.Unlock()
}

func (cli *StreamClient) notifyConnectionState(connected bool) {
	cli.mutex.Lock()
	if connected && (cli.stopped || cli.conn == nil) {
		cli.mutex.Unlock()
		return
	}
	handler := cli.stateHandler
	cli.mutex.Unlock()
	if handler != nil {
		handler(connected)
	}
}

func (cli *StreamClient) shouldReconnect() bool {
	cli.mutex.Lock()
	defer cli.mutex.Unlock()
	return cli.AutoReconnect && !cli.stopped
}

func (cli *StreamClient) disconnect(conn *websocket.Conn) {
	cli.mutex.Lock()
	if cli.conn == conn {
		cli.conn = nil
		cli.sessionId = ""
	}
	cli.mutex.Unlock()
	if conn != nil {
		cli.writeMutex.Lock()
		defer cli.writeMutex.Unlock()
		_ = conn.Close()
	}
}

func (cli *StreamClient) GetHandler(stype, stopic string) (handler.IFrameHandler, error) {
	subs := cli.subscriptions[stype]
	if subs == nil || subs[stopic] == nil {
		return nil, errors.New("HandlerNotRegistedForTypeTopic_" + stype + "_" + stopic)
	}

	return subs[stopic], nil
}

func (cli *StreamClient) CheckConfigValid() error {
	if err := cli.AppCredential.Valid(); err != nil {
		return err
	}

	if err := cli.UserAgent.Valid(); err != nil {
		return err
	}

	if cli.subscriptions == nil {
		return errors.New("subscriptionsNil")
	}

	for ttype, subs := range cli.subscriptions {
		if _, ok := utils.SubscriptionTypeSet[ttype]; !ok {
			return errors.New("UnKnownSubscriptionType_" + ttype)
		}

		if len(subs) <= 0 {
			return errors.New("NoHandlersRegistedForType_" + ttype)
		}

		for ttopic, h := range subs {
			if h == nil {
				return errors.New("HandlerNilForTypeTopic_" + ttype + "_" + ttopic)
			}
		}
	}

	return nil
}

func (cli *StreamClient) GetConnectionEndpoint(ctx context.Context) (*payload.ConnectionEndpointResponse, error) {
	if err := cli.CheckConfigValid(); err != nil {
		return nil, err
	}

	requestModel := payload.ConnectionEndpointRequest{
		ClientId:      cli.AppCredential.ClientId,
		ClientSecret:  cli.AppCredential.ClientSecret,
		UserAgent:     cli.UserAgent.UserAgent,
		Subscriptions: make([]*payload.SubscriptionModel, 0),
		Extras:        cli.extras,
	}
	if localIp, err := utils.GetFirstLanIP(); err == nil {
		requestModel.LocalIP = localIp
	}

	for ttype, subs := range cli.subscriptions {
		for ttopic := range subs {
			requestModel.Subscriptions = append(requestModel.Subscriptions, &payload.SubscriptionModel{
				Type:  ttype,
				Topic: ttopic,
			})
		}
	}

	requestJsonBody, _ := json.Marshal(requestModel)

	var targetHost string
	if len(cli.openApiHost) == 0 {
		targetHost = utils.DefaultOpenApiHost
	} else {
		targetHost = cli.openApiHost
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetHost+utils.GetConnectionEndpointAPIUrl, bytes.NewReader(requestJsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	var transport http.RoundTripper
	if len(cli.proxy) == 0 {
		transport = http.DefaultTransport
	} else {
		proxyURL, err := url.Parse(cli.proxy)

		if err != nil {
			return nil, err
		}
		transport = &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
	}

	baseURL, err := url.Parse(targetHost)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" || baseURL.User != nil {
		return nil, errors.New("invalid open API host")
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second, //设置超时，包含connection时间、任意重定向时间、读取response body时间
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if next.URL.Scheme != baseURL.Scheme || !strings.EqualFold(next.URL.Host, baseURL.Host) {
				return errors.New("cross-origin stream bootstrap redirect is not allowed")
			}
			if len(via) >= 5 {
				return errors.New("too many stream bootstrap redirects")
			}
			return nil
		},
	}

	logger.GetLogger().Debugf("[wire] [http] local => remote:\n%s %s %s\nHost: %s\n%s\n\n%s",
		req.Method, req.URL.RequestURI(), req.Proto, req.Host,
		utils.DumpHeaders(req.Header), requestJsonBody)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	responseJsonBody, err := io.ReadAll(io.LimitReader(resp.Body, maxConnectionEndpointResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(responseJsonBody) > maxConnectionEndpointResponseBytes {
		return nil, errors.New("stream bootstrap response exceeds 1 MiB")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stream bootstrap returned HTTP %d", resp.StatusCode)
	}
	logger.GetLogger().Debugf("[wire] [http] remote => localhost:\n%s %s\n%s\n\n%s",
		resp.Proto, resp.Status,
		utils.DumpHeaders(resp.Header), responseJsonBody)

	endpoint := &payload.ConnectionEndpointResponse{}

	if err := json.Unmarshal(responseJsonBody, endpoint); err != nil {
		return nil, err
	}

	if err := endpoint.Valid(); err != nil {
		return nil, err
	}
	if cli.endpointValidator != nil {
		if err := cli.endpointValidator(endpoint.Endpoint); err != nil {
			return nil, fmt.Errorf("stream endpoint validation failed: %w", err)
		}
	}

	return endpoint, nil
}

const maxConnectionEndpointResponseBytes = 1 << 20

func buildStreamConnectionURL(rawEndpoint, ticket string) (string, error) {
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("invalid stream endpoint")
	}
	query := endpoint.Query()
	query.Set("ticket", ticket)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (cli *StreamClient) OnDisconnect(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
	logger.GetLogger().Debugf("StreamClient.OnDisconnect")

	cli.Close()
	return nil, nil
}

func (cli *StreamClient) OnPing(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
	dfPong := payload.NewDataFrameAckPong(df.GetMessageId())
	dfPong.Data = df.Data

	return dfPong, nil
}

// 返回正常数据包
func (cli *StreamClient) SendDataFrameResponse(ctx context.Context, resp *payload.DataFrameResponse) error {
	if resp == nil {
		return errors.New("SendDataFrameResponseError_ResponseNil")
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	cli.mutex.Lock()
	conn := cli.conn
	cli.mutex.Unlock()
	if conn == nil {
		logger.GetLogger().Errorf("SendDataFrameResponse error, conn nil, maybe disconnected.")
		return errors.New("disconnected")
	}
	cli.writeMutex.Lock()
	defer cli.writeMutex.Unlock()
	return conn.WriteJSON(resp)
}

// 通用注册函数
func (cli *StreamClient) RegisterRouter(stype, stopic string, frameHandler handler.IFrameHandler) {
	if cli.subscriptions == nil {
		cli.subscriptions = make(map[string]map[string]handler.IFrameHandler)
	}

	if _, ok := cli.subscriptions[stype]; !ok {
		cli.subscriptions[stype] = make(map[string]handler.IFrameHandler)
	}

	cli.subscriptions[stype][stopic] = frameHandler
}

// callback类型注册函数
func (cli *StreamClient) RegisterCallbackRouter(topic string, frameHandler handler.IFrameHandler) {
	cli.RegisterRouter(utils.SubscriptionTypeKCallback, topic, frameHandler)
}

// 聊天机器人的注册函数
func (cli *StreamClient) RegisterChatBotCallbackRouter(messageHandler chatbot.IChatBotMessageHandler) {
	cli.RegisterRouter(utils.SubscriptionTypeKCallback, payload.BotMessageCallbackTopic, chatbot.NewDefaultChatBotFrameHandler(messageHandler).OnEventReceived)
}

// AI插件的注册函数
func (cli *StreamClient) RegisterPluginCallbackRouter(messageHandler plugin.IPluginMessageHandler) {
	cli.RegisterRouter(utils.SubscriptionTypeKCallback, payload.PluginMessageCallbackTopic, plugin.NewDefaultPluginFrameHandler(messageHandler).OnEventReceived)
}

// 互动卡片的注册函数
func (cli *StreamClient) RegisterCardCallbackRouter(messageHandler card.ICardCallbackHandler) {
	cli.RegisterRouter(utils.SubscriptionTypeKCallback, payload.CardInstanceCallbackTopic, card.NewDefaultPluginFrameHandler(messageHandler).OnEventReceived)
}

// 事件类型的注册函数
func (cli *StreamClient) RegisterEventRouter(topic string, frameHandler handler.IFrameHandler) {
	cli.RegisterRouter(utils.SubscriptionTypeKEvent, topic, frameHandler)
}

// 所有事件的注册函数
func (cli *StreamClient) RegisterAllEventRouter(frameHandler handler.IFrameHandler) {
	cli.RegisterRouter(utils.SubscriptionTypeKEvent, "*", frameHandler)
}
