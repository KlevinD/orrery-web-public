# 当前主张与支持证据的只读上下文

本轮基于 `Semantica ca6c9f1918ad05e2bbaa4922fb32f34e2a508bb9` 中的
`context/truth_maintenance_filter.py` 对标。Semantica 用一个 TMS 会话快照检查
`required_facts` 和 `required_support_ids`，并在返回前检查会话版本。
Orrery 复用既有时态推理内核与来源/权限边界，增加持久服务上的只读读取协议。

API：`POST /api/ontology/inference/context`；Agent 工具：
`ontology_grounded_context`。沿用 `ontology.read`、canonical scope 授权、来源 ACL、
DataPolicy 和既有并发/预算限制。默认本体管理员获得这个只读工具；其他 Agent
沿用工具绑定与授权流程，不自动获得额外数据权限。

## 返回的含义

- `claims`：按 tenant 和结构化 Atom 标识的主张。多条独立来源可以支持同一主张。
- `supports`：具体的可见断言及主机生成的版本；版本覆盖投影内容、stream/version、
  可见证据和已核对的文档来源头。不同来源、不同发布代次不能替代精确旧支持。
- `proofs`：同一主张的证明路径是 OR；同一路径内的依赖是 AND。
  返回有界证明图，循环不能脱离实际前提自行成立。
- `candidates`：调用方显式声明旧上下文依赖的 claim ID 或 assertion_ref/version；
  所有依赖重新计算，返回 `decisions[].eligible`。空注解、未知字段和伪造主机 stamp
  不能作为已验证输入。模型负责将自然语言材料与具体主张对齐；依赖通过不证明
  任意自然语言正文与这些依赖等价。
- `snapshot_hash`、`access_hash`、`checked_at`、`next_boundary`：分别说明本次可见
  证明输入、当前授权版本、检查时间和已观察到的后续业务时间边界。
  不变输入的检查时间不会制造新版本。每次复用仍需重新读取；它不是写入授权。

`supported_in_snapshot` 说明在这份有界、受权限约束的 Ontology 快照中有来源断言或
结构有效的规则证明。它不证明来源绝对真实，也不替模型判断规则的业务含义。
争议断言作为 `disputed_assertions` 留供模型检查，不能进入确定前提。
没有返回主张可能是超出当前 root 范围，不能由空结果推断全系统不存在。

## 文档来源与读取一致性

入口通过现有 `InferenceSnapshot` 读取两跳、最多 25 个对象、100 条关系、1000 条事实和
400 条证据。超预算失败，不以不完整片段假装完整推理。文档目录、PageIndex provider
ID、Ontology root ID 分开处理；不创建第二份全文索引。

对于 `knowledge_instances` 断言，读取当前领域 claim、原 stream 和投影 hash，
核对 Source 的 Orrery 文档、provider 文档、发布 revision/hash、generation、
bridge binding、当前目录状态和 source head。未映射的 legacy、源撤回、重发、
目录删除或版本不符会排除对应断言，再重算主张和证明；其他独立支持可以保留主张。
这里证明的是本地已记录的发布头。上游变更尚未同步到 Orrery 时，不声称已实时感知。

返回前再次读取投影与来源目录、检查 graph/schema 输入 fence、当前权限/策略修订和
业务时间边界。读取期间发生变化返回 HTTP 409 或相应授权错误，不返回混合版本。
跨存储读取使用有界的乐观复核，不声称跨 PageIndex、知识库、权限库建立单个数据库
事务。真实写入继续使用原事务内 source/authorization/support fence 与幂等回执。

## 与现有执行链和记忆的关系

只读计算复用 `Service.evaluate(..., persist=false)` 的时态过滤、冲突识别、schema
验证、AND/OR 闭包与 grounded proof 检查，不保存 run/proposal/session/decision/receipt，
也不触发模型、后台重抽取或新的定时任务。

写入仍走既有模型直接判断、预览、原子写入和持久回执链，没有新增人工审核。
记忆引用和 lineage 继续表达来源；不会因引用的文档撤回而删除历史经历、偏好或经验。
普通知识搜索结果继续是材料，不能自动标为当前事实。历史 BusinessAsOf/KnownAt
读取沿用原入口，不与本轮当前候选验证混用。

本轮没有自动为所有文档、压缩摘要或缓存生成 claim 映射，也没有将所有 Agent 的
上下文强制迁移到新入口。对已显式声明的依赖，模型可通过新入口在复用前验证。
后续可在现有摘要/缓存链上接入这些声明，继续保持历史与逻辑依赖的区别。

## 验证范围

回归覆盖独立支持撤回、精确版本变更、缺失 AND 前提、争议断言、时间边界、稳定
版本、空依赖拒绝、取消、只读无工作流写入、读取中来源/权限变化，以及真实生产
构造下的文档发布头、撤回、重发与新 revision。另保留原推理执行与历史读取回归。
Linux 组合构建、隔离 PostgreSQL、线上版本和健康检查的实测结果另存部署验收回执，
不能以源码存在代替已上线，也不以隔离夹具代替真实业务 Agent 的完成验收。

对应设计契约：ORR-ONTOLOGY-01、ORR-KNOWLEDGE-01/03、ORR-MEMORY-01、
ORR-AUTH-03、ORR-STORAGE-01、ORR-DEPLOY-01。
