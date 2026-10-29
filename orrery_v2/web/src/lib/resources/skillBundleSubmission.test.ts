import { describe, expect, it, vi } from 'vitest'
import { createSkillBundleSubmissionSession } from './skillBundleSubmission'

describe('skill bundle submission session', () => {
  it('resumes imports without recreating the bundle or re-uploading completed files', async () => {
    const first = new File(['first'], 'first.zip', { type: 'application/zip' })
    const second = new File(['second'], 'second.zip', { type: 'application/zip' })
    const create = vi.fn().mockResolvedValue({ bundleId: 'skill-bundle-quality' })
    const addSkills = vi.fn().mockResolvedValue(undefined)
    let failSecond = true
    const importSkill = vi.fn(async (_bundleId: string, file: File) => {
      if (file === second && failSecond) {
        failSecond = false
        throw new Error('upload failed')
      }
    })
    const session = createSkillBundleSubmissionSession()
    const request = {
      mode: 'create' as const,
      existingBundleId: '',
      submission: {
        name: 'Quality',
        description: 'Review and test',
        skillIds: ['skill.review'],
        files: [first, second]
      },
      operations: { create, addSkills, importSkill }
    }

    await expect(session.submit(request)).rejects.toThrow('upload failed')
    await expect(session.submit(request)).resolves.toEqual({
      bundleId: 'skill-bundle-quality',
      created: true
    })

    expect(create).toHaveBeenCalledTimes(1)
    expect(addSkills).not.toHaveBeenCalled()
    expect(importSkill.mock.calls.map(([, file]) => file.name)).toEqual([
      'first.zip',
      'second.zip',
      'second.zip'
    ])
  })
})
