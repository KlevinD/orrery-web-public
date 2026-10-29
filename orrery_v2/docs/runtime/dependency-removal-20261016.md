# Web / Go 依赖移除记录

2026-10-16 候选分支：`refactor/remove-legacy-dependencies-20261016`。
以当日核实的线上 Go 源码快照和 Web 源码快照整合，保留线上新增功能。
这是尚未部署的候选版本。核验、构建均手动运行，不使用 CI。

| 项目 | 候选版本处理 | 剩余边界 |
| --- | --- | --- |
| Multica | 接入此前独立重实现的提及解析及 Local/S3 存储；排除旧文件与移植指引 | 文件和消息数据契约保持兼容；旧私有 Git 历史不代表可直接公开 |
| Eino | 用 Orrery 自有 `internal/agent/llm` 的消息、模型接口、工具定义、选项与消息流替换；HTTP 模型客户端移至 `providers/profilemodel` | 不包含 Eino 框架或其 JSON schema 模块；原模型配置和业务接线保留 |
| Redis | Centrifuge 以保留 MIT 声明的内存模式源码分发，排除可选 Redis broker/presence/shard；移除 `rueidis`；当前 Compose 无 Redis 服务 | Centrifuge 仍是第三方组件；本次未修改独立 PageIndex 的 Python 环境 |
| AionCore | Web / Go 无外部安装、下载、编译、测试 checkout 或运行依赖；自有会话契约测试替代固定上游 trace | 历史会话改编的 16 个生产文件、共享测试及一份 Claude 样例仍有来源关系，保留完整许可和来源清单；不宣称这些生产实现已全部独立重写 |

本次不发布客户端。Device Protocol v1 的 `aioncore_version` 是兼容字段，
不加载 AionCore。保留该字段不等于依赖外部运行时。

线上 Go 快照 SHA-256：
`98c4fb87050e4a25fe7cdcebbde2111f226da87614589ddac239113f71bd348b`。
线上 Web 快照 SHA-256：
`a180c1a394096bc56861310ea6cf9039fb5939adb39d9665ec5fb15ca7752be2`。
整合排除了旧候选分支中线上没有的偏好模块等残留文件，避免与线上接口混用。
该排除仅属于本候选分支的源码基线整理。

验证覆盖模型配置、工具 schema、模型响应、AgentKernel 授权与工具日志、
消息流取消与关闭、工具参数分片、会话契约、存储与实时消息，并构建 Linux API。
源依赖、许可与修改范围记录在根 `NOTICE`、`THIRD_PARTY.md` 和
`compliance/source-adaptations.json`；Centrifuge 的原始文件摘要及裁剪说明位于
`third_party/centrifuge/UPSTREAM_FILES.json` 和 `ORRERY_ADAPTATION.md`。

仍需单独闭环的开源事项包括 PageIndex 的 PyMuPDF 许可路径及示例 PDF 授权、
最终 Web 分发中的依赖许可证收集、资源授权与拟公开历史的凭据扫描。
本次移除四项安装依赖不代表这些发布条件自动完成。

本次手动验证：本次改动相关的 110 个服务端生产接线用例通过，定向 Go 包测试、
`go vet`、Linux amd64 API 构建及来源清单校验通过。完整服务端回归出现 8 个失败，
并达到默认 10 分钟超时；其中两个“未配置启用模型”的失败在未修改的线上源码
快照中同样复现。未将完整服务端回归标为通过，也未在 Linux 完成全量回归。
Windows 无 gcc，未运行 race detector。本次源码凭据扫描有 39 条脱敏命中，
尚未逐项重新判定，均未命中本次依赖替换修改的文件；这不代表整个拟发布源码
或 Git 历史已通过凭据审查。首轮 API 验证时 Web 与线上归档一致。

## 后续 Web 清理

删除没有页面调用的 ELK schema 布局、旧 D3 context 布局及其过时测试，
移除 `elkjs`、`d3` 聚合包、`@types/d3` 和无生产引用的 `bits-ui`。
当前本体页面继续使用既有 force-graph 组件；直接声明实际使用的
`d3-force` 及其类型，不依赖聚合包的安装提升。ISC 子依赖仍然保留。

锁文件同步后的 `npm ci`、Svelte 检查（0 错误、0 警告）、当前本体图组件
4 项测试、静态 Web 构建和来源清单校验通过。前端清理没有改 API 或数据库。
本候选仍未部署。独立 PageIndex 的默认 PDF 后端及可选 Celery 依赖清理，
在其自己的仓库和验证记录中单独提交，不以本仓 Web 清理代替服务验收。

## 2026-10-17 补充状态

上文保留 10 月 16 日快照。当前更新见 [WEB/API 开源准备状态](../../../docs/opensource-web-release-readiness-20261017.md)。
Linux 实际源码包全量命令已执行，原始测试/子测试事件 9334 通过、15 失败、57 跳过，退出码 1，超时事件 0；
3 项失败已有修复后的定向通过证据，完整回归仍未通过。没有运行 race detector。
源码快照 39 项凭据命中已逐项审阅，无未审阅命中或确认有效凭据；本轮未扫描 Git 历史。
40 项 WEB/API 资源中 16 项来源与完整许可证已对应，24 项仅补齐历史 Git 字节对应，仍待分发依据。
PageIndex 后续默认 PDF 后端与依赖替换、样例 PDF 源码分发排除在独立草稿中处理；完整镜像及原生库分发审查仍未完成。
当前不合并、不部署、不公开发布。历史 AionCore 来源声明仍保留，不能把无外部运行依赖解释成无来源关系。
