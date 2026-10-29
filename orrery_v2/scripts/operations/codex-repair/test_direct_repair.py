import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from codex_repair_worker import Worker, repair_notification, validate_results
from direct_repair import DirectRepair, LocalAPI, DISPATCHER


class DirectRepairTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config = {"state_dir": self.temp.name, "workspace": self.temp.name, "artifact_root": self.temp.name,
                       "source_root": self.temp.name, "agent_id": "steward", "tenant_id": "tenant-a",
                       "execution_mode": "direct", "sources": ["scheduled"], "batch_size": 1,
                       "result_recipient": {"human_profile_id": "human-a", "tenant_id": "tenant-a"}}
        self.worker = Worker(self.config)
        self.addCleanup(self.worker.queue.db.close)
        self.api = Mock()
        self.adapter = DirectRepair(self.config, Mock(), self.api)
        self.adapter.reader.rows.return_value = []
        self.active = {"tenant": "tenant-a", "batch": "batch-test", "directory": Path(self.temp.name),
                       "jobs": [{"job_id": "job-a"}], "execution_mode": "direct"}
        (Path(self.temp.name) / "prompt.txt").write_text("Repair the given failure.")

    def ingest(self, key, source="scheduled", priority=1):
        self.worker.queue.ingest({"observation_key": key, "job_key": key, "source": source, "priority": priority,
                                  "summary": "failed", "title": "Scheduled fixture", "tenant_id": "tenant-a"}, 1)

    def test_only_scheduled_failures_are_claimed_before_limit(self):
        self.ingest("chat", "discussion", 100)
        self.ingest("scheduled")
        with patch.object(self.worker, "native", return_value={"discussion_id": "discussion-a"}) as native:
            self.worker.start()
        self.assertEqual(self.worker.active["jobs"][0]["observations"][0]["source"], "scheduled")
        self.assertEqual(self.worker.active["execution_mode"], "direct")
        self.assertEqual(self.worker.queue.status()["counts"], {"running": 1, "pending": 1})
        self.assertEqual(native.call_args.args[0], "submit")

    def test_native_assignment_wires_complete_workflow_and_readonly_preflight(self):
        self.config['direct_runtime'] = 'codex'
        self.ingest('workflow-fixture')
        base = Path(self.temp.name)
        with patch.object(self.worker, 'prepare_workspace', return_value=(base, base, 'a'*40, base/'batch-workspace')), \
                patch.object(self.worker.direct.business, 'capture', return_value={'available': False}), \
                patch.object(self.worker, 'native', return_value={'discussion_id': 'discussion-a'}):
            self.worker.start()
        prompt = (self.worker.active['directory'] / 'prompt.txt').read_text(encoding='utf-8')
        for term in ('SAME execution', 'repair_preflight.py', 'repair-result.json', 'Activate', 'exact checked file contents'):
            self.assertIn(term, prompt)
        self.assertEqual(self.worker.queue.status()['counts'], {'running': 1})

    def test_scheduled_only_scan_does_not_collect_other_sources(self):
        collector = self.worker.collector
        with patch.object(collector, "repair_ownership", return_value=[]), patch.object(collector, "scheduled", return_value=[]), \
             patch.object(collector, "reliability") as reliability, patch.object(collector, "discussions") as discussions, patch.object(collector, "queues") as queues:
            self.worker.scan()
        reliability.assert_not_called()
        discussions.assert_not_called()
        queues.assert_not_called()

    def test_direct_submission_uses_chat_once_and_recovers_after_lost_response(self):
        self.api.request.return_value = io.BytesIO(b'data: {"discussion_id":"discussion-a"}\n\n')
        with patch.object(self.adapter, "command", return_value=None):
            receipt = self.adapter.submit(self.active)
        self.assertEqual(receipt["discussion_id"], "discussion-a")
        self.assertEqual(self.api.request.call_args.args[0], "/api/discussions/messages")
        self.assertEqual(self.api.request.call_args.args[1]["direct_target_id"], "steward")
        with patch.object(self.adapter, "command", return_value={"ConversationID": "discussion-a", "TurnID": "turn-a"}):
            self.adapter.submit(self.active)
        self.assertEqual(self.api.request.call_count, 1)

    def test_snapshot_requires_tenant_agent_and_source_message_binding(self):
        command = {"ConversationID": "conversation", "TurnID": "turn", "MessageID": "message"}
        records = {"conversation": {"TenantID": "tenant-a", "CreatedBy": DISPATCHER, "DirectAgentID": "steward"},
                   "turn": {"TurnID": "turn", "ConversationID": "conversation", "RootMessageID": "message", "IdempotencyKey": self.adapter.key(self.active), "Status": "running"}}
        with patch.object(self.adapter, "command", return_value=command), patch.object(self.adapter, "record", side_effect=lambda table, key: records.get(key)):
            self.assertEqual(self.adapter.snapshot(self.active)["run"]["status"], "running")
            records["conversation"]["TenantID"] = "tenant-b"
            with self.assertRaisesRegex(ValueError, "binding"):
                self.adapter.snapshot(self.active)
            records["conversation"]["TenantID"] = "tenant-a"
            records["turn"]["RootMessageID"] = "another-message"
            with self.assertRaisesRegex(ValueError, "binding"):
                self.adapter.snapshot(self.active)

    def test_failure_submission_preserves_assignment_with_oversized_diagnostics(self):
        from failure_center import context
        job = {'job_id': 'job-critical', 'observations': [{'source': 'failure_center', 'tenant_id': 'tenant-a',
               'incident_id': 'incident-critical', 'last_seen_ns': 100, 'component': 'web:/holdings',
               'error_code': 'TypeError', 'summary': 'render unavailable', 'diagnostics': {'stack': 'x' * 50000}}]}
        job['failure_context'] = context(job)
        self.active['jobs'] = [job]
        (self.active['directory'] / 'prompt.txt').write_text('boilerplate ' * 5000)
        self.api.request.return_value = io.BytesIO(b'data: {"discussion_id":"discussion-a"}\n\n')
        with patch.object(self.adapter, 'command', return_value=None):
            self.adapter.submit(self.active)
        content = self.api.request.call_args.args[1]['content']
        self.assertLess(len(content), 1600)
        for required in ['job-critical', 'incident-critical', 'tenant-a', 'web:/holdings',
                         job['failure_context']['digest'], 'verification_tool_ids', '"cases"']:
            self.assertIn(required, content)

    def test_unprojected_live_run_identity_survives_timeout_feedback(self):
        from codex_repair_worker import Collector
        from failure_center import write_feedback
        self.config.update(direct_runtime='codex', run_timeout_seconds=10,
                           failure_center_feedback_dir=str(Path(self.temp.name) / 'feedback'))
        observation = Collector({}).normalize({
            'source': 'failure_center', 'source_id': 'failure-observation', 'tenant_id': 'tenant-a',
            'incident_id': 'incident-a', 'incident_version': 1, 'occurrence_count': 1,
            'last_seen_ns': 1792140000000000000, 'occurred_at': 1792140000,
            'component': 'fixture', 'error_code': 'fixture_error', 'summary': 'failed', 'priority': 10,
        }, 0, 1792140001)
        self.worker.queue.ingest(observation, 1)
        batch, jobs = self.worker.queue.claim(1, 2, ['failure_center'])
        directory = Path(self.temp.name) / 'attempts' / batch
        directory.mkdir(parents=True)
        active = dict(self.active, batch=batch, jobs=jobs, directory=directory, started=2)
        (directory / 'prompt.txt').write_text('Repair the given failure.', encoding='utf-8')
        command = {'ConversationID': 'conversation', 'TurnID': 'turn', 'MessageID': 'message'}
        records = {
            'conversation': {'TenantID': 'tenant-a', 'CreatedBy': DISPATCHER, 'DirectAgentID': 'steward'},
            'turn': {'TurnID': 'turn', 'ConversationID': 'conversation', 'RootMessageID': 'message',
                     'IdempotencyKey': self.adapter.key(active), 'Status': 'running', 'RunIDs': []},
        }
        canonical = {'RunID': 'runtime-canonical', 'TurnID': 'turn', 'ConversationID': 'conversation',
                     'AgentID': 'steward', 'Status': 'running', 'CreatedAt': '2026-10-17T12:00:00Z'}
        with patch.object(self.adapter, 'command', return_value=command), \
                patch.object(self.adapter, 'record', side_effect=lambda table, key: records.get(key)), \
                patch('direct_repair_recovery.runs_for_turn', return_value=[canonical]):
            snapshot = self.adapter.snapshot(active)
        self.assertEqual(snapshot['run'], {'status': 'running', 'runtime_run_id': 'runtime-canonical'})
        self.assertEqual(self.adapter.results(snapshot, active)[0], [])
        (directory / 'native-run.json').write_text(json.dumps(snapshot), encoding='utf-8')
        self.worker.active = active
        with patch.object(self.worker.direct, 'cancel', return_value={'status': 'cancelled'}):
            self.assertTrue(self.worker.timeout_active())
        write_feedback(self.worker)
        receipts = list((Path(self.temp.name) / 'feedback').glob('*.json'))
        self.assertEqual(len(receipts), 1)
        receipt = json.loads(receipts[0].read_text(encoding='utf-8'))
        self.assertEqual(receipt['repair_run_id'], 'runtime-canonical')
        self.assertEqual(receipt['status'], 'needs_attention')
        self.assertFalse(receipt['verified'])
        self.assertEqual(receipt['batch_id'], batch)
        self.assertEqual(self.worker.queue.status()['counts'], {'needs_attention': 1})

    def test_unprojected_run_lookup_excludes_other_conversations_turns_and_agents(self):
        self.config['direct_runtime'] = 'codex'
        command = {'ConversationID': 'conversation', 'TurnID': 'turn', 'MessageID': 'message'}
        records = {
            'conversation': {'TenantID': 'tenant-a', 'CreatedBy': DISPATCHER, 'DirectAgentID': 'steward'},
            'turn': {'TurnID': 'turn', 'ConversationID': 'conversation', 'RootMessageID': 'message',
                     'IdempotencyKey': self.adapter.key(self.active), 'Status': 'running', 'RunIDs': []},
        }
        canonical = {'RunID': 'runtime-canonical', 'TurnID': 'turn', 'ConversationID': 'conversation',
                     'AgentID': 'steward', 'Status': 'running', 'CreatedAt': '2026-10-17T12:00:00Z'}
        foreign = [dict(canonical, ConversationID='another-conversation'),
                   dict(canonical, TurnID='another-turn'), dict(canonical, AgentID='another-agent')]
        with patch.object(self.adapter, 'command', return_value=command), \
                patch.object(self.adapter, 'record', side_effect=lambda table, key: records.get(key)), \
                patch('direct_repair_recovery.runs_for_turn', return_value=foreign):
            snapshot = self.adapter.snapshot(self.active)
        self.assertEqual(snapshot['run']['runtime_run_id'], '')
        self.assertFalse((self.active['directory'] / 'runtime-tool-receipts.json').exists())

    def test_native_repair_receives_full_assignment_path_without_truncating_job_identity(self):
        self.config['direct_runtime'] = 'codex'
        self.active['workspace_root_dir'] = str(self.active['directory'])
        self.active['batch_workspace_root'] = str(self.active['directory'] / 'batch')
        full_assignment = 'Full trusted maintenance boundaries and untrusted observations. ' * 1000
        (self.active['directory'] / 'prompt.txt').write_text(full_assignment, encoding='utf-8')
        self.api.request.return_value = io.BytesIO(b'data: {"discussion_id":"discussion-a"}\n\n')
        with patch.object(self.adapter, 'command', return_value=None):
            self.adapter.submit(self.active)
        content = self.api.request.call_args.args[1]['content']
        self.assertLess(len(content), 1200)
        self.assertIn(str(self.active['directory'] / 'prompt.txt').replace('\\', '\\\\'), content)
        self.assertIn('job-a', content)
        self.assertEqual(self.api.request.call_args.args[1]['workspace_root_dir'], self.active['batch_workspace_root'])
        self.assertEqual((self.active['directory'] / 'prompt.txt').read_text(encoding='utf-8'), full_assignment)

    def snapshot(self, status="completed", outcome="needs_attention"):
        return {"run": {"status": status, "runtime_run_id": "runtime-a"}, "messages": [{"Content": json.dumps({"cases": [
            {"job_id": "job-a", "status": outcome, "summary": "result", "evidence": ["verification evidence"]}]})}]}

    def authored(self, case):
        snapshot = self.snapshot()
        snapshot['messages'][0]['Content'] = json.dumps({'cases': [case]}, ensure_ascii=False)
        active = dict(self.active, jobs=[{'job_id': case['job_id']}])
        return self.adapter.results(snapshot, active)[0][0]

    def test_attention_result_does_not_require_a_mission(self):
        cases, error = self.adapter.results(self.snapshot("failed"), self.active)
        self.assertEqual(error, "")
        self.assertEqual(cases[0]["status"], "needs_attention")

    def test_prose_wrapped_case_document_is_recovered(self):
        document = {"cases": [{"job_id": "job-a", "status": "needs_attention",
                                "summary": "MCP permission denied", "evidence": ["permission"]}]}
        snapshot = self.snapshot()
        snapshot["messages"][0]["Content"] = "先说明诊断过程。" + json.dumps(document, ensure_ascii=False)
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(error, "")
        self.assertEqual(cases[0]["job_id"], "job-a")
        self.assertEqual(cases[0]["summary"], "MCP permission denied")

    def test_multiple_prose_wrapped_case_documents_remain_ambiguous(self):
        document = {"cases": [{"job_id": "job-a", "status": "needs_attention",
                                "summary": "one", "evidence": ["e"]}]}
        snapshot = self.snapshot()
        encoded = json.dumps(document)
        snapshot["messages"][0]["Content"] = "说明 " + encoded + " 再说明 " + encoded
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(cases, [])
        self.assertEqual(error, "direct repair must provide exactly one final cases document")

    def test_missing_agent_result_is_reported_as_missing_agent_result(self):
        cases = self.adapter.incomplete_recovered_result(
            self.active, {"trace_ref": "/uploads/trace", "run": {"runtime_run_id": "runtime-a"}},
            "missing or ambiguous final cases document")
        report = repair_notification("每日公众号文章", "needs_attention", cases[0])
        self.assertIsNone(report)
        self.assertNotIn('user_summary', cases[0])
        self.assertIn('runtime-a', cases[0]['evidence'])

    def test_resolution_requires_completed_run_and_actual_context_receipts(self):
        with patch("direct_repair.context_receipts", return_value=["actual receipt"]) as receipts:
            cases, _ = self.adapter.results(self.snapshot(outcome="resolved"), self.active)
            self.assertEqual(cases[0]["status"], "resolved")
            receipts.assert_called_once()
        with patch("direct_repair.context_receipts", side_effect=ValueError("missing context")):
            cases, _ = self.adapter.results(self.snapshot(outcome="resolved"), self.active)
            self.assertEqual(cases[0]["status"], "needs_attention")
        cases, _ = self.adapter.results(self.snapshot("failed", "resolved"), self.active)
        self.assertEqual(cases[0]["status"], "needs_attention")

    def test_unverified_resolution_does_not_keep_a_success_notification(self):
        for run_status in ("completed", "failed"):
            with self.subTest(run_status=run_status):
                snapshot = self.snapshot(run_status, "resolved")
                document = json.loads(snapshot["messages"][0]["Content"])
                document["cases"][0].update(user_summary="封面生成已恢复。", next_step="无需你处理。")
                snapshot["messages"][0]["Content"] = json.dumps(document)
                with patch("direct_repair.context_receipts", side_effect=ValueError("missing context")):
                    cases, error = self.adapter.results(snapshot, self.active)
                self.assertEqual(error, "")
                report = repair_notification("每日公众号文章", cases[0]["status"], cases[0])
                self.assertIsNone(report)

    def test_notification_keeps_internal_diagnosis_in_the_record(self):
        self.ingest("scheduled")
        batch, jobs = self.worker.queue.claim(1, 2)
        result = {"job_id": jobs[0]["job_id"], "status": "needs_attention",
                  "summary": "隔离测试通过，但原任务未恢复。HTTP 400 和 503。" * 40,
                  "evidence": ["enterprise_graph.search_objects actor=agent:steward tenant=system",
                               "bundle:system:" + "a" * 64 + " boundaries=null"],
                  "user_report": "公众号封面生成仍失败。\n\n我查到图片服务没有返回可用结果，文章尚未发布。"}
        result = self.authored(result)
        self.worker.queue.finish(batch, [result], 3)
        active = {"batch": batch, "jobs": jobs}
        self.worker.prepare_reports(active)
        report = json.loads(self.worker.queue.db.execute("SELECT payload FROM reports").fetchone()[0])
        self.assertEqual(report['body'], result['user_report'])
        self.assertEqual(report['title'], '')
        self.assertLess(len(report["body"]), 400)
        for internal in ("HTTP", "隔离测试", "enterprise_graph", "bundle:", "boundaries"):
            self.assertNotIn(internal, report["body"])
        stored = json.loads(self.worker.queue.db.execute("SELECT last_result FROM jobs").fetchone()[0])
        self.assertEqual(stored, result)
        self.worker.prepare_reports(active)
        self.assertEqual(self.worker.queue.db.execute("SELECT COUNT(*) FROM reports").fetchone()[0], 1)

    def test_old_or_invalid_presentation_uses_plain_fallback_without_retrying_repair(self):
        invalid_values = [None, 123, {"text": "结果"}, "", "诊断" * 200,
                          "内部结果：bundle:system:" + "b" * 64,
                          "查询 enterprise_graph.search_objects 后返回 reference_pending。",
                          "上游 HTTP 400，保存 JSON 后不能标记 resolved。",
                          "权限 tenant=system，boundaries=null。", "运行 trace 位于 /uploads/private。",
                          "认证：Bearer secret-value", "诊断 token\x00数据", "原始trace证实HTTP400和503。",
                          "返回的reference_pending状态不能证明完整权限。"]
        for value in invalid_values:
            with self.subTest(value=value):
                case = {"job_id": "job-a", "status": "needs_attention",
                        "summary": "内部诊断 HTTP 503，证据 bundle:system:" + "c" * 64,
                        "evidence": ["private technical receipt"], "user_summary": value, "next_step": value}
                self.assertEqual(validate_results({"cases": [case]}, {"job-a"}), [case])
                report = repair_notification("每日公众号文章", "needs_attention", case)
                self.assertIsNone(report)
        old_case = {"status": "resolved", "summary": "technical-only", "evidence": ["receipt"]}
        self.assertIsNone(repair_notification("每日公众号文章", "resolved", old_case))

    def test_final_queue_status_overrides_stale_agent_success(self):
        self.ingest("scheduled")
        batch, jobs = self.worker.queue.claim(1, 2)
        self.worker.queue.db.execute("UPDATE jobs SET revision=revision+1")
        case = {"job_id": jobs[0]["job_id"], "status": "resolved", "summary": "old resolution",
                "evidence": ["old receipt"], "user_summary": "任务已恢复。", "next_step": "无需你处理。"}
        self.worker.queue.finish(batch, [case], 3, max_attempts=1)
        self.worker.prepare_reports({"batch": batch, "jobs": jobs})
        self.assertEqual(self.worker.queue.db.execute('SELECT count(*) FROM reports').fetchone()[0], 0)

    def test_task_title_cannot_inject_diagnostics_or_a_long_notification(self):
        case = self.authored({'job_id': 'job-a', 'status': 'needs_attention', 'summary': 'internal', 'evidence': [],
                              'user_report': '公众号文章没有发布。'})
        for title in ("任务" * 500, "任务\n验证依据：enterprise_graph.search_objects", "任务\nactor=agent:steward"):
            with self.subTest(title=title):
                report = repair_notification(title, "needs_attention", case)
                self.assertEqual(report['title'], '')
                self.assertEqual(report['body'], case['user_report'])

    def test_cancelled_repair_is_held_instead_of_restarted(self):
        cases, error = self.adapter.results(self.snapshot("cancelled"), self.active)
        self.assertEqual(error, "")
        self.assertEqual(cases[0]["status"], "needs_attention")
        report = repair_notification("每日公众号文章", cases[0]["status"], cases[0])
        self.assertIsNone(report)

    def test_running_or_ambiguous_documents_cannot_close_work(self):
        self.assertEqual(self.adapter.results(self.snapshot("running"), self.active)[0], [])
        snapshot = self.snapshot()
        snapshot["messages"] *= 2
        self.assertEqual(self.adapter.results(snapshot, self.active)[0], [])

    def test_terminal_report_recovers_after_restart_and_retries_without_execution(self):
        self.ingest("scheduled")
        with patch.object(self.worker, "native", return_value={"discussion_id": "discussion-a"}):
            self.worker.start()
        active = self.worker.active
        self.worker.prepare_reports(active)
        self.assertEqual(self.worker.queue.db.execute("SELECT COUNT(*) FROM reports").fetchone()[0], 0)
        case = self.authored({"job_id": active["jobs"][0]["job_id"], "status": "needs_attention", "summary": "Requires a decision", "evidence": [], 'user_report': '发布还需你确认收件范围。'})
        self.worker.queue.finish(active["batch"], [case], 3)
        self.worker.recover_reports()
        self.worker.recover_reports()
        self.assertEqual(self.worker.queue.db.execute("SELECT COUNT(*) FROM reports").fetchone()[0], 1)
        with patch.object(self.worker.direct, "report", side_effect=RuntimeError("delivery unavailable")), patch.object(self.worker, "native") as native:
            self.worker.flush_reports()
            native.assert_not_called()
        self.worker.queue.db.execute("UPDATE reports SET next_attempt_at=0")
        self.worker.queue.db.commit()
        with patch.object(self.worker.direct, "report", return_value={"discussion_id": "result-chat"}) as report:
            self.worker.flush_reports()
            self.worker.flush_reports()
            report.assert_called_once()

    def test_final_report_has_only_fixed_recipient_and_reuses_delivery_key(self):
        self.api.request.return_value = {"discussion_id": "result-chat"}
        self.config["result_recipient"]["channels"] = ["weixin"]
        self.adapter.report({"key": "fixed-result-key", "title": "", "body": "封面已生成，文章仍等待发布审批。", 'source': 'agent_report'})
        calls = self.api.request.call_args_list
        self.assertEqual(calls[0].args[1]["direct_target_id"], "human-a")
        self.assertEqual(calls[1].args[1]["human_profile_id"], "human-a")
        self.assertEqual(calls[1].args[1]["event_type"], "qa")
        self.assertEqual(calls[1].args[1]["idempotency_key"], calls[0].args[2])

    def test_external_api_origin_is_rejected_before_loading_credentials(self):
        with self.assertRaisesRegex(ValueError, "local Orrery"):
            LocalAPI({"api_origin": "https://elsewhere.example"})

    def test_legacy_execution_is_retained_while_new_work_uses_direct_mode(self):
        self.ingest("old-scheduled")
        with patch.object(self.worker, "native", return_value={"discussion_id": "old"}):
            self.worker.start()
        legacy = self.worker.active
        handoff = legacy["directory"] / "handoff.json"
        data = json.loads(handoff.read_text())
        data.pop("execution_mode")
        handoff.write_text(json.dumps(data))
        restarted = Worker(self.config)
        self.addCleanup(restarted.queue.db.close)
        restarted.restore()
        self.assertIsNone(restarted.active)
        self.assertEqual(len(restarted.legacy_active), 1)
        self.ingest("new-scheduled")
        with patch.object(restarted, "native", return_value={"discussion_id": "new"}):
            restarted.start()
        direct_batch = restarted.active["batch"]
        with patch.object(restarted, "native", return_value={"found": True, "run": {"status": "running"}}):
            restarted.reap_legacy()
        self.assertEqual(restarted.active["batch"], direct_batch)
        self.assertEqual(restarted.active["execution_mode"], "direct")
        self.assertEqual(restarted.legacy_active[0]["execution_mode"], "scheduled")
        resumed = Worker(self.config)
        self.addCleanup(resumed.queue.db.close)
        resumed.restore()
        self.assertEqual(resumed.active["batch"], direct_batch)
        self.assertEqual(len(resumed.legacy_active), 1)


if __name__ == "__main__":
    unittest.main()
