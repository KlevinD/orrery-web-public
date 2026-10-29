# Stage 0 Infrastructure Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Stage 0 `internal/platform/*` foundation for Orrery V2 under `orrery_v2`, reusing suitable Orrery and Multica infrastructure code where it fits the target module boundaries.

**Architecture:** The new Go module lives directly under `orrery_v2`. Stage 0 creates only platform packages and test fixtures; no Agent, Mission, Task, Discussion, QA, RuntimeAdapter, transport, or frontend behavior is introduced. Existing Multica / Orrery implementations may be migrated or adapted when they provide clean platform behavior without importing old business concepts.

**Design Principle:** Elegance first. Decoupling is not package splitting for its own sake; it must make business modules more flexible, implementation more robust, and code easier to read. Before implementing any Stage 0 capability, compare Multica and original Orrery first. If a mature implementation can be copied or moved and then adjusted, migrate it. If not, prefer mature standard-library or open-source packages before writing custom infrastructure.

**Tech Stack:** Go 1.25, standard library and mature packages first, `modernc.org/sqlite` for SQLite-backed DB fixtures and stores.

---

## File Structure

Stage 0 creates these packages under `internal/platform`:

- `logging`: structured logger interface, fields, context correlation id helpers, JSON/text slog adapter, test logger.
- `config`: typed platform configuration loaded from defaults, env maps, optional files, and overrides.
- `secret`: secret provider interface, in-memory provider, redacted secret values.
- `clock`: real and fake clocks.
- `idgen`: random and deterministic ID generators.
- `db`: SQLite database opener, ping, close, and migration readiness helpers.
- `migration`: ordered migration runner with `schema_migrations`.
- `uow`: transaction wrapper for atomic work.
- `eventstore`: generic event envelope persistence with stream version and idempotency.
- `outbox`: durable job lifecycle with claim, retry, and dead letter.
- `queue`: job queue interface and in-memory fake/job runner.
- `filestore`: local file artifact store with checksum and content metadata.
- `lock`: in-memory expiring lock.
- `claim`: lease store and negative claim cache.
- `liveness`: heartbeat store with DB fallback semantics represented by interface behavior.
- `sweeper`: bounded sweeper runner.
- `metrics`: bounded non-blocking metric sink.
- `tracing`: trace id context helpers and no-op tracer.
- `health`: live/ready checks with TTL cache.
- `testkit`: reusable fixtures for later goals.

## Tasks

### Task 1: Module Skeleton

- [ ] Create `go.mod`.
- [ ] Run `go mod tidy`.
- [ ] Verify `go test ./...` discovers the empty module.

### Task 2: Logging, Config, Secret, Clock, ID

- [ ] Write failing tests for structured logging context, env-free config loading, secret redaction, fake clock, and deterministic ID generator.
- [ ] Implement minimal packages to pass those tests.
- [ ] Run targeted tests and `go test ./internal/platform/...`.

### Task 3: DB, Migration, UOW, Health

- [ ] Write failing tests for isolated test DB, migration readiness, successful transaction commit, rollback on error, live/ready split.
- [ ] Implement SQLite DB open/close/ping, migration runner, transaction wrapper, and health checks.
- [ ] Run targeted tests and `go test ./internal/platform/...`.

### Task 4: EventStore, Outbox, Queue

- [ ] Write failing tests for append/load, stream version conflict, idempotency key, outbox lifecycle, atomic queue claim, fake queue/job runner.
- [ ] Implement SQLite-backed stores and fake queue.
- [ ] Run targeted tests and `go test ./internal/platform/...`.

### Task 5: FileStore, Lock, Claim, Liveness, Sweeper

- [ ] Write failing tests for artifact refs, checksum validation, expiring locks, lease recovery, negative claim cache versioning, liveness fallback, bounded sweeper behavior.
- [ ] Implement stores and test fixtures.
- [ ] Run targeted tests and `go test ./internal/platform/...`.

### Task 6: Metrics, Tracing, Testkit, BDD Audit

- [ ] Write failing tests for non-blocking bounded metrics, trace id helpers, and testkit fixture creation.
- [ ] Implement metrics/tracing/testkit.
- [ ] Run `go test ./...`.
- [ ] Map every `stage-0-bdd.md` scenario to tests or documented evidence.
