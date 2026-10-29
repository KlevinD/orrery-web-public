export function isTauriDesktop() {
  if (typeof window === 'undefined') {
    return false
  }
  const candidate = window as Window & {
    __TAURI_INTERNALS__?: unknown
    __TAURI__?: unknown
  }
  return Boolean(candidate.__TAURI_INTERNALS__ || candidate.__TAURI__)
}
