import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('ScheduledRunInbox', () => {
  const source = readFileSync('src/routes/scheduled-tasks/ScheduledRunInbox.svelte', 'utf-8')
  const globalStyles = readFileSync('src/app.css', 'utf-8')

  it('uses an email-style split view with a mobile list-detail flow', () => {
    expect(source).toContain('lg:grid-cols-[340px_minmax(0,1fr)]')
    expect(source).toContain('lg:grid-rows-[minmax(0,1fr)]')
    expect(source.match(/class="min-h-0 min-w-0/g)).toHaveLength(2)
    expect(source).toContain('data-scheduled-run-list')
    expect(source).toContain('data-scheduled-run-reading-pane')
    expect(source).toContain('showMobileDetail')
    expect(source).toContain('ariaLabel="返回结果列表"')
    expect(source).toContain('min-h-[620px]')
  })

  it('supports unread and attention filters backed by personal server state', () => {
    expect(source).toContain("type InboxFilter = 'all' | 'unread' | 'attention'")
    expect(source).toContain("readState: exactRunID || filter !== 'unread' ? '' : 'unread'")
    expect(source).toContain("attention: !exactRunID && filter === 'attention'")
    expect(source).toContain('commands.setRunReadState.mutate')
    expect(source).toContain('requestedReadRunID')
    expect(source).not.toContain('localStorage')
  })

  it('renders readable report content, artifacts, and related work links', () => {
    expect(source).toContain('renderSafeMarkdown(selectedItem.run.result.summary)')
    expect(source).toContain('report-body mt-4 max-w-[82ch] text-[15px] leading-7')
    expect(globalStyles).toContain('.report-body table')
    expect(source).toContain('data-scheduled-run-summary')
    expect(source).toContain('<ScheduledRunArtifacts')
    expect(source).toContain('Mission')
    expect(source).toContain('Responsibility')
    expect(source).toContain('在本体中查看')
  })
})
