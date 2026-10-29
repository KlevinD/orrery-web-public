# Stage 4 Deferred Work

Stage 4 now provides the minimum usable managed-local development path: an approved Todo can receive an isolated Git worktree, the bound Agent run executes in that worktree, controlled commits and review submissions are persisted, local patch/bundle export is available, and authorized project users can inspect workspace and run state in the development project console.

The following work is intentionally deferred. These items are not implemented by placeholders or fake-success responses.

## Product Surface

- TODO: Add commit, submission, review, merge-preview, approval, merge, close, and local-export controls to the Todo detail page. The real APIs already exist; until this UI lands they are API-operated.
- TODO: Publish durable commit/submission/review/merge notices into the Todo's canonical conversation and add outbox reconciliation for interrupted delivery.
- TODO: Link the same submission state into Mission Control, including review blockers and final merge evidence.

## Source Providers

- TODO: Add real GitHub, GitLab, and Gitea pull-request adapters, provider receipts, and self-service OAuth/App installation. Current submission push and merge support is limited to the managed authoritative bare-repository path.
- TODO: Add provider webhooks and reconcile remote branch, pull-request, check, and merge state after process restarts or out-of-band changes.

## Workspace Operations

- TODO: Add a system Scheduler job for expired lease recovery, worktree cleanup, bare-repository maintenance, and orphan reconciliation.
- TODO: Add operator-visible cleanup failures, storage pressure metrics, retention controls, and retry actions.
- TODO: Enforce network allowlists at the host/container boundary. Runtime policy is passed to the Agent today, but host-level isolation remains required for hostile workloads.

## Completion Gates

- TODO: Add browser-level end-to-end coverage for approval through execution, review, merge, and Todo/conversation reconciliation.
- TODO: Complete Stage 5 long-running responsibility operations, production alerting, migration/runbook work, and the final intended-versus-implemented audit against both goal specifications.
