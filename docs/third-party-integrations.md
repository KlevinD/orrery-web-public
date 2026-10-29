# 知识库与第三方集成审查

2026-10-19 范围决定：Orrery 本次仅交付自身 Web/API，保留连接独立 PageIndex
知识服务的适配器、配置和[项目链接](https://github.com/VectifyAI/PageIndex)。
独立 PageIndex 服务的源码、依赖与镜像不随 Orrery 分发，也不要求在此次开源。
下文对该独立服务的检查保留为历史记录，不作为 Orrery 本次开源阻塞项。

2026-10-16 更新：当前候选版本的依赖移除以[移除记录](../orrery_v2/docs/runtime/dependency-removal-20261016.md)为准；下文线上核验保留其检查时点。

Orrery 的 Apache-2.0 只覆盖自有源码。SBOM 是依赖清单，不能替代第三方的完整许可证、版权声明或再分发所需材料。复制/修改源码、随产品打包依赖和通过 HTTP 连接独立服务，需要分别记录实际分发内容。

## 知识库

| 组件 | 当前接入方式 | 声明及证据 |
| --- | --- | --- |
| PageIndex 上游 | 主仓库通过 `internal/knowledge/provider/pageindex` 调用 HTTP；独立 Orrery-pageindex 服务内复制上游源码 | [VectifyAI/PageIndex](https://github.com/VectifyAI/PageIndex/blob/main/LICENSE)，MIT，Copyright 2025 Vectify AI。独立服务须保留上游完整许可证与版权。主仓库声明接入关系，不把服务实现标为全部自研。 |
| 本地向量存储 | `internal/knowledge/vector/jsonstore/store.go` 使用 `chunks.json` 和 Go 余弦搜索 | Orrery 自有源码。为兼容已有 JSON 数据，持久化 backend 标识及数据目录仍保留 `lancedb`；它们不代表使用 LanceDB 引擎。 |
| Embedding / LLM API | 通过 HTTP 调用配置的提供方 | 未发现知识库内打包模型权重。API 使用条款、今后选择的模型权重许可及导入文档版权独立处理。 |

## 独立 PageIndex 服务的实际缺口

只读核查 `MinviaOrg/Orrery-pageindex` 的 commit
`1bef1015af5fe5dd7d13a0cb40ae8d7aac6bfe40`：

- `upstream_pageindex/LICENSE` 已保留 Vectify AI 的 MIT 文本。
- `pageindex-kb-service/requirements.txt` 包含 `pymupdf`；PyMuPDF 上游使用 [AGPLv3](https://github.com/pymupdf/PyMuPDF/blob/main/COPYING)，并提供 [商业许可](https://pymupdf.io/licensing/) 路径。未确认具体镜像中已安装的版本或商业授权。镜像/服务公开前须明确许可路径及相应源码提供、再分发或授权条件；PageIndex 的 MIT 不能替代这项审查。不能仅凭该依赖推断通过 HTTP 接入的整个 Orrery 主仓库应改为 AGPL。
- 该 requirements 还包含 LiteLLM、OpenAI Agents SDK、FastAPI、FastMCP、Celery、OpenPyXL、PyPDF2、python-docx、psycopg 等。多数未固定版本，未找到锁文件；主仓库的 Go/npm/Rust SBOM 没有覆盖这个独立 Python 镜像。需按实际镜像摘要收集已解析依赖、完整许可证和服务 SBOM。
- Dockerfile 执行 `COPY upstream_pageindex /app/upstream_pageindex`，因此会带入 `examples/documents/` 中的示例 PDF，包括 `PRML.pdf`。文件名提示可能含教材，尚未核实每份文档内容和授权；软件 MIT 不能自动作为这些文档的再分发依据。正式镜像应排除无必要示例，或提供具体文档的再分发依据。
- 核查树内未发现独立服务根目录的 LICENSE/NOTICE。服务自有代码的发布许可和第三方声明需在该仓库中明确，不能以 Orrery 主仓库的 LICENSE 自动代替。

以上为独立服务的待办记录；本次只修改 Orrery 的声明材料，没有改动该服务的源码、镜像或许可。

## 指定线上实例的只读核验

2026-10-14 使用用户提供的 SSH 目标核查 Docker 容器、systemd 用户服务、运行进程的非敏感队列环境项和当前服务源码。该实例的知识库由 `orrery-pageindex-management.service` 在宿主机运行，使用 `pageindex_service.main`，不是主仓库 Compose 中的 `pageindex` 容器。当前进程及启动环境文件没有设置 Celery/Redis 队列覆盖项，工作目录没有 `.env`；正在使用的配置源码默认 `queue_backend="local"`，服务工厂按 `local` 选择本地队列。HTTP 健康检查返回 200 / ok。

未发现该实例存在 Redis 容器、Redis/Celery 服务或 Redis server 进程，也未发现 6379 监听。当前这个 PageIndex 实例未启用 Redis/Celery。主仓库 `redis:8-bookworm` 是可选 `celery` profile 的部署配置，不能据此认定线上正在使用 Redis 8。该结论只覆盖此次检查的服务器和时点；其他实例、后续部署及可选镜像仍需按实际情况核实。检查未修改或重启线上服务，未记录密钥值。

开源默认部署保留 `PAGEINDEX_QUEUE_BACKEND=local`，不要求安装 Redis。当前服务工厂在该模式创建基于 `ThreadPoolExecutor` 的 `JobWorker`；选择 `celery` 时才创建 `CeleryJobWorker`，现有 Compose 扩展为它配置 Redis broker/result backend。保留这个扩展时，在声明中列为可选集成；Redis 服务器不属于默认运行依赖。若今后完全移除该扩展，才同步移除相应部署配置和组件条目，不能只删声明而留下配置。

## 其他需要具名声明的组件

| 组件 | 当前使用 | 许可证证据 |
| --- | --- | --- |
| AionUi | 直接导入的桌面源码基线 | Apache-2.0，已有 `orrery-desktop/NOTICE` 和固定 upstream commit。 |
| PI coding agent v1.3.0 | API 镜像安装的编码代理及传递依赖 | [MIT](https://github.com/earendil-works/pi/blob/v1.3.0/LICENSE)，Copyright 2025 Mario Zechner；原文保存在 `compliance/licenses/`。 |
| 飞书/Lark SDK、钉钉 Stream SDK | 仓库内 vendored Go 源码替换 | 两者均有原始 MIT LICENSE，分别位于 `orrery_v2/third_party/lark-sdk` 和 `dingtalk-stream-sdk`。 |
| Ory Kratos / Hydra | 身份服务 Compose overlay | [Kratos v1.3.1](https://github.com/ory/kratos/blob/v1.3.1/LICENSE)、[Hydra v2.3.0](https://github.com/ory/hydra/blob/v2.3.0/LICENSE)，Apache-2.0。 |
| OpenFGA | 权限服务 Compose overlay | [项目源码 Apache-2.0](https://github.com/openfga/openfga/blob/main/LICENSE)；具体发布镜像的版本、摘要及依赖声明仍按镜像核实。 |
| PostgreSQL、Node/Python/OS 基础镜像及 UI 依赖 | 服务与前端的运行依赖 | 按最终镜像/安装包收集其具体版本和原始许可证；Go/npm/Rust/Bun 源码清单不等于完整镜像 SBOM。 |

## 模型与实时消息依赖移除（候选版本）

全部 Eino 模型、消息、工具定义、选项及流式消息类型已迁入 Orrery 自有
`internal/agent/llm`，模型配置与 HTTP 客户端位于 `providers/profilemodel`。
`go.mod`、`go.sum`、服务端包依赖图不再包含 Eino 或其 schema 模块。
AgentKernel 的循环、审批及持久化工具日志继续由 Orrery 实现。

实时消息使用保留 MIT 声明的 Centrifuge 内存分发裁剪版本，排除 Redis 后端，
移除传递的 `github.com/redis/rueidis` 客户端。当前 Compose 不包含 Redis/Celery
服务；独立 PageIndex 服务仍按其本地队列运行。这次没有修改独立服务的 Python 环境。
上面的 2026-10-14 记录描述当时的可选配置；不是当前候选配置。

LanceDB 与 Arrow Go 的实验测试及无调用的表结构占位代码已经删除，
相关 Go 模块与校验和通过 `go mod tidy` 清理，并重新生成依赖清单。
实际本地实现改名为 `internal/knowledge/vector/jsonstore`，将文本块和向量写入
`chunks.json`，通过 Go 逐条计算余弦相似度并排序；PageIndex provider 则调用独立
服务的 HTTP 接口。为兼容已有数据，数据库中的 `lancedb` backend 标识和磁盘目录名
保留，不执行数据迁移或删除。旧设计计划明确标记为历史记录，不能作为当前依赖依据。

以上用途根据主分支 `7caa048ea5b4` 及开源准备分支的源码核对，
不构成对当前线上二进制的重新验证；前述线上记录保留其原核验范围。

发布时保留完整许可证和版权；对修改的 Apache 源码保留相关通知并标明改动；按最终分发的依赖履行其具体条件。模型、头像/图标、字体、示例数据和知识库导入文档也保留各自的授权证据。

根目录声明见 `THIRD_PARTY.md`、`NOTICE` 和 `compliance/`。Go 许可证证据可由
`python scripts/opensource-inventory.py --refresh-license-evidence` 显式更新；常规生成与
`--check` 使用已冻结证据，保持跨机器可复现。
