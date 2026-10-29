# Orrery Identity Access Stack

This overlay wires the v2 authentication and authorization code to self-hosted
Kratos, Hydra, and OpenFGA without replacing the app session, canonical People
identity mapping, or local break-glass path from earlier phases.

Design source: `docs/agent-company-mission-control/identity-access-three-phase-roadmap.md`.

## Services

- `kratos` owns internal password credentials and the Feishu OIDC relation for
  People-projected accounts. Its Admin API has no host port and is reachable by
  Orrery only on `kratos-control`.
- `hydra` is the OAuth2/OIDC issuer. It redirects browser login and consent to
  Orrery API routes:
  - `${ORRERY_APP_PUBLIC_URL}/api/oauth2/login`
  - `${ORRERY_APP_PUBLIC_URL}/api/oauth2/consent`
- `orrery-api` validates OIDC bearer tokens through Hydra introspection and
  still emits the same internal `Principal` used by cookie sessions.
- `openfga` is available for `openfga_shadow` / `openfga_enforce` authorization
  modes. The production YAML keeps Orrery in `local` authz mode until a store and
  authorization model have been published.

Internal deployments set `auth.identity.mode: people_sso` and configure the
Kratos public, callback, and private Admin URLs in the selected YAML. Standalone
deployments may leave Kratos disabled. Local login is disabled by default; set
`auth.local.enabled: true` only for controlled bootstrap or break-glass access.
TOTP is disabled by default. Set `auth.local.superadmin_mfa_enabled: true` and configure its TOTP secret
to require a second factor only for the account bound to the
`human-superadmin` profile. Other local accounts are unaffected. A
username equal to `superadmin` is not, by itself, a break-glass identity. An
external superadmin must be administrator-provisioned and explicitly bound to
`human-superadmin`; production auto-link remains disabled.

## Run

Create `.env` from the repository's `.env.example`, fill every blank secret,
and configure the remaining non-sensitive `auth` / `authz` values in
`config/orrery.production.yaml`. The Feishu app ID and secret live together in
that one `.env` because both Orrery API and Kratos consume the same provider
registration:

```bash
docker compose \
  --env-file .env \
  --env-file config/build.env \
  --env-file deploy/releases/local.env \
  -f docker-compose.yml \
  -f deploy/ory/docker-compose.identity.yml \
  up -d kratos-postgres kratos-migrate kratos hydra-postgres hydra-migrate hydra openfga-postgres openfga-migrate openfga orrery-api
```

When running from the repository root compose, keep the same overlay path:

```bash
docker compose \
  --env-file .env \
  --env-file orrery_v2/config/build.env \
  --env-file orrery_v2/deploy/releases/local.env \
  -f docker-compose.yml \
  -f orrery_v2/deploy/ory/docker-compose.identity.yml \
  up -d kratos-postgres kratos-migrate kratos hydra-postgres hydra-migrate hydra openfga-postgres openfga-migrate openfga orrery-api
```

The parent and `orrery_v2` `.env.example` files already set the appropriate
`ORRERY_V2_ROOT` for their compose working directory.

Do not expose Kratos Admin, Hydra Admin, or OpenFGA directly to the public
internet. The overlay publishes only Kratos Public, Hydra Public, and OpenFGA
HTTP on localhost by default for ingress or controlled testing.

## Hydra Clients

Set `auth.hydra.client_sync_enabled: true` in the selected YAML profile and register clients through
`POST /api/oauth2/clients`. The example payloads in
`oauth2-clients.example.json` include redirect URIs for desktop, CLI, agent
runtime, and external integrations.

The API syncs each client into Hydra first and stores only non-sensitive client
metadata in the v2 registry. Hydra-generated `client_secret` is returned only by
the save response.

## OpenFGA Rollout

1. Publish `docs/agent-company-mission-control/openfga/authorization-model.fga`
   using `docs/agent-company-mission-control/openfga/model-publish-runbook.md`.
2. Write `authz.openfga.store_id` and
   `authz.openfga.authorization_model_id` into `config/orrery.production.yaml`.
3. Set `authz.mode: openfga_shadow` and
   `authz.relation_backfill.mode: dry_run` in that YAML file.
4. Check `authorization.openfga_shadow`,
   `authorization.relation_backfill`, and
   `authorization.enforce_transition` readiness reports.
5. Only after mismatch and diff counters are clean, set
   `authz.openfga.enforce_ready: true` and switch
   `authz.mode: openfga_enforce`.

`openfga_enforce` is all-or-nothing and fail closed. Legacy partial object
domain rollout and fail-open settings are rejected at startup; rollback by
switching `authz.mode` back to `openfga_shadow` or `local` and restarting the API.
In write mode, startup backfill and the runtime reconciliation sweeper both
compare direct tuples, write only missing tuples, and prune stale managed tuples.

## Role Scope Cutover

`authz.role_scope.mode: authoritative` in the production YAML is the secure default. The
`authorization.role_scope_transition` readiness check reports how many enabled
human principals and scopes still depend on account-level permissions.

Use `hybrid` only as an explicit, temporary migration fallback while missing
Operations role assignments are repaired. In authoritative mode, effective role
permissions are the source of truth and readiness fails if any non-break-glass
human still depends on legacy account scopes. Break-glass scope retention is
bound only to profile ID `human-superadmin`; the username is not trusted as an
authorization identity. Return to `authoritative` when `transition_ready=true`.
