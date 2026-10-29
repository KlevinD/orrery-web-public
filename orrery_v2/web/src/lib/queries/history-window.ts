export type HistoryWindow = 'today' | '7d' | '30d' | 'all'

export function withinHistoryWindow(value: string | undefined, window: HistoryWindow, now: number): boolean {
  if (window === 'all') return true
  const timestamp = Date.parse(value ?? '')
  // Unknown timestamps must not silently hide records.
  if (!Number.isFinite(timestamp)) return true
  const start = new Date(now)
  start.setHours(0, 0, 0, 0)
  start.setDate(start.getDate() - (window === '7d' ? 6 : window === '30d' ? 29 : 0))
  return timestamp >= start.getTime()
}
