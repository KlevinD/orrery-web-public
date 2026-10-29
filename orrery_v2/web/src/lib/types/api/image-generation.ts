export interface ImageGenerationSettingsDTO {
  enabled: boolean
  protocol: 'openai-compatible'
  base_url: string
  model: string
  api_key_ref: string
}

export interface ImageConnectionTestDTO {
  status: 'ok' | 'warning' | 'failed'
  reason: 'model_listed' | 'model_not_listed' | 'credential_unavailable' | 'connection_failed' | 'authentication_failed' | 'upstream_unavailable' | 'invalid_response'
  model_available: boolean
  generation_verified: boolean
  http_status?: number
  latency_ms: number
}
