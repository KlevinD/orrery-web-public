import { describe, expect, it } from 'vitest'
import { cancelExportSelection, nextExportSelectionState, retainMarketSelection } from './peopleSelection'

describe('retainMarketSelection', () => {
  it('keeps the same array reference when every selected market id is still present', () => {
    const selected = ['profile-a', 'profile-b']
    const marketIds = new Set(['profile-a', 'profile-b', 'profile-c'])

    expect(retainMarketSelection(selected, marketIds)).toBe(selected)
  })

  it('returns filtered ids when a selected market id disappears', () => {
    const selected = ['profile-a', 'profile-missing', 'profile-b']
    const marketIds = new Set(['profile-a', 'profile-b'])

    expect(retainMarketSelection(selected, marketIds)).toEqual(['profile-a', 'profile-b'])
  })

  it('enters selection mode before confirming export', () => {
    expect(nextExportSelectionState(false, [])).toEqual({
      mode: 'selecting',
      shouldExport: false,
      shouldShowEmptySelectionMessage: false
    })
  })

  it('confirms export only when selection mode already has selected ids', () => {
    expect(nextExportSelectionState(true, ['profile-a'])).toEqual({
      mode: 'selecting',
      shouldExport: true,
      shouldShowEmptySelectionMessage: false
    })
  })

  it('shows an empty selection message only after confirming with no selected ids', () => {
    expect(nextExportSelectionState(true, [])).toEqual({
      mode: 'selecting',
      shouldExport: false,
      shouldShowEmptySelectionMessage: true
    })
  })

  it('cancels export selection by exiting selection mode and clearing ids', () => {
    expect(cancelExportSelection()).toEqual({
      selectionMode: false,
      selectedIds: []
    })
  })
})
