import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

from codex_repair_worker import Collector, Queue, Worker, context_receipts, prompt_for, redact, sql_text, validate_results


class RepairQueueTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.queue = Queue(Path(self.temp.name) / "queue.sqlite3")
        self.addCleanup(self.queue.db.close)

    def observation(self, key="one", run="run-1"):
        return {"observation_key": key, "job_key": "runtime:" + run, "priority": 10,
                "summary": "failed", "source": "reliability", "runtime_run_id": run}

    def test_multiple_sources_of_one_run_dispatch_once_and_replay_is_idempotent(self):
        first = self.observation()
        self.assertTrue(self.queue.ingest(first, 1))
        self.assertFalse(self.queue.ingest(first, 2))
        self.assertTrue(self.queue.ingest(self.observation("queue-event"), 3))
        batch, jobs = self.queue.claim(4, 4)
        self.assertEqual(len(jobs), 1)
        self.assertEqual(len(jobs[0]["observations"]), 2)
        self.assertEqual(self.queue.claim(4, 5)[1], [])
        self.assertTrue(batch)

    def test_different_runs_with_same_generic_failure_remain_separate(self):
        self.queue.ingest(self.observation("one", "run-1"), 1)
        self.queue.ingest(self.observation("two", "run-2"), 1)
        self.assertEqual(len(self.queue.claim(4, 2)[1]), 2)

    def test_interrupted_process_retries_and_eventually_requires_attention(self):
        self.queue.ingest(self.observation(), 1)
        self.queue.claim(1, 2)
        self.queue.recover(3, 2)
        self.assertEqual(self.queue.status()["counts"], {"retry_wait": 1})
        self.assertEqual(self.queue.claim(1, 4)[1], [])
        self.queue.claim(1, 1000)
        self.queue.recover(1001, 2)
        self.assertEqual(self.queue.status()["counts"], {"needs_attention": 1})

    def test_new_evidence_during_repair_is_not_silently_marked_resolved(self):
        self.queue.ingest(self.observation(), 1)
        batch, jobs = self.queue.claim(1, 2)
        self.queue.ingest(self.observation("new-error"), 3)
        self.queue.finish(batch, [{"job_id": jobs[0]["job_id"], "status": "resolved", "summary": "fixed", "evidence": ["test passed"]}], 4)
        self.assertEqual(self.queue.status()["counts"], {"pending": 1})

    def test_incomplete_batch_results_preserve_unreported_work(self):
        self.queue.ingest(self.observation("one", "run-1"), 1)
        self.queue.ingest(self.observation("two", "run-2"), 1)
        batch, jobs = self.queue.claim(4, 2)
        self.queue.finish(batch, [{"job_id": jobs[0]["job_id"], "status": "resolved", "summary": "fixed", "evidence": ["verified"]}], 3)
        self.assertEqual(self.queue.status()["counts"], {"resolved": 1, "retry_wait": 1})

    def test_attention_hold_survives_receipts_during_and_after_attempt(self):
        self.queue.ingest(self.observation(), 1)
        batch, jobs = self.queue.claim(1, 2)
        self.queue.ingest(self.observation("during-attempt"), 3)
        self.queue.finish(batch, [{"job_id": jobs[0]["job_id"], "status": "needs_attention",
                                  "summary": "Existing approval pending", "evidence": ["approval-1"]}], 4)
        self.assertEqual(self.queue.status()["counts"], {"needs_attention": 1})
        self.queue.ingest(self.observation("later-receipt"), 5)
        self.assertEqual(self.queue.claim(1, 10000)[1], [])
        row = self.queue.db.execute("SELECT status, attempts, revision FROM jobs").fetchone()
        self.assertEqual(tuple(row), ("needs_attention", 1, 3))
        self.assertEqual(self.queue.db.execute("SELECT COUNT(*) FROM observations").fetchone()[0], 3)

    def test_duplicate_scheduled_task_is_saved_without_a_second_dispatch(self):
        queue = Queue(Path(self.temp.name) / "task-hold.sqlite3", suppress_duplicate_task_repairs=True)
        self.addCleanup(queue.db.close)
        first = {"observation_key": "scheduled:first", "job_key": "runtime:first", "priority": 80,
                 "summary": "failed", "source": "scheduled", "task_id": "task-wechat",
                 "runtime_run_id": "first", "title": "Jordan Belfort - 每日微信公众号正式稿接口草稿箱"}
        second = dict(first, observation_key="scheduled:second", job_key="runtime:second", runtime_run_id="second")
        self.assertTrue(queue.ingest(first, 1))
        batch, jobs = queue.claim(1, 2)
        queue.finish(batch, [{"job_id": jobs[0]["job_id"], "status": "needs_attention",
                              "summary": "blocked", "evidence": ["permission"]}], 3, max_attempts=1)
        self.assertTrue(queue.ingest(second, 4))
        row = queue.db.execute("SELECT status,attempts,last_result FROM jobs WHERE job_key=?", ("runtime:second",)).fetchone()
        self.assertEqual(row[0:2], ("needs_attention", 0))
        self.assertTrue(json.loads(row[2])["suppressed_duplicate"])
        self.assertEqual(queue.claim(1, 5)[1], [])

    def test_new_receipts_cannot_bypass_retry_limit(self):
        self.queue.ingest(self.observation(), 1)
        batch, jobs = self.queue.claim(1, 2)
        self.queue.ingest(self.observation("during-attempt"), 3)
        self.queue.finish(batch, [{"job_id": jobs[0]["job_id"], "status": "retry",
                                  "summary": "Still unavailable", "evidence": []}], 4, max_attempts=1)
        self.assertEqual(self.queue.status()["counts"], {"needs_attention": 1})
        self.assertEqual(self.queue.claim(1, 10000)[1], [])

    def test_hold_old_backlog_preserves_active_recent_and_unreconciled_runs(self):
        for index, occurred in enumerate((10, 20, 6000, 30)):
            observation = self.observation(str(index), "run-" + str(index))
            observation["occurred_at"] = occurred
            if index == 3:
                observation["error_code"] = "scheduled_terminal_state_not_reconciled"
            self.queue.ingest(observation, index + 1)
        self.queue.db.execute("UPDATE jobs SET status='running' WHERE job_key='runtime:run-0'")
        self.queue.db.commit()
        self.assertEqual(self.queue.hold_outside_window(5000, 10000), 1)
        self.assertEqual(self.queue.hold_outside_window(5000, 10001), 0)
        self.assertEqual(self.queue.status()["counts"], {"running": 1, "needs_attention": 1, "pending": 2})
        self.assertEqual(self.queue.db.execute("SELECT COUNT(*) FROM observations").fetchone()[0], 4)

    def test_resolved_case_does_not_reopen_on_poll_but_new_failure_does(self):
        first = self.observation()
        self.queue.ingest(first, 1)
        batch, jobs = self.queue.claim(1, 2)
        self.queue.finish(batch, [{"job_id": jobs[0]["job_id"], "status": "resolved", "summary": "fixed", "evidence": ["verified"]}], 3)
        self.queue.ingest(first, 4)
        self.assertEqual(self.queue.status()["counts"], {"resolved": 1})
        self.queue.ingest(self.observation("another-failure"), 5)
        self.assertEqual(self.queue.claim(1, 6)[1][0]["attempt"], 1)


class CollectionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config = {"artifact_root": self.temp.name, "state_dir": self.temp.name,
                       "source_root": "/source", "stale_grace_seconds": 900}
        self.collector = Collector(self.config)

    def test_nonretryable_semantic_failure_still_goes_to_codex(self):
        item = self.collector.normalize({"source": "reliability", "source_id": "e1",
            "runtime_run_id": "agent-run:run-1", "retryable": False,
            "error_code": "agent_runtime_blocked", "summary": "crop failed"}, 0, 10000)
        self.assertEqual(item["job_key"], "runtime:run-1")

    def test_old_but_active_run_is_not_declared_failed(self):
        item = {"source": "scheduled", "source_id": "s1", "status": "running", "created_at": 1}
        self.assertIsNone(self.collector.normalize(item, 0, 100000))

    def test_historical_delivery_failure_respects_intake_window(self):
        item = {"source": "scheduled", "source_id": "s1", "status": "completed",
                "delivery_status": "failed", "created_at": 1, "completed_at": 10}
        self.assertIsNone(self.collector.normalize(dict(item), 5000, 10000))
        item["completed_at"] = 6000
        self.assertIsNotNone(self.collector.normalize(item, 5000, 10000))

    def test_terminal_trace_detects_stuck_run_even_before_collection_cursor(self):
        path = Path(self.temp.name) / "runs/runtime-1/trace-v2/runtime-trace.json"
        path.parent.mkdir(parents=True)
        path.write_text(json.dumps({"status": "completed", "completed_at": 100,
                                   "result": {"RunID": "runtime-1", "Blocker": {"Summary": "missing job"}}}))
        item = self.collector.normalize({"source": "scheduled", "source_id": "s1", "status": "running",
            "trace_ref": "/uploads/runs/runtime-1/trace-v2/runtime-trace.json", "created_at": 1}, 5000, 10000)
        self.assertEqual(item["job_key"], "runtime:runtime-1")
        self.assertEqual(item["error_code"], "scheduled_terminal_state_not_reconciled")
        self.assertEqual(item["priority"], 100)

    def test_trace_path_cannot_escape_artifact_root(self):
        self.assertIsNone(self.collector.trace("/uploads/../../etc/runtime-trace.json"))
        self.assertIsNone(self.collector.trace("file:///etc/passwd"))

    def test_failed_source_does_not_advance_its_cursor_or_block_other_sources(self):
        worker = Worker(self.config)
        self.addCleanup(worker.queue.db.close)
        worker.queue.set_meta("cursor_reliability", 100)
        with patch.object(worker.collector, "repair_ownership", return_value=[]), \
             patch.object(worker.collector, "reliability", side_effect=RuntimeError("unavailable")), \
             patch.object(worker.collector, "scheduled", return_value=[]), \
             patch.object(worker.collector, "discussions", return_value=[]), \
             patch.object(worker.collector, "queues", return_value=[]):
            worker.scan()
        self.assertEqual(worker.queue.get_meta("cursor_reliability"), "100")
        self.assertIsNotNone(worker.queue.get_meta("cursor_queues"))
        self.assertIn("unavailable", worker.queue.get_meta("error_reliability"))


class ResultContractTests(unittest.TestCase):
    def test_claimed_success_requires_evidence_and_known_case(self):
        for cases in [
            [{"job_id": "one", "status": "resolved", "summary": "fixed", "evidence": []}],
            [{"job_id": "other", "status": "retry", "summary": "later", "evidence": []}],
            [{"job_id": "one", "status": "done", "summary": "done", "evidence": []}],
        ]:
            with self.assertRaises(ValueError):
                validate_results({"cases": cases}, {"one"})

    def test_secrets_redacted_and_sql_values_never_interpreted(self):
        self.assertNotIn("secret-value", redact("Authorization=secret-value password=secret-value https://user:secret-value@db/path"))
        self.assertNotIn("DROP", sql_text("'; DROP TABLE jobs; --"))

    def test_repair_assignment_preserves_business_approval_and_verification_boundaries(self):
        prompt = prompt_for([], {"source_root": "/source", "artifact_root": "/artifacts"})
        self.assertIn("does NOT authorize approving/rejecting business approvals", prompt)
        self.assertIn("A proposed patch without necessary", prompt)
        self.assertIn("Do not modify this worker", prompt)


class ExecutionIntegrationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = self.temp.name
        self.config = {"state_dir": str(Path(root) / "state"), "workspace": root,
                       "source_root": root, "artifact_root": root, "agent_id": "steward"}
        self.worker = Worker(self.config)
        self.addCleanup(self.worker.queue.db.close)
        self.worker.queue.ingest({"job_key": "runtime:fixture", "observation_key": "fixture",
                                 "summary": "failure", "priority": 1, "tenant_id": "system"}, 1)

    def start(self):
        with patch.object(self.worker, "native", return_value={"scheduled_task_id": "native"}):
            self.worker.start()

    def result(self, status="resolved"):
        active = self.worker.active
        path = active["result_path"]
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({"cases": [{"job_id": j["job_id"], "status": status,
                        "summary": "verified repair", "evidence": ["test fixture evidence"]} for j in active["jobs"]]}))

    def test_result_file_cannot_complete_a_running_native_task(self):
        self.start()
        self.result()
        with patch.object(self.worker, "native", return_value={"found": True, "run": {"status": "running"}}):
            self.worker.reap()
        self.assertEqual(self.worker.queue.status()["counts"], {"running": 1})

    def test_completed_result_requires_native_identity_links(self):
        self.start()
        self.result()
        with patch.object(self.worker, "native", return_value={"found": True, "run": {"status": "completed"}}):
            self.worker.reap()
        self.assertEqual(self.worker.queue.status()["counts"], {"retry_wait": 1})

    def test_native_completion_and_verified_result_close_assignment(self):
        self.start()
        self.result()
        run = self.native_run("completed")
        with patch.object(self.worker, "native", return_value={"found": True, "run": run}), patch("codex_repair_worker.context_receipts", return_value=["verified context"]):
            self.worker.reap()
        self.assertEqual(self.worker.queue.status()["counts"], {"resolved": 1})

    def test_claimed_success_without_context_receipts_requires_attention(self):
        self.start()
        self.result()
        run = self.native_run("completed")
        with patch.object(self.worker, "native", return_value={"found": True, "run": run}):
            self.worker.reap()
        self.assertEqual(self.worker.queue.status()["counts"], {"needs_attention": 1})

    def native_run(self, status):
        return dict(status=status, runtime_run_id="runtime", mission_id="mission", task_id="task",
                    responsibility_id="responsibility", scheduled_task_id="codex-repair:" + self.worker.active["batch"])

    def test_failed_native_run_accepts_attention_without_retry_or_claiming_success(self):
        self.start()
        self.result("needs_attention")
        with patch.object(self.worker, "native", return_value={"found": True, "run": self.native_run("failed")}):
            self.worker.reap()
        self.assertEqual(self.worker.queue.status()["counts"], {"needs_attention": 1})
        self.assertEqual(self.worker.queue.claim(4, time.time()+86400)[1], [])
        self.assertEqual(self.worker.queue.get_meta("last_native_result_error"), "")

    def test_failed_native_run_does_not_accept_claimed_resolution(self):
        self.start()
        self.result()
        with patch.object(self.worker, "native", return_value={"found": True, "run": self.native_run("failed")}):
            self.worker.reap()
        self.assertEqual(self.worker.queue.status()["counts"], {"needs_attention": 1})
        self.assertIn("independent verification", self.worker.queue.status()["needs_attention"][0]["last_result"])

    def test_cross_batch_and_malformed_results_still_retry(self):
        self.start()
        self.result("needs_attention")
        run = self.native_run("failed")
        run["scheduled_task_id"] = "codex-repair:another-batch"
        results, error = self.worker.read_results({"found": True, "run": run}, self.worker.active)
        self.assertEqual(results, [])
        self.assertIn("another batch", error)
        self.worker.active["result_path"].write_text('{"cases": "invalid"}')
        with patch.object(self.worker, "native", return_value={"found": True, "run": self.native_run("failed")}):
            self.worker.reap()
        self.assertEqual(self.worker.queue.status()["counts"], {"retry_wait": 1})

    def discarded_result(self):
        self.start()
        self.result("needs_attention")
        active = self.worker.active
        (active["directory"] / "native-run.json").write_text(json.dumps({"found": True, "run": self.native_run("failed")}))
        self.worker.queue.finish(active["batch"], [], time.time())

    def test_reconciliation_is_reviewable_idempotent_and_preserves_attempt_count(self):
        self.discarded_result()
        self.assertEqual(len(self.worker.reconcile_results()["changes"]), 1)
        self.assertEqual(self.worker.queue.status()["counts"], {"retry_wait": 1})
        self.assertEqual(len(self.worker.reconcile_results(True)["changes"]), 1)
        self.assertEqual(self.worker.queue.status()["counts"], {"needs_attention": 1})
        self.assertEqual(self.worker.reconcile_results(True)["changes"], [])
        self.assertEqual(self.worker.queue.db.execute("SELECT attempts FROM jobs").fetchone()[0], 1)

    def test_reconciliation_cannot_overwrite_new_evidence_or_active_work(self):
        self.discarded_result()
        self.worker.queue.ingest({"job_key": "runtime:fixture", "observation_key": "new-evidence",
                                 "summary": "new failure", "priority": 1, "tenant_id": "system"}, time.time())
        self.assertEqual(self.worker.reconcile_results(True)["changes"], [])
        self.worker.queue.claim(1, time.time()+86400)
        self.assertEqual(self.worker.reconcile_results(True)["changes"], [])
        self.assertEqual(self.worker.queue.status()["counts"], {"running": 1})

    def test_context_gate_rejects_failed_tool_and_cross_run_trace(self):
        root = Path(self.temp.name)
        events = root / "trace/events"
        events.mkdir(parents=True)
        refs = []
        for i, name in enumerate(("enterprise_graph.search_objects", "enterprise_graph.build_context_bundle", "knowledge_search", "memory_search")):
            for suffix, event in (("call", {"kind": "tool_call", "tool_call": {"tool_use_id": str(i), "name": name}}),
                                  ("result", {"kind": "tool_result", "tool_result": {"tool_use_id": str(i), "is_error": i==3, "content": [{"text": "actual result"}]}})):
                ref = f"trace/events/{i}-{suffix}.json"
                (root / ref).write_text(json.dumps({"payload": {"event": event}}))
                refs.append("/uploads/" + ref)
        path = root / "trace/runtime-trace.json"
        path.write_text(json.dumps({"run_id": "one", "event_refs": refs}))
        snapshot = {"trace_ref": "/uploads/trace/runtime-trace.json", "run": {"runtime_run_id": "one"}}
        with self.assertRaisesRegex(ValueError, "memory_search"):
            context_receipts(snapshot, root)
        result_path = events / "3-result.json"
        event = json.loads(result_path.read_text())
        event["payload"]["event"]["tool_result"]["is_error"] = False
        result_path.write_text(json.dumps(event))
        self.assertEqual(len(context_receipts(snapshot, root)), 4)
        snapshot["run"]["runtime_run_id"] = "another"
        with self.assertRaisesRegex(ValueError, "another runtime"):
            context_receipts(snapshot, root)

    def test_restart_reuses_identity_after_uncertain_submission(self):
        with patch.object(self.worker, "native", side_effect=RuntimeError("response lost after commit")):
            self.worker.start()
        original = self.worker.active["batch"]
        restarted = Worker(self.config)
        self.addCleanup(restarted.queue.db.close)
        restarted.restore()
        self.assertEqual(restarted.active["batch"], original)
        self.assertFalse(restarted.active["submitted"])
        self.assertEqual(restarted.queue.status()["counts"], {"running": 1})
        with patch.object(restarted, "native", return_value={"scheduled_task_id": "same-native-task"}) as native:
            restarted.reap()
        self.assertEqual(native.call_args.args[0], "submit")
        self.assertEqual(restarted.queue.db.execute("SELECT attempts FROM jobs").fetchone()[0], 1)

    def test_other_tenant_never_dispatched_as_system(self):
        self.worker.queue.ingest({"job_key": "runtime:other", "observation_key": "other",
                                 "summary": "failure", "priority": 2, "tenant_id": "other"}, 2)
        with patch.object(self.worker, "native") as native:
            self.worker.start()
        native.assert_not_called()


if __name__ == "__main__":
    unittest.main()
