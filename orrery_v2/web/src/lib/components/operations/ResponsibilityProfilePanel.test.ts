import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { PeopleResponsibilityProfileDTO } from '#lib/types/api'
import ResponsibilityProfilePanel from './ResponsibilityProfilePanel.svelte'

describe('ResponsibilityProfilePanel', () => {
  it('switches between immutable People responsibility versions', async () => {
    setLocale('en-US')
    render(ResponsibilityProfilePanel, { props: { profile: profileFixture } })

    expect(screen.getByText('Current responsibility')).toBeTruthy()
    expect(screen.getByText('Current version')).toBeTruthy()
    expect(screen.getByText('people-position-1')).toBeTruthy()
    expect(screen.getByText(/Historical responsibility schedule/)).toBeTruthy()
    expect(screen.queryByText(/Responsibility scheduled task/)).toBeNull()

    await fireEvent.change(screen.getByRole('combobox', { name: 'Responsibility versions' }), {
      target: { value: 'version-1' }
    })

    expect(screen.getByText('Original responsibility')).toBeTruthy()
    expect(screen.getByText('Historical version')).toBeTruthy()
    expect(screen.queryByText('Current responsibility')).toBeNull()
  })

  it('renders current responsibilities while history is still unavailable', () => {
    setLocale('en-US')
    const currentOnlyProfile: PeopleResponsibilityProfileDTO = {
      ...profileFixture,
      version_count: 0,
      portfolios: profileFixture.portfolios.map((portfolio) => ({ ...portfolio, versions: [], audit: [] }))
    }

    render(ResponsibilityProfilePanel, { props: { profile: currentOnlyProfile } })

    expect(screen.getByText('Current responsibility')).toBeTruthy()
    expect(screen.getByText('Current version')).toBeTruthy()
    expect(screen.queryByRole('combobox', { name: 'Responsibility versions' })).toBeNull()
  })
})

const profileFixture: PeopleResponsibilityProfileDTO = {
  scope_type: 'agent',
  scope_id: 'agent-1',
  source: 'people',
  responsibility_count: 1,
  version_count: 2,
  updated_at: '2026-08-06T04:00:00Z',
  portfolios: [{
    binding_id: 'binding-1',
    people_tenant_id: 'tenant-preview',
    subject_id: 'people-member-1',
    agent_profile_id: 'agent-1',
    agent_display_name: 'Agent One',
    status: 'active',
    current_version_id: 'version-2',
    current_source_revision: '2:1',
    current_source_digest: 'digest-2',
    pulse_scheduled_task_id: 'pulse-1',
    source_as_of: '2026-08-06T03:50:00Z',
    synced_at: '2026-08-06T04:00:00Z',
    audit: [],
    responsibilities: [responsibility('Current responsibility', 2)],
    versions: [{
      version_id: 'version-2',
      source_revision: '2:1',
      source_digest: 'digest-2',
      source_as_of: '2026-08-06T03:50:00Z',
      recorded_at: '2026-08-06T04:00:00Z',
      responsibilities: [responsibility('Current responsibility', 2)]
    }, {
      version_id: 'version-1',
      source_revision: '1:1',
      source_digest: 'digest-1',
      source_as_of: '2026-07-06T03:50:00Z',
      recorded_at: '2026-07-06T04:00:00Z',
      responsibilities: [responsibility('Original responsibility', 1)]
    }]
  }]
}

function responsibility(title: string, revision: number) {
  return {
    id: 'responsibility-1',
    code: 'RESP-1',
    title,
    description: `${title} description`,
    valid_from: '2026-06-21T00:00:00Z',
    valid_to: null,
    revision,
    sources: [{
      binding_id: 'source-binding-1',
      responsibility_id: 'responsibility-1',
      target_type: 'position',
      target_id: 'people-position-1',
      valid_from: '2026-06-21T00:00:00Z',
      valid_to: null,
      revision
    }]
  }
}
