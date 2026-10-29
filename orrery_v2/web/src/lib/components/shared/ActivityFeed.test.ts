import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import ActivityFeed from './ActivityFeed.svelte'

describe('ActivityFeed', () => {
  it('renders demo-matching event icons, mentioned users, and localized status metadata', () => {
    setLocale('zh-CN')

    const { container } = render(ActivityFeed, {
      props: {
        emptyLabel: 'No events',
        items: [
          {
            id: 'evt-1',
            type: 'mention',
            label: '@Mention',
            author: 'Planner',
            authorType: 'agent',
            body: 'Please review the evidence',
            time: 'Jun 02, 10:00',
            metadata: { mentionedUsers: ['Operator'] }
          },
          {
            id: 'evt-2',
            type: 'tool-call',
            label: 'Tool call',
            author: 'Runner',
            authorType: 'agent',
            body: 'Ran evidence collector',
            time: 'Jun 02, 10:01',
            metadata: { toolName: 'IAM API', oldStatus: 'active', newStatus: 'done' }
          }
        ]
      }
    })

    const mentionIcon = container.querySelector('[data-event-icon="mention"]')
    const toolIcon = container.querySelector('[data-event-icon="tool-call"]')

    expect(mentionIcon).toBeTruthy()
    expect(toolIcon).toBeTruthy()
    expect(mentionIcon?.getAttribute('class')).toContain('text-chart-1')
    expect(toolIcon?.getAttribute('class')).toContain('text-chart-4')
    expect(screen.getByText('Operator')).toBeTruthy()
    expect(screen.getByText('IAM API')).toBeTruthy()
    // Status labels are rendered as separate spans with ArrowRight icon between them
    expect(screen.getByText('进行中')).toBeTruthy()
    expect(screen.getByText('已完成')).toBeTruthy()
  })
})
