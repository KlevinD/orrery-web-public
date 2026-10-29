# Independent public-account occurrences

The production schedule `orrery-native:821523f6-6847-464e-b46f-69b10b32f999`
uses `policy.concurrency=allow_parallel` as of 2026-10-18 19:41:47 Asia/Shanghai.
This is a persisted schedule setting applied through `PATCH /api/scheduled-tasks/{id}`,
not a global change to scheduler defaults.

Previously, an unfinished queued/running occurrence or a failed occurrence awaiting
retry caused `skip_if_running` to skip the next article. Independent occurrences
permit the next scheduled article to be dispatched with its own run identity and
configuration snapshot. Existing run, material, review and approval records remain
independent; changing concurrency neither authorizes a draft nor completes old work.

The schedule still runs daily at 09:00 Asia/Shanghai, retains its existing 5,400-second
execution timeout and retry policy, and uses the same author, permissions, tool scope,
result contract and delivery settings. Repeated scans and repeated dispatch of the
same occurrence must not start duplicate work. Manual requests retain their
idempotency keys.

`TestNonblockingAgentScheduleDispatchesNewOccurrenceWithOldActiveRun` exercises the
production service/repository/sweeper/dispatcher composition with fake external
execution: old queued, running and pending-retry states; actual next occurrence
dispatch; repeated scans; repeated dispatch; manual idempotency; preservation of the
old occurrence, reviewer identity and timeout. These are isolated tests and do not
prove real image generation or channel delivery.

The deployment evidence and the complete before/after configuration are stored in
`D:/Orrery/artifacts/公众号任务独立调度-20261018/APPLIED.json`. No API restart or
manual business run is needed for this setting. Storage/provider failures can still
fail an individual article; allowing independent occurrences does not repair those
services or guarantee immediate execution when shared worker capacity is busy.
