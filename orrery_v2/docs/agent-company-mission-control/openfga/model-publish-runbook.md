# OpenFGA Authorization Model Runbook

## Source Of Truth

- Model file: `docs/agent-company-mission-control/openfga/authorization-model.fga`
- Runtime config key: `authz.openfga.authorization_model_id` in the selected YAML profile
- Rollout evidence:
  - `authorization.openfga_shadow`
  - `authorization.relation_backfill`
  - `authorization.enforce_transition`
  - `GET /api/authorization/relation-backfill/diagnostics`

## Publish

1. Review the model diff in source control. Do not edit the model directly in the OpenFGA console.
2. Publish the model to the target OpenFGA store with the existing deployment workflow or OpenFGA CLI/API.
3. Record the returned authorization model id in deployment config:

```yaml
authz:
  openfga:
    authorization_model_id: <published_model_id>
```

4. Start in shadow mode:

```yaml
authz:
  mode: openfga_shadow
  relation_backfill:
    mode: dry_run
```

5. Confirm readiness evidence:

```text
authorization.enforce_transition shadow_mismatches=0 backfill_validated=true
authorization.relation_backfill diff_missing=0 diff_errors=0
```

Also inspect `authorization.enforce_transition` fields `shadow_object_types` and `shadow_actions`; every object domain and action planned for enforce should have zero mismatches and zero primary/shadow errors.

6. Enable production enforce only after the evidence is clean:

```yaml
authz:
  mode: openfga_enforce
  openfga:
    enforce_ready: true
```

7. Keep relation backfill in `write` mode after enforce unless OpenFGA itself is being rolled back.

## Rollback

- Authorization rollback: set YAML `authz.mode` to `openfga_shadow` or `local`.
- Model rollback: restore the previous YAML `authz.openfga.authorization_model_id`.
- Keep relation writers and backfill enabled during rollback unless OpenFGA itself is unavailable; this preserves tuple continuity for the next rollout attempt.

## Compatibility Checks

- The model must define both `user` and `agent`. Runtime tuple writers produce both subject kinds.
- Mission participants and direct resource operators must allow `[user, agent]`.
- `discussion` depends on `mission` relations and also receives direct `owner` / `member` tuples from lifecycle writes and backfill.
- `role`, `organization`, and `department` now have tuple writers, backfill coverage, operations API object checks, and strict write idempotency for `organization#org role` tuples. Do not enable enforce until shadow mismatch evidence is clean for the target OpenFGA deployment.
