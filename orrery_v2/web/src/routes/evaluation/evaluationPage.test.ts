import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('evaluation page', () => {
  it('uses the compact scorecard layout from the evaluation design', () => {
    const source = readFileSync('src/routes/evaluation/+page.svelte', 'utf-8')

    expect(source).toContain('lg:grid-cols-[240px_minmax(0,1fr)]')
    expect(source).toContain('max-w-[760px]')
    expect(source).toContain("placeholder={t('operations.evaluation.searchAgent')}")
    expect(source).toContain('综合评分')
    expect(source).toContain('任务量')
    expect(source).toContain('质量分')
    expect(source).toContain('稳定性')
    expect(source).toContain('综合评估')
    expect(source).toContain('核心能力')
    expect(source).toContain('能力雷达')
    expect(source).toContain('关联服务')
    expect(source).toContain('近期任务记录')
    expect(source).toContain('暂无任务运行记录')
    expect(source).toContain('AgentMemorySheet')
    expect(source).toContain('查看记忆')
    expect(source).toContain('border-brand/20 bg-brand/10')
    expect(source).toContain('bg-brand/[0.035]')
    expect(source).toContain('h-full rounded-full bg-brand')
    expect(source).not.toContain('rounded-lg bg-foreground px-3')
  })

  it('uses only production workforce and evaluation projections', () => {
    const source = readFileSync('src/routes/evaluation/+page.svelte', 'utf-8')

    expect(source).toContain('useWorkforceMembersQuery')
    expect(source).toContain('useEvaluationListQuery')
    expect(source).toContain('evaluationByEntityId')
    expect(source).toContain('evaluationForMember')
    expect(source).toContain('const evaluation = evaluationForMember(member, evaluationByEntityId)')
    expect(source).not.toContain('temporaryEvaluation')
    expect(source).not.toContain('createTemporaryEvaluationMock')
  })

  it('uses the selected Agent projection throughout the detail view', () => {
    const source = readFileSync('src/routes/evaluation/+page.svelte', 'utf-8')

    expect(source).toContain('selectedAgent.summary')
    expect(source).toContain('selectedAgent.score')
    expect(source).toContain('selectedAgent.taskCount')
    expect(source).toContain('selectedAgent.quality')
    expect(source).toContain('selectedAgent.stability')
    expect(source).toContain('selectedAgent.evaluation?.taskHistory')
    expect(source).toContain('radarPoints')
    expect(source).toContain('<svg')
  })

  it('does not present missing investment data as measured zeroes', () => {
    const source = readFileSync('src/routes/evaluation/+page.svelte', 'utf-8')

    expect(source).toContain("if (value === undefined || !Number.isFinite(value)) return '--'")
    expect(source).not.toContain('Math.max(0, value ?? 0)')
  })
})
