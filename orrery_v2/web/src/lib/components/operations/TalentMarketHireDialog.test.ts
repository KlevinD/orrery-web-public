import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import type {
  OrganizationTreeViewModel,
  WorkforceMemberViewModel
} from '#lib/types/view-models'
import TalentMarketHireDialog from './TalentMarketHireDialog.svelte'

describe('TalentMarketHireDialog', () => {
  it('uses semantic placement fields and a manager picker instead of raw ids', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(TalentMarketHireDialog, {
      props: {
        open: true,
        member: member({
          profileId: 'agent-candidate',
          displayName: 'Market Planner',
          roles: ['role.planner']
        }),
        organizations: organizationTree(),
        members: [
          member({
            profileId: 'manager-1',
            displayName: 'Lead Ada',
            placementStatus: 'internal',
            organizationId: 'org-1',
            departmentId: 'dept-ops',
            positionId: 'pos-lead',
            departmentDisplayName: 'Operations',
            positionDisplayName: 'Lead'
          })
        ],
        onsubmit
      }
    })

    expect(screen.queryByText('Role ID list')).toBeNull()
    expect(screen.queryByText('Reporting line')).toBeNull()

    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ops' } })
    await fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'pos-planner' } })
    await fireEvent.change(screen.getByLabelText('Manager'), { target: { value: 'manager-1' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Hire into unit' }))

    expect(screen.getByRole('option', { name: 'Lead Ada - Operations / Lead' })).toBeTruthy()
    expect(onsubmit).toHaveBeenCalledWith({
      agentProfileId: 'agent-candidate',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'pos-planner',
      roles: 'role.planner',
      managerId: 'manager-1',
      reportingLineId: ''
    })
  })
})

function organizationTree(): OrganizationTreeViewModel[] {
  return [
    {
      id: 'org-1',
      name: 'Orrery',
      unitCount: 1,
      memberCount: 1,
      units: [
        {
          id: 'dept-ops',
          name: 'Operations',
          organizationId: 'org-1',
          parentId: '',
          defaultPositionId: 'pos-planner',
          memberCount: 1,
          children: [],
          positions: [
            {
              id: 'pos-planner',
              name: 'Planner',
              departmentId: 'dept-ops',
              memberCount: 0,
              members: []
            },
            {
              id: 'pos-lead',
              name: 'Lead',
              departmentId: 'dept-ops',
              memberCount: 1,
              members: []
            }
          ]
        }
      ]
    }
  ]
}

function member(overrides: Partial<WorkforceMemberViewModel> = {}): WorkforceMemberViewModel {
  return {
    profileId: 'profile-1',
    displayName: 'Member',
    placementStatus: 'market',
    organizationId: '',
    departmentId: '',
    positionId: '',
    departmentDisplayName: '',
    positionDisplayName: '',
    roles: [],
    managerId: '',
    ...overrides
  } as WorkforceMemberViewModel
}
