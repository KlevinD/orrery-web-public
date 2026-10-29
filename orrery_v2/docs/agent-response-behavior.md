# Agent response behavior

The platform prompt includes two editable blocks: `agent_working_instructions`
and `response_guidance`. Saved standard platform templates gain the missing
blocks when read or normalized. Explicitly disabled blocks stay disabled;
custom minimal templates and native CLI context-only prompts remain explicit.

Ordinary agents receive their configured BaseAgent working instructions and
runtime instructions, with shared prefixes deduplicated. Personal assistants
use their independent persona rather than organization role instructions.
Native CLI sessions also receive response guidance during bootstrap; personal
assistant bootstrap revision changes invalidate incompatible resumed sessions.

Response depth follows the user's request and task complexity. Diagnosis,
research and planning should explain evidence, implications, conditions and
unfinished work. Greetings can remain short. There is no minimum word count,
required chapter layout or generated notification template. Agents that use a
public reply fence must put the complete public answer inside it.

Chat and scheduled result output preserve public HTTP(S) citations and governed
attachment links. Private reasoning, execution manifests, local file paths,
private network targets and links containing credential parameters remain
filtered. Public link targets must survive local path redaction intact.

System instructions and the current request remain complete during context
budgeting. Optional old history is removed first. Required content that exceeds
the configured budget is rejected before model execution rather than silently
rewritten. Tool call/result envelopes remain paired. Background history notices
are limited to three whole messages totaling at most 1024 estimated tokens;
durable conversation history is not changed.

Validation includes prompt migration, personal assistant isolation, disabled
blocks, context integrity, tool envelopes, long public replies and citations,
notice budgeting, production scheduled delivery and optional isolated real
model comparisons. A small model comparison checks behavior but does not prove
an improvement in average answer quality or length. AgentCore uses its own
working instructions; it does not copy the OpenClaw system prompt.
