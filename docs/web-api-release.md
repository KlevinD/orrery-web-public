# Web/API 首发与手动部署

此次公开范围是 Orrery Web、Go API 和所需共享源码。客户端、IRIS、People 独立系统、外部 MCP 的业务完备度及 PageIndex 独立服务均不属于首发交付。固定源码包不包含私有 Git 历史、预编译程序、运行数据或旧示例 PDF。

## 默认部署

需要 Docker Engine 和 Docker Compose v2。默认只启动 PostgreSQL、API、Web，不需要 Redis、Eino、Multica、AionCore 安装或 PageIndex 镜像。

1. 在源码根目录复制 `.env.example` 为 `.env`，设置 `ORRERY_POSTGRES_PASSWORD`、`ORRERY_BOOTSTRAP_SUPERADMIN_PASSWORD`、`ORRERY_SECRET_ONTOLOGY_CURSOR_KEY_V1` 和 `ORRERY_GATEWAY_SECRET_KEY`。两个 KEY 均须分别生成 32 随机字节并按标准 Base64 编码；密码使用自行生成的随机值。其余外部集成凭据可以留空。
2. 独立账号登录时，在 `orrery_v2/config/orrery.production.yaml` 设置 `auth.local.enabled: true`；该配置默认关闭本地密码登录。将 `device_delivery.public_origin` 设置为实际站点地址。公网部署应配置 TLS 和 `auth.require_https: true`。
3. 在根目录手动执行：

```bash
docker compose --env-file .env --env-file orrery_v2/config/build.env --env-file orrery_v2/deploy/releases/local.env build
docker compose --env-file .env --env-file orrery_v2/config/build.env --env-file orrery_v2/deploy/releases/local.env run --rm --no-deps orrery-api -check-config
docker compose --env-file .env --env-file orrery_v2/config/build.env --env-file orrery_v2/deploy/releases/local.env up -d
```

按示例环境访问 `http://127.0.0.1:27381`，账号为 `superadmin`，初始密码为自己设置的 bootstrap 密码。API 默认只绑定 `127.0.0.1:8080`。初始密码不会在文档中提供固定值。默认 Agent 执行需要运营者配置自己的模型/服务凭据；启动成功不代表已配置可用模型。

`local.env` 为本地构建镜像名称；正式分发镜像时使用经过审查的不可变摘要清单。API 中的 Node 安装校验仓库固定 SHA-256，变更 `RUNTIME_NODE_VERSION` 时必须同步更新 `config/node-SHASUMS256.txt`。构建源默认使用 HTTPS。

Linux Git checkout 可以运行 `bash ./deploy.sh` 更新代码并手动部署；源码 ZIP 直接使用上述 Compose 命令。部署脚本保持生产覆盖检查，不会启动 CI。数据库和 API 数据存放于 Docker volumes；升级前备份，普通更新不要运行 `down -v`。

## 可选 PageIndex

PageIndex 仍使用 HTTP 适配。连接已有服务时，配置知识库服务地址、模型及 `secret://knowledge/pageindex` 引用，并通过 `PAGEINDEX_API_KEY` 提供密钥。

需要同机部署独立服务时，另行准备实现 Orrery 服务接口的外部镜像，设置 `PAGEINDEX_IMAGE`、`PAGEINDEX_API_KEY`、`PAGEINDEX_POSTGRES_PASSWORD`，在每条 Compose 命令中增加：

```text
-f docker-compose.yml -f docker-compose.pageindex.yml
```

已有 PostgreSQL 数据卷不会自动再次执行初始化脚本。在启动 PageIndex 前，先用相同的两个 Compose 文件运行：

```bash
docker compose --env-file .env --env-file orrery_v2/config/build.env --env-file orrery_v2/deploy/releases/local.env -f docker-compose.yml -f docker-compose.pageindex.yml exec orrery-postgres bash /docker-entrypoint-initdb.d/20-init-pageindex.sh
```

初次使用新的数据卷会自动初始化。默认 `bootstrap.pageindex.enabled: false` 不自动预填外部知识库；在设置页配置 `http://pageindex:8091` 和 `secret://knowledge/pageindex` 后启用所需知识库。Git 部署脚本使用 `bash ./deploy.sh --with-pageindex` 显式包含此服务。该镜像的依赖和许可由其独立发布审查覆盖。

## 源码核验与依赖声明

使用 [固定提交导出说明](opensource-source-export.md) 生成和核验源码包。Web/API 的依赖记录为 [专用清单](../compliance/web-api-source-inventory.json) 与 [源码 SBOM](../compliance/web-api-sbom.cdx.json)。它们包括构建/测试依赖及 Go 校验和中的保留版本，不是容器内实际运行库清单。完整私有仓库清单中的客户端/Rust 记录不构成首发依赖要求。

```bash
python scripts/web-api-source-inventory.py --check
```

根 LICENSE 只覆盖 Orrery 自有源码。保留 [NOTICE](../NOTICE)、[第三方声明](../THIRD_PARTY.md)、改编来源、MIT/OFL 等原始许可证。当前保留的 16 项图片/字体均已核验；24 项旧图片已从源码树移除，历史来源记录继续保留。AionCore 没有安装依赖，但既有会话源码的历史署名仍适用。

## Current source synchronization

The 2026-10-26 candidate synchronizes committed Web/API and shared source
while retaining the Vite 6.4.5 and Redocly AJV 8.18.3 security updates.
Run `python scripts/third-party-license-bundle.py check` for complete
lock coverage and original notice digests. The four historical publisher
reference gaps are outside the current Web/API lock graph after the
self-developed replacements; the historical review remains available in
[the synchronization report](opensource-sync-20261026.md).
Optional Tauri bridge/CLI and SILK package originals are also retained in
`compliance/npm-license-evidence.json`. This update prepares PR #1;
merging, repository visibility and production deployment have separate states.
