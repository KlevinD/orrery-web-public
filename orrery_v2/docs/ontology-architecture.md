# Ontology Architecture

本文档定义当前本体系统的运行边界和不可破坏的约束。实现以 `internal/app/ontology`、`internal/app/contextgraph`、`internal/app/ontologyreasoning` 和 `internal/app/actioncontract` 为准。

## Company Semantic Control Plane

Ontology 是 Orrery 的公司语义控制枢纽，不只是关系图谱，也不是替代所有业务数据库的超级写模型。它统一回答五类问题：公司里有哪些对象、当前处于什么状态、为什么形成这个状态、允许执行什么动作、执行结果如何反向形成可复用经验。

- Domain Service 保留交易真相和业务不变量的所有权。
- Domain Event 与事件日志保留发生过什么以及如何重放的历史真相。
- Registry 保留对象、属性、关系、MappingRule 和 ActionType 的语义真相。
- Context Graph 提供跨域当前视图、时间视图、证据和来源，不保存原始日志、密钥、完整堆栈或模型思维链。
- ActionContract 是所有受治理写操作的唯一执行入口；Ontology 只能提出或绑定动作，不能绕过授权直接改业务域。
- Evidence、ActionRun 和 WritebackReceipt 保留验证真相；只有经过验证的结果才能更新长期规则。

可靠性闭环遵循 `observe -> classify -> aggregate -> propose -> approve -> execute -> verify -> learn`。模型可以参与分类、诊断和候选生成，但不能直接激活 RepairRule，也不能直接执行修复。RepairRule 激活默认失败关闭，必须由已配置的治理授权器批准；代码修改、权限变化和数据变更始终走高风险 ActionContract 与 HIL。

修复尝试不建立平行任务模型，而是使用 ActionRun：每次尝试必须同时绑定一个 FailureIncident 和一个 RepairRule。成功结束必须提交新的验证证据，失败运行自身可形成不可变失败证据；终态 ActionRun 幂等回写 RepairOutcome，并据此更新或隔离规则。

ActionRun 显式区分 `apply` 与 `dry_run`。默认 Provider 注册表提供无副作用的 `retry`、`restart` 和 `fallback` 预检；生产 Ontology API 只对精确绑定到非定时 Mission Agent 失败运行的 `retry` 开放真实 `apply`，并要求规则处于 active、可逆且幂等，引用同一 incident/rule 版本、args hash 和失败目标的未过期 dry-run preflight，同时消费一次性高风险 HIL receipt。派发后 ActionRun 保持 `started`，只有替换运行到达队列终态并生成验证证据后才完成；失败会隔离事件，终态验证失败还会隔离规则。`restart`、`fallback`、代码补丁、重配置、权限修改、数据修改及无人值守 apply 继续失败关闭。

高风险动作必须消费一次性 HIL receipt。服务端将 receipt 绑定到同一个 tenant、requester、action、ActionType、完整对象集合、args hash、purpose、risk 和 execution mode，任何字段不一致、过期或重放都不能创建 ActionRun。修复预演通过后生成一等 `evidence_item`，记录检查项、精确失败目标以及 Provider/结果引用，并同时关联 ActionRun、FailureIncident 和 RepairRule。真实 retry 的终态验证也生成一等证据，并连接替换 AgentRun、ActionRun、FailureIncident 与 RepairRule。无论预演成功或失败，都不得增加 RepairRule 的 success/failure 统计；只有真实执行完成终态验证后才能产生 RepairOutcome 并参与学习或隔离。

## Layer boundaries

1. Registry 保存类型、属性、关系、映射规则和动作契约，只描述允许出现的结构。
2. Domain Event 是领域写模型到图投影的持久化交接点。事件先写入事件日志，再执行投影；失败事件必须可重放。
3. Conformance Gate 对即将持久化的同一个 ProjectionResult 做结构、方向、属性值类型和事件 schema version 校验。
4. Context Graph 保存租户内当前视图，同时用 fact assertion ledger 保留事实断言历史和来源。
5. Reasoning 只能读取经过数据策略过滤的 Context Bundle。推理输出是候选项，不直接修改 Registry、Context Graph 或执行动作。
6. Governance 把通过校验的 metadata candidate 转换为正式 metadata proposal，后续仍需 review、dry-run、version guard 和 confirmation token。
7. Action Contract 执行动作。高风险动作在没有服务端授权验证器时必须失败关闭。
8. HTTP 和 UI 是租户上下文的传递层，不是授权边界；租户、actor 和 reviewer 均由服务端身份上下文约束。

## Required invariants

- Context Graph 中 object、link、fact、evidence 和 projection audit 的身份是 `(tenant_id, id)`。
- 任何图快照、推理、审计、重放、提案或动作查询都必须提供非空 tenant_id。
- 客户端 tenant_id 仅作为资源定位符：服务端必须解析 canonical ScopeBinding，并使用其中的 AuthorizationTenantID、业务对象和 read/manage action 按原有对象权限授权；它不得改写 principal，也不得绕过授权租户。客户端 actor/reviewer 同样不得覆盖认证主体。
- 同一事件流内按 event version 决定当前图状态；不同事件流才使用 observed_at 处理先后顺序。
- 重放不得用过滤后的事件集合清空整个租户投影。
- 清空派生投影不得删除 projection audit、replay history 或 fact assertion ledger。
- Registry 定义不得被另一个租户以相同 id 覆盖。系统默认定义只在缺失时创建。
- 投影必须先生成一次结果，再对该结果校验并事务写入，禁止校验结果与落库结果来自两次计算。
- 推理 evidence ref、metadata target、action target 和 context pack hash 必须在服务端校验。
- 高风险动作的 approval、policy decision 和 HIL token 不能仅凭客户端提供的 ID 视为有效。
- Reliability 审批必须绑定 incident version、rule version、完整目标集合、args hash 和 execution mode；任一材料变化都使审批失效。HIL receipt 只由服务端读取和消费，不返回浏览器。
- 同一 Reliability approval 的重复 invoke 必须返回同一个 ActionRun，不能再次派发修复。

## Read and write flow

```text
Domain write -> durable event -> mapping projection -> conformance -> transactional context graph
                                             | failure
                                             v
                                      replayable event log

policy-filtered context -> reasoning candidates -> validation -> governance proposal -> reviewed promotion
                                                           \-> action review -> action contract
```

## Knowledge, ontology, and memory association

The three stores meet in Context Graph through evidence-backed retrieval edges:

- every indexed PageIndex document is projected as an Orrery `knowledge_document`;
- exact label or alias matches in the latest document revision create confirmed
  `knowledge_document_mentions_*` links to existing ontology objects;
- durable memory creates `memory_mentions_*` links to the same object dictionary,
  while source citations create `memory_cites_knowledge_*` links;
- graph traversal is bidirectional, so an ontology object returns its related
  knowledge and memory in one hop, and knowledge and memory reach each other in
  two hops through the shared object;
- document revisions reconcile stale links, and the bridge never creates new
  business objects from prose.

These mention links are automatic retrieval metadata and do not require HIL or
approval. They assert only that a source mentions an existing entity. Business
relationships between entities remain evidence-backed ontology assertions; an
exact word match alone never creates such a relationship.

## Memory semantic bridge

Memory-to-ontology association is an Ontology-owned derived projection, not a
frontend classification. `memory.ontology_bridge` scans durable memory records,
records each content hash and extractor version in a retryable run ledger, and
extracts explicit entity mentions with the configured model. The bridge:

- resolves only existing objects in the memory tenant or an authorization-derived
  parent scope (for example the owning organization or canonical mission tenant)
  by exact ID, canonical key, external ID, alias, or label; authorized parent
  objects are materialized as access-preserving references in the memory tenant,
  while arbitrary cross-tenant discovery remains forbidden;
- automatically confirms a link only when the resolution is unique, confidence
  is at least `0.90`, the Registry contains the typed `memory_mentions_*` link,
  and both endpoint types conform;
- stores unresolved, ambiguous, low-confidence, and invalid-type mentions in the
  run projection without writing a Context Graph link;
- writes evidence-backed, source-access-scoped links with stable identities and
  reconciles links that disappear when the memory content changes;
- never creates an ontology object from model output and never changes Registry
  definitions from model output.

The memory UI reads the resulting visible Context Graph neighborhood. It must
not synthesize memory-layer or entity relationships locally.

## Temporal ontology

Ontology definitions declare one temporal mode:

- `static`: identity or configuration that is not selected by business time.
- `state_interval`: a state valid on the half-open interval `[business_valid_from, business_valid_to)`.
- `event`: a business occurrence anchored at `business_valid_from`.
- `measurement`: a measured value anchored at its observation time.

Every projected object, link, and fact can carry two independent time axes:

- Business time: `business_valid_from` and `business_valid_to`. For People-owned
  organization data these fields are authoritative. Orrery must not replace them
  with event delivery time or ingestion time.
- Recorded time: `source_recorded_at` and `source_superseded_at`. These fields
  answer what the source system had recorded at a historical `known_at` and are
  used for correction replay and audit.
- `orrery_ingested_at` records replication latency and operational lineage. It
  does not change business validity.

An as-of snapshot accepts both `business_as_of` and `known_at`. For each
resource and business start, it first selects the latest version known at
`known_at`. It then selects the greatest business start not after
`business_as_of`, and only then evaluates the interval end. This ordering
prevents an older open interval from resurfacing after a relationship is ended.

People owns the temporal backbone for `workforce_subject`,
`organization_unit`, `job_profile`, `position`, `workforce_assignment`,
`reporting_relationship`, `responsibility`, and
`responsibility_binding`. Orrery owns derived measurements and execution
records. Agent execution captures responsibility from the selected People
snapshot; it does not rerun a mission merely because a responsibility exists.

People v2 mapping definitions consume schema version `2` events and support
both flat create payloads and nested catalog revision payloads. The mapping
definitions are installed by default. The production ingestion adapter accepts
the complete People v2 manifest into a tenant-scoped source ledger and retained
Ontology event log. The eight workforce, organization, assignment, reporting,
and responsibility event types listed above project into Context Graph. The
remaining metadata and governance event types are retained and acknowledged but
are explicit no-op graph projections until their mapping definitions are added.
The adapter is covered by transport integrity, replay, projection failure, and
tenant-isolation tests.

## Presentation rules

- 公司全景可以发起经过 principal 过滤的聚合查询；任何详情、审批和执行必须携带列表项返回的 `source_tenant_id`。切换公司范围时清空选择和临时 context pack。
- Advanced Operations 当前只暴露具备完整 ScopeAccessGate、数据可见性和 scoped mutation contract 的 Projection 运维；Reasoning、Actions、Models 在对应 principal-aware contract 完成前不提供旧 facade 或占位入口。
- Reliability 工作台用 Context Graph 的 `failure_incident` 投影建立公司队列，用可靠性写模型读取精确目标、规则、预检、审批和执行状态；浏览器不得从图属性重建修复绑定。
- Registry 图把 LinkType 作为一等节点，MappingRule 指向 LinkType，不把关系类型误画成普通对象间实例边。
- Schema Graph 和 Context Graph 仅加载当前激活模式。
- mutation 只失效受影响的 query subtree，避免刷新整个资源工作台。
