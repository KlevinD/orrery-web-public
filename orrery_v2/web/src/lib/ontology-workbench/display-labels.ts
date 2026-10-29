const definitionKinds: Record<string, string> = {
  object: '对象',
  link: '关系',
  property: '属性',
  mapping: '映射',
  action: '动作',
  event: '事件'
}

const objectTypes: Record<string, string> = {
  organization: '组织', mission: '使命', project: '项目', task: '任务', person: '人员', team: '团队',
  department: '部门', position: '岗位', role_assignment: '角色分配', agent: '数字员工', tool: '工具',
  mcp_server: 'MCP 服务', skill: '技能', idea: '灵感', todo: '待办', issue_comment: '问题评论',
  requirement: '需求', decision: '决策', design_artifact: '设计产物', legal_entity: '法律实体',
  contract: '合同', policy: '政策', approval_case: '审批单', repository: '代码仓库', issue: '问题',
  pull_request: '拉取请求', release: '发布', value_objective: '价值目标', metric: '指标', initiative: '举措',
  experiment: '实验', knowledge_document: '知识文档', knowledge_base: '知识库', knowledge_chunk: '知识片段',
  knowledge_page: '知识页面', knowledge_anchor: '知识锚点', memory_profile: '记忆配置',
  scheduled_task: '定时任务', scheduled_run: '定时执行', agent_task: '智能体任务', agent_version: '智能体版本',
  agent_run: '智能体运行', context_snapshot: '上下文快照', execution_trace: '执行轨迹', evidence_item: '证据项',
  artifact: '产出物', memory_digest: '记忆摘要', memory_record: '记忆', iris_snapshot: 'IRIS 快照',
  action_run: '动作运行', policy_decision: '策略决策'
}

const propertyNames: Record<string, string> = {
  name: '名称', title: '标题', display_name: '显示名称', status: '状态', description: '描述', code: '编码',
  role: '角色', email: '邮箱', skills: '技能', availability: '可用状态', authority_scope: '权限范围',
  purpose: '用途', owner_profile_id: '负责人', level: '级别', job_family: '职族', role_id: '角色 ID',
  scope: '范围', runtime_kind: '运行时类型', risk_level: '风险级别', provider: '提供方',
  provider_tool_id: '提供方工具 ID', transport: '传输方式', version: '版本', enabled: '是否启用',
  summary: '摘要', source: '来源', priority: '优先级', acceptance_criteria: '验收标准', rationale: '决策依据',
  counterparty: '相对方', effective_date: '生效日期', expires_at: '到期时间', jurisdiction: '司法辖区',
  environment: '环境', value: '数值', unit: '单位', observed_at: '观测时间', hypothesis: '假设', outcome: '结果',
  source_uri: '来源地址', knowledge_base_id: '知识库 ID', content_type: '内容类型', size_bytes: '文件大小',
  checksum: '校验值', document_id: '文档 ID', page_number: '页码', heading_path: '标题路径', confidence: '置信度',
  agent_task_id: '智能体任务 ID', runtime_run_id: '运行 ID', source_kind: '来源类型', source_ref: '来源引用',
  trace_ref: '轨迹引用', input_refs: '输入引用', memory_refs: '记忆引用', knowledge_refs: '知识引用',
  memory_kind: '记忆类型', summary_ref: '摘要引用', captured_at: '捕获时间', duration_ms: '耗时（毫秒）',
  actor_kind: '执行主体类型', actor_id: '执行主体', effective_user_kind: '有效用户类型', effective_user_id: '有效用户',
  agent_run_id: '智能体运行 ID', agent_run_ref_status: '运行引用状态'
}

const schemaRelations: Record<string, string> = {
  from: '起点类型', source: '起点类型', to: '终点类型', target: '终点类型', property_of: '所属对象', allows_link: '允许关系', allows_action: '允许动作',
  applies_to: '适用于', projects_object: '投影对象', projects_link: '投影关系', projects_through: '通过映射投影'
}

const attributeNames: Record<string, string> = {
  tenant_scope: '租户范围', canonical_key_rule: '规范键规则', sensitivity: '敏感级别', lifecycle: '生命周期',
  properties: '属性', from_object_type: '起点对象类型', to_object_type: '终点对象类型', cardinality: '基数',
  evidence_required: '必须提供证据', suggested_allowed: '允许建议关系', object_type_id: '对象类型',
  property_id: '属性 ID', value_type: '值类型', authority_source: '权威来源', conflict_policy: '冲突策略',
  event_type: '事件类型', source_domain: '来源领域', source_ref: '来源引用', event_schema_version: '事件结构版本',
  rule_hash: '规则校验值', enabled: '是否启用', category: '类别', description: '描述', provider_kind: '提供方类型',
  provider_ref: '提供方引用', permission_scope: '权限范围', risk_level: '风险级别', dry_run_supported: '支持试运行'
}

const values: Record<string, string> = {
  system: '系统', tenant: '当前范围', active: '启用', disabled: '停用', global: '全局',
  one_to_one: '一对一', one_to_many: '一对多', many_to_one: '多对一', many_to_many: '多对多',
  string: '文本', number: '数字', boolean: '布尔值', array: '数组', object: '对象',
  authority_wins: '权威来源优先', latest_wins: '最新值优先', manual_review: '人工审核',
  in: '流入', out: '流出', resolved: '已解析', unresolved: '待解析', running: '运行中', completed: '已完成', failed: '失败'
}

const domains: Record<string, string> = {
  operations: '运营', mission_control: '使命管理', resource_admin: '资源管理', planning: '规划', legal: '法务',
  development: '研发', value: '价值管理', knowledge: '知识', memory: '记忆', iris: 'IRIS',
  action_contract: '动作契约', agent_runtime: '智能体运行', scheduler: '调度', observability: '可观测性'
}

export function definitionKindLabel(value: string) { return definitionKinds[value] ?? value }
export function objectTypeLabel(value: string) { return objectTypes[value] ?? value }
export function propertyLabel(value: string) { return propertyNames[value] ?? value }
export function schemaRelationLabel(value: string) { return schemaRelations[value] ?? value }
export function attributeLabel(value: string) { return attributeNames[value] ?? value }
export function ontologyValueLabel(value: string) { return values[value] ?? value }
export function domainLabel(value: string) { return domains[value] ?? value }

export function attributeValueLabel(attribute: string, value: unknown) {
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value !== 'string') return JSON.stringify(value)
  if (['from_object_type', 'to_object_type', 'object_type_id'].includes(attribute)) return objectTypeLabel(value)
  return ontologyValueLabel(value)
}
