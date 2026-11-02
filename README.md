# Orrery

Orrery 是面向团队的 AI Agent 协作平台。它把人、Agent、企业知识和工具组织在同一个 Mission 中，将业务目标转成可执行、可审阅、可交付的工作。

Orrery 关注的不是再增加一个聊天窗口，而是让 Agent 进入真实组织：拥有角色和职责，理解企业对象与关系，使用受治理的工具，和人一起推进任务，并把结果、证据和复盘沉淀为组织资产。

## 核心能力

### 数字员工

Agent 在 Orrery 中是组织成员，而不是孤立的工具节点。每个 Agent 可以绑定角色、能力范围、所属组织、当前 Mission、可用知识、工具权限、工作状态和协作关系。

### Mission

Mission 是把目标变成工作的基本单位。一个 Mission 可以包含：

- 目标、背景、范围、优先级和交付标准；
- 人与 Agent 组成的混合团队；
- 任务分解、依赖、排队、状态和阻塞处理；
- 讨论、上下文补充和中间结果评审；
- 交付物、关键结论、证据、QA 和后续行动。

### Enterprise Ontology

Enterprise Ontology 是企业共同的业务语义层。它描述业务对象、关系、状态、来源、访问引用、允许的动作及动作影响，让人、Agent 和应用围绕同一组企业现实协作。

Ontology/ContextGraph 是受治理的语义投影，不替代客户的交易系统，也不把模型输出直接当作事实。事实需要来源和证据；跨租户、跨主体和高风险写入必须经过服务端授权与动作契约。

### RSI

RSI 把运行结果连接到受控改进。它围绕真实的 run、task、trace、context 和反馈收集证据，形成观察、归因、改进候选、评审和版本记录。

一个改进候选需要经过：

1. 冻结基线与依赖；
2. 有界实验或 replay；
3. 独立验证；
4. 资源版本激活；
5. 运行观察与回滚。

RSI 的 shadow 模式用于观察、评估和保存证据。模型建议仍然是候选，不会直接修改生产策略、权限、任务合同或业务数据；生产激活必须经过相应治理和版本化证据。

### 工具、权限与交付治理

Orrery 可以接入客户已有的 MCP 能力和业务系统，并负责能力登记、发现、分配、权限、运行时调用和审计。高风险操作可以进入 HIL/Approval 流程，任务交付必须同时满足交付物、证据和 QA 要求。

## 一次工作的完整链路

```text
业务目标
  -> Mission
  -> 人与 Agent 组队
  -> 任务分解与执行
  -> 受治理的知识、工具和 MCP 调用
  -> 审批 / HIL（按风险需要）
  -> 交付物、证据与 QA
  -> RSI 观察、评审与受控改进
  -> 可复用的组织能力
```

## 开源范围

本仓库当前开源首发范围为 **Web/API 与所需共享源码**。客户端另行准备；People 是独立系统，外部业务 MCP 接入不属于首发验收要求。

- [首发范围、手动构建与部署](docs/web-api-release.md)
- [固定提交源码导出与核验](docs/opensource-source-export.md)
- [Web/API 依赖清单](compliance/web-api-source-inventory.json) 与 [源码 SBOM](compliance/web-api-sbom.cdx.json)
- [V2 README](orrery_v2/README.md) 与 [文档索引](orrery_v2/docs/README.md)

服务端位于 `orrery_v2/cmd/server` 和 `orrery_v2/internal`，SvelteKit 前端位于 `orrery_v2/web`。`cmd/desktop-core` 与部分协议源码用于共享实现，保留源码不代表此次发布客户端。

默认 Docker Compose 启动 PostgreSQL、API 和 Web。PageIndex 通过 HTTP 适配，独立服务使用 `docker-compose.pageindex.yml` 显式启用。Redis、Eino、Multica 和 AionCore 安装均不是默认启动依赖。

Go 开发需要 Go 1.25；Web 和 PageIndex 运行时需要 Node >=22.19。配置、手动验证和部署命令见首发说明。模型、CLI 或外部集成由运营者提供自己的账号与配置。

## 许可证

Orrery 自有源码使用 [Apache-2.0](LICENSE)。请同时保留 [NOTICE](NOTICE)、[第三方声明](THIRD_PARTY.md)、历史改编源码署名、原始许可证和资源授权记录。仓库顶层许可证不替代第三方许可。
