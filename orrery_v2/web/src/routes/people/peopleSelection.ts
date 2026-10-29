export function retainMarketSelection(selectedIds: string[], marketIds: ReadonlySet<string>) {
  let changed = false
  const retained: string[] = []

  for (const id of selectedIds) {
    if (marketIds.has(id)) {
      retained.push(id)
    } else {
      changed = true
    }
  }

  return changed ? retained : selectedIds
}

export function nextExportSelectionState(selectionMode: boolean, selectedIds: string[]) {
  if (!selectionMode) {
    return { mode: 'selecting' as const, shouldExport: false, shouldShowEmptySelectionMessage: false }
  }

  return {
    mode: 'selecting' as const,
    shouldExport: selectedIds.length > 0,
    shouldShowEmptySelectionMessage: selectedIds.length === 0
  }
}

export function cancelExportSelection() {
  return {
    selectionMode: false,
    selectedIds: []
  }
}
