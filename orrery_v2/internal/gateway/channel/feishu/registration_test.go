package feishu

import (
	"reflect"
	"testing"
)

func TestRegistrationOptionsDeclareOnlyRequiredGatewayCapabilities(t *testing.T) {
	options := RegistrationOptions()
	if options == nil || options.Addons == nil || options.Addons.Preset == nil || *options.Addons.Preset {
		t.Fatalf("registration options must use the minimal application preset: %+v", options)
	}
	if got, want := options.Addons.Scopes.Tenant, []string{RegistrationMessageSendScope}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tenant scopes = %v, want %v", got, want)
	}
	if got, want := options.Addons.Events.Items.Tenant, []string{RegistrationMessageEvent}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tenant events = %v, want %v", got, want)
	}
	if !options.CreateOnly {
		t.Fatal("registration must create a dedicated application")
	}
	if options.QRCodeURLValidator == nil {
		t.Fatal("registration must validate the provider-issued QR code URL")
	}
}

func TestRegistrationQRCodeValidatorOnlyAllowsOfficialHTTPSOrigins(t *testing.T) {
	tests := []struct {
		url       string
		wantError bool
	}{
		{url: "https://accounts.feishu.cn/page/launcher?ticket=one"},
		{url: "https://open.feishu.cn/page/launcher?user_code=ABCD-EFGH"},
		{url: "https://accounts.larksuite.com/page/launcher?ticket=one"},
		{url: "https://open.larksuite.com/page/launcher?user_code=ABCD-EFGH"},
		{url: "http://accounts.feishu.cn/page/launcher", wantError: true},
		{url: "https://accounts.feishu.cn.attacker.example/page/launcher", wantError: true},
		{url: "https://open.feishu.cn.attacker.example/page/launcher", wantError: true},
		{url: "https://127.0.0.1/page/launcher", wantError: true},
		{url: "https://user@accounts.feishu.cn/page/launcher", wantError: true},
		{url: "https://accounts.feishu.cn:8443/page/launcher", wantError: true},
	}
	for _, test := range tests {
		err := validateRegistrationQRCodeURL(test.url)
		if test.wantError && err == nil {
			t.Fatalf("URL %q was accepted", test.url)
		}
		if !test.wantError && err != nil {
			t.Fatalf("URL %q was rejected: %v", test.url, err)
		}
	}
}
