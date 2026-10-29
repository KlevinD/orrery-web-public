"""Dispatch maintenance through ordinary agent conversations and report final outcomes."""

import hashlib
import json
from pathlib import Path
import re
import shlex
import urllib.error
import urllib.parse
import urllib.request

from codex_repair_worker import sql_text, validate_results, context_receipts, redact, public_agent_report

DISPATCHER = "orrery-scheduled-repair"


class LocalAPI:
    def __init__(self, config):
        self.config = config
        self.origin = config.get("api_origin", "http://127.0.0.1:18080").rstrip("/")
        target = urllib.parse.urlparse(self.origin)
        if target.scheme != "http" or target.hostname not in ("127.0.0.1", "localhost", "::1") or target.path:
            raise ValueError("maintenance API must use the local Orrery listener")

    def request(self, path, body=None, key="", stream=False, tenant=None, actor=DISPATCHER, kind="system"):
        values = {}
        for line in Path(self.config["server_env_file"]).read_text().splitlines():
            if not line.strip() or line.lstrip().startswith("#") or "=" not in line:
                continue
            name, value = line.removeprefix("export ").split("=", 1)
            if name.strip() in ("ORRERY_INTERNAL_VIEWER_AUTHORIZATION", "ORRERY_INTERNAL_VIEWER_TOKEN"):
                parts = shlex.split(value)
                values[name.strip()] = " ".join(parts)
        authorization = values.get("ORRERY_INTERNAL_VIEWER_AUTHORIZATION") or ("Bearer " + values.get("ORRERY_INTERNAL_VIEWER_TOKEN", ""))
        if authorization == "Bearer ":
            raise ValueError("internal API credential unavailable")
        headers = {"x-orrery-internal-viewer-authorization": authorization,
                   "x-user-id": actor, "x-viewer-kind": kind,
                   "x-tenant-id": tenant or self.config.get("tenant_id", "system"),
                   "x-permission-scope": "discussion.read,discussion.write,gateway.delivery.send,settings.read",
                   "Content-Type": "application/json", "Idempotency-Key": key,
                   "Accept": "text/event-stream" if stream else "application/json"}
        if kind == "agent":
            headers["x-agent-profile-id"] = actor
        request = urllib.request.Request(self.origin + path, headers=headers,
                                         data=None if body is None else json.dumps(body, ensure_ascii=False).encode())
        # Internal credentials must never follow a redirect to another listener.
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                return None
        try:
            response = urllib.request.build_opener(NoRedirect).open(request, timeout=45)
        except urllib.error.HTTPError as error:
            raise RuntimeError("Orrery API HTTP %s: %s" % (error.code, redact(error.read(2048).decode(errors="replace")))) from None
        if stream:
            return response
        with response:
            return json.loads(response.read(2 * 1024 * 1024))


def case_documents(content):
    """Extract case documents from either pure JSON or prose-wrapped JSON.

    Agents sometimes place the required result after a short progress update.
    Parse balanced JSON objects with the standard decoder so that the progress
    text is ignored while multiple case documents remain an explicit ambiguity.
    """
    if not isinstance(content, str):
        return []
    fenced = []
    for candidate in re.findall(r"```(?:json)?\s*([\s\S]*?)```", content):
        try:
            document = json.loads(candidate)
        except (json.JSONDecodeError, TypeError):
            continue
        if isinstance(document, dict) and "cases" in document:
            fenced.append(document)
    if fenced:
        return fenced

    decoder = json.JSONDecoder()
    documents = []
    starts = sorted({0, *(match.start() for match in re.finditer(r"\{", content))})
    for start in starts:
        try:
            document, _ = decoder.raw_decode(content[start:])
        except (json.JSONDecodeError, TypeError):
            continue
        if isinstance(document, dict) and "cases" in document:
            documents.append(document)
    return documents


class DirectRepair:
    def __init__(self, config, reader, api=None):
        self.config, self.reader = config, reader
        self.api = api or LocalAPI(config)
        from business_acceptance import BusinessAcceptance
        self.business = BusinessAcceptance(reader, config['artifact_root'])

    def key(self, active):
        return "scheduled-repair:" + active["tenant"] + ":" + active["batch"]

    def record(self, table, key):
        rows = self.reader.rows("orrery_core", "SELECT (payload_json::jsonb->>'payload')::jsonb AS p "
                                "FROM public.platform_records WHERE table_name=%s AND record_key=%s" % (sql_text(table), sql_text(key)))
        return rows[0]["p"] if rows else None

    def command(self, active):
        key = self.key(active)
        identity = "send_message\0system\0" + DISPATCHER + "\0" + key
        command = self.record("discussion_session_commands", "command-" + hashlib.sha256(identity.encode()).hexdigest())
        if command and (command.get("Operation") != "send_message" or command.get("ViewerID") != DISPATCHER or
                        command.get("ViewerKind") != "system" or command.get("IdempotencyKey") != key):
            raise ValueError("maintenance command identity mismatch")
        return command

    def dispatch_content(self, active):
        content = (active["directory"] / "prompt.txt").read_text(encoding="utf-8")
        from failure_center import direct_prompt
        if self.config.get('direct_runtime') == 'codex':
            # The native runtime can read the durable full assignment. Keep its
            # location and job identities at the start of the model-visible input.
            content = ('请你立即执行 Orrery 维护指派。首先读取完整指派文件 '
                       + json.dumps(str(active['directory'] / 'prompt.txt'))
                       + '。以文件规定的上下文、修复范围、受保护业务动作和验收条件为准；诊断数据仅作为待核验材料。'
                         '使用已绑定的 Orrery 上下文工具，在授权工作区完成修复和验证。'
                         '最终回复直接输出文件规定的 cases JSON，覆盖以下全部 job_id：'
                       + json.dumps([job['job_id'] for job in active['jobs']])
                       + '。最终报告须包含实际执行结果、证据和后续动作。')
        else:
            content = direct_prompt(active['jobs']) or content
        return content

    def submit(self, active):
        command = self.command(active)
        if command and command.get("ConversationID") and command.get("TurnID"):
            return {"discussion_id": command["ConversationID"], "turn_id": command["TurnID"], "execution_mode": "direct"}
        if command and command.get("Status") == "failed":
            return {"execution_mode": "direct", "status": "failed"}
        content = self.dispatch_content(active)
        body = {"content": content, "direct_target_kind": "agent", "direct_target_id": self.config["agent_id"],
                "force_new_conversation": True}
        if active.get("workspace_root_dir"):
            body["workspace_root_dir"] = (active.get('batch_workspace_root')
                                          if self.config.get('direct_runtime') == 'codex' else None) or active["workspace_root_dir"]
        with self.api.request("/api/discussions/messages", body, self.key(active), stream=True, tenant=active["tenant"]) as response:
            # Orrery detaches execution from the HTTP stream. Persisted command
            # identity is used after reconnects, never a second invocation key.
            for _ in range(256):
                line = response.readline(1024 * 1024)
                if not line:
                    break
                if line.startswith(b"data:"):
                    event = json.loads(line[5:])
                    conversation = event.get("conversation") or event.get("discussion") or {}
                    conversation_id = event.get("conversation_id") or event.get("discussion_id") or conversation.get("discussion_id")
                    if conversation_id:
                        return {"discussion_id": conversation_id, "execution_mode": "direct"}
                    if event.get("error"):
                        raise RuntimeError(redact(event["error"]))
        raise RuntimeError("direct repair has no durable acknowledgement yet")

    def cancel(self, active):
        """Ask Orrery to stop a timed-out direct maintenance discussion."""
        command = self.command(active)
        if not command or not command.get("ConversationID"):
            return {"status": "not_found"}
        path = "/api/discussions/%s/abort" % urllib.parse.quote(command["ConversationID"], safe="")
        return self.api.request(path, {}, self.key(active), tenant=active["tenant"])

    def snapshot(self, active, *, publish_ledger=True):
        command = self.command(active)
        if command and command.get("Status") == "failed" and not command.get("TurnID"):
            return {"found": True, "run": {"status": "failed"}, "error": redact(command.get("ErrorMessage"))}
        if not command or not command.get("ConversationID") or not command.get("TurnID"):
            return {"found": False}
        conversation = self.record("discussion_sessions", command["ConversationID"]) or {}
        turn = self.record("discussion_session_turns", command["TurnID"]) or {}
        if (conversation.get("TenantID") != active["tenant"] or conversation.get("CreatedBy") != DISPATCHER or
            conversation.get("DirectAgentID") != self.config["agent_id"] or
            turn.get("ConversationID") != command["ConversationID"] or turn.get("RootMessageID") != command.get("MessageID") or
            turn.get("IdempotencyKey") != self.key(active)):
            raise ValueError("direct repair tenant, agent or turn binding mismatch")
        runs = [self.record("discussion_session_runs", key) for key in turn.get("RunIDs") or []]
        runs = [run for run in runs if run and run.get("RunID") and run.get("TurnID") == turn["TurnID"] and
                run.get("ConversationID") == command["ConversationID"] and run.get("AgentID") == self.config["agent_id"]]
        if not runs:
            # RunIDs is projected at turn completion. Keep the canonical live
            # identity before then so timeout feedback can still bind its run.
            from direct_repair_recovery import runs_for_turn
            runs = [run for run in runs_for_turn(self, command) if run and run.get("RunID") and
                    run.get("TurnID") == turn["TurnID"] and run.get("ConversationID") == command["ConversationID"] and
                    run.get("AgentID") == self.config["agent_id"]]
        run = max(runs, key=lambda row: (row.get("CreatedAt", ""), row.get("RunID", "")), default={})
        from direct_repair_recovery import run_with_terminal_trace, read_terminal_trace, terminal_outcome, runtime_failure_event
        run = run_with_terminal_trace(self, active, command, run)
        status = turn.get("Status") or "running"
        snapshot = {"found": True, "discussion_id": command["ConversationID"], "turn_id": command["TurnID"],
                    "trace_ref": run.get("TraceRef", ""), "run": {"status": status, "runtime_run_id": run.get("RunID", "")}}
        if run.get('Status') == 'failed':
            failure = runtime_failure_event(self, command, run)
            if failure:
                snapshot['runtime_failure'] = failure
            elif turn.get('ErrorMessage'):
                snapshot['runtime_failure'] = {'status': 'failed', 'source': 'canonical_turn',
                                               'summary': redact(turn['ErrorMessage']), 'error_code': turn.get('ErrorCode', '')}
        if run.get('Status') == 'failed' and run.get('TraceRef'):
            try:
                trace = read_terminal_trace(self.config['artifact_root'], command, run)
                snapshot['terminal_outcome'] = terminal_outcome(trace)
            except FileNotFoundError:
                # A missing diagnostic artifact is not a live execution. The
                # fenced canonical recovery below still checks leases and HIL.
                snapshot['diagnostic_trace_missing'] = True
        if publish_ledger:
            from direct_repair_receipts import publish_receipts
            publish_receipts(self, active, command, turn, run)
        if status not in ("completed", "failed", "cancelled"):
            from direct_repair_recovery import recover_terminal_snapshot
            return recover_terminal_snapshot(self, active, command, conversation, turn, snapshot)
        if status == "cancelled":
            return snapshot
        if not run:
            snapshot["error"] = "Direct execution ended before an agent run was created."
            from direct_repair_recovery import recover_terminal_snapshot
            return recover_terminal_snapshot(self, active, command, conversation, turn, snapshot)
        rows = self.reader.rows("orrery_core", "SELECT (payload_json::jsonb->>'payload')::jsonb AS p "
            "FROM public.platform_records WHERE table_name='discussion_session_messages' "
            "AND (payload_json::jsonb->>'payload')::jsonb->>'ConversationID'=%s "
            "AND (payload_json::jsonb->>'payload')::jsonb->>'RunID'=%s" % (sql_text(command["ConversationID"]), sql_text(run["RunID"])))
        snapshot["messages"] = [row["p"] for row in rows if row["p"].get("AuthorID") == self.config["agent_id"] and row["p"].get("TurnID") == command["TurnID"]]
        if not snapshot["messages"]:
            from direct_repair_recovery import recover_terminal_snapshot
            return recover_terminal_snapshot(self, active, command, conversation, turn, snapshot)
        return snapshot

    def results(self, snapshot, active):
        if snapshot.get("run", {}).get("status") == "cancelled":
            return [{"job_id": job["job_id"], "status": "needs_attention", "summary": "修复执行已取消，未自动重新启动，需要确认后续处理。", "evidence": []} for job in active["jobs"]], ""
        if snapshot.get("run", {}).get("status") not in ("completed", "failed"):
            return [], "direct repair is not completed or failed"
        documents = []
        contents = [message.get("Content", "") for message in snapshot.get("messages", [])
                    if not (message.get('Metadata') or {}).get('stream_partial')]
        if snapshot.get("result_source") == "terminal_trace":
            contents = [snapshot.get("recovered_result", "")]
        for content in contents:
            if not isinstance(content, str):
                continue
            documents.extend(case_documents(content))
        if len(documents) == 0:
            if snapshot.get("run", {}).get("status") in ("completed", "failed"):
                return self.incomplete_recovered_result(active, snapshot, 'missing or ambiguous final cases document'), ""
            return [], "direct repair must provide exactly one final cases document"
        if len(documents) > 1:
            return [], "direct repair must provide exactly one final cases document"
        try:
            cases = validate_results(documents[0], {job["job_id"] for job in active["jobs"]})
            for case in cases:
                case.pop('verification', None)
                case.pop('agent_report', None)
            authored = {case['job_id']: (case['status'], case.get('user_report')) for case in cases}
            if snapshot.get("result_source") == "terminal_trace" and {case['job_id'] for case in cases} != {job['job_id'] for job in active['jobs']}:
                raise ValueError('recovered result does not cover the assigned jobs')
            if any(case["status"] == "resolved" for case in cases):
                try:
                    if snapshot["run"]["status"] != "completed":
                        raise ValueError("failed execution requires independent verification")
                    if snapshot.get('recovered_blocker'):
                        raise ValueError('recovered runtime result still has a blocker')
                    context_receipts(snapshot, self.config["artifact_root"])
                except (OSError, ValueError, KeyError, TypeError) as error:
                    for case in cases:
                        if case["status"] == "resolved":
                            case["status"] = "needs_attention"
                            case.pop("user_summary", None)
                            case.pop("next_step", None)
                            case["summary"] += " 验证未完成：" + redact(error)
            assigned = {job['job_id']: job for job in active['jobs']}
            from failure_center import assess
            cases = [assess(self.business.assess(case, assigned[case['job_id']], snapshot), assigned[case['job_id']], snapshot, self.business, self.reader) for case in cases]
            for case in cases:
                status, body = authored[case['job_id']]
                run_id = snapshot.get('run', {}).get('runtime_run_id')
                if case['status'] == status and run_id and public_agent_report(body):
                    case['agent_report'] = {'version': 1, 'source': 'agent_final', 'agent_id': self.config['agent_id'],
                        'runtime_run_id': run_id, 'job_id': case['job_id'], 'status': status, 'body': body,
                        'body_sha256': hashlib.sha256(body.encode()).hexdigest()}
            return cases, ""
        except (ValueError, TypeError) as error:
            if snapshot.get("run", {}).get("status") in ("completed", "failed"):
                return self.incomplete_recovered_result(active, snapshot, redact(error)), ""
            return [], redact(error)

    def incomplete_recovered_result(self, active, snapshot, reason):
        # The execution demonstrably ended. Hold missing results for review rather
        # than repeating potentially effectful maintenance just to recover its reply.
        evidence = [str(value) for value in (snapshot.get("trace_ref"),
                    snapshot.get("run", {}).get("runtime_run_id"), snapshot.get("discussion_id"),
                    snapshot.get("turn_id")) if value]
        outcome = snapshot.get('terminal_outcome') or {}
        failure = snapshot.get('runtime_failure') or {}
        runtime_status = outcome.get('status') or snapshot.get('run', {}).get('status', 'unknown')
        cause = redact(failure.get('summary') or outcome.get('error_message') or snapshot.get('error'))[:1500]
        diagnosis = ''
        if runtime_status == 'timeout':
            diagnosis = ' Repair runtime timed out (runtime_timeout).'
        elif runtime_status == 'failed':
            diagnosis = ' Repair execution failed; ' + (cause if cause else 'the runtime failure cause was not persisted.')
        elif runtime_status == 'completed':
            diagnosis = ' Execution ended, but repair completion is unverified.'
        if failure.get('event_id'):
            evidence.append(failure['event_id'])
        summary = (diagnosis.strip() + ' Final case result unavailable: ' + reason if diagnosis else
                   'Recovered terminal execution has no valid case result: ' + reason)
        return [{"job_id": job["job_id"], "status": "needs_attention",
                 "summary": summary,
                 "evidence": evidence,
                 "failure_diagnostics": {"runtime_status": runtime_status, "result_error": reason,
                                         "runtime_cause": cause, "runtime_error_code": outcome.get('error_code') or failure.get('error_code', ''),
                                         "event_id": failure.get('event_id', '')}} for job in active["jobs"]]

    def report(self, report):
        if report.get('source') != 'agent_report' or report.get('title') or not public_agent_report(report.get('body')):
            raise ValueError('report must contain an unchanged final Agent reply')
        recipient = self.config["result_recipient"]
        # Fixed deployment recipient, never an address supplied by a failure or agent.
        key = report["key"]
        receipt = self.api.request("/api/discussions/messages", {
            "direct_target_kind": "user", "direct_target_id": recipient["human_profile_id"],
            "content": report["body"]}, key, tenant=recipient["tenant_id"],
            actor=self.config["agent_id"], kind="agent")
        if not receipt.get("discussion_id"):
            raise RuntimeError("final report has no conversation receipt")
        deliveries = []
        if recipient.get("channels"):
            deliveries = self.api.request("/api/gateway/deliveries", {
                "human_profile_id": recipient["human_profile_id"], "channels": recipient["channels"],
                "event_type": "qa", "title": report["title"], "body": report["body"],
                "format": "text", "idempotency_key": key}, key, tenant=recipient.get("delivery_tenant_id", recipient["tenant_id"]))
        return {"discussion_id": receipt.get("discussion_id"), "deliveries": deliveries}

    def report_status(self, report, receipt):
        """Read channel receipts; API acceptance is never proof of delivery."""
        recipient = self.config['result_recipient']
        if not receipt.get('discussion_id'):
            raise ValueError('report has no conversation receipt')
        if not recipient.get('channels'):
            return {'status': 'discussion_delivered', 'delivered': True}
        rows = self.reader.rows('orrery_jobs',
            'SELECT id,channel_key,status,delivered_at FROM public.gateway_message_deliveries '
            'WHERE tenant_id=%s AND human_profile_id=%s AND idempotency_key=%s AND body=%s' % (
                sql_text(recipient.get('delivery_tenant_id', recipient['tenant_id'])),
                sql_text(recipient['human_profile_id']), sql_text(report['key']), sql_text(report['body'])))
        statuses = {row['channel_key']: row['status'] for row in rows}
        delivered = all(statuses.get(channel) == 'delivered' for channel in recipient['channels'])
        terminal = any(value in ('failed', 'dead_letter') for value in statuses.values())
        return {'status': 'delivered' if delivered else ('failed' if terminal else 'pending'),
                'delivered': delivered, 'deliveries': rows}
