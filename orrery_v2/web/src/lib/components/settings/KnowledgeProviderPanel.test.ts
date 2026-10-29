import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import KnowledgeProviderPanel from './KnowledgeProviderPanel.svelte'

describe('KnowledgeProviderPanel', () => {
  it('creates, tests, and synchronizes bindings using registered MCP servers', async () => {
    setLocale('zh-CN')
    const oncreate = vi.fn()
    const ontest = vi.fn()
    const onsync = vi.fn()

    render(KnowledgeProviderPanel, {
      props: {
        providers: [{
          id: 'binding-1',
          name: 'Minvia PageIndex',
          mcp_server_id: 'mcp.pageindex',
          mode: 'read_only',
          status: 'active',
          tool_mapping: {},
          folder_include: ['minvia-core'],
          folder_exclude: ['MinviaBrain']
        }],
        servers: [{ id: 'mcp.pageindex', name: 'PageIndex', enabled: true, exposedTools: ['list_folders', 'search_documents'] }],
        oncreate,
        ontest,
        onsync
      }
    })

    expect(screen.getByText('Minvia PageIndex')).toBeTruthy()
    expect(screen.getByText('PageIndex')).toBeTruthy()
    expect(screen.getByText('list_folders')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: '测试 Minvia PageIndex' }))
    expect(ontest).toHaveBeenCalledWith('binding-1')

    await fireEvent.click(screen.getByRole('button', { name: '同步 Minvia PageIndex' }))
    expect(onsync).toHaveBeenCalledWith('binding-1', 'minvia')

    await fireEvent.input(screen.getByLabelText('连接名称'), { target: { value: 'New PageIndex' } })
    await fireEvent.click(screen.getByRole('button', { name: '添加知识库连接' }))
    expect(oncreate).toHaveBeenCalledWith(expect.objectContaining({
      name: 'New PageIndex',
      mcp_server_id: 'mcp.pageindex',
      mode: 'read_only',
      folder_exclude: ['MinviaBrain']
    }))
  })
})
