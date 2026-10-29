import hashlib
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from codex_repair_worker import Collector, Queue
from failure_center import assess, collect, context, direct_prompt, write_feedback


class FailureCenterTests(unittest.TestCase):
    def test_managed_assignment_preserves_bounded_safe_transport_diagnosis(self):
        observation = dict(self.observation(), diagnostics={'cause': 'websocket reset token=private',
            'error_kind': 'websocket', 'sdk_status_code': '503', 'stack': 'untrusted-stack' * 5000})
        job = {'job_id': 'job-a', 'observations': [observation]}
        job['failure_context'] = context(job)
        prompt = direct_prompt([job])
        self.assertIn('websocket reset', prompt)
        self.assertIn('sdk_status_code', prompt)
        self.assertNotIn('private', prompt)
        self.assertNotIn('untrusted-stack', prompt)
        self.assertLess(len(prompt), 2000)

    def observation(self, occurrence=1):
        return {'source': 'failure_center', 'tenant_id': 'system', 'incident_id': 'incident-one',
                'incident_version': occurrence, 'occurrence_count': occurrence, 'last_seen_ns': 1792140000000000000 + occurrence,
                'component': 'javascript:/holdings', 'error_code': 'TypeError', 'source_id': 'observation-' + str(occurrence),
                'occurred_at': 1792140000, 'summary': 'render failed', 'priority': 10}

    def test_durable_intake_coalesces_replay_and_eligible_claims(self):
        directory = self.enterContext(tempfile.TemporaryDirectory())
        q = Queue(Path(directory) / 'queue.sqlite3')
        self.addCleanup(q.db.close)
        collector = Collector({})
        first = collector.normalize(self.observation(), 0, 1792140001)
        self.assertTrue(q.ingest(first, 1))
        self.assertFalse(q.ingest(first, 2))
        old = dict(first, source='queues', observation_key='old', job_key='legacy', priority=100)
        q.ingest(old, 2)
        batch, jobs = q.claim(1, 3, ['scheduled', 'failure_center'])
        self.assertEqual(len(jobs), 1)
        self.assertEqual(jobs[0]['observations'][0]['incident_id'], 'incident-one')
        changed = collector.normalize(self.observation(2), 0, 1792140002)
        q.ingest(changed, 4)
        q.finish(batch, [{'job_id': jobs[0]['job_id'], 'status': 'resolved', 'summary': 'fixed', 'evidence': ['actual-test']}], 5, 1)
        self.assertEqual(q.db.execute('SELECT status FROM jobs WHERE batch_id=?', (batch,)).fetchone()[0], 'needs_attention')

    def test_resolution_requires_receipts_binding_and_current_occurrence(self):
        job = {'observations': [self.observation()]}
        job['failure_context'] = context(job)
        case = {'status': 'resolved', 'summary': 'fixed', 'contract_digest': job['failure_context']['digest'],
                'repair_kind': 'repaired', 'verification_tool_ids': ['actual-test']}
        acceptance = SimpleNamespace(technical_receipts=lambda snapshot, ids: None)
        reader = SimpleNamespace(rows=lambda *args: [{'occurrence_count': 1, 'last_seen_at': self.observation()['last_seen_ns']}])
        checked = assess(case, job, {}, acceptance, reader)
        self.assertEqual(checked['verification']['status'], 'verified')
        reader.rows = lambda *args: [{'occurrence_count': 2, 'last_seen_at': self.observation(2)['last_seen_ns']}]
        self.assertEqual(assess(case, job, {}, acceptance, reader)['status'], 'needs_attention')
        self.assertEqual(assess(dict(case, contract_digest='wrong'), job, {}, acceptance, reader)['status'], 'needs_attention')
        acceptance.technical_receipts = lambda *args: (_ for _ in ()).throw(ValueError('missing actual receipt'))
        self.assertEqual(assess(case, job, {}, acceptance, reader)['status'], 'needs_attention')

    def test_feedback_is_durable_redacted_and_bound_to_verified_run(self):
        directory = self.enterContext(tempfile.TemporaryDirectory())
        root = Path(directory)
        q = Queue(root / 'queue.sqlite3')
        self.addCleanup(q.db.close)
        item = Collector({}).normalize(self.observation(), 0, 1792140001)
        q.ingest(item, 1)
        worker = SimpleNamespace(queue=q, root=root, config={'failure_center_feedback_dir': str(root / 'feedback')})
        write_feedback(worker)
        target = root / 'feedback' / (hashlib.sha256(b'system\0incident-one').hexdigest() + '.json')
        self.assertEqual(json.loads(target.read_text())['status'], 'pending')
        batch, jobs = q.claim(1, 2, ['failure_center'])
        write_feedback(worker)
        self.assertEqual(json.loads(target.read_text())['status'], 'running')
        snapshot = root / 'attempts' / batch / 'native-run.json'
        snapshot.parent.mkdir(parents=True)
        snapshot.write_text(json.dumps({'run': {'runtime_run_id': 'repair-run'}}))
        q.finish(batch, [{'job_id': jobs[0]['job_id'], 'status': 'resolved', 'summary': 'fixed password=private', 'evidence': ['actual-test'],
                         'verification': {'status': 'verified'}}], 3)
        write_feedback(worker)
        receipt = json.loads(target.read_text())
        self.assertTrue(receipt['verified'])
        self.assertEqual(receipt['repair_run_id'], 'repair-run')
        self.assertNotIn('private', receipt['summary'])

    def test_collector_respects_activation_floor_and_loads_only_matching_history(self):
        with tempfile.TemporaryDirectory() as directory:
            seen = []
            row = dict(self.observation(), observation_id='obs-one', failure_source='web_client')
            reader = SimpleNamespace(rows=lambda database, sql: seen.append(sql) or [row])
            values = list(collect(reader, {'artifact_root': directory, 'failure_center_since': '2026-10-16T09:00:00Z'}, 0))
            self.assertIn('1792141200000000000', seen[0])
            self.assertEqual(values[0]['source'], 'failure_center')
            self.assertNotIn('diagnostics', values[0])


if __name__ == '__main__':
    unittest.main()
