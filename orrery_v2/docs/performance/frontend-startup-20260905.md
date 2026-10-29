# Frontend performance audit

## Scope

Base: `4737571cf`, measured with the lockfile dependencies and `npm run build`.
These changes preserve authentication rules, tenant isolation checks, and database
state. Follow-ups change realtime polling cadence and add a read-only scheduler
statistics API, as described below. No Docker is used.

## Changes and measurements

- The global context panel is dynamically imported only when its existing visibility
  conditions mount it. Its loading and failure states retain the existing rail width
  and the mobile close control.
- The results view no longer fetches up to 500 workforce members for hidden task UI.
  Task and editor views still enable that query.
- The task-only refresh button is hidden in results view. The inbox's own refresh
  remains available and refreshes the currently displayed results.

| Production fallback HTML module preloads | Before | After |
| --- | ---: | ---: |
| JavaScript files | 68 | 53 |
| JavaScript bytes, uncompressed | 850,211 | 710,553 |

This is a 16.4% reduction in preloaded JavaScript bytes, not a measured 16.4%
improvement in page latency. Route-specific chunks, gzip, warm-cache behavior,
and authenticated API latency need separate measurement.

## Verification

From `orrery_v2/web`:

```sh
npm ci --no-audit --no-fund
npm run check
npm run test -- src/lib/components/layout/AppShell.test.ts src/lib/components/layout/LazyWorkspaceContextPanel.test.ts src/routes/scheduled-tasks src/lib/queries/notification-badges.test.ts
npm run build
npx playwright test --config playwright.startup.config.ts
```

Playwright needs Chromium installed or `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`.
The dedicated configuration serves the production build on port 5189 and
intercepts all API calls with isolated test fixtures. It verifies desktop/mobile
lazy loading, no repeat chunk download, results-only refresh, task query restoration,
and closing after a chunk load failure. Screenshots are written to `test-results`.
These tests do not measure production authenticated performance.

## Additional fixes

### Additional page fixes

- Knowledge graph failures now stop automatic loading until the user refreshes.
  A failed base-list request also prevents graph loading. Document fan-out is
  bounded to batches of four; a failing batch stops scheduling later batches.
- Settings gateway configuration and account queries are enabled only on the
  Gateway tab. Other query consumers retain their existing enabled-by-default
  behavior. Existing mutation invalidation and explicit account refresh remain.
- Additional validation: 39 tests across knowledge queries, knowledge routes,
  settings interactions, and home-page behavior. Run with:
  `npm run test -- src/lib/queries/knowledge.test.ts src/lib/components/settings/SettingsWorkspace.test.ts src/routes/resources/knowledge src/routes/homePage.test.ts`.
- Home workforce details remain unchanged: the detail mapper overrides display
  names with account nicknames, so disabling details is not yet display-equivalent.

### Realtime and page lifecycle

- Mission snapshot/patch/message events now share a 250 ms refresh window.
  Each subscription waits for its current query refreshes to settle before
  scheduling one trailing refresh for intervening events. All three consumers
  return their invalidation promises. Unsubscribing drops queued refreshes, and
  callbacks retain the original Mission ID when the selected Mission changes.
- Opening a discussion invalidates only discussion notification badges, not the
  approval badge query that fans out into four approval/HIL requests. Explicit
  approval actions and existing periodic badge refreshes remain unchanged.
- Mission SSE and WebSocket polling backs off from 100 ms to 200/400/800 ms and
  then 1 second while no patches are returned. Any patch resets the interval to
  100 ms. Initial snapshots remain immediate, and authorization is still checked
  by the stream source on each poll. After settling, an idle subscription checks
  at most once per second instead of ten times per second. This is a configured
  cadence, not an ECS load benchmark. Full projection reads still occur per poll.
  Long-idle update detection and permission revocation checks can wait up to the
  1-second polling interval, in addition to query, transport, and frontend delays.
- Ontology context and lineage detail queries forward the observer's AbortSignal
  to the API client. Leaving the last observer can cancel its outstanding HTTP
  request. Security-context/tenant query keys, enabled guards, summary scheduling,
  and the existing zero-cache freshness policy are unchanged.
- Memory loads only the selected organization's or agents' directory. Unloaded
  tab counts are omitted rather than displayed as zero. Record requests across
  profiles run in batches of four per query, preserve existing deduplication,
  forward cancellation, and stop scheduling later batches after failure or abort.

Validation for this follow-up:

```sh
# From orrery_v2
go test ./internal/transport/http -run 'TestMissionStream' -count=1 -timeout=60s
# From orrery_v2/web
npm run check
npm run test -- src/lib/queries/missions.test.ts src/lib/queries/memory-loading.test.ts src/lib/queries/ontology-cancellation.test.ts src/routes/memory/memoryPage.test.ts src/lib/components/mission/MissionRoom.test.ts src/lib/components/discussions/DiscussionWorkspace.test.ts src/lib/queries/notification-badges.test.ts
npm run build
npx playwright test --config playwright.startup.config.ts
```

Results: 6 Go stream tests, 77 Vitest tests across 7 files, and 3 existing
desktop/mobile production-build browser regressions passed; Svelte check found
0 errors and 0 warnings. The Go handler lifecycle test uses the Go 1.25 fake
clock to verify actual SSE polling deadlines, patch delivery/version progression,
and cancellation without real-time sleeps or a database. The WebSocket snapshot
tests and shared interval test also pass. Browser tests use isolated API fixtures,
not production authenticated data. Changes are in PR #186, not merged or deployed.

### Scheduler statistics

- Replaced the KPI-only task list request with `GET /api/scheduled-tasks/summary`.
  The old request asked for 500 records, but the HTTP handler capped it at 100,
  so client-side statistics were incomplete for larger task collections.
- The summary uses the existing scheduler viewer resolution and `scheduler.read`
  capability. Explicit organization tenants, system-management scope, rejection
  of unscoped browser requests, and legacy system-tenant normalization are retained.
  Query-string tenant IDs and list filters do not alter its authenticated scope.
- Counts cover all visible tasks: total/enabled/executors/system exclude archived
  tasks; failed counts tasks whose latest run failed, including archived tasks.
  Agent resolution uses the same helper as task lists. The previous misleading
  "last 7 days" label now describes latest-run failures, including archived tasks.
- Statistics load only in task view and use the existing scheduler mutation
  invalidation. Pending statistics show placeholders; errors are visible without
  blocking the task list, and manual refresh retries the summary. Timeline and
  table requests remain because both are visible and use different filters.
- A synthetic 252-active-task fixture produces a 63-byte summary versus a
  22,665-byte 100-task page. This measures serialized fixture payloads, not ECS
  latency or CPU. Existing repository task/run snapshots and their 500 ms cache
  remain in use; this change does not eliminate full backing-store scans.
- OpenAPI, generated TypeScript types, capability registration, HTTP wiring,
  client API, query hook, and page have been updated together. Deploy the backend
  before the frontend, or release them atomically. No merge or deployment was run.

Validation:

```sh
# From orrery_v2
go test ./internal/app/scheduler -run 'TestServiceTaskStats' -count=1 -v -timeout=60s
go test ./internal/transport/http -run 'TestScheduler|TestScheduled|Test.*OpenAPI|Test.*PermissionCapabilit' -count=1 -timeout=60s
# From orrery_v2/web
npm run generate:api
npm run check
npm run test -- src/lib/api/scheduler.test.ts src/lib/queries/scheduler.test.ts src/lib/queries/scheduler-stats.test.ts src/routes/scheduled-tasks
npm run build
npx playwright test --config playwright.startup.config.ts
```

All selected Go tests passed, along with 53 Vitest tests across 12 files and
4 production-build browser tests. Svelte check found 0 errors and 0 warnings.
Service tests seed the existing repository caches under a fake clock, without
opening a database; HTTP tests use the real router with a fake scheduler service.
There was no new live-database integration or authenticated ECS benchmark. Browser
coverage includes desktop/mobile summary rendering, absence of summary requests
in results view, manual refresh, placeholders, failure visibility, and recovery.

### Follow-up work

- Investigate indexed scheduler summary reads to replace full task/run snapshots
  after measuring backing-store cost. The current change reduces payload size and
  fixes pagination correctness, not the cost of uncached full snapshots.
- Add a lightweight notification summary preserving assignment, pending-runtime
  deduplication, and exclusion of legacy HIL-backed approvals. Do not simply sum
  endpoint totals. The four existing approval requests are unchanged here.
- Web bootstrap currently sets the API base URL without an additional network
  round trip. Desktop initialization needs its backend port before session lookup;
  session rotation must also precede application queries. Keep this ordering until
  authenticated traces justify further changes. Ontology cache policy still needs
  measurement before changing freshness or security boundaries.
