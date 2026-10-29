import { ApiClientError, apiFetch } from './client'
import type { AuthSession } from '#lib/stores/session.svelte'

export interface LoginChallengeDTO {
  challenge_id: string
  username: string
  salt: string
  expires_at?: string
}

export interface LoginSessionDTO {
  access_token?: string
  subject_provider?: string
  subject?: string
  identity_subject_id?: string
  user: {
    user_id: string
    username: string
    display_name: string
    nickname?: string
    email?: string
    agent_profile_id: string
    role?: string
  }
  permission_scope: string[]
  expires_at?: string
  session_id?: string
  must_change_password?: boolean
  rotation_required?: boolean
}

export interface AuthProviderDTO {
  name: string
  display_name: string
  kind: 'local' | 'browser' | string
  enabled: boolean
  primary: boolean
  begin_url?: string
}

export interface AuthProviderPageDTO {
  providers: AuthProviderDTO[]
}

export const authApi = {
  listProviders(options: { signal?: AbortSignal } = {}) {
    return apiFetch<AuthProviderPageDTO>('/api/auth/providers', {
      signal: options.signal
    })
  },
  async currentSession(options: { signal?: AbortSignal } = {}) {
    try {
      return await apiFetch<LoginSessionDTO>('/api/auth/session', {
        signal: options.signal,
        suppressUnauthorizedNotification: true
      })
    } catch (error) {
      if (error instanceof ApiClientError && (error.status === 401 || error.status === 404)) return undefined
      throw error
    }
  },
  beginLogin(username: string, options: { signal?: AbortSignal } = {}) {
    return apiFetch<LoginChallengeDTO>('/api/auth/login/begin', {
      method: 'POST',
      signal: options.signal,
      suppressUnauthorizedNotification: true,
      body: JSON.stringify({ username })
    })
  },
  completeLogin(username: string, challengeId: string, password: string, options: { signal?: AbortSignal; mfaCode?: string } = {}) {
    return apiFetch<LoginSessionDTO>('/api/auth/login/complete', {
      method: 'POST',
      signal: options.signal,
      suppressUnauthorizedNotification: true,
      body: JSON.stringify({
        username,
        challenge_id: challengeId,
        password,
        mfa_code: options.mfaCode?.trim() || undefined
      })
    })
  },
  changePassword(newPassword: string, options: { signal?: AbortSignal } = {}) {
    return apiFetch<LoginSessionDTO>('/api/auth/password/change', {
      method: 'POST',
      signal: options.signal,
      body: JSON.stringify({ new_password: newPassword })
    })
  },
  rotateSession(options: { signal?: AbortSignal } = {}) {
    return apiFetch<LoginSessionDTO>('/api/auth/session/rotate', {
      method: 'POST',
      signal: options.signal
    })
  },
  logout() {
    return apiFetch<void>('/api/auth/logout', {
      method: 'POST'
    })
  }
}

export function authSessionFromDTO(dto: LoginSessionDTO): AuthSession {
  return {
    subjectProvider: dto.subject_provider ?? '',
    permissionScope: dto.permission_scope ?? [],
    expiresAt: dto.expires_at ?? '',
    mustChangePassword: Boolean(dto.must_change_password),
    user: {
      userId: dto.user.user_id,
      username: dto.user.username,
      displayName: dto.user.display_name,
      nickname: dto.user.nickname ?? dto.user.display_name,
      email: dto.user.email ?? '',
      agentProfileId: dto.user.agent_profile_id,
      role: dto.user.role ?? ''
    }
  }
}

export async function loginWithPassword(username: string, password: string, options: { signal?: AbortSignal; mfaCode?: string } = {}) {
  const challenge = await authApi.beginLogin(username, options)
  return authApi.completeLogin(username, challenge.challenge_id, password, options)
}

export async function clientPasswordDigest(password: string, salt: string) {
  const bytes = new TextEncoder().encode(`${salt.trim()}\n${password}`)
  const digest = await sha256(bytes)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

export async function clientPasswordProof(password: string, salt: string, challengeId: string) {
  const digest = await clientPasswordDigest(password, salt)
  return sha256Hex(`${digest}\n${challengeId.trim()}`)
}

async function sha256Hex(value: string) {
  const bytes = new TextEncoder().encode(value)
  const digest = await sha256(bytes)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

async function sha256(bytes: Uint8Array) {
  if (globalThis.crypto?.subtle) {
    return globalThis.crypto.subtle.digest('SHA-256', new Uint8Array(bytes).buffer)
  }
  return sha256Fallback(bytes)
}

function sha256Fallback(bytes: Uint8Array) {
  const words: number[] = []
  for (let i = 0; i < bytes.length; i++) {
    words[i >> 2] = (words[i >> 2] ?? 0) | (bytes[i] << (24 - (i % 4) * 8))
  }
  const bitLength = bytes.length * 8
  words[bitLength >> 5] = (words[bitLength >> 5] ?? 0) | (0x80 << (24 - (bitLength % 32)))
  words[(((bitLength + 64) >> 9) << 4) + 15] = bitLength

  let h0 = 0x6a09e667
  let h1 = 0xbb67ae85
  let h2 = 0x3c6ef372
  let h3 = 0xa54ff53a
  let h4 = 0x510e527f
  let h5 = 0x9b05688c
  let h6 = 0x1f83d9ab
  let h7 = 0x5be0cd19
  const schedule = new Array<number>(64)

  for (let i = 0; i < words.length; i += 16) {
    for (let t = 0; t < 16; t++) schedule[t] = words[i + t] ?? 0
    for (let t = 16; t < 64; t++) {
      const s0 = rotateRight(schedule[t - 15], 7) ^ rotateRight(schedule[t - 15], 18) ^ (schedule[t - 15] >>> 3)
      const s1 = rotateRight(schedule[t - 2], 17) ^ rotateRight(schedule[t - 2], 19) ^ (schedule[t - 2] >>> 10)
      schedule[t] = (schedule[t - 16] + s0 + schedule[t - 7] + s1) | 0
    }

    let a = h0
    let b = h1
    let c = h2
    let d = h3
    let e = h4
    let f = h5
    let g = h6
    let h = h7

    for (let t = 0; t < 64; t++) {
      const s1 = rotateRight(e, 6) ^ rotateRight(e, 11) ^ rotateRight(e, 25)
      const ch = (e & f) ^ (~e & g)
      const temp1 = (h + s1 + ch + SHA256_K[t] + schedule[t]) | 0
      const s0 = rotateRight(a, 2) ^ rotateRight(a, 13) ^ rotateRight(a, 22)
      const maj = (a & b) ^ (a & c) ^ (b & c)
      const temp2 = (s0 + maj) | 0
      h = g
      g = f
      f = e
      e = (d + temp1) | 0
      d = c
      c = b
      b = a
      a = (temp1 + temp2) | 0
    }

    h0 = (h0 + a) | 0
    h1 = (h1 + b) | 0
    h2 = (h2 + c) | 0
    h3 = (h3 + d) | 0
    h4 = (h4 + e) | 0
    h5 = (h5 + f) | 0
    h6 = (h6 + g) | 0
    h7 = (h7 + h) | 0
  }

  const out = new Uint8Array(32)
  ;[h0, h1, h2, h3, h4, h5, h6, h7].forEach((word, index) => {
    out[index * 4] = (word >>> 24) & 0xff
    out[index * 4 + 1] = (word >>> 16) & 0xff
    out[index * 4 + 2] = (word >>> 8) & 0xff
    out[index * 4 + 3] = word & 0xff
  })
  return out.buffer
}

function rotateRight(value: number, bits: number) {
  return (value >>> bits) | (value << (32 - bits))
}

const SHA256_K = [
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
]
