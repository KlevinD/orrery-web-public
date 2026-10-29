"""Recover a repair deliverable after its conversation finalization was interrupted."""

import hashlib
import json
from pathlib import Path
import time

from codex_repair_worker import redact, sql_text, timestamp

TERMINAL = ('completed', 'failed', 'cancelled')


def runtime_failure_event(adapter, command, run):
    """Read the canonical failure even when runtime startup wrote no trace."""
    if not run or run.get('Status') != 'failed':
        return None
    rows = adapter.reader.rows('orrery_core',
        "SELECT (payload_json::jsonb->>'payload')::jsonb AS p FROM public.platform_records "
        "WHERE table_name='discussion_session_events' "
        "AND (payload_json::jsonb->>'payload')::jsonb->>'ConversationID'=%s "
        "AND (payload_json::jsonb->>'payload')::jsonb->>'Kind' IN "
        "('discussion_runtime_failed','discussion_agent_mention_failed') "
        "AND (payload_json::jsonb->>'payload')::jsonb->'Metadata'->>'run_id'=%s "
        "AND (payload_json::jsonb->>'payload')::jsonb->'Metadata'->>'turn_id'=%s "
        "ORDER BY (payload_json::jsonb->>'payload')::jsonb->>'CreatedAt' DESC,record_key DESC LIMIT 2" %
        (sql_text(command['ConversationID']), sql_text(run['RunID']), sql_text(command['TurnID'])))
    for row in rows:
        event = row['p']
        metadata = event.get('Metadata') or {}
        agent = metadata.get('agent_id') or metadata.get('target_agent_id')
        if (event.get('ConversationID') != command['ConversationID'] or event.get('Status') != 'failed' or
                event.get('Kind') not in ('discussion_runtime_failed', 'discussion_agent_mention_failed') or
                metadata.get('run_id') != run['RunID'] or metadata.get('turn_id') != command['TurnID'] or
                agent != adapter.config['agent_id'] or not event.get('EventID') or not event.get('Summary')):
            continue
        return {'status': 'failed', 'summary': redact(event['Summary'])[:1500],
                'event_id': event['EventID'], 'source': 'canonical_runtime_event'}
    return None


def run_with_terminal_trace(adapter, active, command, run):
    if not run or run.get('Status') not in ('completed', 'failed') or run.get('TraceRef'):
        return run
    rows = adapter.reader.rows('orrery_core',
        "SELECT tenant_id,run_id,trace_ref,trace_version,status,terminal_status "
        "FROM public.agent_runtime_traces WHERE tenant_id=%s AND run_id=%s AND trace_version=2" %
        (sql_text(active['tenant']), sql_text(run['RunID'])))
    if not rows:
        return run
    if len(rows) != 1:
        raise ValueError('ambiguous terminal repair trace index')
    index = rows[0]
    if (index.get('tenant_id') != active['tenant'] or index.get('run_id') != run['RunID'] or
            index.get('trace_version') != 2 or index.get('status') != 'hot' or
            not trace_status_matches_run(index.get('terminal_status'), run['Status'], 2)):
        raise ValueError('terminal repair trace index binding mismatch')
    # The index is the stored reference; never reconstruct a missing file path.
    reference = index.get('trace_ref', '')
    prefix = '/uploads/discussion-conversations/%s/runs/%s/' % (command['ConversationID'], run['RunID'])
    if not isinstance(reference, str) or not reference.startswith(prefix):
        raise ValueError('terminal repair trace belongs to another conversation or run')
    return dict(run, TraceRef=reference)


def trace_status_matches_run(status, run_status, version):
    # Discussion turns normalize runtime timeouts to failed. Preserve the more
    # specific trace diagnosis without treating it as a completed execution.
    return status == run_status or (version == 2 and status == 'timeout' and run_status == 'failed')


def terminal_outcome(trace):
    outcome = {'status': trace.get('status'), 'error_code': trace.get('error_code', '')}
    message = trace.get('error_message')
    if isinstance(message, str) and message.strip():
        outcome['error_message'] = redact(message)[:1500]
    return outcome


def runs_for_turn(adapter, command):
    rows = adapter.reader.rows('orrery_core',
        "SELECT (payload_json::jsonb->>'payload')::jsonb AS p FROM public.platform_records "
        "WHERE table_name='discussion_session_runs' "
        "AND (payload_json::jsonb->>'payload')::jsonb->>'ConversationID'=%s "
        "AND (payload_json::jsonb->>'payload')::jsonb->>'TurnID'=%s ORDER BY record_key" %
        (sql_text(command['ConversationID']), sql_text(command['TurnID'])))
    return [row['p'] for row in rows]


def recover_terminal_snapshot(adapter, active, command, conversation, turn, snapshot):
    # RunIDs is a projection written at turn completion and can be absent after
    # an interruption. The persisted runs still retain their original turn IDs.
    runs = runs_for_turn(adapter, command)
    if not runs or any(not run.get('RunID') or not timestamp(run.get('CreatedAt')) or
                       run.get('ConversationID') != command['ConversationID'] or
                       run.get('TurnID') != command['TurnID'] or
                       run.get('AgentID') != adapter.config['agent_id'] or
                       run.get('Status') not in TERMINAL for run in runs):
        return snapshot
    if conversation.get('ActiveTurnID') not in (None, '', command['TurnID']):
        return snapshot
    run = max(runs, key=lambda row: (timestamp(row.get('CreatedAt')), row.get('RunID', '')))
    now = time.time()
    ended = timestamp(run.get('CompletedAt'))
    # Let the ordinary writer finish first; elapsed time alone never proves completion.
    if not ended or ended > now - 60 or ended < timestamp(run.get('StartedAt') or run.get('CreatedAt')):
        return snapshot
    source = adapter.record('discussion_session_messages', command['MessageID']) or {}
    from direct_repair import DISPATCHER
    if (source.get('ConversationID') != command['ConversationID'] or
        source.get('TurnID') != command['TurnID'] or source.get('AuthorID') != DISPATCHER or
        source.get('Role') != 'user' or
        source.get('Content', '').strip() not in {
            (active['directory'] / 'prompt.txt').read_text(encoding='utf-8').strip(),
            adapter.dispatch_content(active).strip()}):
        raise ValueError('terminal repair source message does not match its assignment')
    if recovery_is_busy(adapter, command, runs):
        return snapshot
    # Cancellation is a durable canonical outcome even when a killed runtime
    # never wrote its terminal artifact. It can only release work as attention.
    run = run_with_terminal_trace(adapter, active, command, run)
    without_trace = run['Status'] == 'failed' and not run.get('TraceRef')
    trace = {}
    if run['Status'] != 'cancelled' and not without_trace:
        try:
            trace = read_terminal_trace(adapter.config['artifact_root'], command, run)
        except FileNotFoundError:
            # Canonical completion plus expired leases can release the queue to
            # attention. Missing evidence can never establish repair success.
            without_trace = True
    # A concurrent retry can replace the command/active turn while we read files.
    if adapter.command(active) != command or adapter.record('discussion_session_turns', command['TurnID']) != turn:
        return snapshot
    current = adapter.record('discussion_sessions', command['ConversationID']) or {}
    if current != conversation or runs_for_turn(adapter, command) != runs or recovery_is_busy(adapter, command, runs):
        return snapshot
    if run['Status'] == 'cancelled' or without_trace:
        recovered = dict(snapshot)
        recovered.update(trace_ref=run.get('TraceRef', ''),
                         run={'status': run['Status'], 'runtime_run_id': run['RunID']},
                         result_source=('canonical_failure' if run['Status'] == 'failed' else 'canonical_terminal_without_trace') if without_trace else 'canonical_cancellation',
                         recovery={'conversation_status': turn.get('Status'), 'completed_at': run['CompletedAt']})
        recovered.pop('messages', None)
        recovered.pop('error', None)
        return recovered
    result = trace.get('result') or {}
    summary = result.get('Summary') or {}
    recovered = dict(snapshot)
    recovered.update(trace_ref=run['TraceRef'],
                     run={'status': run['Status'], 'runtime_run_id': run['RunID']},
                     result_source='terminal_trace',
                     recovered_result=summary.get('Text', '') if isinstance(summary, dict) else '',
                     recovered_blocker=bool(result.get('Blocker')),
                     terminal_outcome=terminal_outcome(trace),
                     recovery={'conversation_status': turn.get('Status'), 'completed_at': run['CompletedAt']})
    # Never mix a recovered trace with stale messages belonging to a prior attempt.
    recovered.pop('messages', None)
    recovered.pop('error', None)
    return recovered


def recovery_is_busy(adapter, command, runs):
    identity = 'send_message\0system\0orrery-scheduled-repair\0' + command['IdempotencyKey']
    claims = ['discussion-turn:' + command['TurnID'],
              'discussion-command:command-' + hashlib.sha256(identity.encode()).hexdigest()]
    claims.extend(run['RunID'] for run in runs)
    rows = adapter.reader.rows('orrery_runtime',
        "SELECT run_id FROM public.platform_claim_leases WHERE run_id IN (%s) "
        "AND expires_at > (EXTRACT(EPOCH FROM clock_timestamp())*1000000000)::bigint" %
        ','.join(sql_text(key) for key in claims))
    if rows:
        return True
    pending = adapter.reader.rows('orrery_core',
        "SELECT record_key FROM public.platform_records WHERE table_name='discussion_session_events' "
        "AND (payload_json::jsonb->>'payload')::jsonb->>'ConversationID'=%s "
        "AND (payload_json::jsonb->>'payload')::jsonb->>'Kind'='discussion_runtime_approval_requested' "
        "AND COALESCE((payload_json::jsonb->>'payload')::jsonb->>'Status','') "
        "NOT IN ('approved','rejected','cancelled','expired','resolved')" % sql_text(command['ConversationID']))
    return bool(pending)


def read_terminal_trace(artifact_root, command, run):
    root = Path(artifact_root).resolve()
    prefix = '/uploads/discussion-conversations/%s/runs/%s/' % (command['ConversationID'], run['RunID'])
    reference = run.get('TraceRef', '')
    if not reference.startswith(prefix):
        raise ValueError('terminal repair trace belongs to another conversation or run')
    path = (root / reference.removeprefix('/uploads/')).resolve()
    expected = (root / prefix.removeprefix('/uploads/')).resolve()
    if not expected.is_relative_to(root) or not path.is_relative_to(expected) or path.name != 'runtime-trace.json':
        raise ValueError('invalid terminal repair trace path')
    if path.stat().st_size > 16 * 1024 * 1024:
        raise ValueError('terminal repair trace exceeds size limit')
    trace = json.loads(path.read_text(encoding='utf-8'))
    if not isinstance(trace, dict):
        raise ValueError('terminal repair trace must be an object')
    if (trace.get('run_id') != run['RunID'] or trace.get('agent_id') != run['AgentID'] or
        not trace_status_matches_run(trace.get('status'), run['Status'], trace.get('trace_version')) or
        not timestamp(trace.get('completed_at'))):
        raise ValueError('terminal repair trace identity or status mismatch')
    result = trace.get('result') or {}
    if result:
        if not isinstance(result, dict):
            raise ValueError('terminal repair result must be an object')
        summary = result.get('Summary') or {}
        empty_failed_result = (trace.get('trace_version') == 2 and run['Status'] == 'failed' and
                               not result.get('RunID') and not result.get('AgentID') and
                               isinstance(summary, dict) and not summary.get('Text'))
        if not empty_failed_result and (result.get('RunID') != run['RunID'] or result.get('AgentID') != run['AgentID']):
            raise ValueError('terminal repair result belongs to another run or agent')
    return trace
