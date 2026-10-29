import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import McpServerEditor from './McpServerEditor.svelte'
import type { McpServerViewModel } from '#lib/types/view-models'

describe('McpServerEditor', () => {
  it('submits backend transport enum values for streamable HTTP servers', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(McpServerEditor, { props: { onsubmit } })

    expect(screen.queryByLabelText('Resource ID')).toBeNull()

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'HTTP MCP' } })
    await fireEvent.input(screen.getByLabelText('Server'), { target: { value: 'http-mcp' } })
    await fireEvent.change(screen.getByLabelText('Transport'), { target: { value: 'streamable_http' } })
    await fireEvent.input(screen.getByLabelText('Endpoint'), { target: { value: 'https://mcp.example.test/stream' } })
    await fireEvent.input(screen.getByLabelText('Headers'), { target: { value: '{ "Authorization": "Bearer sk_test" }' } })

    await fireEvent.click(screen.getByRole('button', { name: 'Register' }))

    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      transport: 'streamable_http',
      url: 'https://mcp.example.test/stream',
      headers: '{ "Authorization": "Bearer sk_test" }'
    }))
  })

  it('submits a default AK through the selected auth header without exposing it in raw headers JSON', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(McpServerEditor, { props: { onsubmit } })

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Private MCP' } })
    await fireEvent.input(screen.getByLabelText('Server'), { target: { value: 'private' } })
    await fireEvent.change(screen.getByLabelText('Transport'), { target: { value: 'streamable_http' } })
    await fireEvent.input(screen.getByLabelText('Endpoint'), { target: { value: 'https://mcp.example.test/api' } })
    await fireEvent.input(screen.getByLabelText('Default AK'), { target: { value: 'Bearer sk_default' } })

    await fireEvent.click(screen.getByRole('button', { name: 'Register' }))

    expect((screen.getByLabelText('Default AK') as HTMLInputElement).type).toBe('password')
    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      authHeaderName: 'Authorization',
      defaultAK: 'Bearer sk_default',
      headers: ''
    }))
  })

  it('submits pasted MCP JSON without requiring manual server fields', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(McpServerEditor, { props: { onsubmit } })

    const configJson = `{
  "mcpServers": {
    "github": {
      "command": "github-mcp-server",
      "env": {
        "GITHUB_TOKEN": "github-token-fixture-not-stored"
      }
    }
  }
}`
    await fireEvent.input(screen.getByLabelText('MCP JSON'), { target: { value: configJson } })
    await fireEvent.click(screen.getByRole('button', { name: 'Register' }))

    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      configJson,
      name: '',
      serverName: '',
      command: ''
    }))
  })

  it('shows discovery picker TODO instead of hiding the missing discovery route', () => {
    setLocale('en-US')
    render(McpServerEditor)

    expect(screen.getByLabelText('Discovery search')).toBeTruthy()
    expect(screen.queryByLabelText('Discovered ID')).toBeNull()
    expect(screen.queryByText('Discovery result picker is not connected yet.')).toBeNull()
  })

  it('selects an MCP discovery result and submits it through the regular MCP registration form', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(McpServerEditor, {
      props: {
        discoveryResults: [
          {
            id: 'linear',
            name: 'Linear MCP',
            description: 'Issue tracking tools.',
            version: '1.0.0',
            packageRefs: ['npm:@linear/mcp@1.0.0'],
            tools: ['issue.search'],
            secretEnvNames: ['LINEAR_API_KEY'],
            homepage: 'https://linear.app'
          }
        ],
        onsubmit
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Select discovery result Linear MCP' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Register discovered' }))

    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      toolId: '',
      name: 'Linear MCP',
      serverName: 'linear-mcp',
      transport: 'stdio',
      command: 'npx',
      args: '-y, @linear/mcp',
      exposedTools: 'issue.search',
      tags: 'mcp, discovered'
    }))
  })

  it('clears a selected MCP server so users can explicitly register a new server', async () => {
    setLocale('en-US')
    render(McpServerEditor, {
      props: {
        server: {
          id: 'mcp.github',
          name: 'GitHub MCP',
          description: 'GitHub tools',
          kind: 'mcp_server',
          category: 'mcp',
          version: '1.0.0',
          enabled: true,
          riskLevel: 'medium',
          riskTone: 'warning',
          statusLabel: 'Enabled',
          statusTone: 'success',
          source: 'Not provided',
          serverToolId: '',
          downstreamToolName: '',
          manageable: true,
          command: 'github-mcp-server',
          url: '',
          tags: ['mcp'],
          permissions: [],
          secretCount: 0,
          usageCount: 0,
          boundAgentCount: 0,
          boundAgents: [],
          bindings: [],
          updatedAt: 'Jun 01, 2026',
          serverName: 'github',
          transport: 'stdio',
          endpoint: '',
          headers: {},
          authRequired: false,
          authMode: '',
          authHeaderName: 'Authorization',
          defaultAKConfigured: false,
          availabilityStatus: '',
          args: [],
          exposedTools: ['search_issues'],
          exposedToolDetails: [],
          healthPolicy: 'startup'
        } satisfies McpServerViewModel
      }
    })

    expect(screen.queryByLabelText('Resource ID')).toBeNull()
    expect(screen.getByText('mcp.github')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Register new' }))

    expect(screen.queryByLabelText('Resource ID')).toBeNull()
    expect(screen.queryByText('mcp.github')).toBeNull()
    expect(screen.getByRole('button', { name: 'Register' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull()
  })

  it('emits delete for the selected MCP server', async () => {
    const ondelete = vi.fn()
    setLocale('en-US')
    render(McpServerEditor, {
      props: {
        server: mcpServerFixture(),
        ondelete
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }))

    expect(ondelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'mcp.github' }))
  })
})

function mcpServerFixture(): McpServerViewModel {
  return {
    id: 'mcp.github',
    name: 'GitHub MCP',
    description: 'GitHub tools',
    kind: 'mcp_server',
    category: 'mcp',
    version: '1.0.0',
    enabled: true,
    riskLevel: 'medium',
    riskTone: 'warning',
    statusLabel: 'Enabled',
    statusTone: 'success',
    source: 'Not provided',
    serverToolId: '',
    downstreamToolName: '',
    manageable: true,
    command: 'github-mcp-server',
    url: '',
    tags: ['mcp'],
    permissions: [],
    secretCount: 0,
    usageCount: 0,
    boundAgentCount: 0,
    boundAgents: [],
    bindings: [],
    updatedAt: 'Jun 01, 2026',
    serverName: 'github',
    transport: 'stdio',
    endpoint: '',
    headers: {},
    authRequired: false,
    authMode: '',
    authHeaderName: 'Authorization',
    defaultAKConfigured: false,
    availabilityStatus: '',
    args: [],
    exposedTools: ['search_issues'],
    exposedToolDetails: [],
    healthPolicy: 'startup'
  }
}
