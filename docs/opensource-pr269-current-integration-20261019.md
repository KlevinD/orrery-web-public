> Historical preparation record. The subsequently authorized native production release is documented in [the current release record](../orrery_v2/docs/opensource-pr269-native-release-20261019.md). Validation dates, commits and undeployed status below refer to that earlier preparation stage.

# PR #269 当前主线与生产源码整合

本轮继续完善私有 PR #269，交付范围仍为 Web/API 及必需共享源码。公开发布、客户端安装包、独立 People / PageIndex 服务和外部 MCP 业务验收不在本轮范围。

整合固定输入：主线 `981cde06d7e098a1d5028d5253b584454067520b`、实际生产 `29c333844ee0adfad2d64224d89db57adf0a710f`、原候选 `c92056e28f687400aee31cd71fd56dbbb61d57bf`。生产源码与主线先合成为 `cfee674d4a`。原候选保留为合并父提交，未重写历史；整合时仅应用候选相对其生产源码基点 `2ca1540e` 的重构和发布准备增量，避免将历史快照覆盖到近期功能上。逐文件整合记录保存在本地证据目录。

保留了当前 Ontology 持续推理、文档推理、来源撤回与 GroundedContext 权限/时效校验；AgentCore 的持久 checkpoint、任务恢复、上下文压缩、模型选择、工具审批与授权；责任后台循环；个人助理受托身份、知识归属、定时任务、Agent 原文报告和投递边界。生产的桌面 OAuth 身份会话、讨论收尾、飞书 SDK 诊断及维修根因证据同时纳入。

模型接口整合保留了主线的 Responses 流式输出、图片输入、prompt cache 和缓存/推理 token 统计，统一改用 `internal/agent/llm` 与 `providers/profilemodel`。新增生产消息格式夹具，核验旧 checkpoint 序列化与内容 hash；流式合并保留独立元数据，避免把明确上报的零用量变成未知。绝对宿主截止时间继续约束恢复或传输预算，过期执行不能因毫秒级兜底而调用模型。

通知链整合保留工作中心嵌入视图、通知摘要、跨入口已读联动，同时保留候选的用户/权限上下文缓存隔离、切换用户时取消请求、持久报告来源校验和重试时重新鉴权。没有公开 Agent 正文的业务结果仍不能用系统摘要代发。Claude 首次确认已预分配的会话 ID 时也会发出可持久化的绑定事件。

继续保留文件存储/mention 的自有实现、裁剪后的 Centrifuge 内存模式、默认 PostgreSQL/API/Web 拓扑、可选 PageIndex、16 处历史来源锚点及其许可证和署名。浏览器 connector 明确属于客户端导出排除范围，避免以后合入该目录时扩大发布范围。许可证文件保留原始字节；锁文件证据统一使用 CRLF-to-LF hash，二进制素材使用原始字节。原来的审计与验证记录保留，不将旧的全量通过结果用于本轮整合。

原生 Windows 验证补上 resourcectl 的跨进程排他文件锁，Unix 继续使用原 flock；同时修复 OpenClaw 配置路径规范化和跨系统私有路径脱敏。设备共享夹具仅规范文本换行，PI 配置继续校验内容和隔离目录；POSIX 模式位不用于证明 Windows ACL。只有 Windows 明确返回符号链接权限错误 1314 时才跳过相关用例。两张没有逐项权利证据的新文档截图已换成可审阅的 Mermaid 源码，运行时素材未因此减少。

Codex 已完成的原生工具循环继续禁止重放；显示通知不能被当作新的业务工具请求。两项 CLI 过程用例按此契约校正，真实 Mission 创建验收保留在讨论服务集成测试中，分别覆盖 `mission_brief_create` 和 `mission_prepare` 的身份、授权、任务和持久化结果。Ontology 覆盖门禁期望值同步为当前主线的全范围 142 / Web/API 104 项；25 项外部覆盖过期失败继续单列，未放宽门禁。

设计基线为修订 8，主要涉及 `ORR-RUNTIME-01`、`ORR-AUTH-01/02/03`、`ORR-WORK-01`、`ORR-SCHEDULE-01/02`、`ORR-RESP-01`、`ORR-REPLY-01`、`ORR-DELIVERY-01/02`、`ORR-ONTOLOGY-01`、`ORR-RSI-01`、`ORR-STORAGE-01`、`ORR-CONFIG-01` 和 `ORR-UI-01`。仅修正已退休的客户端契约脚本引用，不改变上述业务要求，也不重放用户排除的历史任务。

GitNexus `list_repos` 只有根目录 `36ac499` 的旧索引；对本工作树执行 `detect_changes` 明确返回 repository not found。影响检查使用完整 Git tree、逐文件差异、编译及行为回归替代；没有把旧索引或空查询当作当前代码证据。

当前验证代码基点为 `c4674fc3d05ffdb7dd8bd6a712ec4790bad82a62`。逐项机器可读结果见 [验证摘要](opensource-pr269-validation-20261019.json)，原始手动证据在 `D:/Orrery/artifacts/pr269-current-integration-20261019/`。所有提交带 `[skip ci]`，未调用 CI；用户要求不使用 Docker 后，后续构建、测试与 PostgreSQL 验证全部在原生 Windows 执行。

| 检查 | 实际结果与边界 |
| --- | --- |
| Web | 用户禁止 Docker 前完成的本轮 Linux 结果：1348 项通过、0 失败、0 跳过；Svelte 0 错误/0 警告，生产构建通过。当前 755 份 Web 输入与该轮验证清单逐项哈希一致；后续未重跑容器。 |
| 原生 API | Windows Go 构建通过，二进制 SHA-256 为 `645b2c74f1208427c4f1348d094e95ce69d5077d30c42fe6a93058c009f4e717`。未据此宣称客户端签名或设备联通通过。 |
| Go 全量初轮 | Windows 整合过程中尝试完整套件：166 个包通过、11 个包失败、12 个包跳过；9798 项通过、20 项失败、94 项跳过（含子测试）。该记录保留，不能当作最终提交的全量通过证明。 |
| 修复复测 | resourcectl，execenv/device/agenttrace，Ontology/resourceadmin/nativescheduled，OpenClaw 迁移及 Codex/Mission 专项分别复测；审批崩溃恢复、设备会话、Codex/Pi 和 Git 工作区最后定向复测为 8 个顶层用例 / 含子测试 10 项通过，4 个包通过。没有延长这些用例的原有超时或修改生产审批规则。完整套件未在最终提交上再次全跑。 |
| 原生 PostgreSQL | 独立生产配置校验、六库初始化、未认证请求 401、管理员登录、任务创建和认证读取通过。首次停止时磁盘 checkpoint 同步约 525 秒，超过脚本等待时间；数据库随后正常关闭。同一数据目录重启后以 SQL 核验原任务仍存在，API 健康检查通过，隔离进程已停止。重启后重新登录并通过 API 读取未复测。 |
| 源码与依赖 | 完整清单为 97 条 Go 校验和版本、510 条 npm、519 条 Rust 和 151 项资源；Web/API 专用为 97 条 Go、510 条 npm、16 项已核验素材。严格导出、独立清单及逐项密钥复核通过；最终归档 commit/hash 随 PR 描述和本地归档元数据固定，归档不含 Git 历史、客户端和工作流。 |

源码导出支持测试 12 项通过；默认三服务配置测试 3 项在用户禁止 Docker 前通过。Web/PI 锁文件保持本轮已记录的官方 npm 审计结果，各为 0 漏洞，此结果不覆盖 Go、操作系统或镜像。密钥扫描的 40 项原始命中均须匹配已提交复核清单中的精确坐标及行内容 hash，未扩大忽略规则；新增命中为验证报告中的原生 API 二进制 SHA-256，经重算构建文件摘要后精确复核；未复核命中为 0。

设计契约检查只证明索引和引用的机械一致性，不能自动证明语义兼容。真实外部模型、外部 MCP、独立 PageIndex 服务、真实渠道送达、客户端安装包和签名仍需各自验收。本轮保持私有草稿、未合并、未部署、未公开发布；后续公开方式仍另行讨论。
