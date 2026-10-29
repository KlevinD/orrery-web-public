export interface ApiError {
  code: string
  message: string
  correlationId?: string
}

export interface ErrorResponseBody {
  code?: string
  error?: string
  message?: string
  details?: Record<string, unknown>
  correlation_id?: string
  correlationId?: string
  request_id?: string
  requestId?: string
}
