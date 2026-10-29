import { describe, expect, it } from 'vitest'
import {
  buildSkillDraft,
  createDemoRecording,
  importRecordingFile
} from './skillRecorder'

describe('skill recorder adapter', () => {
  it('builds a reviewable draft from the demo timeline', () => {
    const session = createDemoRecording()
    const draft = buildSkillDraft(session, '客户信息整理流程')

    expect(session.events).toHaveLength(6)
    expect(draft.steps).toHaveLength(6)
    expect(draft.steps.some((step) => step.recommendedTool === 'approval.request')).toBe(true)
    expect(draft.prompt).toContain('副作用动作必须请求运行时审批')
    expect(draft.markdown).toContain('task_type_tags:')
  })

  it('normalizes imported events and redacts sensitive URL and clipboard values', async () => {
    const content = JSON.stringify({
      session: { id: 'session-imported', name: '导入流程' },
      events: [
        { type: 'browser.url', timestamp: '2026-08-03T10:00:00Z', url: 'https://example.com/customers?token=secret' },
        { type: 'clipboard.change', value: 'customer-secret' },
        { type: 'narration', transcript: '导出客户信息' }
      ]
    })
    const file = new File([], 'skill-recorder.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', { value: async () => content })

    const session = await importRecordingFile(file)

    expect(session.id).toBe('session-imported')
    expect(session.events[0].url).toBe('https://example.com/customers')
    expect(session.events[1].text).toBe('[已脱敏]')
    expect(session.events[1].redacted).toBe(true)
    expect(session.events[2].text).toBe('导出客户信息')
  })
})
