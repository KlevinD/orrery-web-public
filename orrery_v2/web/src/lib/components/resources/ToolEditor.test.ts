import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import ToolEditor from './ToolEditor.svelte'
import type { ToolViewModel } from '#lib/types/view-models'

describe('ToolEditor', () => {
  it('edits existing tools and exposes lifecycle actions', async () => {
    setLocale('en-US')
    const onsubmit = vi.fn()
    const ondisable = vi.fn()
    const ondelete = vi.fn()
    render(ToolEditor, {
      props: {
        tool: toolFixture(),
        onsubmit,
        ondisable,
        ondelete
      }
    })

    expect(screen.getByText('Tool Editor')).toBeTruthy()
    expect(screen.getByText('tool.deploy')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Update' }))

    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      toolId: 'tool.deploy',
      name: 'Deploy',
      kind: 'cli_tool',
      command: 'deploy'
    }))
    await fireEvent.click(screen.getByRole('button', { name: 'Disable' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(ondisable).toHaveBeenCalledWith(expect.objectContaining({ id: 'tool.deploy' }))
    expect(ondelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'tool.deploy' }))
    expect(screen.queryByRole('button', { name: 'Register' })).toBeNull()
  })

  it('registers a new tool when no existing tool is selected', async () => {
    setLocale('en-US')
    const onsubmit = vi.fn()
    render(ToolEditor, { props: { onsubmit } })

    expect(screen.getByText('Tool Editor')).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Register' }) as HTMLButtonElement).disabled).toBe(true)
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Deploy' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Register' }))

    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      toolId: '',
      name: 'Deploy',
      kind: 'internal_service_tool'
    }))
    expect(screen.queryByRole('button', { name: 'Disable' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  })
})

function toolFixture(): ToolViewModel {
  return {
    id: 'tool.deploy',
    name: 'Deploy',
    description: 'Deploy service',
    kind: 'cli_tool',
    category: 'operations',
    version: '1.0.0',
    enabled: true,
    riskLevel: 'medium',
    riskTone: 'warning',
    statusLabel: 'Enabled',
    statusTone: 'success',
    source: 'internal',
    serverToolId: '',
    downstreamToolName: '',
    manageable: true,
    command: 'deploy',
    url: '',
    tags: ['operations'],
    permissions: [],
    secretCount: 0,
    usageCount: 0,
    boundAgentCount: 0,
    boundAgents: [],
    bindings: [],
    updatedAt: 'Jun 01, 2026'
  }
}
