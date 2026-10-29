import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { ToolViewModel } from '#lib/types/view-models'
import ToolCallDialog from './ToolCallDialog.svelte'

describe('ToolCallDialog', () => {
  it('renders MCP input schema fields and submits parsed arguments', async () => {
    setLocale('en-US')
    const onsubmit = vi.fn()

    render(ToolCallDialog, {
      props: {
        open: true,
        tool: toolFixture(),
        onsubmit
      }
    })

    await fireEvent.input(screen.getByLabelText('query'), { target: { value: 'is:open' } })
    await fireEvent.input(screen.getByLabelText('limit'), { target: { value: '25' } })
    await fireEvent.change(screen.getByLabelText('exact'), { target: { value: 'true' } })
    await fireEvent.input(screen.getByLabelText('labels'), { target: { value: '["bug","frontend"]' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Call' }))

    expect(onsubmit).toHaveBeenCalledWith({
      query: 'is:open',
      limit: 25,
      exact: true,
      labels: ['bug', 'frontend']
    })
  })

  it('renders raw JSON arguments editor when a tool has no input schema', async () => {
    setLocale('en-US')
    const onsubmit = vi.fn()

    render(ToolCallDialog, {
      props: {
        open: true,
        tool: { ...toolFixture(), inputSchema: undefined },
        onsubmit
      }
    })

    await fireEvent.input(screen.getByLabelText('Arguments JSON'), { target: { value: '{"issue_id":"IDE-1","dry_run":true}' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Call' }))

    expect(onsubmit).toHaveBeenCalledWith({
      issue_id: 'IDE-1',
      dry_run: true
    })
  })

  it('submits non-string enum selections with their schema value type', async () => {
    setLocale('en-US')
    const onsubmit = vi.fn()

    render(ToolCallDialog, {
      props: {
        open: true,
        tool: {
          ...toolFixture(),
          inputSchema: {
            type: 'object',
            properties: {
              priority: { type: 'integer', enum: [1, 2] }
            }
          }
        },
        onsubmit
      }
    })

    await fireEvent.change(screen.getByLabelText('priority'), { target: { value: '2' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Call' }))

    expect(onsubmit).toHaveBeenCalledWith({ priority: 2 })
  })
})

function toolFixture(): ToolViewModel {
  return {
    id: 'mcp.github.search_issues',
    name: 'Search issues',
    description: 'Search GitHub issues',
    kind: 'mcp_tool',
    category: 'mcp',
    version: '1.0.0',
    inputSchema: {
      type: 'object',
      required: ['query'],
      properties: {
        query: { type: 'string', description: 'Search query' },
        limit: { type: 'integer', default: 10 },
        exact: { type: 'boolean', default: false },
        labels: { type: 'array', items: { type: 'string' } }
      }
    },
    enabled: true,
    riskLevel: 'low',
    riskTone: 'success',
    statusLabel: 'Enabled',
    statusTone: 'success',
    source: 'mcp_gateway',
    serverToolId: 'mcp.github',
    downstreamToolName: 'search_issues',
    manageable: false,
    command: '',
    url: '',
    tags: [],
    permissions: ['github.read'],
    secretCount: 0,
    usageCount: 0,
    boundAgentCount: 0,
    boundAgents: [],
    bindings: [],
    updatedAt: 'Jun 01, 2026'
  }
}
