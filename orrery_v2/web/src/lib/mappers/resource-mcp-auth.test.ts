import { describe, expect, it } from 'vitest'
import { mapMcpServerCommand } from './resource'

describe('MCP resource auth mapping', () => {
  it('sends the selected auth header name with a default MCP AK', () => {
    const command = mapMcpServerCommand({
      toolId: '',
      configJson: '',
      name: 'Private MCP',
      description: '',
      version: '',
      serverName: 'private',
      transport: 'streamable_http',
      command: '',
      url: 'https://mcp.example.test/api',
      headers: '{ "X-Trace": "trace-1" }',
      authHeaderName: 'X-API-Key',
      defaultAK: ' sk_default ',
      args: '',
      exposedTools: '',
      tags: ''
    })

    expect(command.headers).toEqual({
      'X-Trace': 'trace-1',
      'X-API-Key': 'sk_default'
    })
    expect(command.auth_header_name).toBe('X-API-Key')
  })
})
