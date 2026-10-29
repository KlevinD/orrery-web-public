# Dependency recovery and storage capacity

Infrastructure recovery is separate from an Agent's repair verdict, completion
of an interrupted business operation, publication of an Agent report, and
delivery through an IM provider (ORR-REPAIR-01, ORR-DELIVERY-01/02,
ORR-REPLY-01, ORR-STORAGE-01).

The existing maintenance loop runs a bounded dependency check at most once per
30 seconds, without creating scheduled tasks or invoking an LLM. It considers
only server-captured infrastructure incidents from the preceding 24 hours,
after two minutes without a new error. PostgreSQL connection/startup failures
are bound to the exact configured host, port and database and use the existing
rollback-only write probe. Knowledge catalog HTTP 500 failures require the
latest complete captured cause and successful queries of every failed folder.
Missing or truncated evidence leaves the incident unresolved.

Two successful observations at least 30 seconds apart are required. Failed
checks, a gap exceeding 90 seconds, or changed incident/target bindings reset
stabilization. A recovery proof binds the tenant, incident, last failure time,
occurrence count, exact targets and verification times. Proof and status update
commit together under an incident version check; existing reliability events
continue to update the Problem Hub and Ontology. Quarantine, business execution
failures and provider send rejections do not qualify for these checks. Recovery
does not manufacture a successful Agent repair run or replay old business work.

Notifications group PostgreSQL startup/connection failures by tenant and server
instance within a two-minute failure window. Original incident IDs and repair
histories are retained. Source cursors still use the original ledger positions;
individual queued notices are rejected by the existing source validator when
superseded by a group. Report text remains authored by the personal assistant
under the existing owner access and delivery checks. Component counts are
cumulative historical counts, not a sum of failures in the current outage.

Storage samples distinguish all free bytes, service-account available bytes,
reserved/unavailable bytes, inode availability and sampling time. Available
space below 1 GiB or 5%, or inode availability below 1%, produces a warning;
below 256 MiB or exhausted inodes produces an error. Low capacity does not mean
the disk is full and does not disable business work. Actual ENOSPC and disk I/O
captures include a timestamped snapshot of the configured artifact filesystem;
this is not a claim that a remote database uses that filesystem. Database write
verification is independent. Historical snapshots are retained separately from
current capacity samples and superseded repair progress.

Production staging must reserve space for upload, decompression, validation and
the retained release. Verified temporary test binaries and extracted validation
sources are removed after validation/deployment; active releases, business data
and rollback artifacts are retained.
