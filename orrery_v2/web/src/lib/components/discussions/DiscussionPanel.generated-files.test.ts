import { render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import { clearSession } from '#lib/stores/session.svelte'
import DiscussionPanel from './DiscussionPanel.svelte'
import { discussion } from './DiscussionPanel.fixtures'

describe('DiscussionPanel generated files', () => {
  beforeEach(() => {
    setLocale('en-US')
    clearSession()
  })

  it('renders an agent-generated workspace file as a download link on its message', () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          status: 'completed',
          runtimeStatus: 'completed',
          messages: [{
            id: 'msg-generated',
            runId: 'run-1',
            role: 'assistant',
            authorId: 'lead-agent',
            authorName: 'Lead Agent',
            content: 'The launch report is ready.',
            createdAt: 'Jun 02, 09:10',
            attachments: [{
              id: 'att-generated',
              discussionId: 'conv-1',
              messageId: 'msg-generated',
              fileName: 'reports/launch.csv',
              contentType: 'text/csv',
              sizeBytes: 1024,
              uploadedBy: 'lead-agent',
              uploadedByKind: 'agent',
              uploadedByName: 'Lead Agent',
              storagePath: '',
              downloadUrl: '/api/discussions/conv-1/attachments/att-generated/download',
              createdAt: 'Jun 02, 09:10'
            }]
          }]
        })
      }
    })

    const link = screen.getByRole('link', { name: 'Download attachment reports/launch.csv' })
    expect(link.getAttribute('href')).toBe('/api/discussions/conv-1/attachments/att-generated/download')
    expect(link.hasAttribute('download')).toBe(true)
    expect(screen.getByText('1.0 KB')).toBeTruthy()
  })
})
