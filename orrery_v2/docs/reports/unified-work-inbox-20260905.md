# Unified work inbox

## Entry points

- Primary navigation: /work-items
- Homepage focus view: /
- Right context sidebar: Work inbox
- Previous Todo/Idea/Mission catalog: /work-items?view=catalog

## Counting rules

All three inbox surfaces use the same WorkInbox component and identity-scoped
query cache. Pending counts are calculated from the complete paginated list,
not the sidebar preview size. Any failed source replaces the count with an
unavailable indicator instead of reporting a partial total.

Pending includes open personal todos, visible pending approvals, assigned HIL
decisions/council participation, and the latest attention-required schedule
outcome. Explicit todo/approval references merge their row while retaining each
approval action. Similar titles or matching approval materials do not imply
that independent decisions are identical.

Results are grouped by scheduled task. The latest result determines the unread
indicator; earlier runs remain accessible through expandable history. Reading
a failed report does not resolve it. A newer successful result supersedes a
historical failure. System-maintenance runs remain excluded by the existing API.

The completed tab explicitly contains completed/canceled todos. Approval and
council decision history remains in the existing approval center; this release
does not claim to aggregate that history.

## Verification

- Svelte check: zero errors and warnings.
- Production build completed.
- Focused tests cover pagination beyond 100 rows, explicit-reference grouping,
  independent approvals, recovered failures, read versus resolved semantics,
  permissions, unavailable counts, and shared main/sidebar requests.
- Production-preview browser checks at 1440px and 390px verify matching counts,
  report grouping/history expansion, and no horizontal overflow.
- Browser checks use mocked API fixtures, not production account data.

## Deployment scope

Frontend assets only. Preserve the active ECS backend and database; no Docker,
database migration, or API restart is required. Retired markup is retained in
web/legacy-backup as non-executable rollback references.

## Remaining constraints

Aggregation currently uses the existing paginated APIs, with a shared cache and
60-second foreground refresh. Historical result retrieval still grows with data
volume; a server-side grouped/summary endpoint is a separate performance follow-up.
The client fails explicitly beyond its pagination safety limit instead of
silently presenting truncated counts.
