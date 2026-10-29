import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import McpServerDeleteConfirmDialog from './McpServerDeleteConfirmDialog.svelte'
import type { McpServerViewModel } from '#lib/types/view-models'

describe('McpServerDeleteConfirmDialog', () => {
  it('warns that deleting a bound MCP server removes Agent tool bindings and AKs', async () => {
    const onconfirm = vi.fn()
    const oncancel = vi.fn()
    setLocale('en-US')

    render(McpServerDeleteConfirmDialog, {
      props: {
        open: true,
        server: mcpServerFixture(),
        onconfirm,
        oncancel
      }
    })

    expect(screen.getByRole('dialog', { name: 'Delete MCP server?' })).toBeTruthy()
    expect(screen.getByText('Deleting Private MCP will remove 2 Agent tool bindings and related AK records.')).toBeTruthy()
    expect(screen.getByText('Ops Agent')).toBeTruthy()
    expect(screen.getByText('Research Agent')).toBeTruthy()
    expect(screen.getByText('Bear****9999')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(oncancel).toHaveBeenCalledOnce()

    await fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    expect(onconfirm).toHaveBeenCalledWith(expect.objectContaining({ id: 'mcp.private' }))
  })
})

function mcpServerFixture(): McpServerViewModel {
  return {
    id: 'mcp.private',
    name: 'Private MCP',
    description: 'Private tools',
    kind: 'mcp_server',
    category: 'mcp',
    version: '1.0.0',
    enabled: true,
    riskLevel: 'medium',
    riskTone: 'warning',
    statusLabel: 'Requires auth',
    statusTone: 'warning',
    source: 'Not provided',
    serverToolId: '',
    downstreamToolName: '',
    manageable: true,
    command: '',
    url: 'https://mcp.example.test/api',
    tags: ['mcp'],
    permissions: [],
    secretCount: 0,
    usageCount: 0,
    boundAgentCount: 2,
    boundAgents: ['Ops Agent', 'Research Agent'],
    bindings: [
      {
        agentId: 'agent.ops',
        label: 'Ops Agent',
        alias: 'ops',
        scope: 'mission',
        mcpAuthRequired: true,
        defaultAKConfigured: false,
        agentAKConfigured: true,
        agentAKMasked: 'Bear****9999',
        authHeaderName: 'Authorization'
      },
      {
        agentId: 'agent.research',
        label: 'Research Agent',
        alias: 'research',
        scope: 'workspace',
        mcpAuthRequired: true,
        defaultAKConfigured: false,
        agentAKConfigured: false,
        agentAKMasked: '',
        authHeaderName: 'Authorization'
      }
    ],
    updatedAt: 'Jun 01, 2026',
    serverName: 'private',
    transport: 'streamable_http',
    endpoint: 'https://mcp.example.test/api',
    headers: {},
    authRequired: true,
    authMode: 'per_agent',
    authHeaderName: 'Authorization',
    defaultAKConfigured: false,
    availabilityStatus: 'requires_auth',
    args: [],
    exposedTools: ['search'],
    exposedToolDetails: [],
    healthPolicy: 'startup'
  }
}
