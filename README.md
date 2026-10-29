# Orrery

Orrery 是面向团队的 AI Agent 协作平台，提供 Web 工作台、Go API、任务与讨论、Agent 执行、审批、知识库适配、权限和审计。

本次开源首发范围为 **Web/API 与所需共享源码**。客户端另行准备；People 是独立系统，外部 MCP 业务接入不属于首发验收要求。

- [首发范围、手动构建与部署](docs/web-api-release.md)
- [固定提交源码导出与核验](docs/opensource-source-export.md)
- [Open-source compliance readiness (2026-10-27)](docs/opensource-compliance-readiness-20261027.md)
- [Web/API 依赖清单](compliance/web-api-source-inventory.json) 与 [源码 SBOM](compliance/web-api-sbom.cdx.json)
- [2026-10-26 自研替换私有同步与验证](docs/opensource-self-developed-sync-20261026.md)
- [Earlier source synchronization and validation](docs/opensource-sync-20261026.md)
- [Manual validation and production deployment](orrery_v2/docs/operations/manual-deployment.md)
- [V2 README](orrery_v2/README.md) 与 [文档索引](orrery_v2/docs/README.md)

默认 Docker Compose 启动 PostgreSQL、API 和 Web。PageIndex 通过 HTTP 适配，独立服务使用 `docker-compose.pageindex.yml` 显式启用。Redis、Eino、Multica 和 AionCore 安装均不是默认启动依赖。

服务端位于 `orrery_v2/cmd/server` 和 `orrery_v2/internal`，SvelteKit 前端位于 `orrery_v2/web`。`cmd/desktop-core` 与部分协议源码用于共享实现，保留源码不代表此次发布客户端。

Go 开发需要 Go 1.25；Web 和 PI 运行时需要 Node >=22.19。配置和手动验证命令见首发说明。模型、CLI 或外部集成由运营者提供自己的账号与配置。

Orrery 自有源码使用 [Apache-2.0](LICENSE)。保留 [NOTICE](NOTICE)、[第三方声明](THIRD_PARTY.md)、历史改编源码署名、原始许可证和资源授权记录。仓库顶层许可证不替代第三方许可。

GitHub Actions CI/CD is disabled by user instruction dated 2026-10-19. Validation, builds and deployments are manual. A push, merge, tag or timer must not build or deploy. Do not restore workflows or automatic deployment cron without a new explicit user instruction.

See [PUBLIC-RELEASE.md](PUBLIC-RELEASE.md) for the sanitized public source scope.
