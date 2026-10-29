# ContextBundle 历史读取与字段屏蔽

`contextgraph.Service.BuildContextBundle` 的请求显式设置 `BusinessAsOf` 或
`KnownAt` 时，使用 `TemporalSubgraphStore.SnapshotSubgraphAsOf`。缺省的另一时间轴
使用本次请求的服务时钟。两项均缺省时继续使用当前子图读取。

历史读取在选定的业务时间和知识时间重建对象、关系和 Fact，再执行对象可见性、
ReBAC、数据策略、证据选择及 token 裁剪。历史关系的端点和 Fact 的 subject
也参与历史版本选择，不能用当前图的邻居替代历史邻居。有效期已结束或访问状态
变为 unresolved 的最新断言不会使旧断言重新出现。已知时间之后的证据不返回。

PlatformStore 的查询包含租户和显式 subject 范围，按需读取相关资源版本，不调用
整租户 `Snapshot` 或 `SnapshotAsOf`。保持最多 200 个对象、400 条关系、1,000 条
Fact、400 条证据，历史读取另外限制每次请求累计读取 6,000 个版本，并设置
5 秒期限。历史候选资源也受数量限制；历史过多时返回 `ErrQueryBudgetExceeded`，
不会截断历史后冒充完整结果，也不会退回整租户读取。历史关联查询使用现有 JSON
字段及现有索引，数据库仍可能检查多于输出数量的行；5 秒期限同时约束这类查询。
此修改不包含数据库迁移。

只实现当前子图接口的后端收到显式时间时返回 `ErrQueryInvalid`。应用服务的历史
坐标与消费层输入是两个接口；现有 consumer/MCP 输入提供当前上下文，不公布历史
日期参数。

字段屏蔽以对象 ID 和字段名为范围。删除对象属性时，同一对象下 `Predicate`
与该字段名相同的整个 Fact 也被排除；其他对象的同名字段继续按各自策略判断。
证据和 schema 从筛选后的资源生成，因此只由被排除 Fact 引用的证据不会返回。

验证覆盖内存、SQLite、独立 PostgreSQL schema、生产数据策略及 schema 接线。
回归包括晚到纠错、业务区间结束、知识时间、历史端点和 subject 变化、深度限制、
同 ID 的租户隔离、unresolved 纠错、历史数量限制、6,001 条无关历史记录，以及
属性/Fact/独占证据中的屏蔽值。Linux PostgreSQL 既有测试分别运行在独立 schema，
避免不同迁移测试复用表结构。
