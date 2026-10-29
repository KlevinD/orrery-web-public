export function downloadTalentMarketZip(blob: Blob, filename = defaultTalentMarketExportFilename()) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

function defaultTalentMarketExportFilename() {
  const date = new Date().toISOString().slice(0, 10)
  return `orrery-talent-market-${date}.zip`
}
