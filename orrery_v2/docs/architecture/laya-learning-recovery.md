# Laya learning review and recovery advice

The existing bounded RSI observer persists complete authorized failure evidence
and an asynchronous decision assessment. This integration consumes those records
in two read paths:

- `rsi_learning_plan`, bound to ordinary non-human Agents, reads only the
  server-selected tenant and Agent. It groups one cursor page by tool/error code,
  retains every observation, and prioritizes repeated failures and review-worthy
  cases. Counts describe the current page, not global recurrence. The Agent must
  follow `next_cursor` to read the rest. Session-only Agents are excluded.
- An authorized Mission operational view reads at most four failed/stale
  sessions with a shared 250 ms budget. It shows up to four evidence-linked
  suggestions in a separate section. Advice failures do not fail the view.

An intact, non-truncated assessment with no fallback and selected-choice
probability at least 0.85 can select a review direction. This is a conservative
heuristic, not calibrated correctness. Labels remain hypotheses. Missing,
uncertain or invalid assessments request more evidence. Recorded unknown tool
outcomes always require downstream reconciliation before considering another
call. A stale heartbeat alone does not prove that business execution failed.

Original inputs, outputs and errors stay in the existing protected sample store;
neither tool output nor Mission advice includes raw evidence. New run index rows
are derived from durable samples and validated again when read. Existing samples
are available in learning plans immediately; historical samples without the run
index become available to Mission advice when saved again. An indexing failure
does not delete the already persisted source sample.

This release provides a prioritized review input and visible investigation
suggestions. It does not install an autonomous training worker, invoke retries,
change skill versions, bypass evaluation/publication gates, or enable memory
filtering. Task success improvement and classification accuracy require separate
evaluation on reviewed production cases. An isolated live canary verifies the
model-to-record-to-advice integration, not diagnostic accuracy.
