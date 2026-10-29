# Orrery 配置治理与实施状态

> 基线：2026-07-24。范围为 `orrery_v2/` 主线、根目录及 standalone Compose、桌面 sidecar、Hydra/OpenFGA overlay。当前只做**实例级配置**，不设计多租户配置继承。

## 结论

静态配置接入整改已经完成：

- 非敏感 Orrery 运行配置以 YAML 为唯一权威来源；API 启动必须选择 YAML。
- env 只承载配置选择器、secret、一次性 bootstrap 和容器外部拓扑，不再覆盖 YAML 中的非敏感字段。
- development、WSL development、desktop 和 production 使用同一 schema、同一加载链和同一安全校验，只允许值不同。
- 构建参数集中在 `config/build.env`，镜像引用集中在 `deploy/releases/*.env`；Dockerfile 和 Compose 不再复制工具链默认值。
- production 使用无密码 PostgreSQL URL，密码通过 `ORRERY_DATABASE_PASSWORD` 注入；六个数据库角色缺一即失败。
- YAML 当前全部是 restart-required，没有对外宣称热更新。

动态 Settings 的 revision/CAS、分权审计、历史明文迁移和 Gateway 主密钥轮换仍是后续治理项。这些不影响静态配置已经统一接入的结论。

## 配置边界

| 配置面 | 权威来源 | 内容 | 生效方式 |
| --- | --- | --- | --- |
| 开发/生产静态配置 | `config/orrery.*.yaml` | 非敏感、结构化、实例级运行参数 | 校验后重启 |
| API secret | Secret Manager、secret volume、allowlisted env | 数据库密码、认证 token/key、Gateway 主密钥、bootstrap 密码 | 重启；部分 bootstrap 用后撤销 |
| 构建配置 | `config/build.env` | 工具链版本、包源、镜像源 | 重新构建 |
| 发布制品 | `deploy/releases/<release>.env` | API/Web/PageIndex/Hydra/OpenFGA 完整 image reference | 重新发布 |
| 部署拓扑 | production env / Compose interpolation | host bind、published port、volume、PageIndex 自身运行参数 | 重建/重启 |
| 实例动态业务配置 | `core.app_settings/default`、`core.gateway_settings` | 已批准在线修改的业务参数 | 请求时读取或 adapter reload |
| 用户偏好 | user preference 存储 | 语言、主题、个人通知、个人账号绑定 | 即时 |

同一个值不得长期同时由 YAML、env 和数据库控制。

# 第一部分：开发配置

## 统一契约

以下配置都由 `internal/platform/config` 严格加载：

- `config/orrery.development.yaml`
- `config/orrery.wsl-development.yaml`
- `config/orrery.desktop.yaml`
- `config/orrery.production.yaml`

四份文件使用相同顶层 section。`schema_version` 当前固定为 `1`，`profile` 只允许 `development` 或 `production`，且必须与 `environment` 一致。未知顶层 section、未知嵌套字段和多文档 YAML 会直接失败。

开发与生产的差异只体现在值：

| 项目 | 开发 | 生产 |
| --- | --- | --- |
| 数据库 | 可让六角色指向同一个 PostgreSQL；desktop 可使用六个 SQLite 文件 | 六角色必须使用 PostgreSQL |
| 地址和路径 | loopback、本地目录 | 容器服务名、持久卷目录 |
| Identity | 本地 Kratos/OpenFGA 或 local auth | 生产 IdP/Hydra/OpenFGA |
| Secret | 开发专用 secret | Secret Manager/KMS/只读挂载 |

`dev.ps1` 在 WSL Identity provisioning 后，通过 `cmd/configctl` 从 tracked WSL YAML 结构化生成 `.dev/orrery.wsl.yaml`，只写入实际 Kratos 端口和 OpenFGA Store/Model 值。它不再通过非敏感 env 创建另一套配置语义。`dev.sh` 只选择 development YAML 并注入数据库密码。

Tauri sidecar 会把 `orrery.desktop.yaml` 写入应用 data dir，再用 `-config` 启动后端。桌面不再依赖 `ORRERY_ENV` / `ORRERY_BASE_DIR` env-only 启动路径。

## 开发 env

`config/env/development.env.example` 与 production API 使用同一 secret contract，只是值为开发值。仅开发工具使用的主机路径和依赖发现项放在 `config/env/dev-tools.env.example`，不传入生产 API。

典型 dev-tools 项包括：

- `ORRERY_DEV_POSTGRES_*`、`ORRERY_WSL_POSTGRES_*`
- `ORRERY_WSL_DISTRO`
- `PAGEINDEX_PROJECT_ROOT`
- `VITE_API_PROXY_TARGET`
- `ORRERY_SERVER_BIN`
- runtime 诊断开关和本机 Codex 凭据文件路径

# 第二部分：构建配置

## 唯一入口

| 文件 | 内容 |
| --- | --- |
| `config/build.env` | `GO_VERSION`、`NODE_VERSION`、`RUNTIME_NODE_VERSION`、`PYTHON_VERSION`、APT/Go/npm/pip 镜像源 |
| `deploy/releases/local.env` | 本地源码构建使用的 image name/tag |
| `deploy/releases/example.env` | 生产不可变 image digest 模板 |
| `config/env/dev-tools.env.example` | 本地源码 checkout 和工具发现路径 |

Compose 对 build 参数使用必填插值；缺少 `build.env` 会明确失败。Dockerfile 只声明 `ARG`，不再维护第二份版本/镜像源默认值。发布镜像使用完整引用：

```text
ORRERY_API_IMAGE
ORRERY_WEB_IMAGE
PAGEINDEX_IMAGE
ORY_HYDRA_IMAGE
OPENFGA_IMAGE
```

生产应把 `ORRERY_RELEASE_FILE` 指向由 CI 生成并使用 digest 的 manifest。根 `deploy.sh` 依次加载：

1. `.env`：部署拓扑、共享身份提供方注册和 secret；
2. `orrery_v2/config/build.env`：构建权威值；
3. `ORRERY_RELEASE_FILE`：发布制品权威值。

手工 Compose 同样显式传入三类文件，例如：

```bash
docker compose \
  --env-file .env \
  --env-file orrery_v2/config/build.env \
  --env-file orrery_v2/deploy/releases/local.env \
  up -d --build
```

# 第三部分：生产配置

## YAML 中的配置

`config/orrery.production.yaml` 保存所有非敏感实例级启动配置：

- `server`、`device_delivery`、`paths`
- `database` 六角色无密码拓扑
- `runtime.execenv`、`runtime.managed`、`runtime.mcp`
- `integrations.legal`
- `bootstrap.pageindex`、`bootstrap.gateway`
- `filestore`、`logging`、`health`、`queue`、`lock`、`liveness`、`metrics`
- `secret`、`sweeper`、`tracing`、`resource`
- `auth`、`authz`、`discussion`

Hydra/OpenFGA overlay 也遵守这条规则：非敏感 URL、issuer、普通 OAuth client ID、Store ID、Model ID、mode 和 timeout 写入 production YAML。飞书 `ORRERY_FEISHU_APP_ID` 是唯一例外：它和 app secret 组成同一个外部应用注册，Orrery API 与 Kratos 必须从同一份 `.env` 读取，避免两边配置漂移。

正式 profile 校验包括：

1. schema/profile/environment 一致；
2. 核心 section 完整；
3. 六个数据库角色完整；
4. production 数据库全部为 PostgreSQL；
5. PostgreSQL URL 不得内嵌密码，`password_env` 固定为 `ORRERY_DATABASE_PASSWORD`；
6. YAML 不得包含认证授权明文 secret；
7. runtime MCP 基本字段完整。

`device_delivery.public_origin` 在 production 必须配置为浏览器与桌面客户端实际访问的 HTTPS origin，例如 `https://orrery.example.com`。它同时用于 Device Gateway 的 WSS 地址、DPoP target 和短期 artifact upload URL，不能写内部容器地址或带 path 的 URL。`default_tenant_id` 仅用于没有显式 tenant claim 的单租户登录，`max_artifact_bytes` 是服务端硬上限，当前允许范围为 1 byte 到 100 MiB。

## API env allowlist

允许进入 Orrery API 容器的 env 分为四类。

配置选择器：

- `ORRERY_CONFIG`

数据库、认证和运行时 secret：

- `ORRERY_DATABASE_PASSWORD`
- `ORRERY_SECRET_*`
- `ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET`
- `ORRERY_FEISHU_APP_ID`、`ORRERY_FEISHU_APP_SECRET`、`ORRERY_FEISHU_STATE_SECRET`
- `ORRERY_OIDC_CLIENT_SECRET`、`ORRERY_HYDRA_ADMIN_TOKEN`
- `ORRERY_AUTHZ_OPENFGA_TOKEN`
- `ORRERY_RUNTIME_MCP_TOKEN` / `ORRERY_RUNTIME_MCP_AUTHORIZATION`
- `ORRERY_INTERNAL_VIEWER_TOKEN` / `ORRERY_INTERNAL_VIEWER_AUTHORIZATION`
- `ORRERY_GATEWAY_SECRET_KEY` / `ORRERY_GATEWAY_SECRET_KEY_FILE`

一次性 bootstrap：

- `ORRERY_BOOTSTRAP_SUPERADMIN_PASSWORD`

维护/运行时凭据边界：

- `ORRERY_OPERATOR_ID`：一次性维护命令审计操作者；优先使用对应 CLI flag
- provider 的 token env、`ORRERY_CODEX_AUTH_FILE`、`ORRERY_CODEX_CONFIG_FILE`：隔离 runtime 凭据，不是 Orrery 非敏感应用配置

Compose 的 `ORRERY_API_BIND`、`ORRERY_API_PORT` 等变量只做宿主端口插值，不注入 API 容器。

以下旧非敏感 env 已退出正式加载链：`ORRERY_DATABASE_URL(S)`、`ORRERY_ENV`、`ORRERY_BASE_DIR`、`ORRERY_API_ADDR`、`ORRERY_LOG_*`、`ORRERY_FILESTORE_*`、除共享 `ORRERY_FEISHU_APP_ID` 外的 `ORRERY_AUTH*` 非 secret 项、`ORRERY_AUTHZ*` 非 secret 项、`ORRERY_DISCUSSION_AGENT_*`、`ORRERY_EXECENV_ROOT`、`ORRERY_ENABLE_MANAGED_RUNTIME`、`ORRERY_LEGAL_MCP_URL`、`ORRERY_MCP_REGISTRY_URL` 和 PageIndex bootstrap 的非 secret env。旧 `config.Source` / env-file / override 解析入口已经删除，而不是仅在 server 层绕开；即使进程环境仍存在这些变量，`LoadYAML` 也不会读取它们。

## Settings 哪些不能由普通用户修改

普通 Settings 只应包含可在线消费、可校验、可审计的实例业务参数。以下字段不得作为普通用户动态配置：

- 数据库、监听地址、trusted proxy、认证/授权模式；
- 文件系统根目录、Workspace Root/Allowlist、secret root；
- runtime executable、任意 extra args、SSH key path、remote token；
- `bypassPermissions`、auto approval 等审批绕过；
- Gateway 主密钥、Raw Gateway YAML；
- 日志文件路径、队列/锁/进程拓扑。

这些配置属于部署运维或专门的高危管理权限，当前统一走 YAML/secret 发布并重启。Settings 中尚未接线的“只保存不消费”字段应隐藏或删除，不能返回 fake success。

可以保留为实例级动态配置的内容包括：

- Discussion loop limit、Memory enabled；
- Knowledge provider、非 secret URL/timeout/folder；
- LLM provider/model/temperature/context 和 `api_key_ref`；
- Prompt Blocks；
- Gateway channel enabled、endpoint 和经批准的加密 channel credential。

LLM/PageIndex 不再接受或返回明文 API key，必须使用 `secret://...` 引用。历史明文数据仍需单独迁移清理。

## 动态配置后续门槛

当前 `core.app_settings/default` 是单实例逻辑记录，暂不增加 tenant 维度。要把动态配置称为可靠生产配置，仍需完成：

- section PATCH，而不是整文档覆盖；
- revision/ETag + `If-Match` CAS；
- 分权权限和脱敏审计；
- preflight、desired/active revision、apply status；
- last-known-good、失败回滚和多副本 revision 确认。

在这些能力完成前，认证授权、文件系统和进程边界一律不进入动态 Settings。

## Reload 策略

`LoadYAML` 在启动、preflight 和内部 reload 入口使用同一 secret 合并链，但生产尚未接入完整 watcher/原子 apply/回滚/多副本协调。因此所有 YAML 修改仍按以下流程：

```text
修改 YAML -> -check-config -> 发布 -> 重启 -> 健康检查
```

## 自检

发布前至少执行：

```bash
ORRERY_DATABASE_PASSWORD=test-password go run ./cmd/server -config ./config/orrery.production.yaml -check-config
ORRERY_DATABASE_PASSWORD=test-password go run ./cmd/server -config ./config/orrery.development.yaml -check-config
go run ./cmd/server -config ./config/orrery.desktop.yaml -check-config
go test ./internal/platform/config ./cmd/configctl ./cmd/server
```

配置相关实现入口：`internal/platform/config/config.go`、`internal/platform/config/yaml.go`、`config/schema/orrery.schema.json`、`cmd/server/server_entrypoint.go`、`cmd/configctl/main.go`、两个 Compose、`dev.ps1`、`dev.sh` 和 `src-tauri/src/lib.rs`。
