# Responsibility background runtime

Responsibility coordination is a server lifecycle service, `responsibility.runtime`.
It reuses the existing People reconciler, autonomy policy, approval, autoplan, and
responsibility coordinator. The Agent inference/tool execution kernel is unchanged.

Responsibility assignments, activations, runtime outcomes, policies, People
portfolios, blockers, and autoplan changes write a coalesced wakeup in the same
core database transaction. The worker checks durable wakeups every second.
Successful processing acknowledges only the versions observed before the cycle;
concurrent changes survive. Failed processing retains wakeups and backs off from
5 seconds to 1 minute. A database lease prevents overlapping service instances.
Cycles have a 1 minute budget; shutdown cancels and waits for the worker.

The coordinator's own bookkeeping suppresses recursive wakeups. Attempt heartbeat
and audit writes do not wake the service. An idle recovery cycle runs every hour
to recover missed notifications, expired leases, retries, and external People
changes. People source fetches still respect the configured reconcile interval.

Startup registers the replacement service and archives only recognized system
owned coordinator, People batch, and legacy portfolio pulse schedules. Historical
definitions and execution records remain available; startup never recreates the
old schedules. Legacy planner cron settings remain readable for configuration
compatibility but no longer control this service.

Background evaluations have their own evaluation ID and do not fabricate scheduler
run IDs, create daily evaluation occurrences, or send public scheduler reports.
Errors and counts are recorded in server logs; work and approvals remain in their
existing domain records. Actual Agent business execution and delivery retain their
normal policy and approval controls.
