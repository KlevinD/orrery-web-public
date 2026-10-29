> Historical preparation record. The latest private synchronization and validation are recorded in
> [the self-developed replacement sync](opensource-self-developed-sync-20261026.md).
> The [earlier source synchronization report](opensource-sync-20261026.md) retains its original results.

2026-10-19 当前交付范围：只交付 Orrery 自身 Web/API。PageIndex 作为独立外部知识服务，
保留连接支持、配置说明和[项目链接](https://github.com/VectifyAI/PageIndex)，
不要求在此次开源或交付该服务的源码、依赖和镜像。此前针对独立服务的准备记录保留为历史，
不作为本次发布条件。Orrery 的实际分发材料按[第三方许可附件流程](third-party-license-bundle.md)核验。

PR269 已于 2026-10-19 将依赖重构部署到 API/Web，生产基点 `81df3a7e`；
实际原生验证与局限见[上线记录](../orrery_v2/docs/opensource-pr269-native-release-20261019.md)。

历史准备结论：[2026-10-19 Web/API 全量验证](opensource-web-full-validation-20261019.md)。已修复 Linux npm 10 的 Web 锁文件兼容问题，完成 API/Web 镜像构建、默认 PostgreSQL/API/Web 冷启动与重启持久化、Linux Go/Web 全量回归及数据库/运行时补充契约。上一阶段两项工程验证缺口已关闭；以下为保留的历史阶段记录。公开发布方式按用户要求另行讨论。

最新首发准备：[2026-10-18 Web/API 源码收尾](opensource-web-source-finalization-20261018.md)。已完成依赖安全更新、默认 PageIndex 解耦、专用清单和独立运行验证；当前工程验证缺口为完整 Compose 冷启动和 Linux 全量回归。以下旧阶段记录保留，不能据此重新把外部 MCP 或 People 列为开源阻塞。

范围更正（2026-10-18）：用户确认 People 为独立系统，原清单中其他业务接入事项属于外部 MCP，均不属于此次 Orrery Web/API 开源要求。此前把 25 项过期豁免列为开源阻塞的结论已撤回。默认覆盖门禁现按 Web/API 范围通过，外部完整集成另行验收。最新范围和验证见[首发范围说明](opensource-web-mcp-scope-20261018.md)；下文为此前阶段快照。

最新补充：[2026-10-18 Ontology 手动门禁核验](opensource-web-ontology-gate-review-20261018.md)。已修复 PowerShell 吞掉失败退出码及报告丢失过期豁免的问题；完整 Ontology 包 408 通过、1 失败，25 项过期豁免仍阻塞发布。两种手动门禁实际退出码均为 1，未延期豁免或提升覆盖状态。下文保留此前阶段快照。

# Orrery 开源准备

WEB/API 首发使用[固定提交源码导出](opensource-source-export.md)，排除全部客户端目录、
预编译文件和示例 PDF；普通全仓库 Git 归档不符合当前首发范围。

2026-10-18 最新补充：[文档与品牌图片整理](opensource-web-documentation-brand-assets-20261018.md)，剩余 11 张旧 PNG 已移出当前源码树，架构改用 Mermaid，登录/授权页改用现有品牌文字；当前保留 16 项图片/字体文件，16 已核验、0 待核验。此前[默认头像替换](opensource-web-default-avatar-replacement-20261018.md)和[运行时素材替换](opensource-web-runtime-asset-replacement-20261018.md)已移除 13 张旧图，累计 24 项未解决历史来源记录均继续保留。严格素材核验可通过；Ontology、镜像/原生库分发及其余业务验收门禁仍阻塞公开发布。此前已接入[补写报告组织通知与来源校验](opensource-web-recovered-report-notifications-20261018.md)、[独立报告生命周期](opensource-web-scheduled-report-lifecycle-20261018.md)及[任务通知页面](opensource-web-notification-ui-20261018.md)。

接续 2026-09-21 决策：Claude 与 PageIndex 保持适配，自研 Core 脱离 AionCore
供应商二进制。此次不处理 IRIS，不更改仓库可见性、不合并 main。

## 交付范围

- 首发范围为 Web / Go 服务端。AionCore 不是 Web 的构建、运行或测试安装依赖；
  当前会话契约与验收由 Orrery 维护。历史会话改编代码和保留的 Claude fixture
  仍载有 Apache-2.0 来源义务，已补完整许可证、NOTICE 和[来源清单](../orrery_v2/docs/runtime/runtime-source-provenance.md)。
  本次替换原移植测试与固定上游 trace，不宣称整个会话层已无第三方源码来源。

- Web / Go 服务端的第三方迁入残留已按[替换范围](multica-removal.md)重实现；
  消息执行复用 AgentCore，提及解析与 Local/S3 存储保留业务和历史数据契约。

- 根目录采用 Apache-2.0，保留 AionUi 和第三方声明。
- Claude 使用已有 Go 原生 CLI 适配器；删除 SDK sidecar、默认镜像及开发脚本的
  SDK 安装。旧 `agent_sdk` 设置迁移为 `native`，保留本地可执行文件与参数。
- PageIndex 通过 HTTP 服务契约和 `PAGEINDEX_IMAGE` 集成；部署不再要求
  `../Orrery-pageindex` 或替用户拉取外部源码。
- 删除未接入生产的 LanceDB/Arrow Go 实验、Eino 旧 Agent 消息/工具执行链路及
  无调用配置；实际本地 JSON 存储改名为 `jsonstore`，保留已有数据路径兼容。
  2026-10-16 候选版本进一步以 Orrery 自有 `internal/agent/llm` 和
  `providers/profilemodel` 替代全部 Eino 模型/消息接口，移除 Eino 及其 schema 模块。
- 实时消息保留 Centrifuge 内存模式和 WebSocket 协议，以保留 MIT 声明的裁剪源码
  排除 Redis 后端及客户端模块；当前 Compose 不包含 Redis 服务。
- 桌面构建编译 `orrery_v2/cmd/desktop-core`，生成版本、平台、架构与 SHA-256
  清单。打包和安装验证使用该清单，不再下载 AionCore。
- Core 实现本地会话、Claude 原生会话、流事件、明确审批、取消与 SQLite 消息保存。
  同一会话并发发送返回冲突，拒绝不完整审批和跨来源浏览器请求。
  重启后执行中会话标记 interrupted，待审批不恢复；有消息的历史会话只读，
  直到实现 provider resume，禁止静默开启丢失上下文的新会话。
- 恢复五个合成人才市场样例：财务规划、财务控制/核算、法务、人力和采购行政。
  不带工具、技能或凭据；删除后不重复创建。

## 桌面状态

Core 是可构建、可测试的独立源码实现，尚未覆盖完整 AionUi 界面的全部 API。
此次源码准备不代表正式安装包发布。`orrery-desktop/core-compatibility.json`
记录缺口；正式 release gate 必须失败，直到完成设置/助手/provider、文件/附件、
provider 重启续接和各平台 Electron 实际端到端验证。开发预检不能替代安装包、
签名、公证或第三方资源授权验证。

既有设计文档的供应商版本、固定摘要、资源和 API 记录保留为迁移历史。
当前构建来源以 `upstream-lock.json` 的 `core` 项及本文件为准。
README、开发指引、上游同步和桌面发布文档已统一为自研 Core 流程；不再要求
另行克隆 AionCore 或设置已移除的供应商二进制授权变量。历史客户端设计保留边界
要求，并在入口标明被迁移说明替代的构建步骤。
Device Protocol v1 的 `aioncore_version` 暂保留以兼容协议，值为本地 Core 版本；
它不是供应商二进制依赖。

## 验证

```bash
cd orrery_v2
go test ./cmd/desktop-core ./cmd/server ./internal/transport/http
go test ./internal/agent/runtime/providers/claude ./internal/knowledge/provider/pageindex
cd ../orrery-desktop
bun install --frozen-lockfile
node scripts/prepareOrreryCore.js
bun run test:contract
bun run compliance:check
bun run release:preflight
cd ..
python scripts/opensource-inventory.py
```

本次验证手动运行，不使用 CI；Go 全量测试需要 Linux，既有 `cmd/resourcectl` 使用 Unix flock，在 Windows
上不能编译。Core 和本次改动需要独立 Windows 验证。真实 Claude 验收需要用户安装
并登录自己的 CLI；协议 fixture 不代表真实账号、网络或收费服务验收。

## 公开前条件

根目录依赖和资源清单、桌面 CycloneDX 清单记录证据。未确认的资源授权必须补证据
或替换；不能将仓库来源当成授权。安全审计的实际结果、许可证例外和平台测试缺口
随审阅材料保留。

源码归档只包含版本控制下已审阅的文件，禁止 `.env`、客户数据、数据库、日志、
node_modules、构建输出及第三方可执行文件。gitleaks 扫描源码和拟公开的完整可达
历史；报告隐藏密钥值。真实凭据先轮换，不通过忽略规则掩盖未处置发现。新建公开
仓库时使用审阅快照、私有保留旧历史；未经批准不重写主线。

公开仓库、发行包、自动更新与合并主线是此准备 PR 之后的独立操作。

2026-10-16 的线上源码整合与移除范围见[依赖移除记录](../orrery_v2/docs/runtime/dependency-removal-20261016.md)。候选分支尚未部署；不把线上旧二进制描述为已移除 Eino。
