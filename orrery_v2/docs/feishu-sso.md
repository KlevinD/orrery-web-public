# 飞书、People 与 Kratos 登录

## 账号边界

内部部署使用以下单向关系：

```text
飞书通讯录 -> People 人员事实 -> Kratos 账号与登录关系 -> Orrery 会话
```

- People 是姓名、工号、企业邮箱、在职状态、组织归属和 `person_id` 的事实源。
- Kratos 保存密码凭据以及飞书 OIDC 关系；Orrery 不为普通 People 用户保存新密码。
- Orrery 只消费 People 投影中的 `person_id` 和 revision，不根据显示名、任意 subject 或邮箱自动绑定账号。
- `human-superadmin` 不进入 People/Kratos 投影，继续使用 Orrery 本地密码作为应急账号。
- 停用账号，不删除人员、Kratos identity 或既有绑定；停用时撤销 Kratos 与 Orrery 会话。

独立部署仍可使用 `standalone` 模式，直接启用 Orrery 本地登录和 Orrery 飞书绑定。内部部署使用 `people_sso`，不会注册 Orrery 的直接飞书 provider。

## 飞书应用

使用企业自建应用，并为通讯录扫描配置应用身份权限。当前扫描器读取根部门用户列表，使用 `open_id` 作为飞书来源记录 ID，并读取姓名、工号、邮箱、企业邮箱和在职状态。

最小的细粒度 API/字段权限为：

- `contact:contact.base:readonly`
- `contact:user.base:readonly`
- `contact:user.department:readonly`
- `contact:user.employee:readonly`
- `contact:user.employee_number:read`
- `contact:user.email:readonly`

同时把应用的通讯录数据权限范围配置为全部需要同步的部门和人员。扫描根部门并生成可禁用人员的完整快照时，需要覆盖全员；只授权部分范围会得到部分数据，不能用于禁用缺失人员。权限或数据范围变更后，需要创建应用版本并由管理员审核发布。

权限依据：[获取部门下所有用户列表](https://open.feishu.cn/api-explorer?from=op_doc_tab&apiName=list&project=contact&resource=user&version=v3)、[通讯录权限范围](https://open.feishu.cn/document/server-docs/contact-v3/scope/scope_authority)。上线前仍应在飞书开放平台对应接口页复核权限要求。

Kratos 内的飞书 OIDC 回调 URL 为：

```text
https://login.orrery.example.com/self-service/methods/oidc/callback/feishu
```

Orrery 接收 Kratos 会话的回调 URL 为：

```text
https://orrery.example.com/api/auth/identity/kratos/callback
```

## 内部配置

在内部实例 YAML 中配置非敏感值：

```yaml
auth:
  local:
    enabled: true
    superadmin_mfa_enabled: true
  identity:
    mode: people_sso
    auto_link_enabled: false
  kratos:
    public_url: https://login.orrery.example.com
    callback_url: https://orrery.example.com/api/auth/identity/kratos/callback
    display_name: 统一登录
    session_cookie_names: ory_kratos_session
    admin_url: http://kratos:4434
    schema_id: default
    feishu_provider_id: feishu
  feishu:
    app_id: ""
    callback_url: ""
    binding_callback_url: ""
    allowed_tenant_key: tenant_xxx
    auto_provision_enabled: false
    directory_sync_enabled: true
```

`auth.local.enabled` 只为 `human-superadmin` 应急登录保留。普通用户的密码和飞书入口均在 Kratos 登录流中选择。`auto_link_enabled` 和 `auto_provision_enabled` 在内部模式必须保持关闭。

飞书应用注册由 Orrery API 与 Kratos 共用同一份 `.env`；`app_id` 不再在
YAML 中维护第二份值。Secret 通过 Secret Manager、只读 secret 或
allowlisted env 注入：

```dotenv
ORRERY_FEISHU_APP_ID=cli_xxx
ORRERY_FEISHU_APP_SECRET=xxx
ORRERY_KRATOS_ADMIN_TOKEN=
ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET=xxx
```

Kratos Admin API 只连接到 Compose 的 `kratos-control` 网络，不映射宿主机端口。若外部代理为 Admin API 增加 Bearer 校验，再设置 `ORRERY_KRATOS_ADMIN_TOKEN`。

## 同步与开通

目录同步是显式的两阶段流程。第一步拉取并暂存完整快照，只做匹配，不修改 People：

```bash
./orrery-server -config config/orrery.production.yaml \
  -people-directory-sync-dry-run
```

命令返回 `snapshot_id`、人员数以及 create/update/disable/conflict 统计。匹配优先级为工号、已验证企业邮箱、既有 Kratos 飞书关系；冲突项不自动修改。

复核快照后按精确 ID apply：

```bash
./orrery-server -config config/orrery.production.yaml \
  -people-directory-apply-snapshot=people-directory-snapshot_xxx
```

只有 complete 快照允许执行禁用。partial 或 scanning 快照会在修改前失败。新人员默认获得 `basic.user` 和 `member`；既有 `person_id`、密码凭据和 Kratos identity 均保留。命令响应中的 `initial_credentials` 只出现一次，关闭终端结果后不能通过 People 查询重新获取，也不要写入普通日志或流水线产物。

历史人员账号先运行总账盘点：

```bash
./orrery-server -config config/orrery.production.yaml \
  -people-account-reconcile-dry-run
```

确认没有 `duplicate_kratos_username`、`kratos_person_conflict`、`projection_identity_conflict` 或 `identity_binding_conflict` 后执行：

```bash
./orrery-server -config config/orrery.production.yaml \
  -people-account-reconcile-apply
```

reconciliation 不删除或重建账号。兼容的旧密码 hash 只在首次创建 Kratos identity 时导入；无兼容 hash 时生成一次性初始密码并要求首次修改。

使用随机初始密码登录后，Orrery 服务端通过仅内网可达的 Kratos Admin API 读取 `password_change_required` 管理标记，并只创建受限 Orrery 会话；管理接口查询失败时拒绝创建会话。该会话没有业务权限，根布局会强制进入登录页的改密表单。改密成功后由 Orrery 更新 Kratos 密码凭据、撤销该 identity 的 Kratos 与 Orrery 旧会话并清除标记；普通 People 记录的密码字段始终保持为空。Kratos 更新失败时保留受限会话和标记，不放行业务访问。

## 独立 Orrery 模式

独立实例保持：

```yaml
auth:
  identity:
    mode: standalone
    auto_link_enabled: false
  feishu:
    app_id: ""
    callback_url: https://orrery.example.com/api/auth/identity/feishu/callback
    binding_callback_url: https://orrery.example.com/api/profile/identity/feishu/callback
    allowed_tenant_key: tenant_xxx
    auto_provision_enabled: false
    directory_sync_enabled: false
```

该模式下，飞书 SSO 绑定与 Gateway 机器人绑定仍然彼此独立。内部实例不启用这组直接飞书回调。

## 验证与回滚

上线前依次验证：

1. dry-run 无冲突，apply 输出与复核人员一致。
2. 密码登录和飞书登录都进入同一个 People `person_id`。
3. 屈正、李皮、高闯分别登录后显示自己的姓名，不复用其他浏览器会话。
4. 禁用测试账号后，Kratos identity 变为 inactive，Kratos session 与 Orrery app session 均失效。
5. `human-superadmin` 本地密码和 MFA 仍可使用，且没有 People/Kratos 投影。

回滚时先设置 `auth.feishu.directory_sync_enabled: false` 并停止 apply，再将 `auth.identity.mode` 切回 `standalone`。保留 People 投影表、Kratos 数据库、identity bindings 和账号 ID，不删除 volume；切换后重启 Orrery API，并使用应急账号检查会话和管理权限。
