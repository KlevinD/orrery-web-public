# Web/API 源码首发准备（2026-10-18）

后续状态：本页两项工程验证缺口已由 [2026-10-19 全量验证](opensource-web-full-validation-20261019.md) 关闭；本页保留当时的中断证据。

本轮完成默认部署解耦、依赖漏洞修复、构建下载校验、Web/API 专用清单和独立运行验证。首发只包括 Web/API 与所需共享源码；客户端、IRIS、People 独立系统、外部 MCP 业务验收和 PageIndex 独立服务不在此次交付范围。

| 项目 | 已落实内容 |
| --- | --- |
| API 构建 | Docker 下载 Go 模块前复制本地 Centrifuge 模块清单；复用构建缓存；HTTPS 下载 Node 并核验固定 SHA-256。 |
| 依赖安全 | Web 更新安全版本及 Vitest 4；PI 从 0.79.9 升至 1.3.0，解决上游 shrinkwrap 固定漏洞依赖的问题。两份 npm 锁文件经官方 registry 审计均为 0 漏洞。 |
| PI 兼容 | 等待 `agent_settled` 才返回完成；底层 `agent_end` 后仍允许重试和后续运行。验证恢复后的最终正文及取消结果，保留宿主工具/权限边界。 |
| 默认部署 | PostgreSQL、API、Web 三项即可解析 Compose 配置；PageIndex 使用独立 overlay 显式启用，验证自己的凭据。新配置不自动 seed PageIndex。 |
| 文档与清单 | 新的 [部署说明](web-api-release.md)、[Web/API 清单](../compliance/web-api-source-inventory.json) 和 [源码 SBOM](../compliance/web-api-sbom.cdx.json)；导出包可在没有 Git、客户端和 Rust 锁文件时校验专用清单。 |

源码清单包括 97 条 Go 校验和版本、Web 391 条与 PI 118 条 npm 记录，以及 16 项已核验资源；589 个依赖在专用 SBOM 中按包和版本去重。这些包括构建/测试工具和保留版本，不是线上进程或镜像内的实际依赖数量。完整私有仓库清单中的 Rust/客户端记录不属于此次首发。

保留 PI 的 MIT 原始许可、Playwright 的 Apache-2.0 LICENSE/NOTICE 及对应包完整性证据。当前 npm 清单没有空白许可字段；其他依赖的原始许可仍分别适用。16 项图片/字体已核验，24 项旧图片已移出源码树，未解决的历史来源记录保留。AionCore 安装依赖已移除，保留源码的历史署名仍适用；顶层 Apache-2.0 不替代第三方许可。

## 实际验证

- 最终 Web 全量：1289 通过、0 失败、1 跳过；4 workers，单测试 15 秒上限。跳过项是 Windows 不执行的 POSIX 静态资源清理脚本场景。此前失败记录保留，修复构造 mock、过期查询断言和占位示例后，164 项定向测试及完整复跑通过。
- 最终 Svelte 检查 0 错误、0 警告，静态生产构建通过；最终 Web 锁文件在独立空目录 `npm ci --ignore-scripts` 通过；PI 1.3.0 的 clean install 通过。
- Go 配置/PI：196 通过、1 跳过；server 发布相关定向测试 284 通过；注册、发现和发布脚本结构检查 6 通过。
- 12 个 Chromium 登录/授权/图像设置页面场景通过，接口使用隔离 fixture，不是外部身份系统验收。图像设置覆盖保存、连接检查、移动端和只读权限。
- 真实 API 进程：未认证请求返回 401，bootstrap 账户登录、任务创建、重启后重新登录与数据持久化通过；没有启动 PageIndex。该测试显式使用允许的本地 SQLite 配置，不冒充默认 PostgreSQL Compose 冷启动。
- 真实 PI 1.3.0 RPC 进程：状态查询、提示接收、生成文本和 `agent_settled` 通过；模型端点为本地确定性 HTTP fixture，不代表外部付费模型账号已验收。
- 3 项真实 Compose 合并测试、12 项源码导出测试、清单校验和 Bash 语法检查通过。

## 尚未完成的工程验证

1. 默认 PostgreSQL/API/Web 的完整镜像构建及整套冷启动。API 构建已通过 Go 模块下载和 Linux 编译，镜像运行层构建过程中 Docker Desktop 两次退出，完整镜像没有产出；第二次日志明确记录 GUI `POST /app/quit`。保留中断证据，未继续重启全局引擎。
2. 最终版本的 Linux 全量 `go test ./...`。此前运行已完成 50 个包、3027 项测试；server 包达到整体 10 分钟上限，之后引擎退出。该失败不能写成全量通过；最终 PI/配置与相关 server 测试在 Windows 上已重新通过。

这两项属于构建/运行回归；People、外部 MCP 的过期接入记录不是本次开源阻塞。分发容器/原生程序时需记录相应镜像/二进制的实际组成和许可；不把镜像清单缺失泛化为纯源码发布的阻塞。

本轮依据设计基线修订 7 核对 `ORR-RUNTIME-01`、`ORR-KNOWLEDGE-01` 与 `ORR-CONFIG-01`；运行边界变化是 PI 1.3 终态适配和首次部署的可选知识服务，现有任务、权限、知识归属与投递链不在本次改动范围。GitNexus 缺少此工作树索引；实际 query/impact/detect_changes 返回找不到仓库，使用源码、哈希与相关测试核验。

本轮全部手动验证，未使用 CI。固定提交源码包及逐项扫描证据保存在私有审计目录，未部署线上、合并或公开发布。
