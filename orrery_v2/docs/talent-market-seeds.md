# 开源版人才市场示例

Orrery 启动时会幂等初始化五个合成的后台职能岗位示例：财务规划与分析、财务控制与会计、法务合同与合规、人力资源与组织运营、采购与行政运营。

这些档案满足人才市场的边界：

- placement.status 为 market，没有组织、部门、职位或汇报关系，因此不会被默认内部人员查询或自动编入 Mission。
- 运行时标记为 managed，没有绑定工具、技能、外部账号或凭据。用户雇佣后可以按自己的部署方式绑定运行时和资源。
- 每个档案有独立的个人记忆引用。启动时的通用 Agent 记忆引导会按引用创建记忆档案。
- 名称、人格、提示词和职责都是合成示例，不代表真实个人，也不包含第三方私有资料。
- 财务、法务、人事和采购岗位只提供分析、整理和建议，不自动执行付款、签约、用工或采购决定。
- 五个岗位分别复用一个不同的内置头像，头像文件由 workforce avatar 默认清单提供。
- 导出时会保留 talent_market 元数据，可以作为开源示例包继续导入其他 Orrery 实例。

默认档案 ID 为：

- talent-market-backoffice-finance-planning
- talent-market-backoffice-finance-control
- talent-market-backoffice-legal-contract
- talent-market-backoffice-hr-operations
- talent-market-backoffice-procurement-operations

如果部署方不希望自动创建示例，可以设置：

    ORRERY_SKIP_DEFAULT_TALENT_MARKET_BOOTSTRAP=true

后续版本只会新增或更新后台职能岗位；已经被管理员雇佣或修改的旧档案不会被启动引导强制删除。人才市场导入、导出和雇佣接口仍然可以管理自定义档案。
