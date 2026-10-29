# Weixin iLink Gateway

Orrery 的微信接入默认不再通过 OpenClaw hook companion 中转。Gateway 直接使用微信 iLink Bot HTTP API：

```text
Weixin iLink Bot API -> Orrery Gateway -> Orrery discussion/agent -> Weixin iLink Bot API
```

也就是说：

- Orrery 通过 `getupdates` 主动轮询微信入站消息。
- Orrery 跑平台内 discussion/agent 流程。
- Orrery 通过 `sendmessage` 主动把回复发回微信。
- OpenClaw 不再负责把 Orrery 的回复代发给微信。

## 系统连接与个人绑定

管理员先在 `Settings -> Gateway -> Weixin` 配置系统级连接：

字段来源：

- 系统配置不包含微信 Token。管理员不能扫码、粘贴或设置全局微信 Token。
- `loginBaseUrl` 是扫码登录接口地址，默认 `https://ilinkai.weixin.qq.com`。
- `botType` 是微信 iLink 登录二维码类型，默认 `3`。
- `ilinkBaseUrl`、`getUpdatesPath`、`sendMessagePath` 是消息收发接口配置，默认对应微信 iLink Bot API。
- 这里不显示或编辑个人微信用户 ID、个人默认 Bot、当前 Bot 和个人账号 Token。

系统连接可用后，每个用户从头像菜单进入 `个人管理`：

- 管理员只需先启用系统级 Weixin Gateway；零账号状态可以正常保存并等待用户绑定。
- 选择自己的默认 Bot，再使用本人的微信扫码绑定。
- 后端从已认证会话取得真人用户 ID；客户端不能指定或替换其他真人用户。
- 一个微信用户 ID 只绑定一个系统真人用户。扫码成功后，后端同时保存该微信账号的独立 Bot Token、默认 Bot 和当前 Bot。
- 个人扫码只更新当前租户下的个人账号、真人和 Bot 绑定，不会启用 Gateway，也不会覆盖系统级 iLink 地址。
- 用户可以解除自己的绑定，也可以修改默认 Bot。管理员设置接口返回配置时会移除全部个人绑定和个人账号信息。
- 微信内发送 `/换人` 获取可用 Bot 列表；发送 `/换人 botID` 或 `/换人 名称` 切换当前 Bot。
- 没有个人绑定或默认 Bot 时不会走兜底 Agent，只返回绑定提示。

```yaml
weixin:
  enabled: true
  extra:
    ilinkBaseUrl: https://ilinkai.weixin.qq.com/ilink/bot
    getUpdatesPath: getupdates
    sendMessagePath: sendmessage
    loginBaseUrl: https://ilinkai.weixin.qq.com
    botType: "3"
    channelVersion: 2.4.6
    ilinkAppId: bot
    ilinkAppClientVersion: "132102"
    statePath: "" # empty means <gateway config dir>/state/weixin-ilink-state.json
    seenMessageLimit: 5000
    # 以下映射由个人管理流程维护，Settings API 会对管理员隐藏。
    agentBindings: {}
    activeAgentBindings: {}
    weixinUserBindings: {}
    weixinAccounts: {}
    contextTokens: 8192
    replyChunkSize: 1800
    pollIntervalSeconds: 2
    requestTimeoutSeconds: 60
```

`agentBindings`、`activeAgentBindings`、`weixinUserBindings` 和 `weixinAccounts` 是后端保护字段。结构化和 YAML 设置更新都会保留已有值，前端不能借设置接口读取、覆盖或清空它们。未绑定的微信用户会收到：

```text
当前微信用户未注册，请先登录 Orrery，在头像菜单的个人管理中完成微信绑定并选择默认 Bot。
```

保存系统级 Gateway 配置后，生产服务会热重载全部 Gateway channel；个人绑定变化只重载微信 channel 和路由，不中断其他平台。重载失败时，持久化配置和运行态都会回滚到修改前的版本。默认 Docker Compose 不再发布独立的微信桥接入站端口。

## Reliability

- `getupdates` cursor 和已处理消息 ID 会持久化到 `statePath`。不填时默认写入 Gateway 配置目录下的 `state/weixin-ilink-state.json`。
- Orrery 只有在入站消息完成处理、回复成功调用 `sendmessage` 后，才推进 `getupdates` cursor。
- 如果 `sendmessage` 或协议校验失败，本轮轮询会返回错误并保留旧 cursor，下一轮继续重试。

## Security

- 不存在系统级或全局微信 Token；零个人账号时 channel 保持空闲，不尝试建立消息连接。
- 个人扫码取得的账号 Token 按 `(tenant_id, weixin_user_id)` 独立保存，不会通过 Settings API 返回。
- 个人绑定会话只能由发起扫码的已认证真人用户轮询或提交验证码。
- 只有 iLink 客户端标记为已验证的单聊发送者，才能映射为所绑定的真人身份。群聊消息、旧 bridge 上报的未验证身份，以及把 Bot/账号 ID 冒充发送者的消息都不会继承真人权限。
- 生产环境的登录地址、扫码重定向地址和消息 API 地址必须使用标准端口 HTTPS，且域名必须是官方 `weixin.qq.com` 域名。HTTP、环回地址、私网地址、自定义域名、带凭据或查询参数的地址都会被拒绝；仅非生产测试环境可显式使用环回地址。
- 登录客户端不跟随 HTTP 重定向，响应体上限为 1 MiB；验证码仅接受 4 到 8 位 ASCII 数字，扫码会话有总量上限且必须属于已认证用户和租户。
- `sendmessage` 由 Orrery 后端主动调用。
- iLink 消息接口请求会携带 `AuthorizationType: ilink_bot_token`、`Authorization: Bearer <token>` 和随机 `X-WECHAT-UIN`。
- 不需要开放 `18792` 之类的公网入站端口。

## 当前边界

系统级 Gateway 连接配置仍由平台管理员统一维护；微信账号、真人绑定、默认/当前 Bot 则以 `(tenant_id, weixin_user_id)` 作为数据库主键。个人接口只采用已认证 Principal 的 tenant，不能用请求头覆盖；iLink 入站必须同时匹配 Bot 账号和发送者才能解析租户。绑定和 `/换人` 都只查询该租户组织内的 Agent，执行消息前还会再次验证真人与 Agent 的组织归属。

数据库迁移会把历史未分租户的微信绑定归入 `system` tenant。多副本部署仍应在后续为整份系统 Gateway 配置增加数据库 revision/CAS；当前单进程更新已经串行化，但平台级配置的跨副本同时编辑还不是强一致模型。
