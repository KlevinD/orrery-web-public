import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import ComplianceFooter from './ComplianceFooter.svelte'

describe('ComplianceFooter', () => {
  it('renders both official registration links without bundled artwork', () => {
    render(ComplianceFooter)

    const icp = screen.getByRole('link', { name: '京ICP备2026039091号' })
    expect(icp.getAttribute('href')).toBe('https://beian.miit.gov.cn/')
    expect(icp.getAttribute('target')).toBe('_blank')
    expect(icp.getAttribute('rel')).toBe('noreferrer')

    const police = screen.getByRole('link', { name: '京公网安备11011502040404号' })
    expect(police.getAttribute('href')).toBe('https://beian.mps.gov.cn/#/query/webSearch?code=11011502040404')
    expect(police.getAttribute('target')).toBe('_blank')
    expect(police.getAttribute('rel')).toBe('noreferrer')
    expect(police.querySelector('img')).toBeNull()
  })
})
