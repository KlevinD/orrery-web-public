package feishu

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	larkregistration "github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

const (
	RegistrationMessageSendScope = "im:message:send_as_bot"
	RegistrationMessageEvent     = "im.message.receive_v1"
)

type RegistrationQRCode struct {
	URL      string
	ExpireIn int
}

type RegistrationStatus struct {
	Status   string
	Interval int
}

type RegistrationResult struct {
	AppID       string
	AppSecret   string
	OpenID      string
	TenantBrand string
}

type RegistrationClient interface {
	Register(
		ctx context.Context,
		onQRCode func(RegistrationQRCode),
		onStatus func(RegistrationStatus),
	) (RegistrationResult, error)
}

type SDKRegistrationClient struct{}

func NewSDKRegistrationClient() *SDKRegistrationClient {
	return &SDKRegistrationClient{}
}

func (c *SDKRegistrationClient) Register(
	ctx context.Context,
	onQRCode func(RegistrationQRCode),
	onStatus func(RegistrationStatus),
) (RegistrationResult, error) {
	options := RegistrationOptions()
	options.OnQRCode = func(info *larkregistration.QRCodeInfo) {
		if info != nil && onQRCode != nil {
			onQRCode(RegistrationQRCode{URL: strings.TrimSpace(info.URL), ExpireIn: info.ExpireIn})
		}
	}
	options.OnStatusChange = func(info *larkregistration.StatusChangeInfo) {
		if info != nil && onStatus != nil {
			onStatus(RegistrationStatus{Status: strings.TrimSpace(info.Status), Interval: info.Interval})
		}
	}
	result, err := larkregistration.RegisterApp(ctx, options)
	if err != nil {
		return RegistrationResult{}, err
	}
	if result == nil || strings.TrimSpace(result.ClientID) == "" || strings.TrimSpace(result.ClientSecret) == "" {
		return RegistrationResult{}, errors.New("feishu application registration returned incomplete credentials")
	}
	registered := RegistrationResult{AppID: strings.TrimSpace(result.ClientID), AppSecret: strings.TrimSpace(result.ClientSecret)}
	if result.UserInfo != nil {
		registered.OpenID = strings.TrimSpace(result.UserInfo.OpenID)
		registered.TenantBrand = strings.TrimSpace(result.UserInfo.TenantBrand)
	}
	return registered, nil
}

func RegistrationOptions() *larkregistration.Options {
	minimal := false
	return &larkregistration.Options{
		Source: "orrery-gateway",
		AppPreset: &larkregistration.AppPreset{
			Name: "Orrery",
			Desc: "Orrery Gateway",
		},
		Addons: &larkregistration.AppAddons{
			Preset: &minimal,
			Scopes: larkregistration.AppAddonsScopes{
				Tenant: []string{RegistrationMessageSendScope},
			},
			Events: larkregistration.AppAddonsEvents{
				Items: larkregistration.AppAddonsEventItems{
					Tenant: []string{RegistrationMessageEvent},
				},
			},
		},
		CreateOnly:         true,
		QRCodeURLValidator: validateRegistrationQRCodeURL,
	}
}

func validateRegistrationQRCodeURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("invalid Feishu registration URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" || (parsed.Port() != "" && parsed.Port() != "443") || !officialRegistrationHost(host) {
		return fmt.Errorf("Feishu registration URL must use an official HTTPS origin")
	}
	return nil
}

func officialRegistrationHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "accounts.feishu.cn", "open.feishu.cn", "accounts.larksuite.com", "open.larksuite.com":
		return true
	default:
		return false
	}
}
