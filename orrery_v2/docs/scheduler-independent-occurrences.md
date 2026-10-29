# Independent scheduled occurrences

The scheduler defaults to `allow_parallel` for new tasks. The API, task form,
scheduled Mission creation, system seeds and migration catalogs share this
default. An unfinished older occurrence does not cause a later occurrence to be
skipped. Production dispatch uses `scheduler-run:<runID>` queue keys so an older
execution delivery does not serialize later occurrences behind itself.

Startup applies `independent-occurrences-v1` once, before scheduler workers start.
It atomically changes existing empty/`skip_if_running` policies to
`allow_parallel`, including disabled and archived definitions. It preserves all
other policy fields, targets, identities, execution/delivery contracts, enabled
states, schedules and next occurrence timestamps. Only concurrency and mutation
timestamps change. Runs, configuration snapshots, approvals and historical
results are not rewritten. The durable completion marker prevents subsequent
restarts from overriding an operator's later explicit policy choice.

`skip_if_running` remains an explicit opt-in for workloads that require it. Its
legacy serial queue behavior remains supported. Within either policy a run keeps
one durable dispatch identity; retries do not create another occurrence. Tenant,
creator authorization, QA, approval and business operation idempotency still
apply. Migration does not enable retired schedules or replay missed occurrences.

Independent scheduling permits overlap; worker capacity can still cause queue
delay. Storage failures, missing tool inputs and pending approvals require their
own recovery. This change does not approve, publish or declare any business
result completed.
