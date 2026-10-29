// SvelteKit 3 keeps the legacy store runtime available for this app while
// its public declarations move to `$app/state`.
declare module '$app/stores' {
  interface LegacyReadable<T = any> {
    subscribe(run: (value: T) => void): () => void
  }

  export const page: LegacyReadable
  export const navigating: LegacyReadable
  export const updated: LegacyReadable & { check(): Promise<boolean> }
}
