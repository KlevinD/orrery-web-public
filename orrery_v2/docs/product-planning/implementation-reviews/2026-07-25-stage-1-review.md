# Stage 1 Review: Development Project Foundation

Date: 2026-07-25

Scope: PR 1-3 of `development-project-management-implementation-plan.md`, covering tenant-safe authorization, development project and SCM resource foundations, managed-local verification, and the first production UI.

## Intended-versus-implemented review

| Intended invariant | Enforcement reviewed | Result |
|---|---|---|
| Tenant identity is part of every stored and authorized object | Project, DevelopmentProject, SCMConnection, Todo, Mission records; ReBAC/OpenFGA object normalization; relation projection and backfill | Enforced server-side; ambiguous tenant and resource key segments are rejected |
| Coarse scopes never grant object access by themselves | Authorization action mapping, relation evaluator, list/get/mutation services, HTTP viewer construction | Enforced; list filters unauthorized objects and guessed IDs return not found |
| Development project membership and repository changes are concurrent and replay safe | Aggregate versions, binding/member versions, compare-and-swap storage, mutation receipts, pending-effect reconciliation | Enforced; retries do not repeat provider calls or relation/audit effects |
| Repository use requires both project authority and connection use | Binding service resolves the connection through `scm_connection.use` before persisting | Enforced and covered by denial tests |
| Secrets are references and never returned by project/connection APIs | Secret provider validation, domain-to-DTO mapping, OpenAPI and frontend types | Enforced; project policy exposes only allowed-secret count and connection DTO omits `SecretRef` |
| Only production-backed providers appear usable | Provider capability checks and initial connection status | Unsupported providers persist as `error`; they cannot be used or verified until an adapter is installed |
| Persisted effects are safe to execute after restart | Aggregate decoding validates domain values, owners, repository invariants, receipt shapes and effect payloads | Enforced; unknown effect kinds and verification failure categories fail closed |
| Archived projects cannot start new execution or mutate resources | Development project service status guards and capabilities | Enforced and tested |
| UI is capability driven and responsive | Development project list/detail, six tabs, SCM settings, mutation controls, desktop/mobile Playwright checks | Enforced; read-only users do not fetch connection lists or see mutation controls |

## Review findings and remediation

1. Failed repository verification replay originally reconstructed every provider failure as a repository-policy error. A stable failure category is now persisted and validated, so the first response and replay preserve the same `errors.Is` and HTTP category without repeating provider calls.
2. DevelopmentProject and SCMConnection decoding originally checked identity columns but accepted invalid aggregate values and arbitrary pending-effect payloads. Decoding now validates status, policy, membership ownership, repository uniqueness, receipt result shapes, effect kinds and audit identity before reconciliation can execute them.
3. Legacy Project IDs allowed path separators, and list filters accepted unsafe organization/status values. Project service and repository validation now use the same bounded, control-free key rules as the new development resources.
4. Connections for providers without a production verifier were initially marked active. Creation and credential rotation now leave unsupported providers in error state; runtime capabilities remain unavailable.
5. Pending-effect catalog scans could repeatedly select the same failing first page. Reconciliation now serializes each service scan and advances a rotating cursor, including across failures.

## Production wiring review

- Development project, SCM connection, managed-local verifier, authorization projector, audit recorder, secret validator and pending-effect reconcilers are assembled in the server production path.
- Pending effects reconcile immediately at process start and then on the platform sweeper interval; this sweeper is infrastructure recovery, not Todo scheduling.
- API handlers require real authentication context, `If-Match` versions and `Idempotency-Key` values for mutations.
- No demo route, fake-success adapter, placeholder service or mock production facade was introduced.

## Verification

- `go test ./... -count=1 -timeout 15m`
- `npm run check`
- `npm run test`
- `npm run build`
- `npx playwright test e2e/development-projects.spec.ts --workers=1`
- `git diff --check`

The Stage 1 review is complete with no accepted unresolved authorization, tenant-isolation, concurrency or production-wiring findings.
