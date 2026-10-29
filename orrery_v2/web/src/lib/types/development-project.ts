import type { components } from '#lib/types/api.generated'

export type DevelopmentProjectDTO = components['schemas']['DevelopmentProjectDTO']
export type DevelopmentProjectPageDTO = components['schemas']['DevelopmentProjectPageDTO']
export type DevelopmentProjectMemberDTO = components['schemas']['DevelopmentProjectMemberDTO']
export type DevelopmentProjectMemberPageDTO = components['schemas']['DevelopmentProjectMemberPageDTO']
export type DevelopmentProjectMemberMutationDTO = components['schemas']['DevelopmentProjectMemberMutationDTO']
export type DevelopmentRepositoryBindingDTO = components['schemas']['DevelopmentRepositoryBindingDTO']
export type DevelopmentRepositoryBindingPageDTO = components['schemas']['DevelopmentRepositoryBindingPageDTO']
export type DevelopmentRepositoryMutationDTO = components['schemas']['DevelopmentRepositoryMutationDTO']
export type DevelopmentExecutionSummaryDTO = components['schemas']['DevelopmentExecutionSummaryDTO']
export type DevelopmentExecutionPageDTO = components['schemas']['DevelopmentExecutionPageDTO']
export type DevelopmentExecutionSignalDTO = components['schemas']['DevelopmentExecutionSignalDTO']
export type DevelopmentWorkspaceDTO = components['schemas']['DevelopmentWorkspaceDTO']
export type DevelopmentWorkspacePageDTO = components['schemas']['DevelopmentWorkspacePageDTO']
export type TodoExecutionRunDTO = components['schemas']['TodoExecutionRunDTO']
export type TodoExecutionRunPageDTO = components['schemas']['TodoExecutionRunPageDTO']
export type CreateDevelopmentProjectRequest = components['schemas']['CreateDevelopmentProjectRequest']
export type UpdateDevelopmentProjectRequest = components['schemas']['UpdateDevelopmentProjectRequest']
export type AddDevelopmentProjectMemberRequest = components['schemas']['AddDevelopmentProjectMemberRequest']
export type UpdateDevelopmentProjectMemberRequest = components['schemas']['UpdateDevelopmentProjectMemberRequest']
export type BindDevelopmentRepositoryRequest = components['schemas']['BindDevelopmentRepositoryRequest']
export type UpdateDevelopmentRepositoryRequest = components['schemas']['UpdateDevelopmentRepositoryRequest']
export type SCMConnectionDTO = components['schemas']['SCMConnectionDTO']
export type SCMConnectionPageDTO = components['schemas']['SCMConnectionPageDTO']
export type CreateSCMConnectionRequest = components['schemas']['CreateSCMConnectionRequest']
export type UpdateSCMConnectionRequest = components['schemas']['UpdateSCMConnectionRequest']
export type RotateSCMConnectionRequest = components['schemas']['RotateSCMConnectionRequest']
export type SCMConnectionVerificationDTO = components['schemas']['SCMConnectionVerificationDTO']
export type SCMRepositoryMetadataDTO = components['schemas']['SCMRepositoryMetadataDTO']
export type SCMRepositoryPageDTO = components['schemas']['SCMRepositoryPageDTO']

export interface DevelopmentProjectFilters {
  organizationId?: string
  status?: '' | 'active' | 'archived'
  limit?: number
  offset?: number
}

export interface SCMConnectionFilters {
  organizationId?: string
  provider?: string
  status?: string
  limit?: number
  offset?: number
}

export interface DevelopmentExecutionFilters {
  todoId?: string
  todoStatus?: string
  missionStatus?: string
  assigneeId?: string
  blocked?: boolean
  pendingApproval?: boolean
  limit?: number
  offset?: number
}

export interface DevelopmentArtifactFilters {
  todoId?: string
  status?: string
  limit?: number
  offset?: number
}
