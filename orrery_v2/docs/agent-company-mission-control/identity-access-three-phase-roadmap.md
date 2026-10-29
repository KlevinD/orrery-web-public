# 认证与授权三阶段落地方案

> 配置迁移说明（2026-07-24）：本文记录实现历史，因此仍包含旧的非 secret `ORRERY_*` 示例；当前加载器不再接受这些配置。运维配置以 `docs/configuration-governance.md` 和所选实例 YAML 的 `auth` / `authz` section 为准，仅 secret env 名仍有效。

本文定义 Orrery v2 的统一身份认证与授权目标架构，并把实现拆成三个可连续交付的阶段。

核心原则：

```text
目标架构一次设计，工程能力分阶段启用。

第一阶段建好的 identity mapping、app session、认证中间件、权限 facade，
第二阶段和第三阶段继续沿用，不因为引入 Hydra 或 OpenFGA 推翻前期成果。
```

## 0. 当前状态与问题

当前 v2 已经完成一轮本地认证加固：

- 密码 verifier 改为服务端 Argon2id，旧 SHA verifier 可平滑升级。
- 浏览器 token 改为 HttpOnly cookie，不再写入 localStorage。
- URL `access_token` 已关闭。
- 新增 logout revoke。
- `X-Forwarded-For` 只信任可信代理。

这轮改造不应被废弃。它作为三阶段路线里的本地 bootstrap / break-glass 能力保留，用于：

- 初始部署时创建第一个管理员。
- 外部 IdP 临时不可用时的受控恢复入口。
- 本地开发和离线测试。

但本地认证不再作为长期主路径。长期主路径是：

```text
外部身份系统认证用户 -> v2 映射 AgentProfile -> v2 签发自己的 HttpOnly app session -> 业务层只认 v2 Principal -> 授权层统一检查 capability / relation。
```

## 1. 最终目标架构

### 1.1 稳定边界

最终架构分四层：

```text
Identity Provider Layer
  Ory Kratos / Ory Hydra / Keycloak / ZITADEL / future provider

Identity Mapping Layer
  external subject -> Orrery human AgentProfile

App Session Layer
  orrery_access_token HttpOnly cookie / API bearer token -> Orrery Principal

Authorization Layer
  coarse capability + object relation check
  local policy first, OpenFGA later
```

业务 handler 不直接依赖 Kratos、Hydra 或 OpenFGA。业务层只接收稳定的 `ViewerRef` / `Principal`：

```text
Principal {
  subject_provider
  subject_id
  user_id
  username
  agent_profile_id
  viewer_kind
  tenant_id
  coarse_scopes
}
```

### 1.2 必须新增的稳定模型

建议新增 `identity_subjects` 表或等价存储：

| 字段 | 含义 |
| --- | --- |
| `id` | v2 内部 identity subject ID |
| `provider` | `local`、`kratos`、`hydra`、`keycloak`、`zitadel` 等 |
| `subject` | 外部 provider 的稳定 subject / identity ID |
| `agent_profile_id` | 绑定的 human AgentProfile |
| `username` | v2 展示和兼容用 username |
| `email` | 可选 |
| `display_name` | 可选 |
| `status` | `active`、`disabled`、`pending_link` |
| `last_login_at` | 最近登录时间 |
| `created_at` / `updated_at` | 审计字段 |

唯一约束：

```text
unique(provider, subject)
unique(provider, username) where provider = 'local'
unique(agent_profile_id, provider) 可选，按是否允许一个 AgentProfile 绑定多个 IdP 决定
```

### 1.3 稳定接口

第一阶段就定义这些接口，后续阶段只替换实现：

```go
type IdentityProvider interface {
    Name() string
    BeginBrowserLogin(ctx context.Context, req BeginIdentityLoginRequest) (IdentityLoginRedirect, error)
    CompleteBrowserLogin(ctx context.Context, req CompleteIdentityLoginRequest) (ExternalIdentity, error)
    VerifyAPIToken(ctx context.Context, token string) (ExternalIdentity, error)
}

type IdentityMapper interface {
    ResolveOrLink(ctx context.Context, identity ExternalIdentity) (IdentitySubject, error)
}

type AppSessionService interface {
    Create(ctx context.Context, subject IdentitySubject) (SessionDTO, error)
    Authenticate(ctx context.Context, token string) (Principal, error)
    Revoke(ctx context.Context, token string) error
}

type AuthorizationService interface {
    Can(ctx context.Context, principal Principal, action string, object ObjectRef) error
}
```

当前 `userauth.Service` 可以逐步演进为 `AppSessionService + LocalIdentityProvider`，不是删除重写。

## 2. 三阶段总览

| 阶段 | 目标 | 主交付物 | 下一阶段是否继续使用 |
| --- | --- | --- | --- |
| 第一阶段：身份外置基础 | Kratos 接入，v2 不再长期管理密码 | identity mapping、provider adapter、app session、local bootstrap | 继续使用；Hydra 和 OpenFGA 都复用 Principal |
| 第二阶段：OIDC / 多客户端 | Hydra 接入，支持标准 OAuth2/OIDC token | OIDC login/consent、API token 校验、client/scopes 管理 | 继续使用；OpenFGA 使用同一 subject/principal |
| 第三阶段：对象级授权 | OpenFGA 接入 mission/project/resource relation check | authorization facade、FGA model、tuple writer、check adapter | 成为长期授权主路径 |

## 3. 第一阶段：身份外置基础

### 3.1 目标

把“用户怎么登录、密码怎么存、MFA 怎么做”从 v2 业务代码里拆出去。第一阶段推荐接 Ory Kratos；如果部署成本优先，也可以用同一 provider 接口接 Keycloak / ZITADEL。

第一阶段不引入 Hydra 的原因：

```text
v2 Web 作为一方应用，只需要浏览器登录 + v2 app session。
Hydra 是 OAuth2/OIDC server，主要价值在多客户端和第三方授权，不是替代 Kratos 管用户密码。
```

### 3.2 交付物

1. 新增 `identity_subjects` 持久化。
2. 新增 `internal/app/identity` 模块：
   - `ExternalIdentity`
   - `IdentitySubject`
   - `IdentityMapper`
   - provider adapter 接口
3. 把当前本地账号实现包装为 `LocalIdentityProvider`。
4. 新增 `KratosIdentityProvider`：
   - 支持 browser login flow。
   - 通过 Kratos session / identity traits 获取 subject、email、display name。
   - 不把 Kratos session 直接暴露给业务 handler。
5. `userauth.Service` 拆分或包装为 app session：
   - 继续签发 `orrery_access_token` HttpOnly cookie。
   - `AuthenticateToken` 返回统一 `Principal`。
6. 登录页改成 provider-aware：
   - 默认显示外部登录入口。
   - local 登录仅在 bootstrap / break-glass 配置开启时显示。

### 3.3 关键流程

浏览器登录：

```text
GET /login
  -> 选择 Kratos provider
  -> v2 BeginBrowserLogin
  -> 跳转 Kratos login flow
  -> Kratos 完成登录
  -> 回到 v2 callback
  -> v2 CompleteBrowserLogin 获取 ExternalIdentity
  -> IdentityMapper 绑定 AgentProfile
  -> AppSessionService.Create
  -> Set-Cookie orrery_access_token
  -> 回到 v2 app
```

API 请求：

```text
浏览器携带 orrery_access_token
  -> v2 authenticate middleware
  -> Principal
  -> AuthorizationService.Can
  -> handler
```

### 3.4 与现有体系融合

保留：

- `AgentProfile` 作为人的业务身份。
- `UserAccountSpec.PermissionScope` 作为迁移期 coarse scopes。
- 当前 cookie session 机制。
- 当前 superadmin / pm profile seed。

新增：

- 外部 subject 到 `AgentProfileID` 的绑定。
- 管理员手动 link / unlink 外部身份的能力。
- 自动创建 human profile 的策略开关：
  - 企业环境建议默认关闭，必须管理员预先邀请或审核。
  - 开发环境可开启 auto-link / auto-create。

### 3.5 验收标准

- 新用户可通过 Kratos 登录 v2。
- 登录后业务 handler 看到的 viewer 与当前本地登录一致。
- 本地 bootstrap 账号仍可在配置允许时登录。
- 禁用 AgentProfile 或 identity subject 后，已有 session 失效或下次请求被拒绝。
- 不再新增 v2 本地密码账户作为主路径。

## 4. 第二阶段：Hydra / OIDC / 多客户端

### 4.1 目标

在第一阶段的身份层和 app session 基础上，引入标准 OAuth2/OIDC 能力，服务这些场景：

- 桌面端、移动端或 CLI。
- 外部系统调用 v2 API。
- Agent runtime / MCP gateway 需要标准 bearer token。
- 企业 SSO / 第三方授权需要 client、scope、audience、JWKS。

第二阶段不是替换第一阶段。Hydra 使用第一阶段的身份登录结果作为 login/consent app 的输入。

### 4.2 交付物

1. 部署 Hydra。
2. 新增 v2 `OIDCLoginConsentService`：
   - login challenge 读取当前 v2 app session 或 Kratos session。
   - consent challenge 把 Hydra scopes 映射到 v2 coarse scopes。
3. 新增 `OIDCClientRegistry`：
   - first-party web
   - desktop
   - CLI
   - agent runtime
   - external integration
4. 后端认证中间件支持两类凭证：
   - 浏览器 `orrery_access_token` cookie。
   - API `Authorization: Bearer <OIDC access token>`。
5. `Principal` 仍然统一输出，不让业务 handler 区分 cookie / OIDC。

### 4.2.1 当前落地状态：OIDC bearer token 基础

已完成第一小阶段，作为 Hydra login/consent 接入前的稳定认证边界：

- 新增 `identity.OIDCTokenProvider`，通过配置的 introspection URL 校验 OIDC / Hydra access token。
- 新增 `userauth.TokenPrincipalService`，把 token subject 复用 `IdentityMapper` 映射到 `identity_subjects`，再输出统一 `Principal`。
- HTTP middleware 同时支持：
  - `orrery_access_token` cookie -> 现有 app session。
  - `Authorization: Bearer <OIDC access token>` -> OIDC token provider。
- 兼容旧行为：`Authorization: Bearer iat-...` 仍按 v2 app session 处理。
- OIDC scope 只收敛为当前 coarse scope：
  - `orrery:mission.read` -> `mission.read`
  - `orrery:operations.manage` -> `operations.manage`
  - `openid` / `profile` / `email` / `offline_access` 不进入业务权限。
- bearer token 的最终权限为 `账号 coarse scopes ∩ token scopes`，避免 token scope 自动继承账号全部权限。
- 禁用 `identity_subjects` 后，OIDC bearer token 下次请求会被拒绝。

当前配置项：

```text
ORRERY_OIDC_INTROSPECTION_URL
ORRERY_OIDC_CLIENT_ID
ORRERY_OIDC_CLIENT_SECRET
ORRERY_OIDC_ISSUER
ORRERY_OIDC_AUDIENCE
ORRERY_OIDC_PROVIDER
```

后续 `OIDCLoginConsentService` 和 `OIDCClientRegistry` 会继续复用上述 token provider、identity mapper 和 Principal 输出。

### 4.2.2 当前落地状态：Hydra login / consent 边界

已完成第二小阶段：

- 新增 `HydraAdminClient`：
  - 读取 login / consent challenge。
  - accept login / consent request。
  - 支持自托管 Hydra 或 Ory Network 风格的 admin base URL。
- 新增 `OIDCLoginConsentService`：
  - login challenge 只读取当前 v2 app session 得到 `Principal`。
  - login accept 的 Hydra subject 使用稳定的 `AgentProfileID`，并把原始 subject/provider 写入 session claims。
  - consent challenge 根据 client registry 和当前 `Principal.CoarseScopes` 生成 grant scope。
- 新增 `OIDCClientRegistry` 默认客户端：
  - `orrery-web`
  - `orrery-desktop`
  - `orrery-cli`
  - `orrery-agent-runtime`
  - `orrery-external-integration`
- HTTP 新增 Hydra 回调路由：
  - `GET /api/oauth2/login?login_challenge=...`
  - `GET /api/oauth2/consent?consent_challenge=...`
- 未登录访问 login / consent route 时，回到 `/login?return_to=...`，登录后继续原 challenge。
- first-party / trusted client 可自动 consent；external integration 默认不自动 consent，浏览器进入显式 consent UI，JSON caller 可读取 pending consent。
- consent grant scope 仍然收窄：
  - client allowed scopes
  - requested scopes
  - 当前 Principal coarse scopes

当前新增配置项：

```text
ORRERY_HYDRA_ADMIN_URL
ORRERY_HYDRA_ADMIN_TOKEN
ORRERY_HYDRA_REMEMBER_FOR
ORRERY_HYDRA_CLIENT_SYNC_ENABLED
```

后续状态：

- Hydra client registry 的管理 API / 审计事件已在 4.2.3 落地。
- Hydra / OpenFGA 部署 overlay 与 redirect URL 配置已在 4.2.6 补齐。

### 4.2.3 当前落地状态：OIDC client registry 管理与审计

已完成第三小阶段：

- `OIDCClientRegistry` 支持线程安全 list / upsert。
- 新增 `OIDCClientAdminService`，生产环境与 `OIDCLoginConsentService` 共享同一个 registry 实例。
- HTTP 新增管理 API：
  - `GET /api/oauth2/clients`
  - `POST /api/oauth2/clients`
- 管理 API 沿用 `permissions.read` / `permissions.manage`，不新增平行权限体系。
- client upsert 会写 operations audit：
  - `entity_type=oidc_client`
  - `action=oidc.client.upsert`
  - metadata 记录 client kind、auto consent、allowed scopes、audience。
- permission capability registry 已登记这两个 endpoint，权限矩阵能发现它们。

Hydra / OpenFGA 部署 overlay 与 redirect URL 配置已在 4.2.6 补齐。

### 4.2.4 当前落地状态：external integration 显式 consent

已完成第四小阶段：

- `OIDCLoginConsentService` 保留原 `AcceptConsent` auto-consent 语义，同时新增：
  - `PreviewConsent`：读取 Hydra consent challenge，按 client allowed scopes、requested scopes、当前 Principal coarse scopes 计算可授予 scope，不 accept Hydra request。
  - `ApproveConsent`：只在用户明确同意后对非 auto-consent client 执行 Hydra consent accept。
  - `RejectConsent`：对 Hydra consent request 返回 `access_denied`，让 OAuth2 flow 正常结束。
- consent challenge 的 Hydra subject 会和当前 app session Principal subject 对齐校验，避免其他用户代同意已泄露的 challenge。
- HTTP 新增显式 consent API：
  - `GET /api/oauth2/consent/pending?consent_challenge=...`
  - `POST /api/oauth2/consent/accept`
  - `POST /api/oauth2/consent/reject`
- `GET /api/oauth2/consent` 在 external integration 场景下：
  - 浏览器请求跳转到 `/oauth2/consent?consent_challenge=...`。
  - JSON 请求直接返回 pending consent DTO。
- Web 新增 `/oauth2/consent` 独立页面，展示 client、grant scopes、grant audience，并提供同意 / 拒绝动作。
- 登录页与全局未登录跳转现在保留安全的 `return_to`，OAuth2 login / consent challenge 不会在登录后丢失。
- OpenAPI 已记录 login / consent / OIDC client registry 路由与 DTO，`web/src/lib/types/api.generated.ts` 已同步新增 path / schema。

Hydra / OpenFGA 部署 overlay 与 redirect URL 配置已在 4.2.6 补齐。

### 4.2.5 当前落地状态：OIDC client registry 同步 Hydra Admin client

已完成第五小阶段：

- `OIDCClient` 增加真实 OAuth2 client 所需元数据：
  - `redirect_uris`
  - `grant_types`
  - `response_types`
  - `token_endpoint_auth_method`
  - `client_secret`
- `OIDCClientAdminService` 支持可选 `OIDCClientProvisioner`：
  - 未配置 provisioner 时仍是 registry-only，不改变前期行为。
  - 配置 provisioner 时先写 Hydra Admin，再把非敏感 client 元数据写入 registry。
  - `client_secret` 只在本次 save response 中返回，registry list 不保存/回显 secret。
- `HydraAdminClient` 支持真实 OAuth2 client upsert：
  - `GET /admin/clients/{client_id}` 判断存在性。
  - 不存在时 `POST /admin/clients`。
  - 存在时 `PUT /admin/clients/{client_id}`。
  - authorization code client 必须提供 `redirect_uris`，避免创建无法完成 OAuth2 flow 的 Hydra client。
- 生产配置新增 `ORRERY_HYDRA_CLIENT_SYNC_ENABLED`：
  - 默认关闭，避免部署方还没准备 redirect URL 时打断 registry-only 管理。
  - 设置为 `true` 且 `ORRERY_HYDRA_ADMIN_URL` 可用时，`POST /api/oauth2/clients` 会同步真实 Hydra client。
- OpenAPI / generated TS types 已加入 OAuth2 client 元数据字段。

部署缺口已在 4.2.6 作为独立 overlay 补齐；当前根目录 `docker-compose.yml` 存在用户改动，落地时未触碰。

### 4.2.6 当前落地状态：Hydra / OpenFGA deployment overlay

已完成第六小阶段：

- 新增 `deploy/ory/docker-compose.identity.yml`：
  - 独立部署 `hydra-postgres` / `hydra-migrate` / `hydra`。
  - 独立部署 `openfga-postgres` / `openfga-migrate` / `openfga`，为第三阶段 shadow/enforce 提供运行服务。
  - 对 `orrery-api` 追加 Hydra/OIDC/OpenFGA 环境变量，不替换 app session、identity mapping、local break-glass 或授权 facade。
  - Hydra login / consent URL 固定指向 v2 API：
    - `${ORRERY_APP_PUBLIC_URL}/api/oauth2/login`
    - `${ORRERY_APP_PUBLIC_URL}/api/oauth2/consent`
  - v2 OIDC introspection 指向 `http://hydra:4445/admin/oauth2/introspect`，Principal 输出仍复用第一阶段 IdentityMapper。
- 身份栈配置统一进入部署根目录的 `.env`（模板为 `.env.example`）：
  - 明确 `ORRERY_APP_PUBLIC_URL`、`ORY_HYDRA_PUBLIC_URL`、Hydra system secret、Hydra/OpenFGA Postgres secret。
  - `ORRERY_FEISHU_APP_ID` 与 `ORRERY_FEISHU_APP_SECRET` 由 Orrery API 和 Kratos 共同读取，不再维护身份栈专用 env 文件。
  - Kratos 继续作为可选 browser identity provider，通过 `ORRERY_KRATOS_PUBLIC_URL` / `ORRERY_KRATOS_CALLBACK_URL` 接入。
  - OpenFGA 默认保持 `ORRERY_AUTHZ_MODE=local`，待 store/model 发布后切 `openfga_shadow`，避免未准备好 relation model 时误切 enforce。
- 新增 `deploy/ory/oauth2-clients.example.json`：
  - 给 `orrery-desktop`、`orrery-cli`、`orrery-agent-runtime`、`orrery-external-integration` 提供 redirect URI / grant type / token endpoint auth method 示例。
  - 这些 client 通过现有 `POST /api/oauth2/clients` 管理 API 注册，并在 `ORRERY_HYDRA_CLIENT_SYNC_ENABLED=true` 时同步写入 Hydra。
- 新增 `deploy/ory/README.md`：
  - 记录 overlay 可叠加 repo 内 compose，也可叠加上层根目录 compose。
  - 记录 OpenFGA 发布模型、shadow、dry-run diff、全量 enforce 的启用证据。
- `orrery_v2/docker-compose.yml` 与 `orrery_v2/.env.example` 已补齐 auth / oidc / hydra / openfga 环境变量透传与样例。
- 已补合同测试，防止部署 overlay、OIDC client redirect 示例和本设计文档漂移。

### 4.3 关键流程

OAuth2 Authorization Code + PKCE：

```text
client -> Hydra /oauth2/auth
Hydra -> v2 login app
v2 login app -> Kratos / existing app session
v2 accepts Hydra login request
Hydra -> v2 consent app
v2 maps requested scopes to allowed scopes
v2 accepts consent
Hydra issues code/token
client calls v2 API with bearer token
v2 verifies token/JWKS/introspection
v2 maps subject to Principal
handler executes
```

### 4.4 Scope 设计

Hydra scopes 不直接等同于最终对象权限，只表达 API 能力边界：

```text
openid
profile
email
orrery:mission.read
orrery:mission.write
orrery:resource.read
orrery:resource.manage
orrery:operations.read
orrery:operations.manage
orrery:offline_access
```

对象级判断留给第三阶段 OpenFGA。

### 4.5 与第一阶段的关系

继续使用：

- `identity_subjects`
- `IdentityMapper`
- `Principal`
- `AppSessionService`
- Kratos identity
- local bootstrap provider

新增能力：

- 标准 OAuth2/OIDC token。
- client 管理。
- consent / scope 管理。

不做的事：

- 不把业务 handler 改成直接依赖 Hydra。
- 不删除 v2 app session。
- 不用 Hydra scope 替代对象权限。

### 4.6 验收标准

- Web 仍可用第一阶段 cookie session。
- CLI / desktop 可走 Authorization Code + PKCE。
- API bearer token 可被 v2 验证并映射成同一 Principal。
- 禁用 identity subject 后，OIDC token 不再通过或被降权。
- Hydra client 和 scope 变更有审计记录。

## 5. 第三阶段：OpenFGA 对象级授权

### 5.1 目标

把“某人能否操作某个对象”的判断从粗粒度 `permission_scope` 迁到关系模型：

```text
user:alice can edit mission:mission-1
user:pm can view project:project-1
agent:worker-1 can use resource:mcp-server-1
user:manager can manage department:engineering
```

### 5.2 交付物

1. 新增 `internal/app/authorization` facade：
   - `Can`
   - `BatchCan`
   - `ListObjects`
   - `WriteRelations`
   - `DeleteRelations`
2. 第一版实现为 local policy adapter，复用当前 accesscontrol / PermissionScope。
3. 新增 `OpenFGAAuthorizationAdapter`。
4. 新增 FGA relation tuple writer：
   - mission 创建 / 成员变更
   - project 创建 / 成员变更
   - discussion 成员变更
   - resource binding 变更
   - organization / department / role 变更
5. 新增 tuple repair / backfill job：
   - 从当前数据库重建 OpenFGA tuples。
   - 可 dry-run。
   - 可对比 v2 本地 policy 与 FGA check 差异。

### 5.2.1 当前落地状态：Authorization facade 接入 HTTP 注册路由

已完成第一小阶段：

- `internal/app/authorization` facade 已作为 HTTP registered route 的优先授权入口。
- 生产 wiring 使用 `authorization.NewService(authorization.NewScopeAdapter(nil))`。
- 当前 adapter 仍复用 coarse `PermissionScope`，不直接引入 OpenFGA。
- 保留 facade 到既有 handler 的 request scope 传递，但不保留 fail-open fallback：
  - facade allow 后会向 request 补齐对应 scope，旧 handler 仍可读取 `ViewerRef.PermissionScope`。
  - facade deny 后不会回退到 header scope 或 legacy handler fallback；只有明确配置的 authorization/accesscontrol/local scope adapter 可以作出 allow。
- `ScopeAdapter` 的 scope 规则：
  - `*.manage` 隐含对应 `*.read`。
  - `operations.manage/read` 仅授权注册为 operations 域的能力，不兼容授权 org/workforce/roles/permissions/audit。
  - `approvals.manage` 与 `approvals.decide` 兼容。

后续状态：

- OpenFGA adapter / shadow check 已在 5.2.3 落地。
- tuple writer 与 backfill 已在 5.2.4 起持续落地，并在 5.2.17 覆盖 organization / department / role。

### 5.2.2 当前落地状态：高价值路由 ObjectRef 与语义 action

已完成第二小阶段：

- HTTP registered route 调用 authorization facade 时，已从纯 route object 升级为优先传递业务对象：
  - `/api/missions/{mission_id}/...` -> `ObjectRef{Type: "mission", ID: mission_id}`
  - `/api/projects/{project_id}` -> `ObjectRef{Type: "project", ID: project_id}`
  - `/api/resources/.../{tool_id|skill_id|memory_profile_id}` -> `ObjectRef{Type: "resource", ID: ...}`
  - `/api/resources/tools/call` / `/api/resources/bindings` 等 body/query 携带资源 ID 的 route 会在授权前提取资源 ID，并恢复 request body 给后续 handler。
- 高价值对象 route 的 facade action 已从 coarse scope 规范到语义动作：
  - mission: `mission.view` / `mission.edit` / `mission.manage`
  - project: `project.view` / `project.edit` / `project.manage`
  - resource/tool: `resource.view` / `resource.use` / `resource.manage`
- 迁移期 coarse scope 映射保持向后兼容：
  - project 当前仍复用已有 `mission.read` / `mission.write`。
  - resource use 对应现有 `tool.call`，不误要求资源管理员权限。
  - facade allow 后仍向 request 补齐原 capability scope，旧 handler 不需要感知语义 action。
- 未能解析业务对象的 route 仍回退到 `ObjectRef{Type: "route", ID: "METHOD /template"}`。

后续状态：

- tuple writer 与 backfill 已在 5.2.4 起持续落地。

### 5.2.3 当前落地状态：OpenFGA adapter 与 shadow/enforce 模式

已完成第三小阶段：

- 新增 `OpenFGAAdapter`，通过 OpenFGA HTTP API 调用：
  - `POST /stores/{store_id}/check`
  - `POST /stores/{store_id}/list-objects`
  - `POST /stores/{store_id}/write`
- adapter 会先执行 coarse scope gate，再对有 relation mapping 的 action 调 OpenFGA：
  - `mission.view/edit/manage`
  - `project.view/edit/manage`
  - `resource.view/use/manage`
- OpenFGA request 固定携带 `authorization_model_id`，避免生产环境使用“最新 model”导致授权行为漂移。
- 新增 `ShadowAdapter`：
  - `openfga_shadow`：local scope adapter 决定是否放行，OpenFGA 旁路检查并记录 mismatch，不阻断主路径。
  - `openfga_enforce`：OpenFGA adapter 作为主授权路径。
  - relation write 在 shadow 模式下会写入 OpenFGA，即使 primary local scope adapter 不支持 relation write。
- 生产配置已接入：

```text
ORRERY_AUTHZ_MODE=local|openfga_shadow|openfga_enforce
ORRERY_AUTHZ_OPENFGA_API_URL
ORRERY_AUTHZ_OPENFGA_STORE_ID
ORRERY_AUTHZ_OPENFGA_AUTHORIZATION_MODEL_ID
ORRERY_AUTHZ_OPENFGA_TOKEN
ORRERY_AUTHZ_OPENFGA_TIMEOUT
ORRERY_AUTHZ_OPENFGA_IGNORE_DUPLICATE_WRITES
ORRERY_AUTHZ_OPENFGA_ENFORCE_READY=true|false
ORRERY_AUTHZ_RELATION_BACKFILL_MODE=write|dry_run|off
```

默认仍是 `local`，所以未配置 OpenFGA 的部署不受影响。

### 5.2.4 当前落地状态：tuple writer、delete cleanup 与启动 backfill

已完成第四小阶段：

- authorization facade / adapter 接口补齐 `DeleteRelations`：
  - `OpenFGAAdapter` 使用 `POST /stores/{store_id}/write` 的 `deletes.tuple_keys` 删除 tuple。
  - `ShadowAdapter` 在 `openfga_shadow` 下继续以 local 为主，但会旁路执行 OpenFGA write/delete。
  - local `ScopeAdapter` 对 relation write/delete 返回 unsupported，由 projector 在 shadow best-effort 下兼容。
- 新增 `RelationProjector` 作为业务层唯一 tuple writer：
  - project 创建写 `user:<creator> owner project:<project_id>`。
  - mission 创建写 `creator owner mission`，任务创建 / 指派写 assignee participant。
  - resource bind/unbind 写入或删除 `agent:<agent_id> operator resource:<resource_id>`。
  - relation tuple id 稳定，backfill 可重复执行并在写入前去重。
- mission projection 记录 `CreatedByID` / `CreatedByKind`，用于 backfill 和删除 mission 时清理 owner tuple。
- production 启动时在 `openfga_shadow` / `openfga_enforce` 模式运行 relation backfill：
  - projects 从 `projectadmin.Repository.ListProjects` 重建 owner。
  - missions 从 projection 重建 owner / participant。
  - agent directory 的 tool / skill refs 重建 resource operator。
- production mission 删除会同步清理 mission owner / participant relations。
- 已补测试覆盖：
  - OpenFGA write/delete payload。
  - shadow write/delete。
  - projector write/delete/backfill 去重。
  - project / mission / resource 在线 hook。
  - server backfill snapshot 与 mission delete cleanup。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。
- 分对象 shadow mismatch 指标沉淀后进入 organization/workforce/role enforce 切换。

### 5.2.5 当前落地状态：backfill dry-run/off/write 保护

已完成第五小阶段：

- `RelationProjector` 新增 `PlanBackfill` 和 `BackfillWithOptions`：
  - `PlanBackfill` 只生成稳定、去重后的 relation tuples。
  - `BackfillWithOptions(DryRun=true)` 返回 tuple 数量，不调用 OpenFGA write。
  - 原 `Backfill` 保持兼容，默认仍执行写入。
- production backfill 新增运行模式：
  - `write`：默认行为，按当前 snapshot 写入 OpenFGA。
  - `dry_run`：构建 snapshot 和 tuple plan，并在 5.2.10 后附带 OpenFGA relation diff，但不写 OpenFGA。
  - `off`：完全跳过启动 backfill。
- 新增配置：

```text
ORRERY_AUTHZ_RELATION_BACKFILL_MODE=write|dry_run|off
```

- 已补测试覆盖：
  - projector dry-run 不写 sink，但返回 tuple 统计。
  - server backfill runner 的 `dry_run` / `off` 行为。
  - 配置解析默认值、`dry_run` 和非法值拒绝。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.6 当前落地状态：生产 enforce readiness guard

已完成第六小阶段：

- 生产环境启用 `ORRERY_AUTHZ_MODE=openfga_enforce` 时，必须显式配置：

```text
ORRERY_AUTHZ_OPENFGA_ENFORCE_READY=true
```

- 非生产环境不强制该开关，便于测试和本地验证 enforce 行为。
- 这个 guard 防止只改 `ORRERY_AUTHZ_MODE` 就把生产授权切到强制模式；生产切换必须先完成 backfill、shadow mismatch 收敛和人工确认。
- 已补测试覆盖：
  - production + `openfga_enforce` 未设置 ready 会拒绝启动授权服务。
  - 设置 ready 后可构造 enforce adapter。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.7 当前落地状态：shadow mismatch 聚合统计

已完成第七小阶段：

- `LoggingShadowRecorder` 不再只打印单条 mismatch，也会维护进程内聚合统计：
  - total shadow decisions。
  - mismatch 总数。
  - primary allow / shadow deny。
  - primary deny / shadow allow。
  - primary error / shadow error。
- mismatch 日志会带上当前累计统计，便于 shadow 阶段观察 OpenFGA 与本地授权差异趋势。
- recorder 暴露 `Snapshot()`，后续 readiness API / admin report 可以直接复用。
- 已补测试覆盖：
  - mismatch 方向统计。
  - shadow error 计数。
  - mismatch 日志携带累计 counters。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.8 当前落地状态：shadow stats readiness report

已完成第八小阶段：

- production `openfga_shadow` wiring 会保留同一个 `LoggingShadowRecorder` 实例。
- Runtime ready check 新增 `authorization.openfga_shadow`：
  - `OK=true`，只作为报告，不在 shadow 阶段摘流。
  - message 包含 total、mismatches、primary/shadow allow-deny 方向、primary/shadow errors。
- `authorizationServiceFromPlatform` 保持原签名兼容；生产 wiring 使用 `authorizationServiceFromRuntime` 同时完成 service 创建和 readiness 注册。
- 已补测试覆盖 readiness report 能读取 shadow stats。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.9 当前落地状态：backfill plan report

已完成第九小阶段：

- `RelationBackfillPlan.Report()` 会对 dry-run/write 使用同一份 tuple plan 生成报告：
  - 按 object type 统计。
  - 按 relation 统计。
  - 按 subject kind 统计。
  - 按 tenant 统计。
- `RelationBackfillResult` 携带 `Report`，所以启动 backfill 和 dry-run 都能返回同一结构。
- 已补测试覆盖 projector report 与 server dry-run report。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.10 当前落地状态：OpenFGA relation diff 基础

已完成第十小阶段：

- authorization facade 新增可选 `RelationChecker` 能力，`Service.CheckRelation` 只在底层 adapter 支持时启用，避免改变既有 `Adapter` 必实现面。
- `OpenFGAAdapter.CheckRelation` 直接用 tuple 调 OpenFGA `/check`，用于 backfill diff；这条路径不走 action -> relation 的业务授权映射，也不走 coarse scope gate。
- `ShadowAdapter.CheckRelation` 在 shadow 模式优先查 OpenFGA shadow adapter，使 dry-run diff 对比的是 OpenFGA 当前 tuple 状态。
- `RelationProjector.BackfillWithOptions(Diff=true)` 会基于同一份 backfill plan 查询当前 relation 是否存在，并返回：
  - checked tuple 数。
  - missing tuple 数与明细。
  - OpenFGA check 错误数与错误信息。
- `ORRERY_AUTHZ_RELATION_BACKFILL_MODE=dry_run` 现在表示“只读计划 + OpenFGA diff”，仍不会写入 tuple。
- 已补测试覆盖 OpenFGA direct relation check、shadow 优先 relation check、projector dry-run diff、server dry-run diff 配置传递。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.11 当前落地状态：backfill report/diff 管理员诊断接口

已完成第十一小阶段：

- 新增 `GET /api/authorization/relation-backfill/diagnostics` 管理员诊断接口。
- 接口按请求执行一次 `dry_run + diff`，返回当前 snapshot 生成的 backfill report 和 OpenFGA relation diff；不会写入 OpenFGA tuple。
- 访问严格要求 `permissions.read` / `permissions.manage`，不接受 `operations.read` / `operations.manage` 别名；handler 自身也会做显式权限检查，避免只依赖 route registry。
- response 包含：
  - backfill mode、dry-run/disabled 状态。
  - projects/missions/discussions/resource_bindings/tuples 汇总。
  - 按 object type、relation、subject kind、tenant 聚合的 report。
  - checked/missing/errors、missing tuple 明细和 check error messages。
- 已补权限 capabilities 注册、OpenAPI contract、HTTP route 测试。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.12 当前落地状态：relation backfill readiness report

已完成第十二小阶段：

- production 启动时 relation backfill 执行完成后，会注册 `authorization.relation_backfill` ready check。
- ready message 包含：
  - backfill mode、disabled、dry_run。
  - projects、missions、discussions、resource_bindings、tuples 汇总。
  - diff_checked、diff_missing、diff_errors。
- write/off/local 路径会报告状态但不额外阻断。
- dry-run diff 如果发现 missing tuple 或 check error，ready check 会置为 not ready；这让 shadow 收敛和 enforce 切换前的 tuple 缺口能被 `/api/health` / runtime readiness 看见。
- 已补测试覆盖 dry-run diff missing 时 readiness not ready 且 message 携带 mode/diff counters。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.13 当前落地状态：enforce transition readiness report

已完成第十三小阶段：

- 新增 `authorization.enforce_transition` ready check。
- 该 check 只在 `openfga_shadow` 有 shadow recorder 时注册，用于 enforce 切换前汇总：
  - shadow_total、shadow_mismatches。
  - primary_allow_shadow_deny、primary_deny_shadow_allow。
  - primary_errors、shadow_errors。
  - backfill_mode、backfill_disabled、backfill_dry_run、backfill_validated。
  - diff_checked、diff_missing、diff_errors。
- 当 shadow mismatch/error 为 0、relation backfill 未关闭、处于 dry-run、且 backfill diff 覆盖全部 tuple 并没有 missing/error 时，ready check 才为 OK。
- `write` 模式只证明已执行启动回填，不作为 enforce 切换的 diff 证据；切换前应先使用 `ORRERY_AUTHZ_RELATION_BACKFILL_MODE=dry_run` 取得 `backfill_validated=true`。
- 该 check 不替代 `ORRERY_AUTHZ_OPENFGA_ENFORCE_READY=true` 人工开关，而是把人工开关前的证据固化到 readiness。
- 已补测试覆盖 shadow mismatch + backfill missing、缺少 diff 证据时 enforce transition readiness not ready，以及 clean shadow + clean diff 时 ready。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.14 当前落地状态：OpenFGA enforce 硬边界

已完成第十四小阶段：

- `openfga_enforce` 改为全量 hard boundary：
  - `ORRERY_AUTHZ_OPENFGA_ENFORCE_OBJECT_TYPES` 仅接受空值 / `all`；配置具体对象域会拒绝启动。
  - OpenFGA / ReBAC 缺 action relation mapping 时直接 deny，不再沿用 coarse scope allow。
  - OpenFGA dependency error 在 enforce 下直接返回错误，不再 fallback 到 local adapter。
- 推荐切换顺序：
  - `openfga_shadow` + `ORRERY_AUTHZ_RELATION_BACKFILL_MODE=dry_run`。
  - 确认 `authorization.enforce_transition` 中 `shadow_mismatches=0` 且 `backfill_validated=true`。
  - 生产显式设置 `ORRERY_AUTHZ_OPENFGA_ENFORCE_READY=true`。
  - 设置 `ORRERY_AUTHZ_MODE=openfga_enforce` 后所有注册授权入口 fail closed。
- 回滚方式：
  - 回滚 `ORRERY_AUTHZ_MODE=openfga_shadow` 或 `local`，前期 relation writer/backfill 成果保留。
- 已补测试覆盖 partial object-domain rollout 被拒绝、缺 relation mapping deny、facade deny 不 fallback。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.15 当前落地状态：OpenFGA model artifact 与发布 runbook

已完成第十五小阶段：

- 新增版本化模型文件：
  - `docs/agent-company-mission-control/openfga/authorization-model.fga`
- 新增发布与回滚 runbook：
  - `docs/agent-company-mission-control/openfga/model-publish-runbook.md`
- 模型显式定义 `user` 和 `agent`，因为当前 tuple writer 会写入 `user:*` owner 和 `agent:*` participant/operator。
- 模型包含 `mission` / `project` / `resource` 当前 enforce 域，并已经接入 `discussion` / `organization` / `department` / `role` 的 relation writer/backfill 基础。
- 已补 contract test，确认模型 artifact 覆盖运行时 relation 域和关键 relation。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.16 当前落地状态：discussion relation writer 与 backfill coverage

已完成第十六小阶段：

- `RelationProjector` 新增 discussion tuple writer / cleaner：
  - `mission:<mission_id> mission discussion:<discussion_id>`。
  - `user|agent:<owner> owner discussion:<discussion_id>`。
  - `user|agent:<member> member discussion:<discussion_id>`。
- mission 创建时，如果创建了 discussion group，会同步写 discussion mission/owner/member relations。
- task assignee 加入 discussion group 时，会同步写 `member discussion` relation。
- mission 删除时会清理 mission relations 和 discussion relations，避免 OpenFGA 留存旧 discussion tuple。
- relation backfill snapshot 现在会从 mission projection 重建 discussion relations，diagnostics / readiness report 增加 `discussions` 计数。
- authorization facade 增加 `discussion.read` / `discussion.write` / `discussion.manage` 到 `viewer` / `poster` / `manager` 的关系映射。
- 已补测试覆盖 projector write/delete、server snapshot/delete cleanup、lifecycle task assignee discussion member tuple、HTTP diagnostics DTO/OpenAPI schema。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。

### 5.2.17 当前落地状态：organization / department / role relation writer 与 backfill coverage

已完成第十七小阶段：

- `RelationProjector` 新增 organization / department / role 关系写入与清理：
  - `user|agent:<admin> admin organization:<organization_id>`。
  - `organization:<organization_id> org department:<department_id>`。
  - `user|agent:<manager> manager department:<department_id>`。
  - workforce member 入组写 `member organization`、`member department`。
  - role assignment 写 `organization:<organization_id> org role:<organization_id>/<role_id>` 与 `user|agent:<assignee> assignee role:<organization_id>/<role_id>`，避免不同组织的内置 role id 串权。
- `operations.CommandService` 新增 `SetAuthorizationRelations`：
  - `CreateOrganization` 写 organization admin。
  - `CreateOrganizationUnit` 写 department org。
  - `UpsertWorkforceMember` / `HireTalentFromMarket` 写 workforce placement 和 profile role assignment。
  - `ReleaseWorkforceMember` 删除旧 workforce placement。
  - `DeleteOrganizationUnit` 与成员迁移路径会删除旧 department tuple 并为迁移成员写新 placement。
  - `PutRoleAssignments` 和 `UpdateOrganizationOwner` 同步 role assignment、organization admin、department manager relations。
- production wiring 现在创建并挂载 `RoleStore`，operations query/command 共享同一 store，relation writer 继续复用前序 OpenFGA shadow/enforce projector。
- 启动 backfill 和管理员 diagnostics 现在通过 operations service 重建：
  - organization owner/admin。
  - department org/manager。
  - workforce member org/department/role placement。
  - explicit role assignments。
- relation backfill result、report、HTTP diagnostics DTO、OpenAPI schema、readiness message 增加：
  - `organizations`
  - `departments`
  - `workforce_members`
  - `role_assignments`
- 已补测试覆盖 projector tuple 形状、operations lifecycle hook、server backfill snapshot、HTTP OpenAPI/diagnostics contract。

后续状态：

- organization / workforce / role API 的对象级 authorization facade check 已在 5.2.18 落地；shadow mismatch 收敛证据已在 5.2.20 / 5.2.21 结构化。
- role `org` tuple 在严格 OpenFGA enforce 下的写前幂等策略已在 5.2.19 落地。

### 5.2.18 当前落地状态：organization / workforce / role API 对象级授权检查

已完成第十八小阶段：

- `operations.QueryService` / `operations.CommandService` 新增 `SetAuthorizationChecker`，生产 wiring 挂载同一个 authorization facade：
  - `local` 模式仍由 coarse scope 决定。
  - `openfga_shadow` 模式记录 organization / department / role API 的 shadow mismatch。
  - `openfga_enforce` 模式对 `organization` / `department` / `role` 等对象域一并执行硬边界；切换前必须依赖 shadow/readiness 证据。
- operations service 层新增对象级检查，handler 不直接依赖 OpenFGA：
  - `GetOrgGraph(organization_id=...)` -> `organization.read`。
  - `ListWorkforceMembers(organization_id=...)` -> `workforce.member.read` on organization。
  - `ListWorkforceMembers(department_id=...)` -> `workforce.member.read` on department。
  - 创建部门、创建岗位、删除组织/部门/岗位 -> `organization.manage` / `department.manage`。
  - 人员 upsert、启停、释放、从人才市场入职 -> `workforce.member.manage` on department。
  - 角色分配写入 -> `role.manage` on organization。
  - 组织负责人变更 -> `organization.manage` 或 `role.manage`，按调用方持有的 coarse scope 保持旧兼容。
  - 按组织 code 查询角色分配时，会对组织、部门或 scoped role 对象执行 `role.assignment.read` / `role.read`。
- `ViewerRef` 补充 `AgentProfileID`；HTTP operations handler 从当前 `Principal` / `X-Agent-Profile-ID` 传入，authorization principal 对 human viewer 优先使用 `AgentProfileID`，与 role/owner tuple 的 `user:<agent_profile_id>` key 对齐。
- authorization facade 增加组织域语义 action：
  - `organization.read` -> `org.read` + `viewer organization`。
  - `organization.manage` -> `org.manage` + `admin organization`。
  - `department.read` -> `org.read` + `viewer department`。
  - `department.manage` -> `org.manage` + `editor department`。
  - `workforce.member.read` -> `workforce.read` + `viewer organization|department`。
  - `workforce.member.manage` -> `workforce.manage` + `editor department`。
  - `role.read` -> `roles.read` + `viewer role`。
  - `role.manage` -> `roles.manage` + `admin role|organization|department`。
  - `role.assignment.read` -> `roles.read` + `viewer organization|department`。
- OpenFGA 模型补齐组合 relation：
  - `organization.viewer = admin or member`。
  - `department.viewer = member or manager or admin`。
  - `department.editor = manager or admin`。
  - `role.viewer = assignee or admin`。

### 5.2.19 当前落地状态：role org tuple 严格写入幂等

已完成第十九小阶段：

- `OpenFGAAdapter.WriteRelations` 在 `ORRERY_AUTHZ_OPENFGA_IGNORE_DUPLICATE_WRITES=false` 的 strict 写入模式下，会对 role scoped object 的组织归属 tuple 做写前 direct relation check：
  - `organization:<organization_id> org role:<organization_id>/<role_id>` 已存在时跳过写入。
  - 同批其他 tuple，例如 `user|agent:<assignee> assignee role:<organization_id>/<role_id>`，仍按原路径写入。
- adapter 对同一 write batch 内重复 tuple 做内存去重，避免同批重复 key 触发 OpenFGA duplicate write。
- `ORRERY_AUTHZ_OPENFGA_IGNORE_DUPLICATE_WRITES=true` 仍保留为通用兼容保护，但 role `org` tuple 不再依赖该配置才能在 strict enforce 下重复执行。

### 5.2.20 当前落地状态：shadow mismatch 按对象域/action 聚合

已完成第二十小阶段：

- `LoggingShadowRecorder` 在保留全局 shadow counters 的同时，新增按维度聚合：
  - `ByObjectType`：按 `mission`、`project`、`resource`、`discussion`、`organization`、`department`、`role` 等对象域统计 total / mismatches / primary_errors / shadow_errors。
  - `ByAction`：按 `mission.view`、`workforce.member.manage`、`role.manage` 等语义 action 统计同样 counters。
- `Snapshot()` 会深拷贝聚合 map，避免 readiness/report 调用方修改 recorder 内部状态。
- readiness message 已携带聚合摘要：
  - `authorization.openfga_shadow` 增加 `object_types=...`、`actions=...`。
  - `authorization.enforce_transition` 增加 `shadow_object_types=...`、`shadow_actions=...`。
- organization / department / role 进入 enforce 前，可以直接用 readiness 中对应对象域和 action 的 mismatch/error 计数作为审查证据。

该操作化缺口已在 5.2.21 补齐：

- 进入 organization / department / role enforce 前，使用 `openfga_shadow + relation_backfill=dry_run` 确认 `authorization.enforce_transition` 的 shadow mismatch 与 diff missing/error 均为 0。

### 5.2.21 当前落地状态：enforce transition 结构化诊断接口

已完成第二十一小阶段：

- 新增 `GET /api/authorization/enforce-transition/diagnostics` 管理员诊断接口：
  - 权限要求沿用 `permissions.read` / `permissions.manage`。
  - 返回 `ready` 与 `backfill_validated`，和 `authorization.enforce_transition` readiness 的判定逻辑一致。
  - 返回 shadow 全局 counters、按 object type 聚合 counters、按 action 聚合 counters。
  - 返回一次 live `relation_backfill=dry_run + diff` 结果，包含 checked/missing/errors、missing tuple 明细和 backfill report。
- production wiring 复用同一个 `LoggingShadowRecorder` 和 `RelationProjector`：
  - readiness 与管理员诊断接口不会分叉出两套判定逻辑。
  - 请求诊断时重新执行 dry-run diff，避免只依赖启动时 backfill 结果。
- OpenAPI、generated TS types 和 permission capability registry 已登记该接口。

后续执行不需要继续改造前期产物；进入 organization / department / role enforce 前，使用该接口确认目标对象域/action 的 shadow mismatch/error 为 0，且 backfill `diff_missing=0` / `diff_errors=0`。

### 5.2.22 当前落地状态：OpenFGA dependency fail-closed guard

已完成第二十二小阶段：

- `ORRERY_AUTHZ_OPENFGA_FAIL_OPEN_DEV=true` 在所有环境都会拒绝构造授权服务。
- 在 `openfga_enforce` 下：
  - OpenFGA dependency error 直接失败，不回退到 local coarse scope adapter。
  - OpenFGA 正常返回 deny 时仍然 deny。
  - relation write/delete 继续由 primary authorization adapter 负责，不提供 fail-open 写路径。
- `openfga_shadow` 仍保持原迁移语义：local adapter 决定主路径，OpenFGA 旁路 mismatch 记录。

### 5.3 第一版 FGA 模型

模型 source of truth 放在 `docs/agent-company-mission-control/openfga/authorization-model.fga`。进入 enforce 前必须覆盖 `mission`、`project`、`resource`、`discussion`、organization / workforce / role 等注册授权入口；切换前仍需要按域观察 shadow mismatch 与 backfill diff 收敛。

```text
model
  schema 1.1

type user

type agent

type organization
  relations
    define admin: [user, agent]
    define member: [user, agent]
    define viewer: admin or member

type department
  relations
    define org: [organization]
    define manager: [user, agent]
    define member: [user, agent]
    define admin: admin from org
    define viewer: member or manager or admin
    define editor: manager or admin

type role
  relations
    define org: [organization]
    define assignee: [user, agent]
    define admin: admin from org
    define viewer: assignee or admin

type mission
  relations
    define owner: [user, agent]
    define participant: [user, agent]
    define department: [department]
    define viewer: owner or participant or manager from department or admin from department
    define editor: owner or manager from department or admin from department
    define manager: owner or admin from department

type discussion
  relations
    define mission: [mission]
    define owner: [user, agent]
    define member: [user, agent]
    define viewer: owner or member or viewer from mission
    define poster: owner or member or editor from mission
    define manager: owner or manager from mission

type project
  relations
    define owner: [user, agent]
    define participant: [user, agent]
    define org: [organization]
    define viewer: owner or participant or member from org or admin from org
    define editor: owner or admin from org

type resource
  relations
    define owner: [user, agent]
    define bound_mission: [mission]
    define viewer: owner or viewer from bound_mission
    define operator: [user, agent] or owner or editor from bound_mission
```

### 5.4 业务接入方式

业务 handler 不直接调用 OpenFGA client，而是调用 v2 authorization facade：

```go
if err := authz.Can(ctx, principal, "mission.edit", ObjectRef{Type: "mission", ID: missionID}); err != nil {
    return err
}
```

facade 内部映射：

```text
mission.view -> check user:X viewer mission:Y
mission.edit -> check user:X editor mission:Y
mission.manage -> check user:X manager mission:Y
resource.use -> check user:X operator resource:Y
discussion.read -> check user:X viewer discussion:Y
discussion.write -> check user:X poster discussion:Y
discussion.manage -> check user:X manager discussion:Y
organization.read -> check user:X viewer organization:Y
organization.manage -> check user:X admin organization:Y
department.read -> check user:X viewer department:Y
department.manage -> check user:X editor department:Y
workforce.member.read -> check user:X viewer organization|department:Y
workforce.member.manage -> check user:X editor department:Y
role.read -> check user:X viewer role:org/role
role.manage -> check user:X admin role:org/role or organization|department:Y
role.assignment.read -> check user:X viewer organization|department:Y
```

### 5.5 与前两阶段的关系

继续使用：

- 第一阶段的 identity subject。
- 第二阶段的 OIDC subject / token。
- 统一 Principal。
- coarse scopes。

变化：

- coarse scopes 从最终授权依据降级为能力上限。
- OpenFGA check 成为对象级最终判断。

例子：

```text
用户必须同时满足：
1. Principal 具备 orrery:mission.write 能力上限。
2. OpenFGA 判断 user:X editor mission:Y 为 true。
```

### 5.6 渐进启用策略

第三阶段先按对象域完成 shadow/readiness 验证，再一次切到 OpenFGA enforce：

1. mission view/edit/manage。
2. project view/edit/manage。
3. resource view/use/manage。
4. discussion membership。
5. organization / workforce administration。

每个对象域先 shadow check：

```text
当前本地授权允许，OpenFGA 拒绝 -> 记录 diff，不拦截。
当前本地授权拒绝，OpenFGA 允许 -> 记录 diff，不放行。
所有对象域 diff 收敛后切 enforce。
```

### 5.7 验收标准

- mission/project/resource 三类对象有完整 tuple backfill。
- shadow check 差异可观测。
- enforce 后核心对象权限不再依赖单纯 `permission_scope`。
- OpenFGA 不可用时策略明确：
  - `openfga_enforce` 默认 fail closed。
  - `ORRERY_AUTHZ_OPENFGA_FAIL_OPEN_DEV=true` 在所有环境都会拒绝启动。

## 6. 阶段间不返工约束

### 6.1 第一阶段产物的长期归宿

| 第一阶段产物 | 第二阶段用途 | 第三阶段用途 |
| --- | --- | --- |
| `identity_subjects` | Hydra subject 映射 | OpenFGA user key 来源 |
| `Principal` | OIDC bearer token 统一输出 | FGA check 输入 |
| `AppSessionService` | Web 继续使用 cookie session | Web 继续使用 cookie session |
| `LocalIdentityProvider` | break-glass | break-glass |
| `IdentityMapper` | Hydra token subject 映射 | tuple owner 映射 |

### 6.2 第二阶段产物的长期归宿

| 第二阶段产物 | 第三阶段用途 |
| --- | --- |
| OIDC access token 校验 | API / CLI / Agent runtime 身份输入 |
| Hydra clients | 外部集成入口 |
| Hydra scopes | OpenFGA check 前的 capability 上限 |
| consent service | 控制第三方客户端可申请的权限范围 |

### 6.3 第三阶段不会替代的内容

OpenFGA 不替代：

- 登录。
- 用户资料。
- app session。
- OIDC client。
- 审计日志。
- `AgentProfile`。

OpenFGA 只回答：

```text
这个 principal 对这个 object 是否具备这个 relation/action。
```

## 7. 推荐目录结构

```text
internal/app/identity/
  dto.go
  service.go
  store.go
  provider_local.go
  provider_kratos.go
  mapper.go

internal/app/authsession/
  service.go
  store.go
  cookie.go

internal/app/authorization/
  service.go
  object_ref.go
  adapter_local.go
  adapter_openfga.go
  model_mission.go
  tuple_writer.go

internal/transport/http/
  auth_identity_handler.go
  auth_oidc_handler.go
  auth_logout_handler.go
  authorization_middleware.go

cmd/server/
  identity_wiring.go
  authorization_wiring.go
```

现有 `internal/app/userauth` 不立即删除。第一阶段先作为 local provider 和 app session 的来源，等新模块稳定后再收敛命名。

## 8. 数据迁移策略

### 8.1 本地账号迁移到 Kratos

推荐策略：

1. 导出 human AgentProfile 的 username/email/display name。
2. 在 Kratos 创建 identity。
3. 写入 `identity_subjects(provider='kratos', subject=kratos_identity_id, agent_profile_id=...)`。
4. 对旧本地密码不做明文迁移：
   - 用户首次外部登录后绑定。
   - 或管理员发起密码重置 / 邀请。
5. 保留 local provider 仅给 bootstrap 用户。

### 8.2 OpenFGA tuple backfill

从现有表和领域模型重建：

- organization admin/member。
- department manager/member。
- mission owner/participant。
- project owner/participant。
- resource owner/bound_mission。

Backfill 必须可重复执行，tuple key 稳定，避免重复写入。

## 9. 配置开关

建议新增：

```text
ORRERY_AUTH_PROVIDER=local|kratos|oidc
ORRERY_LOCAL_LOGIN_ENABLED=false
ORRERY_BREAK_GLASS_LOGIN_ENABLED=true

ORRERY_KRATOS_PUBLIC_URL=
ORRERY_KRATOS_ADMIN_URL=

ORRERY_HYDRA_PUBLIC_URL=
ORRERY_HYDRA_ADMIN_URL=
ORRERY_HYDRA_CLIENT_SYNC_ENABLED=false
ORRERY_OIDC_ISSUER=
ORRERY_OIDC_AUDIENCE=

ORRERY_AUTHZ_BACKEND=local|openfga
ORRERY_OPENFGA_API_URL=
ORRERY_OPENFGA_STORE_ID=
ORRERY_OPENFGA_MODEL_ID=
ORRERY_OPENFGA_SHADOW_MODE=true
```

当前 v2 阶段 1a 已落地的配置键：

```text
ORRERY_AUTH_LOCAL_ENABLED=false
ORRERY_KRATOS_PUBLIC_URL=https://kratos.example
ORRERY_KRATOS_CALLBACK_URL=https://app.example/api/auth/identity/kratos/callback
ORRERY_KRATOS_DISPLAY_NAME="Company SSO"
```

行为约束：

- local 登录默认关闭；显式设置 `ORRERY_AUTH_LOCAL_ENABLED=true` 才作为受控 break-glass 入口显示。
- Kratos browser flow 回调固定走 `/api/auth/identity/kratos/callback`，v2 回调后仍签发自己的 `orrery_access_token` HttpOnly app session。
- 登录页通过 `/api/auth/providers` 获取可用 provider，browser provider begin URL 形如 `/api/auth/identity/{provider}/begin?return_to=/`。

## 10. 实施顺序

### Milestone 1：身份抽象先落地

- 新增 `identity_subjects`。
- 新增 Principal 类型。
- 当前 cookie session 认证改为输出 Principal。
- 当前本地账号包装成 `LocalIdentityProvider`。
- 业务 handler 不感知变化。

当前已落代码入口：

- `internal/app/identity`：`ExternalIdentity`、`IdentitySubject`、`Principal`、provider/mapper/session 边界、context principal、内存 store、SQL store、`identity_subjects` migration。
- `internal/app/userauth`：当前本地账号包装为 `LocalIdentityProvider`，`AuthenticatePrincipal` 继续复用现有 cookie/app session。
- `internal/transport/http`：认证中间件把 token 认证结果写入 Principal context，旧 `X-User-ID` / `X-Permission-Scope` header 仅作为兼容输出。
- `cmd/server/AuthModule`：生产启动注册 identity migration。

### Milestone 2：Kratos 接入

- 部署 Kratos。
- 新增 Kratos provider。
- 登录页支持 Kratos 登录。
- 管理员可 link / unlink external identity。
- local 登录默认关闭，仅保留 break-glass。

当前已落代码入口：

- `internal/app/identity/kratos_provider.go`：Kratos browser login begin + whoami session completion adapter，只输出 `ExternalIdentity`，不把 Kratos session 暴露给业务 handler。
- `internal/app/userauth/federated_login.go`：外部身份完成后复用 `IdentityMapper` 与现有 app session；session 记录 provider/subject 元数据，`AuthenticatePrincipal` 仍统一输出 `Principal`。
- `internal/transport/http/auth_handler.go`：新增 `/api/auth/providers`、`/api/auth/identity/{provider}/begin`、`/api/auth/identity/{provider}/callback`；local login 受 `ORRERY_AUTH_LOCAL_ENABLED` 控制。
- `web/src/routes/login/+page.svelte`：provider-aware 登录页，外部 browser provider 优先，本地账号仅在后端声明可用时显示。
- `cmd/server/auth_config.go`：Kratos provider 生产装配，local 默认关闭，显式 `ORRERY_AUTH_LOCAL_ENABLED=true` 才开启。

阶段 1b 已落代码入口：

- `internal/app/identity/admin_service.go`：管理员 link、disable/unlink、enable、list external identity subject。
- `internal/transport/http/identity_admin_handler.go`：新增 `/api/identity/subjects` 管理 API，读走 `permissions.read`，写走 `permissions.manage`，成功变更写 operations audit。
- `internal/app/userauth/service.go`：外部 app session 每次认证时校验 `identity_subjects` 状态；subject disabled / missing 后既有 session 立即变为 unauthorized。

阶段 1 后续可选增强：

- 给 `/api/identity/subjects` 接运营管理 UI。

### Milestone 3：Hydra 接入

- 部署 Hydra。
- v2 实现 Hydra login / consent app。
- 支持 OIDC Authorization Code + PKCE。
- 后端 API 支持 OIDC bearer token -> Principal。

### Milestone 4：Authorization facade

- 所有敏感 handler 改走 `AuthorizationService.Can`。
- adapter 先用本地 policy。
- 这个 milestone 必须在 OpenFGA 前完成，避免 handler 直接绑定 OpenFGA。

当前已先落 `internal/app/authorization` facade：

- `ScopeAdapter`：复用当前 `Principal.CoarseScopes` 做迁移期能力上限。
- `ReBACAdapter`：复用当前 `internal/app/rebac` tuple store，接口形状对齐后续 OpenFGA check / write / list。
- 后续切 handler 时只依赖 facade，不直接依赖 OpenFGA client。

### Milestone 5：OpenFGA shadow

- 建 FGA model。
- 写 tuple writer。
- 跑 backfill。
- shadow check mission/project/resource。
- 记录 diff。

### Milestone 6：OpenFGA enforce

- 按对象域完成 shadow 证据后全量 enforce。
- SaaS 生产 fail closed。
- 保留本地 policy 作为 coarse capability 和应急诊断参考。

## 11. 主要风险与控制

| 风险 | 控制 |
| --- | --- |
| 一次引入三套基础设施过重 | 分阶段部署；第一阶段只接 identity，Hydra/OpenFGA 不阻塞 |
| 业务 handler 直接依赖外部系统 | 强制通过 Principal 和 AuthorizationService facade |
| local 账号迁移造成锁死 | break-glass provider + 管理员 link + 审计 |
| OpenFGA 模型设计错误 | shadow check + diff 收敛 + 分对象 enforce |
| Hydra scopes 被误当对象权限 | scopes 只做 capability 上限，最终对象判断走 OpenFGA |
| IdP 不可用导致全部不可登录 | 已登录 app session 短期可用；break-glass 可控开启 |

## 12. 技术选型结论

推荐默认路线：

```text
第一阶段：Kratos + v2 app session
第二阶段：Hydra + OIDC clients
第三阶段：OpenFGA + v2 authorization facade
```

如果交付速度优先，可以把第一阶段 provider 从 Kratos 换成 Keycloak 或 ZITADEL，但仍保留同一接口和数据模型。这样不会影响第二阶段和第三阶段。

参考：

- Ory Kratos identity / session: https://www.ory.com/docs/kratos
- Ory Hydra OAuth2 / OIDC: https://www.ory.com/docs/hydra
- OpenFGA authorization concepts: https://openfga.dev/docs/authorization-concepts
- OpenFGA production guidance: https://openfga.dev/docs/best-practices/running-in-production
