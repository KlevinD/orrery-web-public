import { render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it } from 'vitest'
import ApprovalMaterial from './ApprovalMaterial.svelte'
import { setLocale } from '#lib/i18n'

describe('ApprovalMaterial', () => {
  beforeEach(() => setLocale('zh-CN'))

  it('renders business approval material as readable Chinese sections', () => {
    render(ApprovalMaterial, { props: {
      material: { kind: 'business_approval', language: 'zh-CN', title: '公众号文章发布审批', content: '', fields: [] },
      businessMaterial: {
        language: 'zh-CN', title: '公众号文章发布审批', decisionRequest: '是否批准发布本期公众号文章？',
        businessContext: '本周产品更新需要对外同步。', proposedAction: '使用主账号发布已完成校对的文章。',
        expectedResult: '用户能在公众号看到本期更新。', impactScope: '公众号主账号及其订阅用户。',
        risks: [{ description: '内容表述可能产生歧义。', mitigation: '发布前由市场负责人完成终审。' }]
      },
      evidenceRefs: ['knowledge:campaign-brief'],
      artifacts: [{ label: '公众号终稿', url: 'https://example.test/final', kind: 'document' }],
      readableOnly: true
    } })

    expect(screen.getByText('需要您决定')).toBeTruthy()
    expect(screen.getByText('是否批准发布本期公众号文章？')).toBeTruthy()
    expect(screen.getByText('业务背景')).toBeTruthy()
    expect(screen.getByText('拟执行动作')).toBeTruthy()
    expect(screen.getByText('预期结果')).toBeTruthy()
    expect(screen.getByText('影响范围')).toBeTruthy()
    expect(screen.getByText('风险与应对')).toBeTruthy()
    expect(screen.getByText('发布前由市场负责人完成终审。')).toBeTruthy()
    expect(screen.getByRole('link', { name: '公众号终稿' }).getAttribute('href')).toBe('https://example.test/final')
    expect(screen.queryByText('knowledge:campaign-brief')).toBeNull()
  })

  it('renders workflow content as readable markdown without raw payload fields', () => {
    render(ApprovalMaterial, { props: {
      material: {
        kind: 'wechat_draft', language: 'markdown', title: 'Reviewed title',
        content: '# Reviewed article\n\nThis is the content awaiting approval.',
        fields: [
          { key: 'approval_request_id', value: 'call_123' },
          { key: 'material_hash', value: 'sha256:material' },
          { key: 'account_id', value: 'wechat-main' }
        ]
      },
      summary: 'A concise approval summary.',
      evidenceRefs: ['knowledge:internal-reference'],
      readableOnly: true
    } })

    expect(screen.getByText('审批内容')).toBeTruthy()
    expect(screen.getByText('A concise approval summary.')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Reviewed article' })).toBeTruthy()
    expect(screen.getByText('This is the content awaiting approval.')).toBeTruthy()
    expect(screen.queryByText('approval_request_id')).toBeNull()
    expect(screen.queryByText('sha256:material')).toBeNull()
    expect(screen.queryByText('knowledge:internal-reference')).toBeNull()
    expect(screen.queryByText('markdown')).toBeNull()
  })

  it('keeps raw payload fields available outside the workflow review surface', () => {
    render(ApprovalMaterial, { props: {
      material: {
        kind: 'command_execution', language: 'shell', title: 'Command', content: 'npm test',
        fields: [{ key: 'working_directory', value: '/workspace' }]
      }
    } })

    expect(screen.getByText('working_directory')).toBeTruthy()
    expect(screen.getByText('/workspace')).toBeTruthy()
  })

  it('does not repeat a summary that became the readable content fallback', () => {
    render(ApprovalMaterial, { props: {
      material: {
        kind: 'tool_use', language: 'text', title: 'Approval',
        content: '批准执行客户续约风险评估。', fields: []
      },
      summary: '批准执行客户续约风险评估。',
      readableOnly: true
    } })

    expect(screen.getAllByText('批准执行客户续约风险评估。')).toHaveLength(1)
    expect(document.querySelector('[data-approval-summary]')).toBeNull()
  })

  it('shows only natural request context and hides runtime plumbing fields', () => {
    render(ApprovalMaterial, { props: {
      material: {
        kind: 'tool_use', language: 'text', title: '发布任务复核', content: '内容发布复核',
        fields: [
          { key: 'request_context', value: '查找适合复核本期发布内容的人才。' },
          { key: 'query', value: '内容发布复核' },
          { key: 'approval_request_id', value: 'call_secret_12345678' },
          { key: 'material_hash', value: 'sha256:material' }
        ]
      },
      summary: 'Runtime approval required for discussion_talent_list',
      readableOnly: true
    } })

    expect(screen.getByText('内容发布复核')).toBeTruthy()
    expect(screen.getByText('原始请求')).toBeTruthy()
    expect(screen.getByText('查找适合复核本期发布内容的人才。')).toBeTruthy()
    expect(screen.queryByText('Runtime approval required for discussion_talent_list')).toBeNull()
    expect(screen.queryByText('approval_request_id')).toBeNull()
    expect(screen.queryByText('sha256:material')).toBeNull()
  })

  it('fails closed when a request has no readable approval content', () => {
    render(ApprovalMaterial, { props: {
      material: {
        kind: 'hil_decision', language: 'json', title: 'Decision context', content: '',
        fields: [{ key: 'approval_request_id', value: 'call_secret_12345678' }]
      },
      summary: 'Runtime approval required for unknown_tool',
      readableOnly: true
    } })

    expect(screen.getByRole('alert').textContent).toContain('未提供可读的审批内容')
    expect(screen.queryByText('approval_request_id')).toBeNull()
  })
})
