import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import ProfileCard from './ProfileCard.svelte'

describe('ProfileCard', () => {
  it('renders the demo source badge without requiring duplicate tags', () => {
    setLocale('zh-CN')
    render(ProfileCard, {
      props: {
        name: 'Planner Agent',
        subtitle: 'Mission planner',
        source: 'agent'
      }
    })

    expect(screen.getByText('Planner Agent')).toBeTruthy()
    expect(screen.getByText('AI')).toBeTruthy()
  })

  it('translates the human source badge when the locale changes', () => {
    setLocale('en-US')
    render(ProfileCard, {
      props: {
        name: 'Case Owner',
        subtitle: 'Human reviewer',
        source: 'human'
      }
    })

    expect(screen.getByText('Human')).toBeTruthy()
  })
})
