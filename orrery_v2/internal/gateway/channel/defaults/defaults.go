package defaults

import (
	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/channel/dingtalk"
	"orrery_v2/internal/gateway/channel/feishu"
	"orrery_v2/internal/gateway/channel/weixin"
	gwconfig "orrery_v2/internal/gateway/config"
)

func NewRegistry() *channel.Registry {
	reg := channel.NewRegistry()
	reg.RegisterFeishu(func(config gwconfig.FeishuAdapterConfig) channel.Adapter {
		return feishu.NewAdapter(config, feishu.NewSDKClient(nil))
	})
	reg.RegisterWeixin(func(config gwconfig.WeixinAdapterConfig) channel.Adapter {
		return weixin.NewAdapter(config, weixin.NewClient())
	})
	reg.Register("dingtalk", func(config gwconfig.PlatformAdapterConfig) channel.Adapter {
		return dingtalk.NewAdapter(config, dingtalk.NewSDKClient(nil))
	})
	return reg
}
