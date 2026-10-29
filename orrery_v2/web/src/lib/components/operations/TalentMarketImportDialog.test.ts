import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { TalentMarketImportPreviewResponseDTO } from '#lib/types/api'
import TalentMarketImportDialog from './TalentMarketImportDialog.svelte'

describe('TalentMarketImportDialog', () => {
  it('shows import progress, current file, logs, conflicts, and package overlaps', () => {
    setLocale('en-US')
    render(TalentMarketImportDialog, {
      props: {
        currentFileName: 'engineering-code-reviewer.md',
        phase: 'committing',
        importedCount: 1,
        failedCount: 1,
        skippedCount: 1,
        logs: [
          { level: 'info', message: 'Parsed agents.zip', fileName: 'agents.zip' },
          { level: 'error', message: 'Resolved name conflicts remain', fileName: 'duplicate.md' }
        ],
        preview: {
          items: [
            {
              candidate_index: 0,
              status: 'create',
              candidate: {
                agent_profile_id: 'profile-code-reviewer',
                real_name: 'Code Reviewer',
                description: 'Reviews code.',
                source_path: 'engineering-code-reviewer.md'
              }
            },
            {
              candidate_index: 1,
              status: 'conflict',
              candidate: {
                agent_profile_id: 'profile-existing',
                real_name: 'Existing Expert',
                description: 'Conflicts with an existing market profile.',
                source_path: 'duplicate.md'
              },
              conflicts: [
                {
                  candidate_index: 1,
                  conflict_type: 'real_name',
                  existing_agent_profile_id: 'profile-existing',
                  existing_real_name: 'Existing Expert',
                  existing_placement_status: 'market',
                  candidate_real_name: 'Existing Expert'
                }
              ]
            },
            {
              candidate_index: 2,
              status: 'create',
              candidate: {
                agent_profile_id: 'profile-code-reviewer-copy',
                real_name: 'Code Reviewer',
                description: 'Duplicate in the same package.',
                source_path: 'engineering-code-reviewer-copy.md'
              }
            }
          ],
          conflicts: [
            {
              candidate_index: 1,
              conflict_type: 'real_name',
              existing_agent_profile_id: 'profile-existing',
              existing_real_name: 'Existing Expert',
              existing_placement_status: 'market',
              candidate_real_name: 'Existing Expert'
            }
          ]
        },
        itemStatuses: {
          0: { state: 'imported', message: 'Imported' },
          1: { state: 'failed', message: 'Still conflicts' },
          2: { state: 'skipped', message: 'Skipped' }
        }
      }
    })

    expect(screen.getByText('3 total')).toBeTruthy()
    expect(screen.getByText('1 imported')).toBeTruthy()
    expect(screen.getByText('1 failed')).toBeTruthy()
    expect(screen.getByText('Current file: engineering-code-reviewer.md')).toBeTruthy()
    expect(screen.getByText('Import log')).toBeTruthy()
    expect(screen.getByText('Parsed agents.zip')).toBeTruthy()
    expect(screen.getByText('Package overlaps')).toBeTruthy()
    expect(screen.getAllByText(/Code Reviewer/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/Conflicts with market talent Existing Expert/).length).toBeGreaterThan(0)
    expect(screen.getByText('Still conflicts')).toBeTruthy()
  })

  it('applies overwrite to every conflicting import candidate', async () => {
    setLocale('en-US')
    const submit = vi.fn()
    render(TalentMarketImportDialog, {
      props: {
        preview: bulkConflictPreview(),
        onsubmit: submit
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'All overwrite' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Import' }))

    const candidates = submit.mock.calls[0][0]
    expect(candidates).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ agent_profile_id: 'profile-conflict-a', conflict_resolution: 'overwrite' }),
        expect.objectContaining({ agent_profile_id: 'profile-conflict-b', conflict_resolution: 'overwrite' }),
        expect.objectContaining({ agent_profile_id: 'profile-new', conflict_resolution: 'create' })
      ])
    )
  })

  it('applies rename to every conflicting import candidate with generated names', async () => {
    setLocale('en-US')
    const submit = vi.fn()
    render(TalentMarketImportDialog, {
      props: {
        preview: bulkConflictPreview(),
        onsubmit: submit
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'All rename' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Import' }))

    const candidates = submit.mock.calls[0][0]
    expect(candidates).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          agent_profile_id: 'profile-conflict-a',
          conflict_resolution: 'rename',
          resolved_real_name: 'Existing Expert 2'
        }),
        expect.objectContaining({
          agent_profile_id: 'profile-conflict-b',
          conflict_resolution: 'rename',
          resolved_real_name: 'Existing Expert 3'
        }),
        expect.objectContaining({ agent_profile_id: 'profile-new', conflict_resolution: 'create' })
      ])
    )
  })
})

function bulkConflictPreview(): TalentMarketImportPreviewResponseDTO {
  return {
    items: [
      {
        candidate_index: 0,
        status: 'conflict',
        candidate: {
          agent_profile_id: 'profile-conflict-a',
          real_name: 'Existing Expert',
          source_path: 'duplicate-a.md'
        },
        conflicts: [
          {
            candidate_index: 0,
            conflict_type: 'real_name',
            existing_agent_profile_id: 'profile-existing',
            existing_real_name: 'Existing Expert',
            existing_placement_status: 'market',
            candidate_real_name: 'Existing Expert'
          }
        ]
      },
      {
        candidate_index: 1,
        status: 'conflict',
        candidate: {
          agent_profile_id: 'profile-conflict-b',
          real_name: 'Existing Expert',
          source_path: 'duplicate-b.md'
        },
        conflicts: [
          {
            candidate_index: 1,
            conflict_type: 'real_name',
            existing_agent_profile_id: 'profile-existing',
            existing_real_name: 'Existing Expert',
            existing_placement_status: 'market',
            candidate_real_name: 'Existing Expert'
          }
        ]
      },
      {
        candidate_index: 2,
        status: 'create',
        candidate: {
          agent_profile_id: 'profile-new',
          real_name: 'New Expert',
          source_path: 'new.md'
        }
      }
    ],
    conflicts: [
      {
        candidate_index: 0,
        conflict_type: 'real_name',
        existing_agent_profile_id: 'profile-existing',
        existing_real_name: 'Existing Expert',
        existing_placement_status: 'market',
        candidate_real_name: 'Existing Expert'
      },
      {
        candidate_index: 1,
        conflict_type: 'real_name',
        existing_agent_profile_id: 'profile-existing',
        existing_real_name: 'Existing Expert',
        existing_placement_status: 'market',
        candidate_real_name: 'Existing Expert'
      }
    ]
  }
}
