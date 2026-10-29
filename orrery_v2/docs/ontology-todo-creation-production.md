# Todo 创建事实的生产投影

`todo.created` 为直接创建任务和已接受 Idea 转任务记录创建来源。Repository 创建事务同步写入持久创建意图；生产服务正常返回前尝试发布，后台平台 sweeper 负责中断恢复。现有 `planning.todo.changed` 继续负责任务对象及当前标题、状态、责任人。

事件的租户来自已保存的 Todo，发生时间及发布身份来自 `CreatedAt`、`CreatedBy`、`CreatedByKind`，来源 ACL 继续指向原租户的 `todo` 业务对象。事件 ID 包含编码后的租户和任务 ID，避免同名任务跨租户共用日志幂等键。

Idea 转任务同步保存真实转换者的 `CreatedByKind` 和 `CreatedBySubjectID`，与直接创建保持一致；事件不通过缺省服务身份补造来源。

创建映射只写三个属性：`created_at`、`creation_actor_id`、`creation_source`。这些属性与当前状态投影分开，因此晚到或重复重放创建事件不会回写初始标题、状态、责任人、对象标签或当前事件证据。该规则不声明生成 Todo 对象；来源事实补充已有的任务对象。

启动安装默认定义时，只向全局 Todo 定义增加这三个缺少的属性，使用预期版本更新。现有属性、显示名称、AllowedActions 和租户自定义定义遵循原有治理；重复安装不增加 Todo 定义版本。

## 恢复边界

发布沿用 `applyOntologyEvent`：先持久保存事件，再应用图投影。投影失败时，已记录的事件可通过既有受限重放路径恢复；相同事件 ID 的重复投影保持来源事实与证据身份。缺少租户、任务 ID、创建人、创建者类型、创建时间或来源的创建发布会在记录事件前拒绝。隔离数据库中注入实际事实写入故障，验证图事务回滚且日志保留，修复存储后用新 runner 恢复。

`CreateTodo` 和 `ConvertIdea` 在业务事务内向同一 Core 数据库的 `platform_records` 逻辑表 `todo_creation_outbox` 写入创建意图，不跨库借用 Jobs outbox。意图写入失败时，任务创建和 Idea 转换完整回滚。意图只冻结原租户、任务 ID、创建者/授权主体、时间和来源；普通任务修改不会覆盖，任务删除不会丢弃待发意图。

生产入口通过 `DeliverCreationEvent` 同步尝试交付。`todo.creation_events` 注册到现有平台 sweeper，独立于用户定时任务和 Todo 自动化，每次最多处理 100 条到期待发记录。CAS 领取设置一分钟重试租期，避免同时领取和失败记录长期挡住其他任务；进程在领取后退出也能在租期后恢复。只有事件追加与图投影都成功后才以 CAS 删除意图。图写入或回执删除失败可重复交付，相同事件 ID 保持幂等；超出租期的并发交付仍可能重复，属于至少一次语义。

旧任务不扫描回填；直接绕过 Repository 的原始数据库写入不受此契约覆盖。`planning.todo.changed` 的可变状态事件仍有独立交付缺口，创建意图不重放初始状态。因此覆盖清单保留 `partial_connected`，不代表全生命周期已可靠接入。待发记录无最大次数丢弃策略；持续无效来源会保留并返回恢复错误。当前查询限制结果数，未增加 JSON 字段专用索引，大规模积压下的查询成本需要另行验证。

## 验证与契约

- `TestProductionTodoAndIdeaWritesPlanningOntologyContextGraph`：真实 HTTP 入口，验证直接创建、Idea 接受后转换、创建事实、来源事件及原有审批/状态约束。
- `TestTodoCreationOntology*`：迟到重放保留当前任务快照、事件日志幂等、投影失败后记录可恢复、无效来源拒绝、同 ID 跨租户及来源 ACL 隔离、持久化定义升级与重复安装。
- `TestInstallDefaultsUpgradesTodoCreationPropertiesOnce`、`TestTodoCreationUpgradePreservesTenantDefinition`：受限属性升级、现有字段和行动授权保留、租户定义不变。
- `TestIdeaConversionPersistsResponsibleCreator`：人和 Agent 转换者的真实身份及授权主体落库。
- `TestTodoCreationOutbox*`（Todo 包）：实际关闭重开 SQLite 数据库、任务修改/删除后恢复；意图插入失败完整回滚直接创建和 Idea 转换；限量、失败租期、并发领取、跨租户及不回放旧任务。
- `TestTodoCreationOutboxProductionRecovery`：真实生产发布器和已注册 sweeper，注入事件追加前、追加后图写入及回执删除故障；恢复后保留最新任务状态、原身份和唯一创建事件。
- `scripts/check-context-graph-production-gate.ps1`：仍按当前 UTC 时间检查所有覆盖记录及未接入豁免。

涉及 `ORR-ONTOLOGY-01`、`ORR-RSI-01` 和 `ORR-DEPLOY-01`。本次增加任务创建的可追踪事实，不改变业务授权、QA、审批、私助委托、Agent 公开正文或渠道发送。图投影和测试通过不等于真实业务完成，也不等于候选生成、隔离实验、采纳回滚和效果反馈已形成完整 RSI。
