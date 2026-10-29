"""Expose canonical receipt identities without trusting the model's execution labels."""

import hashlib
import json
import os
from pathlib import Path
import tempfile


def receipt_prompt(directory):
    return ('\nCanonical tool receipt ledger: ' + json.dumps(str(Path(directory) / 'runtime-tool-receipts.json'))
            + '. After verification, read this ledger and programmatically copy the completed verification command\'s '
              'tool_call_id into verification_tool_ids. Chunk ID, process/session IDs and tool labels '
              'are not receipt identities. The dispatcher refreshes this file on its existing polling '
              'cycle; if your completed command is not listed, wait up to 90 seconds and read it again. '
              'Do not repeat the repair, tests or business action merely to obtain an ID. If a receipt '
              'remains unavailable, report needs_attention. This ledger helps select IDs; the dispatcher '
              'independently verifies the original trace and does not trust this file as proof.\n')


def publish_receipts(adapter, active, command, turn, run):
    if adapter.config.get('direct_runtime') != 'codex':
        return
    # The caller has already validated the command, tenant, agent and source turn.
    # RunIDs may only be projected at completion, so consult the bound live runs.
    if not run:
        from direct_repair_recovery import runs_for_turn
        runs = runs_for_turn(adapter, command)
        runs = [row for row in runs if row.get('ConversationID') == command['ConversationID']
                and row.get('TurnID') == turn['TurnID'] and row.get('AgentID') == adapter.config['agent_id']]
        run = max(runs, key=lambda row: (row.get('CreatedAt', ''), row.get('RunID', '')), default={})
    if not run.get('RunID'):
        return
    if (run.get('ConversationID') != command['ConversationID'] or run.get('TurnID') != turn['TurnID']
            or run.get('AgentID') != adapter.config['agent_id']):
        raise ValueError('receipt run binding mismatch')
    from codex_repair_worker import redact
    tools = []
    for tool in (run.get('ToolCalls') or [])[-400:]:
        if not isinstance(tool, dict) or not isinstance(tool.get('tool_call_id'), str) or not tool['tool_call_id']:
            continue
        item = {key: str(tool.get(key) or '')[:512] for key in ('tool_call_id', 'name', 'status')}
        arguments = tool.get('arguments') or {}
        if isinstance(arguments, dict) and isinstance(arguments.get('command'), str):
            command_text = redact(arguments['command'])
            item['command'] = command_text[:512]
            item['command_tail'] = command_text[-512:]
            item['command_sha256'] = hashlib.sha256(arguments['command'].encode()).hexdigest()
        tools.append(item)
    document = {'conversation_id': command['ConversationID'], 'turn_id': turn['TurnID'],
                'run_id': run['RunID'], 'tools': tools}
    directory = Path(active['directory'])
    fd, temporary = tempfile.mkstemp(prefix='.runtime-tool-receipts-', dir=directory)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as output:
            json.dump(document, output, ensure_ascii=False, indent=2)
        os.replace(temporary, directory / 'runtime-tool-receipts.json')
    finally:
        Path(temporary).unlink(missing_ok=True)
