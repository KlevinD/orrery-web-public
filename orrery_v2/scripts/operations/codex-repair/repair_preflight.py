"""Read-only feedback before the repair Agent submits its final cases.

This checks the candidate against the assigned jobs and original live trace.
It never repairs IDs, changes queue state, dispatches work or approves closure.
The existing terminal acceptance checks remain authoritative.
"""
import argparse
import difflib
import hashlib
import json
from pathlib import Path
import re
import shlex


CONTEXT_TOOLS = {'enterprise_graph_search_objects', 'enterprise_graph_build_context_bundle',
                 'knowledge_search', 'memory_search', 'session_search', 'session_read'}
REQUIRED_CONTEXT = {'enterprise_graph_search_objects', 'enterprise_graph_build_context_bundle',
                    'knowledge_search', 'memory_search'}


def workflow_prompt(directory, workspace, artifact_root):
    candidate = Path(workspace) / 'repair-result.json'
    command = shlex.join(['python3', str(Path(__file__).resolve()), '--attempt-dir', str(directory),
                          '--artifact-root', str(artifact_root), '--result', str(candidate)])
    return ("\nComplete this maintenance workflow in the SAME execution:\n"
            "1. Diagnose the concrete failure against current logs, source and requirements. "
            "Move from exploration to a repair decision once the cause is supported; reserve time for verification and the final report.\n"
            "2. Apply the authorized scoped correction in this batch workspace. If already recovered, "
            "verify that recovery and explain why no mutation is needed; do not invent a patch.\n"
            "3. Run checks that exercise the affected behavior. A source read, context search, "
            "result-schema check or this preflight is not a recovery test.\n"
            "4. Activate changes when needed, preserving current production and rollback material; "
            "verify the affected running component afterwards. A tested but unactivated patch is needs_attention.\n"
            "For production activation, prepare and test before taking the shared publication locks. "
            "Use production_release.py beside this preflight with the live expected release and commit, "
            "the existing worker config, and a reviewed final activation command. "
            "It serializes final activation and pauses only new repair dispatch; keep collection, "
            "result reconciliation, configured timeouts and Agent report delivery running. "
            "Do not stop the maintenance service for an API/Web release. "
            "Let existing executions finish; never cancel them merely to obtain a release window. "
            "A repair must not wait for its own execution to finish before returning: "
            "if its API activation requires that same execution to end, hand off a durable verified "
            "activation request and report the pending state truthfully. "
            "The guard now automatically submits an independent systemd activation when called inside the API; "
            "on activation_deferred, finish this turn promptly, cite its request_id and explain that independent activation is pending without asking the user to operate the server. "
            "Do not poll the independent service while keeping this same turn running, and do not claim resolved from a queued receipt. "
            "If the production base changed, recompose and revalidate instead of overwriting it.\n"
            "5. Write the complete cases JSON candidate to " + str(candidate) + ". "
            "Select verification_tool_ids programmatically from runtime-tool-receipts.json; do not retype opaque IDs. "
            "Your user_report should explain the specific problem, actual correction or existing recovery, "
            "verification, activation and any remaining blocker in your own Chinese wording.\n"
            "Before your final reply run this read-only preflight command:\n" + command + "\n"
            "If needs_correction, correct the candidate in this execution and check again, at most twice. "
            "An ID suggestion is not accepted evidence: copy the exact real ID and rerun the check. "
            "For missing live receipts wait up to 90 seconds, without repeating tests or business actions. "
            "If still blocked, report needs_attention with the actual blocker. "
            "After ready_for_final, use the exact checked file contents for your final cases JSON; "
            "do not rewrite its IDs from memory. Preflight does not close a job or establish business completion: "
            "the dispatcher still verifies the final trace, current incident and original business acceptance.\n")


def read_json(path, limit=4*1024*1024):
    path = Path(path)
    if not path.is_file() or path.stat().st_size > limit:
        raise ValueError('required JSON file is unavailable or too large: ' + path.name)
    return json.loads(path.read_text(encoding='utf-8'))


def live_receipts(root, snapshot):
    run_id = (snapshot.get('run') or {}).get('runtime_run_id', '')
    conversation = snapshot.get('discussion_id', '')
    if not snapshot.get('found') or not all(re.fullmatch(r'[A-Za-z0-9_-]+', v or '') for v in (run_id, conversation)):
        raise ValueError('the dispatcher has not yet saved a bound runtime snapshot; wait for its next poll')
    root = Path(root).resolve()
    folder = (root / 'discussion-conversations' / conversation / 'runs' / run_id / 'trace-v2' / 'events').resolve()
    if not folder.is_relative_to(root) or not folder.is_dir():
        raise ValueError('the bound runtime events are not available yet')
    paths = sorted(p for p in folder.glob('*.json') if not p.name.endswith('.meta.json'))
    if len(paths) > 10000:
        raise ValueError('live trace exceeds the preflight event limit')
    calls, successful = {}, {}
    for path in paths:
        if not path.resolve().is_relative_to(folder):
            raise ValueError('live event path escapes its runtime')
        event = (read_json(path).get('payload') or {}).get('event') or {}
        if event.get('kind') == 'tool_call':
            call = event.get('tool_call') or {}
            identifier, name = call.get('tool_use_id'), call.get('name')
            if identifier and name:
                if identifier in calls:
                    raise ValueError('duplicate tool identity in live trace')
                calls[identifier] = str(name).replace('.', '_')
        elif event.get('kind') == 'tool_result':
            result = event.get('tool_result') or {}
            identifier = result.get('tool_use_id')
            if identifier in calls:
                successful.pop(identifier, None)
                if result.get('is_error') is False and result.get('content'):
                    successful[identifier] = calls[identifier]
    return successful


def check(attempt_dir, candidate, artifact_root):
    from codex_repair_worker import RESULT_SCHEMA, public_agent_report, validate_results
    directory = Path(attempt_dir)
    assignments = read_json(directory / 'assignments.json')
    assigned = {j['job_id']: j for j in assignments}
    document = read_json(candidate, 1024*1024)
    cases = validate_results(document, set(assigned))
    errors = []
    if set(document) != {'cases'} or {c['job_id'] for c in cases} != set(assigned):
        errors.append('candidate must contain exactly the assigned cases')
    properties = RESULT_SCHEMA['properties']['cases']['items']['properties']
    snapshot = read_json(directory / 'native-run.json')
    successful = live_receipts(artifact_root, snapshot)
    technical = {i: n for i, n in successful.items() if n not in CONTEXT_TOOLS}
    for case in cases:
        job_id = case['job_id']
        if set(case) - set(properties):
            errors.append(job_id + ': unexpected result fields')
        if not public_agent_report(case.get('user_report')):
            errors.append(job_id + ': missing or invalid final user_report')
        if case['status'] != 'resolved':
            continue
        if (snapshot.get('run') or {}).get('status') in ('failed', 'cancelled'):
            errors.append(job_id + ': failed or cancelled execution cannot claim resolved')
        context = assigned[job_id].get('failure_context') or assigned[job_id].get('business_context') or {}
        if not context.get('digest') or case.get('contract_digest') != context['digest']:
            errors.append(job_id + ': contract_digest does not match this assignment')
        if case.get('repair_kind') not in ('repaired', 'already_recovered'):
            errors.append(job_id + ': missing repair_kind')
        missing_context = REQUIRED_CONTEXT - set(successful.values())
        if missing_context:
            errors.append(job_id + ': missing successful context receipts: ' + ', '.join(sorted(missing_context)))
        ids = case.get('verification_tool_ids')
        if not isinstance(ids, list) or not ids or len(ids) > 20 or any(not isinstance(i, str) for i in ids):
            errors.append(job_id + ': missing valid verification_tool_ids')
            continue
        for identifier in ids:
            if identifier not in technical:
                suggestions = difflib.get_close_matches(identifier, technical, n=2, cutoff=0.7)
                errors.append(job_id + ': verification ID is absent, failed, empty or context-only: ' + identifier
                              + ('; exact existing candidates: ' + ', '.join(suggestions) if suggestions else ''))
    digest = hashlib.sha256(json.dumps(document, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    return {'status': 'needs_correction' if errors else 'ready_for_final', 'candidate_sha256': digest,
            'runtime_run_id': snapshot['run']['runtime_run_id'], 'errors': errors,
            'terminal_acceptance_pending': True}


def main(argv=None):
    from codex_repair_worker import redact
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--attempt-dir', required=True)
    parser.add_argument('--result', required=True)
    parser.add_argument('--artifact-root', required=True)
    args = parser.parse_args(argv)
    try:
        outcome = check(args.attempt_dir, args.result, args.artifact_root)
    except (OSError, ValueError, KeyError, TypeError) as error:
        outcome = {'status': 'needs_correction', 'errors': [redact(error)], 'terminal_acceptance_pending': True}
    print(json.dumps(outcome, ensure_ascii=False))
    return 0 if outcome['status'] == 'ready_for_final' else 1


if __name__ == '__main__':
    raise SystemExit(main())
