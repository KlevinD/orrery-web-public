import type { SkillBundleEditorSubmission } from '#lib/types/view-models'

export interface SkillBundleSubmissionOperations {
  create(input: {
    name: string
    description: string
    skillIds: string[]
  }): Promise<{ bundleId: string }>
  addSkills(bundleId: string, skillIds: string[]): Promise<void>
  importSkill(bundleId: string, file: File): Promise<void>
}

export interface SkillBundleSubmissionRequest {
  mode: 'create' | 'add'
  existingBundleId: string
  submission: SkillBundleEditorSubmission
  operations: SkillBundleSubmissionOperations
}

export function createSkillBundleSubmissionSession() {
  let createdBundleId = ''
  let appliedSkillIds = new Set<string>()
  let completedFiles = new WeakSet<File>()

  return {
    async submit(request: SkillBundleSubmissionRequest) {
      const { mode, submission, operations } = request
      let bundleId = request.existingBundleId.trim() || createdBundleId
      if (mode === 'create' && createdBundleId === '') {
        const created = await operations.create({
          name: submission.name,
          description: submission.description,
          skillIds: submission.skillIds
        })
        bundleId = created.bundleId.trim()
        if (bundleId === '') throw new Error('created skill bundle id is empty')
        createdBundleId = bundleId
        appliedSkillIds = new Set(submission.skillIds)
      }
      if (bundleId === '') throw new Error('skill bundle id is required')

      const pendingSkillIds = submission.skillIds.filter((skillId) => !appliedSkillIds.has(skillId))
      if (pendingSkillIds.length > 0) {
        await operations.addSkills(bundleId, pendingSkillIds)
        for (const skillId of pendingSkillIds) appliedSkillIds.add(skillId)
      }
      for (const file of submission.files) {
        if (completedFiles.has(file)) continue
        await operations.importSkill(bundleId, file)
        completedFiles.add(file)
      }
      return { bundleId, created: mode === 'create' }
    },
    reset() {
      createdBundleId = ''
      appliedSkillIds = new Set<string>()
      completedFiles = new WeakSet<File>()
    }
  }
}
