import { apiFetch, type ApiFetchOptions } from './client'
import type {
  AcceptMemoryCandidateRequest,
  MemoryCandidatePageDTO,
  MemoryCommandReceiptDTO,
  MemoryRecordDTO,
  MemoryRecordPageDTO,
  RejectMemoryCandidateRequest
} from '#lib/types/memory'

export const memoryApi = {
  listRecords: (query = '', options?: ApiFetchOptions) =>
    apiFetch<MemoryRecordPageDTO>(`/api/memory/records${query}`, options),
  getRecord: (memoryId: string, options?: ApiFetchOptions) =>
    apiFetch<MemoryRecordDTO>(`/api/memory/records/${encodeURIComponent(memoryId)}`, options),
  listCandidates: (query = '', options?: ApiFetchOptions) =>
    apiFetch<MemoryCandidatePageDTO>(`/api/memory/candidates${query}`, options),
  acceptCandidate: (candidateId: string, body: AcceptMemoryCandidateRequest, options?: ApiFetchOptions) =>
    apiFetch<MemoryRecordDTO>(`/api/memory/candidates/${encodeURIComponent(candidateId)}/accept`, {
      ...options,
      method: 'POST',
      body: JSON.stringify(body)
    }),
  rejectCandidate: (candidateId: string, body: RejectMemoryCandidateRequest, options?: ApiFetchOptions) =>
    apiFetch<MemoryCommandReceiptDTO>(`/api/memory/candidates/${encodeURIComponent(candidateId)}/reject`, {
      ...options,
      method: 'POST',
      body: JSON.stringify(body)
    })
}
