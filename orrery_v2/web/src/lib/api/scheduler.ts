import { apiFetch, type ApiFetchOptions } from './client'
import type {
  CreateScheduledTaskRequest,
  ResponsibilityEvaluationItemPageDTO,
  RunScheduledTaskNowRequest,
  ScheduledRunDTO,
  ScheduledRunInboxPageDTO,
  ScheduledRunPageDTO,
  ScheduledRunReadStateDTO,
  ScheduledTaskDTO,
  ScheduledTaskPageDTO,
  ScheduledTaskStatsDTO,
  UpdateScheduledTaskRequest
} from '#lib/types/scheduler'

export const schedulerApi = {
  getTaskStats: (options?: ApiFetchOptions) =>
    apiFetch<ScheduledTaskStatsDTO>('/api/scheduled-tasks/summary', options),
  listTasks: (query = '', options?: ApiFetchOptions) =>
    apiFetch<ScheduledTaskPageDTO>(`/api/scheduled-tasks${query}`, options),
  getTask: (scheduledTaskID: string, options?: ApiFetchOptions) =>
    apiFetch<ScheduledTaskDTO>(`/api/scheduled-tasks/${scheduledTaskID}`, options),
  createTask: (body: CreateScheduledTaskRequest, options?: ApiFetchOptions) =>
    apiFetch<ScheduledTaskDTO>('/api/scheduled-tasks', jsonMutation('POST', body, options)),
  updateTask: (scheduledTaskID: string, body: UpdateScheduledTaskRequest, options?: ApiFetchOptions) =>
    apiFetch<ScheduledTaskDTO>(`/api/scheduled-tasks/${scheduledTaskID}`, jsonMutation('PATCH', body, options)),
  cancelTask: (scheduledTaskID: string, options?: ApiFetchOptions) =>
    apiFetch<ScheduledTaskDTO>(`/api/scheduled-tasks/${scheduledTaskID}/cancel`, { ...options, method: 'POST' }),
  runNow: (scheduledTaskID: string, body: RunScheduledTaskNowRequest = {}, options?: ApiFetchOptions) =>
    apiFetch<ScheduledRunDTO>(`/api/scheduled-tasks/${scheduledTaskID}/run-now`, jsonMutation('POST', body, options)),
  listRuns: (scheduledTaskID: string, query = '', options?: ApiFetchOptions) =>
    apiFetch<ScheduledRunPageDTO>(`/api/scheduled-tasks/${scheduledTaskID}/runs${query}`, options),
  listRunInbox: (query = '', options?: ApiFetchOptions) =>
    apiFetch<ScheduledRunInboxPageDTO>(`/api/scheduled-runs${query}`, options),
  setRunReadState: (scheduledRunID: string, read: boolean, options?: ApiFetchOptions) =>
    apiFetch<ScheduledRunReadStateDTO>(
      `/api/scheduled-runs/${encodeURIComponent(scheduledRunID)}/read`,
      jsonMutation('PATCH', { read }, options)
    ),
  listResponsibilityEvaluations: (
    scheduledTaskID: string,
    scheduledRunID: string,
    query = '',
    options?: ApiFetchOptions
  ) => apiFetch<ResponsibilityEvaluationItemPageDTO>(
    `/api/scheduled-tasks/${encodeURIComponent(scheduledTaskID)}/runs/${encodeURIComponent(scheduledRunID)}/responsibility-evaluations${query}`,
    options
  ),
  readRunArtifact: (
    scheduledTaskID: string,
    scheduledRunID: string,
    artifactID: string,
    disposition: 'inline' | 'attachment' = 'attachment',
    options?: ApiFetchOptions
  ) => apiFetch<Blob>(
    runArtifactURL(scheduledTaskID, scheduledRunID, artifactID, disposition),
    { ...options, responseType: 'blob' }
  ),
  runArtifactURL
}

function runArtifactURL(
  scheduledTaskID: string,
  scheduledRunID: string,
  artifactID: string,
  disposition: 'inline' | 'attachment' = 'attachment'
) {
  const query = new URLSearchParams({ artifact_id: artifactID, disposition })
  return `/api/scheduled-tasks/${encodeURIComponent(scheduledTaskID)}/runs/${encodeURIComponent(scheduledRunID)}/artifacts/content?${query}`
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}
