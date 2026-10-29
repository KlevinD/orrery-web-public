import { buildSkillMarkdown } from '#lib/skillMarkdown'

export type RecorderSource = 'native' | 'imported' | 'demo'

export type RecorderEventKind =
  | 'app.activate'
  | 'app.title-change'
  | 'browser.url'
  | 'clipboard.change'
  | 'narration'
  | 'frame'
  | 'action'

export interface RecorderEvent {
  id: string
  timestamp: string
  kind: RecorderEventKind
  app?: string
  title?: string
  url?: string
  text?: string
  detail?: string
  redacted?: boolean
}

export interface RecordingSession {
  id: string
  name: string
  source: RecorderSource
  status: 'recording' | 'ready'
  startedAt: string
  endedAt?: string
  events: RecorderEvent[]
  privacy: {
    screenshots: boolean
    clipboard: boolean
    voice: boolean
  }
}

export interface SkillRecorderStep {
  id: string
  intent: string
  source: RecorderEventKind
  recommendedTool: string
  requiresApproval: boolean
  evidence?: string
}

export interface SkillRecorderForm {
  name: string
  description: string
  version: string
  taskTypeTags: string
  prompt: string
}

export interface SkillRecorderDraft extends SkillRecorderForm {
  id: string
  sourceSessionId: string
  createdAt: string
  steps: SkillRecorderStep[]
  markdown: string
}

export const RECORDER_TOOL_OPTIONS = [
  'browser.navigate',
  'browser.inspect',
  'browser.fill',
  'browser.assert',
  'desktop.activate',
  'data.read',
  'approval.request',
  'agent.interpret'
]

interface JsonObject {
  [key: string]: unknown
}

interface RecorderBridge {
  probe?: () => Promise<unknown> | unknown
  startSession?: (options: {
    name: string
    captureClipboard: boolean
    captureVoice: boolean
  }) => Promise<unknown> | unknown
  stopSession?: (sessionId: string) => Promise<unknown> | unknown
}

declare global {
  interface Window {
    /** Optional Electron/Tauri companion bridge for desktop recording. */
    orrerySkillRecorder?: RecorderBridge
  }
}

const sideEffectPattern = /提交|发送|删除|付款|支付|发布|修改|导出|创建|覆盖|转发/i

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function textValue(value: unknown): string | undefined {
  if (typeof value !== 'string' && typeof value !== 'number') return undefined
  const text = String(value).replace(/\s+/g, ' ').trim()
  return text || undefined
}

function makeId(prefix: string) {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return `${prefix}-${crypto.randomUUID()}`
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function sanitizeText(value: unknown, maxLength = 180) {
  return textValue(value)?.slice(0, maxLength)
}

function sanitizeUrl(value: unknown) {
  const raw = textValue(value)
  if (!raw) return undefined
  try {
    const parsed = new URL(raw)
    return `${parsed.origin}${parsed.pathname}`
  } catch {
    return raw.slice(0, 220)
  }
}

function timestampValue(value: unknown, fallback: Date) {
  if (typeof value === 'number') {
    const date = new Date(value < 100000000000 ? value * 1000 : value)
    if (!Number.isNaN(date.getTime())) return date.toISOString()
  }
  if (typeof value === 'string') {
    const date = new Date(value)
    if (!Number.isNaN(date.getTime())) return date.toISOString()
  }
  return fallback.toISOString()
}

function normalizeKind(value: unknown, source: JsonObject): RecorderEventKind {
  const kind = textValue(value)?.toLowerCase() ?? ''
  if (kind.includes('clipboard') || 'clipboard' in source) return 'clipboard.change'
  if (kind.includes('narrat') || kind.includes('voice') || 'narration' in source) return 'narration'
  if (kind.includes('url') || kind.includes('navigation') || 'url' in source || 'href' in source) return 'browser.url'
  if (kind.includes('title')) return 'app.title-change'
  if (kind.includes('activate') || kind.includes('focus')) return 'app.activate'
  if (kind.includes('frame') || kind.includes('screenshot')) return 'frame'
  return 'action'
}

function normalizeEvent(input: unknown, index: number): RecorderEvent | undefined {
  if (!isRecord(input)) return undefined
  const payload = isRecord(input.payload) ? input.payload : {}
  const source = { ...payload, ...input }
  const kind = normalizeKind(source.kind ?? source.type ?? source.eventType, source)
  const timestamp = timestampValue(source.timestamp ?? source.time ?? source.ts ?? source.at, new Date(Date.now() + index))
  const app = sanitizeText(source.app ?? source.application ?? source.applicationName ?? source.process)
  const title = sanitizeText(source.title ?? source.windowTitle ?? source.pageTitle)
  const url = sanitizeUrl(source.url ?? source.href ?? source.location)
  const detail = sanitizeText(source.detail ?? source.description ?? source.label ?? source.name)
  const text = sanitizeText(source.text ?? source.transcript ?? source.narration ?? source.value)

  if (kind === 'clipboard.change') {
    return {
      id: textValue(source.id) ?? `event-${index + 1}`,
      timestamp,
      kind,
      app,
      title,
      detail: '剪贴板内容已脱敏，未进入技能草稿',
      text: '[已脱敏]',
      redacted: true
    }
  }

  return {
    id: textValue(source.id) ?? `event-${index + 1}`,
    timestamp,
    kind,
    app,
    title,
    url,
    detail,
    text
  }
}

function eventCandidates(payload: unknown): unknown[] {
  if (Array.isArray(payload)) return payload
  if (!isRecord(payload)) return []
  if (Array.isArray(payload.events)) return payload.events
  if (Array.isArray(payload.timeline)) return payload.timeline
  if (Array.isArray(payload.eventLog)) return payload.eventLog
  if (isRecord(payload.recording)) return eventCandidates(payload.recording)
  if (isRecord(payload.session)) return eventCandidates(payload.session)
  return []
}

function sessionFromPayload(payload: unknown, source: RecorderSource, fallbackName: string, allowEmpty = false): RecordingSession {
  const root = isRecord(payload) ? payload : {}
  const metadata = isRecord(root.session) ? root.session : isRecord(root.recording) ? root.recording : root
  const events = eventCandidates(payload)
    .map((event, index) => normalizeEvent(event, index))
    .filter((event): event is RecorderEvent => Boolean(event))

  if (!allowEmpty && events.length === 0) {
    throw new Error('没有识别到可用的录制事件，请导入包含 events 或 timeline 的 JSON')
  }

  const startedAt = timestampValue(metadata.startedAt ?? metadata.startTime, new Date())
  const endedAtValue = metadata.endedAt ?? metadata.endTime
  return {
    id: textValue(metadata.id ?? metadata.sessionId) ?? makeId('recording'),
    name: textValue(metadata.name ?? metadata.title) ?? fallbackName,
    source,
    status: 'ready',
    startedAt,
    endedAt: endedAtValue ? timestampValue(endedAtValue, new Date()) : undefined,
    events,
    privacy: {
      screenshots: Boolean(metadata.captureScreenshots ?? metadata.screenshots ?? true),
      clipboard: Boolean(metadata.captureClipboard ?? metadata.clipboard ?? true),
      voice: Boolean(metadata.captureVoice ?? metadata.voice ?? false)
    }
  }
}

export function createDemoRecording(): RecordingSession {
  const started = Date.now() - 420000
  const event = (offset: number, payload: Omit<RecorderEvent, 'id' | 'timestamp'>): RecorderEvent => ({
    ...payload,
    id: `demo-event-${offset}`,
    timestamp: new Date(started + offset * 1000).toISOString()
  })

  return {
    id: 'demo-recording-customer-export',
    name: '客户信息整理流程',
    source: 'demo',
    status: 'ready',
    startedAt: new Date(started).toISOString(),
    endedAt: new Date(started + 260000).toISOString(),
    events: [
      event(0, { kind: 'app.activate', app: 'Google Chrome', title: 'CRM 客户列表' }),
      event(4, { kind: 'browser.url', app: 'Google Chrome', url: 'https://crm.example.com/customers', detail: '进入客户列表' }),
      event(22, { kind: 'narration', text: '筛选高价值客户，打开第一条记录并导出客户信息' }),
      event(74, { kind: 'app.title-change', app: 'Google Chrome', title: '客户详情 - Demo Account', detail: '打开第一条客户记录' }),
      event(112, { kind: 'action', app: 'Google Chrome', detail: '导出客户信息', text: '导出为 CSV' }),
      event(146, { kind: 'clipboard.change', app: 'Google Chrome', redacted: true })
    ],
    privacy: { screenshots: true, clipboard: true, voice: true }
  }
}

export async function importRecordingFile(file: File) {
  let payload: unknown
  try {
    payload = JSON.parse(await file.text())
  } catch {
    throw new Error('录制文件必须是 JSON 格式')
  }
  return sessionFromPayload(payload, 'imported', file.name.replace(/\.json$/i, '') || '导入的录制会话')
}

function getBridge() {
  return typeof window === 'undefined' ? undefined : window.orrerySkillRecorder
}

function withTimeout<T>(promise: Promise<T>, timeoutMs: number) {
  return Promise.race([
    promise,
    new Promise<T>((_, reject) => window.setTimeout(() => reject(new Error('录制器桥接响应超时')), timeoutMs))
  ])
}

export async function probeSkillRecorder() {
  const bridge = getBridge()
  if (!bridge) return { available: false, label: '未连接', detail: '等待本地录制器桥接' }
  if (bridge.probe) {
    try {
      await withTimeout(Promise.resolve(bridge.probe()), 1800)
      return { available: true, label: '已连接', detail: '本地录制器可用' }
    } catch {
      return { available: false, label: '连接失败', detail: '桥接未响应' }
    }
  }
  const available = Boolean(bridge.startSession && bridge.stopSession)
  return {
    available,
    label: available ? '已连接' : '配置不完整',
    detail: available ? '本地录制器可用' : '缺少 startSession 或 stopSession'
  }
}

export async function startNativeRecording(name: string) {
  const bridge = getBridge()
  if (!bridge?.startSession) throw new Error('未检测到本地录制器桥接')
  const response = await withTimeout(Promise.resolve(bridge.startSession({ name, captureClipboard: true, captureVoice: true })), 1800)
  const session = sessionFromPayload(response, 'native', name, true)
  return { ...session, status: 'recording' as const }
}

export async function stopNativeRecording(sessionId: string) {
  const bridge = getBridge()
  if (!bridge?.stopSession) throw new Error('未检测到本地录制器桥接')
  const response = await withTimeout(Promise.resolve(bridge.stopSession(sessionId)), 5000)
  return sessionFromPayload(response, 'native', '本地录制会话')
}

function eventContent(event: RecorderEvent) {
  return [event.detail, event.text, event.title, event.url].filter(Boolean).join(' ')
}

function stepFromEvent(event: RecorderEvent, index: number): SkillRecorderStep {
  const content = eventContent(event)
  let intent = content || '观察当前工作状态'
  let recommendedTool = 'agent.interpret'

  switch (event.kind) {
    case 'app.activate':
      intent = `切换到 ${event.app ?? event.title ?? '目标应用'}`
      recommendedTool = 'desktop.activate'
      break
    case 'app.title-change':
      intent = `确认当前页面或窗口为 ${event.title ?? '目标页面'}`
      recommendedTool = 'browser.assert'
      break
    case 'browser.url':
      intent = `打开页面 ${event.url ?? '录制中的页面'}`
      recommendedTool = 'browser.navigate'
      break
    case 'clipboard.change':
      intent = '读取经过脱敏的中间数据'
      recommendedTool = 'data.read'
      break
    case 'narration':
      intent = event.text ?? event.detail ?? '理解操作者意图'
      recommendedTool = 'agent.interpret'
      break
    case 'frame':
      intent = event.detail ?? '检查当前页面状态'
      recommendedTool = 'browser.inspect'
      break
    case 'action':
      intent = event.detail ?? event.text ?? '执行录制动作'
      recommendedTool = sideEffectPattern.test(intent) ? 'approval.request' : 'browser.fill'
      break
  }

  return {
    id: `step-${index + 1}`,
    intent,
    source: event.kind,
    recommendedTool,
    requiresApproval: sideEffectPattern.test(intent),
    evidence: [event.app, event.title, event.url].filter(Boolean).join(' · ') || undefined
  }
}

export function buildSkillDraft(session: RecordingSession, goal: string, toolOverrides: Record<string, string> = {}): SkillRecorderDraft {
  const name = goal.trim() || session.name || '录制流程技能'
  const steps = session.events.map(stepFromEvent).map((step) => ({
    ...step,
    recommendedTool: toolOverrides[step.id] ?? step.recommendedTool
  }))
  const version = '0.1.0'
  const description = `基于操作录制生成的流程技能，包含 ${steps.length} 个经过复核的步骤`
  const taskTypeTags = 'recorded, workflow, browser'
  const prompt = [
    `# ${name}`,
    '',
    '## 执行步骤',
    ...steps.flatMap((step, index) => [
      `${index + 1}. ${step.intent}`,
      `   - 工具：${step.recommendedTool}`,
      `   - 审批：${step.requiresApproval ? '需要' : '不需要'}`,
      ...(step.evidence ? [`   - 证据：${step.evidence}`] : [])
    ]),
    '',
    '## 安全边界',
    '- 剪贴板内容在进入技能前已脱敏。',
    '- 发送、删除、支付、发布、修改和导出等副作用动作必须请求运行时审批。',
    '- 页面状态与录制证据不一致时停止当前技能并返回人工确认。',
    '- MCP 工具不可用时不降级为坐标点击，改为请求重新规划。'
  ].join('\n')
  const form: SkillRecorderForm = { name, description, version, taskTypeTags, prompt }

  return {
    ...form,
    id: makeId('recorded-skill'),
    sourceSessionId: session.id,
    createdAt: new Date().toISOString(),
    steps,
    markdown: buildSkillMarkdown(form)
  }
}
