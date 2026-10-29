import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import ResourceBindingPanel from './ResourceBindingPanel.svelte'

describe('ResourceBindingPanel', () => {
  it('requires an Agent AK before binding auth-required MCP servers without a default AK', async () => {
    const onbind = vi.fn()
    setLocale('en-US')

    render(ResourceBindingPanel, {
      props: {
        resource: authRequiredMcpResource(),
        resourceType: 'mcp_server',
        onbind
      }
    })

    expect(screen.getByText('Agent AK required')).toBeTruthy()
    expect(screen.getByText('Ops Agent')).toBeTruthy()
    expect(screen.getByText('X-API-Key')).toBeTruthy()
    expect(screen.getByText('Bear****9999')).toBeTruthy()

    const bindButton = screen.getByRole('button', { name: 'Bind' }) as HTMLButtonElement
    await fireEvent.input(screen.getByLabelText('Agent profile ID'), { target: { value: 'agent.research' } })

    expect(bindButton.disabled).toBe(true)

    await fireEvent.input(screen.getByLabelText('Agent AK'), { target: { value: 'Bearer sk_agent' } })
    await fireEvent.click(bindButton)

    expect(onbind).toHaveBeenCalledWith(expect.objectContaining({
      resourceId: 'mcp.private',
      resourceType: 'mcp_server',
      agentId: 'agent.research',
      agentAK: 'Bearer sk_agent',
      authHeaderName: 'Authorization'
    }))
  })

  it('binds auth-required MCP servers without per-Agent AK when a default AK is configured', async () => {
    const onbind = vi.fn()
    setLocale('en-US')

    render(ResourceBindingPanel, {
      props: {
        resource: authRequiredMcpResource({ defaultAKConfigured: true, bindings: [] }),
        resourceType: 'mcp_server',
        onbind
      }
    })

    expect(screen.getByText('Default AK configured')).toBeTruthy()
    expect(screen.queryByLabelText('Agent AK')).toBeNull()

    await fireEvent.input(screen.getByLabelText('Agent profile ID'), { target: { value: 'agent.shared' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Bind' }))

    expect(onbind).toHaveBeenCalledWith(expect.objectContaining({
      resourceId: 'mcp.private',
      resourceType: 'mcp_server',
      agentId: 'agent.shared'
    }))
    expect(onbind.mock.calls[0][0].agentAK).toBeUndefined()
  })

  it('shows the entered Agent existing AKs across MCP servers with their usage', async () => {
    setLocale('en-US')

    render(ResourceBindingPanel, {
      props: {
        resource: authRequiredMcpResource({ bindings: [] }),
        resourceType: 'mcp_server',
        relatedResources: [
          authRequiredMcpResource({
            name: 'Private MCP',
            bindings: [{
              agentId: 'agent.research',
              label: 'Research Agent',
              alias: 'private',
              scope: 'mission',
              mcpAuthRequired: true,
              defaultAKConfigured: false,
              agentAKConfigured: true,
              agentAKMasked: 'priv****0001',
              authHeaderName: 'Authorization'
            }]
          }),
          authRequiredMcpResource({
            id: 'mcp.analytics',
            name: 'Analytics MCP',
            bindings: [{
              agentId: 'agent.research',
              label: 'Research Agent',
              alias: 'analytics',
              scope: 'workspace',
              mcpAuthRequired: true,
              defaultAKConfigured: false,
              agentAKConfigured: true,
              agentAKMasked: 'anal****0002',
              authHeaderName: 'X-API-Key'
            }]
          }),
          authRequiredMcpResource({
            id: 'mcp.ops',
            name: 'Ops MCP',
            bindings: [{
              agentId: 'agent.ops',
              label: 'Ops Agent',
              alias: 'ops',
              scope: 'mission',
              mcpAuthRequired: true,
              defaultAKConfigured: false,
              agentAKConfigured: true,
              agentAKMasked: 'ops****0003',
              authHeaderName: 'Authorization'
            }]
          })
        ]
      }
    })

    await fireEvent.input(screen.getByLabelText('Agent profile ID'), { target: { value: 'agent.research' } })

    expect(screen.getByText('Private MCP · mission')).toBeTruthy()
    expect(screen.getByText('Analytics MCP · workspace')).toBeTruthy()
    expect(screen.getByText('anal****0002')).toBeTruthy()
    expect(screen.queryByText('Ops MCP · mission')).toBeNull()
  })
})

function authRequiredMcpResource(overrides: Record<string, unknown> = {}) {
  return {
    id: 'mcp.private',
    name: 'Private MCP',
    authRequired: true,
    authMode: 'per_agent',
    authHeaderName: 'Authorization',
    defaultAKConfigured: false,
    bindings: [
      {
        agentId: 'agent.ops',
        label: 'Ops Agent',
        alias: 'private-api',
        scope: 'mission',
        mcpAuthRequired: true,
        defaultAKConfigured: false,
        agentAKConfigured: true,
        agentAKMasked: 'Bear****9999',
        authHeaderName: 'X-API-Key'
      }
    ],
    ...overrides
  } as any
}
