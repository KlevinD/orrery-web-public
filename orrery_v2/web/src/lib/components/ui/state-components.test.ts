import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import EmptyState from './EmptyState.svelte'
import ErrorState from './ErrorState.svelte'
import LoadingState from './LoadingState.svelte'
import PermissionDeniedState from './PermissionDeniedState.svelte'
import { setLocale } from '#lib/i18n'

describe('page state components', () => {
  it('renders loading state text', () => {
    render(LoadingState, { props: { title: 'Loading missions' } })

    expect(screen.getByText('Loading missions')).toBeTruthy()
  })

  it('renders empty state title and description', () => {
    render(EmptyState, { props: { title: 'No tools', description: 'Register a tool from the resource gateway.' } })

    expect(screen.getByText('No tools')).toBeTruthy()
    expect(screen.getByText('Register a tool from the resource gateway.')).toBeTruthy()
  })

  it('renders correlation id when backend errors provide one', () => {
    render(ErrorState, {
      props: {
        title: 'Request failed',
        message: 'Backend rejected the request.',
        correlationId: 'req-123'
      }
    })

    expect(screen.getByText('Request failed')).toBeTruthy()
    expect(screen.getByText('req-123')).toBeTruthy()
  })

  it('renders a neutral localized state for route access denial', () => {
    setLocale('zh-CN')
    render(PermissionDeniedState)

    expect(screen.getByText('没有访问权限')).toBeTruthy()
    expect(screen.getByText(/联系管理员调整角色或权限/)).toBeTruthy()
  })

  it('replaces permission-denied backend errors with the access state', () => {
    setLocale('zh-CN')
    render(ErrorState, {
      props: {
        title: '请求失败',
        message: 'knowledge permission denied',
        correlationId: 'req-secret'
      }
    })

    expect(screen.getByText('没有访问权限')).toBeTruthy()
    expect(screen.queryByText('请求失败')).toBeNull()
    expect(screen.queryByText('req-secret')).toBeNull()
  })
})
