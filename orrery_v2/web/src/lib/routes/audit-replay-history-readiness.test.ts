import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('audit replay history migration', () => {
  it('does not call removed ontology replay history endpoints from the audit page', () => {
    const page = readFileSync('src/routes/audit/+page.svelte', 'utf-8')
    const api = readFileSync('src/lib/api/operations.ts', 'utf-8')
    const queries = readFileSync('src/lib/queries/operations.ts', 'utf-8')

    expect(api).not.toContain('/api/ontology-events/replay-history')
    expect(queries).not.toContain('/api/ontology-events/replay-history')
    expect(page).not.toContain('replay-history')
  })

  it('moves projection replay attempts to the scoped operations route', () => {
    const page = readFileSync('src/routes/audit/+page.svelte', 'utf-8')
    const api = readFileSync('src/lib/api/operations.ts', 'utf-8')
    const queries = readFileSync('src/lib/queries/operations.ts', 'utf-8')

    expect(api).not.toContain('/api/projection-audits/replay-history')
    expect(queries).not.toContain('/api/projection-audits/replay-history')
    expect(page).toContain('/resources/ontology/operations/projections')
    expect(page).not.toContain('/api/projection-audits/replay-history')
  })

  it('loads compact audit pages into a scrollable ledger', () => {
    const page = readFileSync('src/routes/audit/+page.svelte', 'utf-8')
    const list = readFileSync('src/lib/components/operations/AuditLogList.svelte', 'utf-8')

    expect(page).toContain('const auditPageSize = 10')
    expect(page).toContain('limit: auditPageSize')
    expect(page).toContain('offset: auditOffset')
    expect(page).toContain('operations.audit.pagination.next')
    expect(list).toContain('max-h-[30rem]')
    expect(list).toContain('overflow-y-auto')
    expect(list).toContain('overscroll-contain')
  })
})
