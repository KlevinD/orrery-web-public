import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import McpServerDetailPanel from './McpServerDetailPanel.svelte'
import type { McpServerViewModel } from '#lib/types/view-models'

describe('McpServerDetailPanel', () => {
  it('renders MCP server details and LLM-facing tool schemas', () => {
    setLocale('zh-CN')

    render(McpServerDetailPanel, {
      props: {
        server: {
          id: 'mcp.github',
          name: 'GitHub MCP',
          description: 'GitHub issue tools',
          kind: 'mcp',
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
          command: '',
          url: 'https://mcp.example.test/mcp',
          tags: ['mcp'],
          permissions: [],
          secretCount: 0,
          usageCount: 0,
          boundAgentCount: 0,
          boundAgents: [],
          bindings: [],
          updatedAt: 'Jun 09, 2026',
          serverName: 'github',
          transport: 'streamable_http',
          endpoint: 'https://mcp.example.test/mcp',
          headers: {},
          authRequired: false,
          authMode: '',
          authHeaderName: 'Authorization',
          defaultAKConfigured: false,
          availabilityStatus: '',
          args: [],
          exposedTools: ['search_issues', 'create_issue'],
          exposedToolDetails: [{
            name: 'search_issues',
            description: 'Search issues by query.',
            inputSchema: {
              type: 'object',
              properties: {
                query: { type: 'string' }
              }
            },
            outputSchema: { type: 'object' }
          }],
          healthPolicy: 'startup'
        } satisfies McpServerViewModel
      }
    })

    expect(screen.getByText('GitHub MCP')).toBeTruthy()
    expect(screen.getByText('mcp.github')).toBeTruthy()
    expect(screen.getByText('search_issues')).toBeTruthy()
    expect(screen.getByText('create_issue')).toBeTruthy()
    expect(screen.getByText('Search issues by query.')).toBeTruthy()
    expect(screen.getByText(/"query"/)).toBeTruthy()
  })
})
