import { apiFetch } from './client'

export interface OAuth2ConsentClientDTO {
  client_id: string
  display_name?: string
  kind?: string
  first_party: boolean
}

export interface OAuth2ConsentPreviewDTO {
  challenge: string
  subject?: string
  client: OAuth2ConsentClientDTO
  requested_scopes: string[]
  grant_scopes: string[]
  requested_audience?: string[]
  grant_audience?: string[]
}

export interface OAuth2RedirectDTO {
  redirect_to: string
}

export const oauth2Api = {
  pendingConsent(consentChallenge: string, options: { signal?: AbortSignal } = {}) {
    return apiFetch<OAuth2ConsentPreviewDTO>(
      `/api/oauth2/consent/pending?consent_challenge=${encodeURIComponent(consentChallenge)}`,
      {
        signal: options.signal
      }
    )
  },
  acceptConsent(consentChallenge: string) {
    return apiFetch<OAuth2RedirectDTO>('/api/oauth2/consent/accept', {
      method: 'POST',
      body: JSON.stringify({ consent_challenge: consentChallenge })
    })
  },
  rejectConsent(consentChallenge: string) {
    return apiFetch<OAuth2RedirectDTO>('/api/oauth2/consent/reject', {
      method: 'POST',
      body: JSON.stringify({ consent_challenge: consentChallenge })
    })
  }
}
