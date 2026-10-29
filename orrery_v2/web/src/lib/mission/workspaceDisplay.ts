export function formatList(items: string[]) {
  return items.length > 0 ? items.join(', ') : ''
}

export function formatTaskEvidenceLabel(
  evidenceRequirements: string[],
  evidenceCount: number,
  formatCount: (count: number) => string
) {
  return formatList(evidenceRequirements) || formatCount(evidenceCount)
}
