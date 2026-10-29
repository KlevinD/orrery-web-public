# Formal task artifact byte verification

This increment extends ORR-RSI-01, ORR-WORK-01, ORR-TRACE-01 and ORR-ONTOLOGY-01.
QA acceptance and artifact byte verification are separate facts. Neither proves
causal improvement, full task quality, channel delivery or a ready RSI baseline.

The host materializer records full SHA-256 and byte size. New runtime output and
workspace copies use tenant/run scoped content keys. Existing scheduled immutable
URIs remain supported when their tenant and embedded size/digest match. Arbitrary
URLs, local paths, legacy unscoped runtime keys, tool logs and approval/human
records are not fetched or silently converted into verified file evidence.

Formal submissions retain exact child record addresses. The original submission
transaction captures a hash-only manifest of those rows and artifact references.
It rolls back with the business write. Legacy submissions without exact addresses
retain their domain behavior and are not scanned or historically backfilled.

The existing improvement sweeper reconciles manifests after original runtime
terminal/business association. It rereads exact source rows, checks their hashes,
then streams bounded bytes from the host file store and checks digest and size.
Each manifest uses at most 64 MiB of bytes, 128 items and three durable attempts;
a sweep handles at most eight manifests with a 30-second context. Empty manifests
and unsupported references remain partial. Content mismatch is rejected; temporary
storage failures retry after bounded delays. Durable cursors prevent delayed or
invalid entries from starving later work. Source mutation fails verification and
invalidates read-only inspection of affected old receipts.

Immutable verification receipts and their projection outbox commit atomically.
An ontology `improvement_artifact_verification` node has real links to the
original case and business assessment. Failed projection remains queued.
`resourcectl improvements --tenant ... --attempt ...` reports supplemental
`artifact_verifications`; historical runtime reviews/business assessments remain
unchanged, including their original blockers and `baseline_ready=false`.

The verification is an observation of bytes at a recorded time, not a promise of
permanent future availability. Raw artifact bodies, names, URIs and user reports
stay out of the RSI manifest/verification ledger. Frozen nine-domain baselines,
independent experiments proving business gain, candidate adoption and production
activation/observation/rollback still require separate implementation and proof.
