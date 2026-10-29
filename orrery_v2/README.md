# Orrery V2

这是 Orrery 的 v2 纯净实现目录。

v2 是当前正式版本和开发主线，不以兼容旧实现为目标，优先保证架构解耦、功能稳定、边界清晰和长期可维护。旧系统只作为调研与参考来源，不在 v2 中做零散补丁式迁移。

当前产品方向为 SaaS-first。`orrery_v2/` 聚焦 Go control plane、SaaS Web 和服务端 Runtime Bridge；新的本地个人助理位于平级目录 `../orrery-desktop/`，以 AionUI、Electron 和本地 AionCore 为基础。当前 `src-tauri/` 只保留旧打包能力，不再承载新客户端开发。

## 文档入口

- [V2 文档索引](./docs/README.md)
- [服务端 Runtime Bridge 方案](./docs/runtime/runtime-bridge.md)
- [Desktop 客户端方案](../orrery-desktop/docs/orrery-desktop-client.md)
- [SaaS 产品与架构规划](./docs/product-planning/orrery-saas-product-architecture-plan.md)
- [SaaS 客户端、服务端与协议边界规划](./docs/agent-company-mission-control/saas-monorepo-boundaries.md)
- [后续规划](./docs/future-planning.md)
- [Agent 公司与 Mission Control 协作模型](./docs/agent-company-mission-control/agent-company-mission-control-model.md)
- [项目结构设计与公共约束](./docs/agent-company-mission-control/project-structure-guidelines.md)

## 产品规划

- SaaS 产品与架构规划：明确产品战略、ICP、价值主张、商业模式、PRD、Outcome Roadmap、架构边界和风险预演。
- 工作环境沙箱：隔离 Agent、工具、脚本和文件写入，补齐权限、配额、审计和清理策略。
- 功能个人助理：提供个人常驻助理，支持偏好记忆、待办整理、提醒和跨 Mission 上下文协助。
- 审批流规划与角色身份分配：统一审批节点、审批角色、会签、代理审批和风险升级规则。
- 人员参与任务视图：展示人员在 Mission / Task / Squad 中的职责、状态、贡献、审批责任和可见性。
- 首页资源统计：汇总人员、Agent、技能、工具、MCP、知识库、记忆、运行队列、审批和任务状态。
- 权限管理增强：补齐角色、资源授权、任务上下文权限、临时授权、审计和变更影响分析。
- 自动化：支持提醒、监控、巡检、定期汇总、周期执行和失败重试，并纳入权限与审计体系。

## 本地开发一键启停

Windows PowerShell:

```powershell
cd D:\Orrery\orrery_v2
.\dev.ps1 start
.\dev.ps1 status
.\dev.ps1 logs
.\dev.ps1 stop
```

Linux / macOS Bash:

```bash
cd orrery_v2
bash ./dev.sh start
bash ./dev.sh status
bash ./dev.sh logs --follow
bash ./dev.sh stop
```

默认启动：

- API: 默认 `http://127.0.0.1:8080`
- Web: `http://127.0.0.1:5178`
- Windows Database: `dev.ps1` 默认使用 `.dev\remote.env` 中定义的远端 PostgreSQL 六库，直接连接且不创建 SSH 隧道

也可以用 `.\dev.ps1 restart` / `bash ./dev.sh restart` 重启，或用 `.\dev.ps1 logs -Follow` / `bash ./dev.sh logs --follow` 持续看日志。Windows 脚本优先停止自己记录 PID 的进程；默认 API `8080` 或 Kratos Public API `4433` 被占用且对应服务不可用时，会向操作系统申请随机空闲的回环端口。Web 地址仍固定为 `5178`，Vite 会自动代理到实际 API 端口；`.\dev.ps1 status` 会显示本次使用的 API 和 Kratos 端口，`stop/restart` 也会读取同一份运行状态，不会误操作占用默认端口的系统服务。显式传入 `-ApiAddr` 时不会随机替换该地址。

`dev.ps1` 的默认模式只读取被 Git 忽略的 `.dev\remote.env`。该文件必须完整提供 `ORRERY_DATABASE_CORE_URL`、`ORRERY_DATABASE_AUDIT_URL`、`ORRERY_DATABASE_EVENTS_URL`、`ORRERY_DATABASE_JOBS_URL`、`ORRERY_DATABASE_RUNTIME_URL`、`ORRERY_DATABASE_OBSERVABILITY_URL` 和 `ORRERY_DATABASE_PASSWORD`。启动脚本会拒绝 SQLite、loopback 地址、缺失库、错误库名、URL 内嵌密码和不同端点混用；根据 env 生成的 `.dev\orrery.remote.yaml` 也不会被 Git 跟踪。远端模式不会执行 `-init-db`，只读校验线上 schema 已满足当前代码，并以 `-disable-background-workers` 启动本地 API，防止本地进程执行启动期数据初始化或重复消费线上 outbox、队列、定时任务和消息网关。线上实例仍负责迁移及后台工作。

只有明确需要隔离的本地开发库时才使用 `.\dev.ps1 start -Local`。`-Local` 会恢复仓库内 `config\orrery.wsl-development.yaml` 和 `deploy\dev\identity\docker-compose.yml` 的 WSL PostgreSQL、Kratos、OpenFGA 开发栈；本机忽略的 `.dev\orrery.wsl.yaml` 和 `.dev\identity\docker-compose.yml` 仍可覆盖本地模式。`-SkipDependencies` 仅影响这个显式本地模式。远端模式不会读取会话级 `ORRERY_CONFIG` 来覆盖数据库来源。

Docker Compose 单独管理身份依赖时，在 `orrery_v2` 目录执行：

```powershell
# 启动 Kratos、Kratos PostgreSQL、OpenFGA 和 OpenFGA PostgreSQL
docker compose -f deploy/dev/identity/docker-compose.yml up -d

# 配置变更后重建 Kratos
docker compose -f deploy/dev/identity/docker-compose.yml up -d --force-recreate kratos

# 查看状态和日志
docker compose -f deploy/dev/identity/docker-compose.yml ps
docker compose -f deploy/dev/identity/docker-compose.yml logs -f kratos

# 停止身份依赖；不加 -v，保留身份数据库
docker compose -f deploy/dev/identity/docker-compose.yml down
```

该 Compose 默认只向宿主机回环地址发布 Kratos Public API `4433` 和 OpenFGA HTTP API `8082`；可以通过 `ORRERY_KRATOS_PUBLIC_PORT` 修改 Kratos 宿主端口。Kratos Admin API `4434` 和两个依赖数据库均不做端口映射。单独启动 Compose 只提供依赖；Store、Model 和 API enforce 配置由 `dev.ps1` provisioning。部署后可用 `docker compose -f deploy/dev/identity/docker-compose.yml config` 检查最终端口和网络配置。

`dev.sh` 默认设置 `ORRERY_CONFIG=./config/orrery.development.yaml`，但不会管理 Windows 本地 PostgreSQL 进程；在非 Windows 环境使用前需要提供匹配的 PostgreSQL，或选择一份使用相同 schema 的开发 YAML。数据库拓扑不再通过 env 覆盖。

## 数据库后端

数据库后端没有隐式 fallback。API 启动时必须通过 `-config` 或 `ORRERY_CONFIG` 选择 YAML，并由 `database.default_url` / `database.urls` 显式定义六个数据库角色；缺少配置文件或数据库配置会启动失败。

PostgreSQL 拓扑和用户名保存在 YAML，密码只通过 `ORRERY_DATABASE_PASSWORD` 注入：

```powershell
$env:ORRERY_DATABASE_PASSWORD = "secret"
go run ./cmd/server -config .\config\orrery.production.yaml -addr 127.0.0.1:8080
```

正式 profile 必须显式配置 `core/audit/events/jobs/runtime/observability` 六个角色。开发可以让六个角色指向同一个本地库；生产必须使用 PostgreSQL，并通过 `database.password_env: ORRERY_DATABASE_PASSWORD` 引用密码。

SQLite 仍作为开发/桌面实例的显式可选后端，参考同 schema 的桌面配置：

```powershell
go run ./cmd/server -config .\config\orrery.desktop.yaml -addr 127.0.0.1:8080
```

Docker 生产形态可以直接启动 PostgreSQL 和 API：

```powershell
copy .env.example .env
$gatewayKey = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($gatewayKey)
[Convert]::ToBase64String($gatewayKey) # 写入 .env 的 ORRERY_GATEWAY_SECRET_KEY，并在所有 API 副本间共享
# 启用身份 overlay 时，飞书 App ID 和 secret 只写入这份 .env，Orrery API 与 Kratos 会共同读取
docker compose --env-file .env --env-file config/build.env --env-file deploy/releases/local.env build orrery-api
docker compose --env-file .env --env-file config/build.env --env-file deploy/releases/local.env run --rm --no-deps orrery-api -check-config
docker compose --env-file .env --env-file config/build.env --env-file deploy/releases/local.env up --build orrery-api
```

Compose 会把 `config/orrery.production.yaml` 只读挂载到 `/etc/orrery/orrery.yaml`。数据库拓扑由 YAML 定义，`ORRERY_DATABASE_PASSWORD` 只注入密码；其他 secret 仍由 env/Secret Manager 提供。生产还必须提供一个 base64 编码的 32-byte Gateway 配置加密主密钥：优先使用 `ORRERY_GATEWAY_SECRET_KEY`，或通过 `ORRERY_GATEWAY_SECRET_KEY_FILE` 指向只读 secret mount。主密钥缺失时 API 拒绝启动，生产不会按节点自动生成。

## 旧 Tauri 打包（maintenance-only）

`src-tauri/` 是旧的一体化打包入口：Tauri 启动本地 Go 后端 sidecar，前端通过运行时 bootstrap 获取本地 API 端口。它仅用于维护已有构建和私有化场景，不是 [Orrery Desktop 新客户端](../orrery-desktop/README.md) 的实现基础；不要在这里增加 Device Connector、Workspace Grant 或新的个人助理功能。

构建 Windows 桌面包：

```powershell
cd D:\Python\projects\IdeaDo\orrery_v2
.\scripts\build-desktop.ps1
```

仅构建 Go sidecar 和前端资源、不调用 Tauri 打包：

```powershell
.\scripts\build-desktop.ps1 -SkipTauri
```

构建 Linux 桌面包：

```bash
cd orrery_v2
bash ./scripts/build-desktop.sh
```

仅构建 Linux Go sidecar 和前端资源、不调用 Tauri 打包：

```bash
bash ./scripts/build-desktop.sh --skip-tauri
```

Linux 导出 agency-agents Talent Market 导入包：

```bash
bash ./scripts/export-agency-agents-talent-market.sh --source-dir ../../_refs/agency-agents-zh
bash ./scripts/test-agency-agents-import.sh
```

将 Council of High Intelligence 的协调器和 18 个分析席转换为 Talent Market 导入包：

```powershell
python .\scripts\council_of_high_intelligence_import.py `
  --source-root D:\path\to\council-of-high-intelligence `
  --output-zip .\data\imports\council-of-high-intelligence-orrery-talent-market.zip `
  --source-commit <commit-sha>
```

桌面开发模式：

```powershell
cd D:\Python\projects\IdeaDo\orrery_v2\web
npm run tauri:dev
```

Linux 桌面开发模式：

```bash
cd orrery_v2/web
npm run tauri:dev:linux
```

## 实现原则

- SaaS-first 是当前产品方向；普通用户工作台、租户管理端、平台运维端必须保持清晰边界。
- 租户、权限、审计、用量和执行安全是 SaaS 目标架构的一等约束，不能只靠前端筛选或页面隐藏实现。
- 先后端，后前端。
- 先基础设施，后业务能力。
- 先日志、配置、数据库、事件、投影等公共能力，再进入 Agent、Mission、Runtime。
- Agent 是责任主体，工具、MCP、CLI、API、RPA 都是资源，不直接作为团队成员。
- BaseAgent 保持薄内核，能力通过 AgentAssembler 与组件注册表组装。
- 任何实现 goal 都必须遵循 `project-structure-guidelines.md` 中的目录、依赖方向、日志、文件大小和测试约束。
- 所有开发禁止使用 demo 相关路由、handler、环境变量或启动参数作为实现路径。后端必须走生产 handler 和真实服务；如遇能力缺口，应显式返回依赖缺失或补齐生产实现，禁止用 demo facade、fake success、mock 数据兜底。
