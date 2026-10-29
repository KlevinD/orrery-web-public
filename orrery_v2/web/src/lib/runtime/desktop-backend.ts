import { invoke } from '@tauri-apps/api/core'

export const desktopBackendAdapter = {
  async startLocalBackend(): Promise<number> {
    const port = await invoke<number>('start_local_backend')
    return port
  }
}
