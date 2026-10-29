package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultIlinkQRCodePollTimeoutSeconds = 35
const maxIlinkResponseBytes = 1 << 20

type IlinkLoginClient struct {
	httpClient *http.Client
}

type IlinkLoginOptions struct {
	LoginBaseURL          string
	BotType               string
	IlinkAppID            string
	IlinkAppClientVersion string
	RequestTimeoutSeconds int
	AllowInsecureLoopback bool
}

type IlinkQRCode struct {
	Code string
	URL  string
}

type IlinkQRCodeStatus struct {
	Status       string
	BotToken     string
	IlinkBotID   string
	BaseURL      string
	IlinkUserID  string
	RedirectHost string
}

func NewIlinkLoginClient(client *http.Client) *IlinkLoginClient {
	return &IlinkLoginClient{httpClient: client}
}

func (c *IlinkLoginClient) FetchQRCode(ctx context.Context, opts IlinkLoginOptions, localTokens []string) (IlinkQRCode, error) {
	opts = normalizeIlinkLoginOptions(opts)
	if err := validateIlinkEndpointOrigin(opts.LoginBaseURL, opts.AllowInsecureLoopback); err != nil {
		return IlinkQRCode{}, err
	}
	body := map[string]any{"local_token_list": compactNonBlank(localTokens, 10)}
	raw, err := json.Marshal(body)
	if err != nil {
		return IlinkQRCode{}, err
	}
	endpoint, err := ilinkLoginEndpoint(opts.LoginBaseURL, "ilink/bot/get_bot_qrcode", url.Values{"bot_type": []string{opts.BotType}})
	if err != nil {
		return IlinkQRCode{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return IlinkQRCode{}, err
	}
	headers, err := ilinkLoginHeaders(opts, true)
	if err != nil {
		return IlinkQRCode{}, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http(opts).Do(req)
	if err != nil {
		return IlinkQRCode{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return IlinkQRCode{}, fmt.Errorf("weixin get_bot_qrcode HTTP %d", resp.StatusCode)
	}
	var out struct {
		QRCode             string `json:"qrcode"`
		QRCodeImageContent string `json:"qrcode_img_content"`
		ErrCode            int    `json:"errcode"`
		ErrMessage         string `json:"errmsg"`
		Ret                int    `json:"ret"`
	}
	if err := decodeIlinkJSON(resp.Body, &out); err != nil {
		return IlinkQRCode{}, err
	}
	if out.ErrCode != 0 || out.Ret != 0 {
		message := strings.TrimSpace(out.ErrMessage)
		if message == "" {
			message = "non-zero response"
		}
		return IlinkQRCode{}, fmt.Errorf("weixin get_bot_qrcode failed: %s (ret=%d errcode=%d)", message, out.Ret, out.ErrCode)
	}
	qr := IlinkQRCode{Code: strings.TrimSpace(out.QRCode), URL: strings.TrimSpace(out.QRCodeImageContent)}
	if qr.Code == "" || qr.URL == "" {
		return IlinkQRCode{}, errors.New("weixin get_bot_qrcode response missing qrcode/qrcode_img_content")
	}
	return qr, nil
}

func (c *IlinkLoginClient) PollQRCodeStatus(ctx context.Context, opts IlinkLoginOptions, qrcode string, verifyCode string) (IlinkQRCodeStatus, error) {
	opts = normalizeIlinkLoginOptions(opts)
	if err := validateIlinkEndpointOrigin(opts.LoginBaseURL, opts.AllowInsecureLoopback); err != nil {
		return IlinkQRCodeStatus{}, err
	}
	qrcode = strings.TrimSpace(qrcode)
	if qrcode == "" {
		return IlinkQRCodeStatus{}, errors.New("qrcode is required")
	}
	query := url.Values{"qrcode": []string{qrcode}}
	if strings.TrimSpace(verifyCode) != "" {
		query.Set("verify_code", strings.TrimSpace(verifyCode))
	}
	endpoint, err := ilinkLoginEndpoint(opts.LoginBaseURL, "ilink/bot/get_qrcode_status", query)
	if err != nil {
		return IlinkQRCodeStatus{}, err
	}
	pollCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		pollCtx, cancel = context.WithTimeout(ctx, time.Duration(defaultIlinkQRCodePollTimeoutSeconds)*time.Second)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return IlinkQRCodeStatus{}, err
	}
	headers, err := ilinkLoginHeaders(opts, false)
	if err != nil {
		return IlinkQRCodeStatus{}, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http(opts).Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
			return IlinkQRCodeStatus{Status: "wait"}, nil
		}
		return IlinkQRCodeStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return IlinkQRCodeStatus{}, fmt.Errorf("weixin get_qrcode_status HTTP %d", resp.StatusCode)
	}
	var out struct {
		Status       string `json:"status"`
		BotToken     string `json:"bot_token"`
		IlinkBotID   string `json:"ilink_bot_id"`
		BaseURL      string `json:"baseurl"`
		IlinkUserID  string `json:"ilink_user_id"`
		RedirectHost string `json:"redirect_host"`
		ErrCode      int    `json:"errcode"`
		ErrMessage   string `json:"errmsg"`
		Ret          int    `json:"ret"`
	}
	if err := decodeIlinkJSON(resp.Body, &out); err != nil {
		return IlinkQRCodeStatus{}, err
	}
	if out.ErrCode != 0 || out.Ret != 0 {
		message := strings.TrimSpace(out.ErrMessage)
		if message == "" {
			message = "non-zero response"
		}
		return IlinkQRCodeStatus{}, fmt.Errorf("weixin get_qrcode_status failed: %s (ret=%d errcode=%d)", message, out.Ret, out.ErrCode)
	}
	status := IlinkQRCodeStatus{
		Status:       strings.TrimSpace(out.Status),
		BotToken:     strings.TrimSpace(out.BotToken),
		IlinkBotID:   strings.TrimSpace(out.IlinkBotID),
		BaseURL:      strings.TrimSpace(out.BaseURL),
		IlinkUserID:  strings.TrimSpace(out.IlinkUserID),
		RedirectHost: strings.TrimSpace(out.RedirectHost),
	}
	if status.Status == "" {
		return IlinkQRCodeStatus{}, errors.New("weixin get_qrcode_status response missing status")
	}
	return status, nil
}

func normalizeIlinkLoginOptions(opts IlinkLoginOptions) IlinkLoginOptions {
	if strings.TrimSpace(opts.LoginBaseURL) == "" {
		opts.LoginBaseURL = defaultIlinkLoginBaseURL
	}
	if strings.TrimSpace(opts.BotType) == "" {
		opts.BotType = defaultIlinkBotType
	}
	if strings.TrimSpace(opts.IlinkAppID) == "" {
		opts.IlinkAppID = defaultIlinkAppID
	}
	if strings.TrimSpace(opts.IlinkAppClientVersion) == "" {
		opts.IlinkAppClientVersion = defaultIlinkAppClientVersion
	}
	if opts.RequestTimeoutSeconds <= 0 {
		opts.RequestTimeoutSeconds = defaultRequestTimeoutSeconds
	}
	return opts
}

func (c *IlinkLoginClient) http(opts IlinkLoginOptions) *http.Client {
	var client http.Client
	if c != nil && c.httpClient != nil {
		client = *c.httpClient
	} else {
		client.Timeout = time.Duration(opts.RequestTimeoutSeconds) * time.Second
		client.Transport = secureIlinkTransport()
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func ilinkLoginHeaders(opts IlinkLoginOptions, post bool) (map[string]string, error) {
	headers := map[string]string{
		"iLink-App-Id":            opts.IlinkAppID,
		"iLink-App-ClientVersion": opts.IlinkAppClientVersion,
	}
	if post {
		uin, err := newRandomWechatUIN()
		if err != nil {
			return nil, err
		}
		headers["Content-Type"] = "application/json"
		headers["AuthorizationType"] = "ilink_bot_token"
		headers["X-WECHAT-UIN"] = uin
	}
	return headers, nil
}

func ilinkLoginEndpoint(baseURL string, path string, query url.Values) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultIlinkLoginBaseURL
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "https://" + baseURL
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return "", err
	}
	rel, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	out := parsed.ResolveReference(rel)
	out.RawQuery = query.Encode()
	return out.String(), nil
}

func ValidateIlinkBotBaseURL(baseURL string, allowInsecureLoopback bool) error {
	return validateIlinkEndpointOrigin(NormalizeIlinkBotBaseURL(baseURL), allowInsecureLoopback)
}

func validateIlinkEndpointOrigin(baseURL string, allowInsecureLoopback bool) error {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultIlinkLoginBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid Weixin iLink URL: %w", err)
	}
	if parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Weixin iLink URL must be an origin/path without credentials, query, or fragment")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if allowInsecureLoopback && isLoopbackIlinkHost(host) && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		return nil
	}
	if parsed.Scheme != "https" {
		return errors.New("Weixin iLink URL must use HTTPS")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return errors.New("Weixin iLink URL must use the standard HTTPS port")
	}
	if host != "ilinkai.weixin.qq.com" && !strings.HasSuffix(host, ".weixin.qq.com") {
		return errors.New("Weixin iLink URL must use an official weixin.qq.com host")
	}
	return nil
}

func isLoopbackIlinkHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func decodeIlinkJSON(body io.Reader, target any) error {
	limited := io.LimitReader(body, maxIlinkResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(raw) > maxIlinkResponseBytes {
		return errors.New("Weixin iLink response exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("Weixin iLink response contains trailing JSON")
	}
	return nil
}

func compactNonBlank(values []string, limit int) []string {
	if limit <= 0 {
		limit = len(values)
	}
	out := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == gwconfigMaskValue() {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func NormalizeIlinkBotBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return defaultIlinkBaseURL
	}
	if strings.HasSuffix(baseURL, "/ilink/bot") {
		return baseURL
	}
	return baseURL + "/ilink/bot"
}

func NormalizeIlinkLoginBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return defaultIlinkLoginBaseURL
	}
	if strings.HasSuffix(baseURL, "/ilink/bot") {
		return strings.TrimSuffix(baseURL, "/ilink/bot")
	}
	return baseURL
}

func gwconfigMaskValue() string {
	return "********"
}
