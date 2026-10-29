const productShell = document.querySelector('.product-shell')
const railToggle = document.querySelector('[data-rail-toggle]')
const sidebarTabs = Array.from(document.querySelectorAll('[data-sidebar-tab]'))
const sidebarPanels = Array.from(document.querySelectorAll('[data-sidebar-panel]'))
const contextAction = document.querySelector('[data-context-action="new-task"]')
const panelFilters = Array.from(document.querySelectorAll('.panel-filter'))
const panelSections = Array.from(document.querySelectorAll('[data-panel-kind]'))
const contextPanels = Array.from(document.querySelectorAll('[data-context-panel]'))
const conversationList = document.querySelector('.conversation-list')
const missionTabStrip = document.querySelector('.mission-tab-strip')
const missionPlusTab = document.querySelector('.mission-plus-tab')
const chatThread = document.querySelector('.chat-thread')
const composer = document.querySelector('[data-composer]')
const composerInput = document.querySelector('[data-composer-input]')
const sendMissionButton = document.querySelector('[data-send-mission]')
const attachmentMenu = document.querySelector('[data-attachment-menu]')
const slashMenu = document.querySelector('[data-slash-menu]')
const draftPanel = document.querySelector('[data-draft-panel]')
const detailDrawer = document.querySelector('[data-detail-drawer]')
const detailTitle = document.querySelector('[data-detail-title]')
const detailKicker = document.querySelector('[data-detail-kicker]')
const detailBody = document.querySelector('[data-detail-body]')
const reportModal = document.querySelector('[data-report-modal]')
const reportVersion = document.querySelector('[data-report-version]')
const panelTitle = document.querySelector('[data-panel-title]')
const panelStatus = document.querySelector('[data-panel-status]')

const contextConfig = {
  home: { action: '新建 Mission' },
  team: { action: '分配团队任务' },
  mission: { action: '新建 Mission' },
  people: { action: '新建组织任务' },
  mcp: { action: '新建 MCP 任务' },
  knowledge: { action: '新建知识任务' },
  audit: { action: '新建审计任务' },
  settings: { action: '新建配置任务' }
}

const templates = {
  'sales-report': {
    title: '董事会销售报告',
    prompt: '帮我基于 sales_q2.csv 生成一份董事会风格分析报告，包含趋势图、异常说明、数据来源和下一步建议。',
    goal: '基于销售数据生成董事会风格分析报告',
    output: 'HTML 报告、PDF、SVG 图表',
    agents: 'Data Analyst、RVAE Reporter、Governance',
    permissions: '读取 sales_q2.csv，调用 RVAE 图表生成',
    steps: ['理解报告目标和口径', '读取销售数据并识别异常', '生成趋势图和董事会摘要', '归档工具调用和数据来源']
  },
  'marketing-risk': {
    title: 'Marketing 发布复核',
    prompt: '复核一次营销发布风险，检查外发文案、审批链路和敏感字段。',
    goal: '复核营销发布风险并生成审批建议',
    output: '风险清单、审批建议、审计摘要',
    agents: 'Risk Reviewer、Governance、Content Agent',
    permissions: '读取营销发布草稿，检查审批记录',
    steps: ['读取发布草稿', '识别外发风险', '确认审批责任人', '生成复核结论']
  },
  'people-approval': {
    title: 'People 组织变更',
    prompt: '把组织变更计划转成审批单，列出影响团队、成员角色和需要确认的负责人。',
    goal: '将组织变更计划整理成可审批任务',
    output: '审批单、影响清单、待办分派',
    agents: 'People Agent、Approval Agent、Governance',
    permissions: '读取组织结构和成员职责',
    steps: ['识别组织变更范围', '生成审批字段', '分派成员确认', '记录审批证据']
  },
  'learning-path': {
    title: '学习路径设计',
    prompt: '从知识库生成一套新员工学习路径，按角色拆分任务和验收标准。',
    goal: '从知识库生成角色化学习路径',
    output: '学习路径、任务清单、验收标准',
    agents: 'Knowledge Agent、Learning Designer、Governance',
    permissions: '读取知识库文档和团队模板',
    steps: ['检索知识库', '归纳角色能力', '生成学习任务', '附加验收标准']
  }
}

const missions = [
  {
    id: 'board-sales',
    title: '董事会销售报告',
    status: 'running',
    statusLabel: '运行中',
    subtitle: '运行中 · RVAE 生成图表',
    section: '今天',
    needsAction: false,
    approvalStatus: '等待确认',
    approvalCopy: '允许读取 sales_q2.csv 并调用 RVAE 生成图表',
    context: 'home',
    prompt: '帮我基于这份销售数据，生成一份董事会风格分析报告，包含趋势图、异常说明、数据来源和下一步建议。',
    assistantCopy: '我会把它作为一个 Mission 执行。计划调用数据分析 Agent、报告生成 Agent 和 RVAE 图表引擎，所有 MCP 调用和产物都会写入审计记录。',
    steps: [
      ['plan', '创建 Mission', 'completed'],
      ['data', '读取数据', 'completed'],
      ['chart', '生成图表', 'running'],
      ['report', '整理报告', 'pending']
    ],
    reportVersion: 1
  },
  {
    id: 'marketing-risk',
    title: 'Marketing 发布复核',
    status: 'awaiting',
    statusLabel: '等待审批',
    subtitle: '等待审批 · 风险确认',
    section: '今天',
    needsAction: true,
    approvalStatus: '等待风险确认',
    approvalCopy: '确认本次营销发布不包含客户敏感字段并允许提交审批',
    context: 'home',
    prompt: '复核一次营销发布风险，检查外发文案、审批链路和敏感字段。',
    assistantCopy: '我已识别到 2 个需要确认的外发字段，建议由 Marketing Owner 完成风险确认后再提交审批。',
    steps: [
      ['plan', '创建 Mission', 'completed'],
      ['data', '读取草稿', 'completed'],
      ['chart', '风险确认', 'blocked'],
      ['report', '生成建议', 'pending']
    ],
    reportVersion: 1
  },
  {
    id: 'people-change',
    title: 'People 组织变更',
    status: 'completed',
    statusLabel: '已完成',
    subtitle: '已完成 · 审批单已生成',
    section: '今天',
    needsAction: false,
    approvalStatus: '已归档',
    approvalCopy: '组织变更审批单已生成并记录负责人',
    context: 'people',
    prompt: '把组织变更计划转成审批单，列出影响团队、成员角色和需要确认的负责人。',
    assistantCopy: '审批单、影响成员和负责人已整理完成，审计证据已归档。',
    steps: [
      ['plan', '创建 Mission', 'completed'],
      ['data', '读取组织', 'completed'],
      ['chart', '生成审批单', 'completed'],
      ['report', '归档证据', 'completed']
    ],
    reportVersion: 2
  },
  {
    id: 'learning-path',
    title: '学习路径设计',
    status: 'draft',
    statusLabel: '草稿',
    subtitle: '草稿 · 等待补充角色',
    section: '本周',
    needsAction: true,
    approvalStatus: '等待补充',
    approvalCopy: '需要确认目标角色和知识库范围',
    context: 'knowledge',
    prompt: '从知识库生成一套新员工学习路径，按角色拆分任务和验收标准。',
    assistantCopy: '我可以先根据知识库形成草案，但需要你确认目标角色和培训周期。',
    steps: [
      ['plan', '创建草稿', 'running'],
      ['data', '读取知识库', 'pending'],
      ['chart', '生成路径', 'pending'],
      ['report', '整理验收', 'pending']
    ],
    reportVersion: 1
  },
  {
    id: 'mcp-audit',
    title: 'MCP 工具风险检查',
    status: 'failed',
    statusLabel: '失败可重试',
    subtitle: '失败 · RVAE 图表权限不足',
    section: '本周',
    needsAction: true,
    approvalStatus: '工具权限不足',
    approvalCopy: 'rvae.render_chart_svg 缺少图表导出权限，可重试或替换工具',
    context: 'mcp',
    prompt: '检查 MCP 工具权限、外部动作和审计证据是否完整。',
    assistantCopy: '工具风险检查已发现一个失败调用。你可以重试 RVAE 图表生成，或改用 HTML 表格产物。',
    steps: [
      ['plan', '创建 Mission', 'completed'],
      ['data', '读取工具目录', 'completed'],
      ['chart', '图表生成', 'failed'],
      ['report', '整理审计', 'pending']
    ],
    reportVersion: 1
  }
]

let activeMissionId = 'board-sales'
let draftMission = null

function getActiveMission() {
  return missions.find((mission) => mission.id === activeMissionId) || missions[0]
}

function getStatusClass(status) {
  if (status === 'completed') return 'completed'
  if (status === 'awaiting') return 'warning'
  if (status === 'failed') return 'danger'
  if (status === 'draft') return 'planning'
  return 'running'
}

function groupMissionsBySection() {
  return missions.reduce((groups, mission) => {
    const section = mission.section || '今天'
    groups[section] = groups[section] || []
    groups[section].push(mission)
    return groups
  }, {})
}

function renderMissionList() {
  if (!conversationList) return

  const groups = groupMissionsBySection()
  conversationList.innerHTML = Object.entries(groups).map(([section, sectionMissions]) => {
    const buttons = sectionMissions.map((mission) => `
      <button class="conversation ${mission.id === activeMissionId ? 'active' : ''}" type="button" data-mission-id="${mission.id}">
        <span>${mission.title}</span>
        <small>${mission.needsAction ? '待我处理 · ' : ''}${mission.statusLabel}</small>
      </button>
    `).join('')

    return `<div class="nav-section-label">${section}</div>${buttons}`
  }).join('')
}

function renderMissionTabs() {
  if (!missionTabStrip || !missionPlusTab) return

  const tabMarkup = missions.slice(0, 6).map((mission) => `
    <button class="mission-tab ${mission.id === activeMissionId ? 'active' : ''}" type="button" role="tab" aria-selected="${mission.id === activeMissionId}" data-context="${mission.context || 'home'}" data-mission-id="${mission.id}">
      <span>${mission.title}</span>
      <small>${mission.subtitle}</small>
    </button>
  `).join('')

  missionTabStrip.innerHTML = `${tabMarkup}<button class="mission-plus-tab" type="button" aria-label="新建并行 Mission" title="新建并行 Mission">＋</button>`
}

function renderProgressSteps(mission) {
  const progress = document.querySelector('.progress-strip')
  if (!progress) return

  progress.innerHTML = mission.steps.map(([id, label, state]) => `
    <button class="progress-step ${state} ${state === 'blocked' ? 'is-blocked' : ''}" type="button" data-step-id="${id}">
      <span></span>${label}
    </button>
  `).join('')
}

function renderActiveMission() {
  const mission = getActiveMission()
  if (panelTitle) panelTitle.textContent = mission.title
  if (panelStatus) {
    panelStatus.textContent = mission.statusLabel
    panelStatus.className = `status-badge ${getStatusClass(mission.status)}`
  }

  const userMessage = document.querySelector('.user-message .message-body p')
  const assistantMessage = document.querySelector('.assistant-message .message-body > p')
  const missionTitle = document.querySelector('.intent-card strong')
  const sideApprovalStatus = document.querySelector('[data-side-approval-status]')
  const sideApprovalCopy = document.querySelector('[data-side-approval-copy]')
  const approvalCardStatus = document.querySelector('.approval-card span')
  const approvalCardCopy = document.querySelector('.approval-card strong')

  if (userMessage) userMessage.textContent = mission.prompt
  if (assistantMessage) assistantMessage.textContent = mission.assistantCopy
  if (missionTitle) missionTitle.textContent = mission.title
  if (sideApprovalStatus) sideApprovalStatus.textContent = mission.approvalStatus
  if (sideApprovalCopy) sideApprovalCopy.textContent = mission.approvalCopy
  if (approvalCardStatus) approvalCardStatus.textContent = mission.approvalStatus
  if (approvalCardCopy) approvalCardCopy.textContent = mission.approvalCopy

  document.querySelector('.approval-card')?.classList.toggle('is-approved', mission.approvalStatus.includes('已'))
  document.querySelector('.hil-card')?.classList.toggle('is-approved', mission.approvalStatus.includes('已'))

  renderProgressSteps(mission)
  renderMissionList()
  renderMissionTabs()
  applyWorkspaceContext(mission.context || 'home')
}

function applyPanelFilter(filter) {
  for (const button of panelFilters) {
    const isActive = button.dataset.filter === filter
    button.classList.toggle('active', isActive)
    button.setAttribute('aria-pressed', String(isActive))
  }

  for (const section of panelSections) {
    const matchesFilter = filter === 'all' || section.dataset.panelKind === filter
    const matchesContext = !section.classList.contains('is-context-hidden')
    section.classList.toggle('is-filtered-out', !(matchesFilter && matchesContext))
  }
}

function activateSidebarTab(tabName) {
  for (const tab of sidebarTabs) {
    const isActive = tab.dataset.sidebarTab === tabName
    tab.classList.toggle('active', isActive)
    tab.setAttribute('aria-selected', String(isActive))
  }

  for (const panel of sidebarPanels) {
    panel.classList.toggle('active', panel.dataset.sidebarPanel === tabName)
  }

  applyWorkspaceContext(tabName)
}

function applyWorkspaceContext(contextName) {
  const config = contextConfig[contextName] || contextConfig.mission
  if (contextAction) contextAction.textContent = config.action

  for (const section of contextPanels) {
    const contexts = (section.dataset.contextPanel || '').split(/\s+/)
    const mappedContext = contextName === 'home' ? 'mission' : contextName
    section.classList.toggle('is-context-hidden', !contexts.includes(mappedContext))
  }

  applyPanelFilter(document.querySelector('.panel-filter.active')?.dataset.filter || 'all')
}

function setActiveMission(missionId) {
  if (!missions.some((mission) => mission.id === missionId)) return
  activeMissionId = missionId
  renderActiveMission()
  closeDetail()
}

function fillDraft(template, prompt) {
  draftMission = { ...template, prompt }
  draftPanel?.classList.remove('is-hidden')
  document.querySelector('[data-draft-title]').textContent = template.title
  document.querySelector('[data-draft-goal]').textContent = template.goal
  document.querySelector('[data-draft-output]').textContent = template.output
  document.querySelector('[data-draft-agents]').textContent = template.agents
  document.querySelector('[data-draft-permissions]').textContent = template.permissions
  document.querySelector('[data-draft-steps]').innerHTML = template.steps.map((step) => `<li>${step}</li>`).join('')
}

function showDraftFromTemplate(templateId) {
  const template = templates[templateId]
  if (!template) return
  if (composerInput) {
    composerInput.value = template.prompt
    composerInput.focus()
  }
  fillDraft(template, template.prompt)
  updateComposerState()
}

function inferTemplateFromInput(value) {
  if (/营销|发布|风险/.test(value)) return templates['marketing-risk']
  if (/组织|People|审批/.test(value)) return templates['people-approval']
  if (/知识|学习|路径/.test(value)) return templates['learning-path']
  return templates['sales-report']
}

function createMissionFromComposer() {
  const value = composerInput?.value.trim() || ''
  if (!value) return
  const template = inferTemplateFromInput(value)
  fillDraft(template, value)
}

function startBlankMissionDraft() {
  const template = templates['sales-report']
  if (composerInput) {
    composerInput.value = ''
    composerInput.focus()
  }
  fillDraft(
    {
      ...template,
      title: '新建 Mission',
      goal: '等待输入业务目标后自动拆解 Mission',
      permissions: '根据输入目标自动识别需要的数据、Agent 和工具权限',
      steps: ['输入业务目标', '确认 Mission 草案', '授权必要工具', '开始执行并生成产物']
    },
    ''
  )
  updateComposerState()
}

function confirmDraftMission() {
  if (!draftMission) return
  const id = `mission-${Date.now()}`
  const mission = {
    id,
    title: draftMission.title,
    status: 'awaiting',
    statusLabel: '等待确认',
    subtitle: '等待确认 · Mission 草案已生成',
    section: '今天',
    needsAction: true,
    approvalStatus: '等待确认',
    approvalCopy: draftMission.permissions,
    context: 'home',
    prompt: draftMission.prompt,
    assistantCopy: `我已生成 Mission 草案：${draftMission.goal}。确认权限后会开始执行，并把产物和审计证据同步到右侧控制台。`,
    steps: [
      ['plan', '创建 Mission', 'completed'],
      ['data', '确认输入', 'blocked'],
      ['chart', '生成产物', 'pending'],
      ['report', '归档审计', 'pending']
    ],
    reportVersion: 1
  }

  missions.unshift(mission)
  activeMissionId = id
  draftPanel?.classList.add('is-hidden')
  composerInput.value = ''
  draftMission = null
  updateComposerState()
  renderActiveMission()
}

function handleApprovalAction(action) {
  const mission = getActiveMission()

  if (action === 'approve') {
    mission.status = 'running'
    mission.statusLabel = '运行中'
    mission.subtitle = '运行中 · Agent 执行中'
    mission.needsAction = false
    mission.approvalStatus = '已批准'
    mission.approvalCopy = '权限已确认，Agent 可以继续执行并写入审计记录'
    mission.steps = mission.steps.map(([id, label, state]) => {
      if (state === 'blocked' || id === 'chart') return [id, label, 'running']
      return [id, label, state]
    })
  }

  if (action === 'reject') {
    mission.status = 'canceled'
    mission.statusLabel = '已拒绝'
    mission.subtitle = '已拒绝 · 等待重新规划'
    mission.needsAction = false
    mission.approvalStatus = '已拒绝'
    mission.approvalCopy = '用户拒绝当前权限请求，可修改计划后重新提交'
    mission.steps = mission.steps.map(([id, label, state]) => state === 'running' || state === 'blocked' ? [id, label, 'failed'] : [id, label, state])
  }

  if (action === 'revise') {
    mission.status = 'awaiting'
    mission.statusLabel = '需要修改'
    mission.subtitle = '需要修改 · 等待补充说明'
    mission.needsAction = true
    mission.approvalStatus = '要求修改'
    mission.approvalCopy = '请补充报告口径、数据范围或替代工具后再开始'
  }

  renderActiveMission()
}

function getDetailContent(type, id) {
  const detailMap = {
    agent: {
      'data-analyst': ['Data Analyst', '清洗 sales_q2.csv，识别西区续费异常。最近输出：西区续费率低于均值 7.2 个百分点。可操作：查看日志、重试、暂停。'],
      'rvae-reporter': ['RVAE Reporter', '生成趋势图和董事会报告版式。当前使用 trend line 配置，报告版本 v1。可操作：重生成图表、导出 SVG。'],
      governance: ['Governance Agent', '记录数据读取、图表生成和报告导出的审计证据。当前无外部发送动作。']
    },
    artifact: {
      'html-report': ['HTML 董事会报告', '版本 v1，包含执行摘要、趋势图、异常说明、数据来源和下一步建议。可打开完整报告或导出 PDF。'],
      'trend-chart': ['SVG 收入趋势图', '由 RVAE 生成，来源 sales_q2.csv。可重生成图表或查看渲染参数。'],
      'pdf-report': ['PDF 报告', '等待 HTML 报告确认后生成。当前状态：排队中。']
    },
    audit: {
      'read-dataset': ['resource.read_dataset', '用户视角：读取 sales_q2.csv。审计视角：授权人林可，成功，trace id audit-read-0421。'],
      'render-chart': ['rvae.render_chart_svg', '用户视角：生成收入趋势图，未外发数据。审计视角：执行中，trace id audit-rvae-1188。'],
      'export-report': ['rvae.export_report_html', '用户视角：准备导出 HTML 报告。审计视角：排队中，trace id audit-html-2037。']
    },
    todo: {
      'data-auth': ['数据读取授权', '已由当前用户确认，允许读取 sales_q2.csv。该待办不再阻塞 Mission。'],
      'west-renewal': ['西区续费异常复核', '负责人：林可。该异常会影响董事会报告结论，未确认前建议保持 Needs input 状态。'],
      'report-tone': ['报告口径审批', '负责人：周铭。确认董事会风格措辞和下一步建议是否可发布。']
    },
    step: {
      plan: ['创建 Mission', '系统已理解目标并创建 Mission，推荐 Data Analyst、RVAE Reporter 和 Governance Agent。'],
      data: ['读取数据', '读取 sales_q2.csv 和组织上下文，记录数据来源。'],
      chart: ['生成图表', 'RVAE 正在生成收入趋势图。如失败，可在 Agent 详情中重试。'],
      report: ['整理报告', '生成董事会摘要、异常说明和下一步建议。']
    }
  }

  return detailMap[type]?.[id] || ['Mission 详情', '该对象已接入详情抽屉，可继续补充更多真实数据。']
}

function openDetail(type, id) {
  const [title, body] = getDetailContent(type, id)
  if (!detailDrawer || !detailTitle || !detailBody) return
  detailKicker.textContent = type === 'audit' ? '审计详情' : type === 'artifact' ? '产物详情' : type === 'agent' ? 'Agent 详情' : '对象详情'
  detailTitle.textContent = title
  detailBody.innerHTML = `
    <p>${body}</p>
    <div class="detail-actions">
      <button type="button">查看日志</button>
      <button type="button">重试</button>
      <button type="button">复制 trace id</button>
    </div>
  `
  detailDrawer.classList.remove('is-hidden')
}

function closeDetail() {
  detailDrawer?.classList.add('is-hidden')
}

function openReportModal() {
  const mission = getActiveMission()
  if (reportVersion) reportVersion.textContent = `v${mission.reportVersion || 1}`
  reportModal?.classList.remove('is-hidden')
}

function closeReportModal() {
  reportModal?.classList.add('is-hidden')
}

function handleReportAction(action) {
  const mission = getActiveMission()
  if (action === 'regenerate-summary' || action === 'regenerate-chart') {
    mission.reportVersion = (mission.reportVersion || 1) + 1
    if (reportVersion) reportVersion.textContent = `v${mission.reportVersion}`
  }
  if (action === 'export-pdf') {
    openDetail('artifact', 'pdf-report')
  }
  if (action === 'show-sources') {
    openDetail('audit', 'read-dataset')
  }
}

function showAttachmentMenu() {
  attachmentMenu?.classList.toggle('is-hidden')
  slashMenu?.classList.add('is-hidden')
}

function updateComposerState() {
  const value = composerInput?.value || ''
  if (sendMissionButton) sendMissionButton.disabled = value.trim().length === 0
  slashMenu?.classList.toggle('is-hidden', !value.startsWith('/'))
}

for (const button of panelFilters) {
  button.addEventListener('click', () => applyPanelFilter(button.dataset.filter || 'all'))
}

railToggle?.addEventListener('click', () => {
  const isExpanded = productShell?.classList.toggle('rail-expanded') || false
  railToggle.setAttribute('aria-label', isExpanded ? '收起左侧功能栏' : '展开左侧功能栏')
})

for (const tab of sidebarTabs) {
  tab.addEventListener('click', () => activateSidebarTab(tab.dataset.sidebarTab || 'mission'))
}

document.addEventListener('click', (event) => {
  const target = event.target.closest('button, [data-detail-type], [data-step-id]')
  if (!target) return

  if (target.dataset.missionId) setActiveMission(target.dataset.missionId)
  if (target.dataset.templateId) showDraftFromTemplate(target.dataset.templateId)
  if (target.dataset.contextAction === 'new-task') startBlankMissionDraft()
  if (target.classList.contains('mission-plus-tab')) startBlankMissionDraft()
  if (target.dataset.confirmDraft !== undefined) confirmDraftMission()
  if (target.dataset.editDraft !== undefined) composerInput?.focus()
  if (target.dataset.approvalAction) handleApprovalAction(target.dataset.approvalAction)
  if (target.dataset.detailType) openDetail(target.dataset.detailType, target.dataset.detailId)
  if (target.dataset.stepId) openDetail('step', target.dataset.stepId)
  if (target.dataset.detailClose !== undefined) closeDetail()
  if (target.dataset.openReport !== undefined) openReportModal()
  if (target.dataset.reportClose !== undefined) closeReportModal()
  if (target.dataset.reportAction) handleReportAction(target.dataset.reportAction)
  if (target.dataset.attachSource !== undefined) showAttachmentMenu()
  if (target.dataset.attachmentKind && composerInput) {
    composerInput.value = `${composerInput.value} [${target.textContent.trim()}]`.trim()
    attachmentMenu?.classList.add('is-hidden')
    updateComposerState()
  }
  if (target.dataset.command && composerInput) {
    composerInput.value = `${target.dataset.command} `
    composerInput.focus()
    updateComposerState()
  }
})

composer?.addEventListener('submit', (event) => {
  event.preventDefault()
  createMissionFromComposer()
})

sendMissionButton?.addEventListener('click', createMissionFromComposer)
composerInput?.addEventListener('input', updateComposerState)

document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    closeDetail()
    closeReportModal()
    attachmentMenu?.classList.add('is-hidden')
    slashMenu?.classList.add('is-hidden')
  }
})

applyWorkspaceContext('home')
renderActiveMission()
updateComposerState()
