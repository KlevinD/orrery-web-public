# AgentCore blocked task review

Protocol `orrery-blocked-task-review-v1` adds one host stopping review to a task turn when its executing model proposes a version 1 `orrery-agent-result` with `outcome=blocked` and model budget remains. The same model compares the task and actual tool evidence, continues authorized corrections where possible, or preserves a real blocker with its evidence and resumption condition. Ordinary chat and completed/refused results do not trigger this review.

The review uses the existing model/tool budgets, deadline, permissions, approval policy and quality requirements. It does not replay completed or uncertain writes, approve actions, publish content or rewrite the Agent's public answer. A second blocked proposal can end the turn. Checkpoint recovery and context compaction preserve the consumed allowance; a new user turn receives its own allowance.

Resolved execution evidence records the protocol and one-review limit for RSI. Observation, independent business acceptance and activation of a different mechanism remain separate. This change does not activate autonomous source changes or claim a full RSI loop.

Validation covers actual local artifact correction and verification, real blockers, exhausted budgets, tool denial, recovery, compaction and the production BaseAgent → terminal/review/case → Ontology composition. Opt-in live tests seed one premature stopping proposal and then use a real model for continuation with synthetic local artifacts; they do not replay customer tasks or send channel messages.
