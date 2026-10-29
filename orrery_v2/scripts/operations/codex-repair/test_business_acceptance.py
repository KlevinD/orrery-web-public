import copy
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from business_acceptance import BusinessAcceptance
from codex_repair_worker import Worker, repair_notification
from direct_repair import DirectRepair


class BusinessAcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.subject = BusinessAcceptance(Mock(), self.root)
        self.run = {'scheduled_run_id': 'run-a', 'scheduled_task_id': 'schedule-a', 'tenant_id': 'tenant-a',
                    'task_id': 'task-a', 'mission_id': 'mission-a', 'runtime_run_id': 'runtime-a',
                    'started_at': '2026-09-09T00:00:00Z', 'status': 'failed',
                    'configuration_snapshot': {'scheduled_task_id': 'schedule-a', 'tenant_id': 'tenant-a',
                        'description': 'Retain business constraints', 'target': {'task_goal': 'Produce the authorized report', 'prompt': 'Do not publish before approval'}}}
        self.task = {'Status': 'approved', 'Contract': {'ID': 'task-a', 'MissionID': 'mission-a', 'Goal': 'Produce the authorized report',
            'AcceptanceCriteria': [{'ID': 'c1', 'Description': 'Report includes requested totals'}],
            'ExpectedDeliverables': [{'ID': 'd1', 'Title': 'Report'}],
            'EvidenceRequirements': [{'ID': 'r1', 'Kind': 'artifact', 'Required': True}],
            'Constraints': [{'Description': 'No publishing'}]}}
        self.records = {('scheduled_task_runs', 'run-a'): self.run, ('mission_task_records', 'task-a'): self.task}
        self.related = {
            ('mission_task_submissions', 'task_id', 'task-a'): [{'ID': 'submission-a', 'TaskID': 'task-a', 'RunID': 'runtime-a', 'TaskVersionAccepted': 4, 'EvidenceRefs': ['e1'], 'DeliverableRefs': ['d1-sub']}],
            ('mission_task_evidence_submissions', 'submission_id', 'submission-a'): [{'TaskID': 'task-a', 'SubmissionID': 'submission-a', 'Evidence': {'ID': 'e1', 'Kind': 'artifact', 'AcceptanceCriterionID': 'c1', 'EvidenceRequirementID': 'r1', 'ArtifactRef': {'ID': 'a1', 'URI': '/original-workspace/report.md'}}}],
            ('mission_task_deliverable_submissions', 'submission_id', 'submission-a'): [{'TaskID': 'task-a', 'SubmissionID': 'submission-a', 'Deliverable': {'ID': 'd1-sub', 'ExpectedDeliverableID': 'd1', 'ArtifactRef': {'ID': 'a1'}}}],
        }
        self.subject.record = Mock(side_effect=lambda table, key: copy.deepcopy(self.records.get((table, key), {})))
        self.subject.related = Mock(side_effect=lambda table, field, key: copy.deepcopy(self.related.get((table, field, key), [])))
        self.job = {'job_id': 'job-a', 'observations': [{'source': 'scheduled', 'tenant_id': 'tenant-a', 'task_id': 'schedule-a', 'source_id': 'run-a'}]}
        self.context = self.subject.capture(self.job)
        self.job['business_context'] = self.context

    def artifact(self):
        content = b'Report with totals'
        sha = hashlib.sha256(content).hexdigest()
        h = lambda s, n: hashlib.sha256(s.encode()).hexdigest()[:n]
        key = '/'.join(('scheduled-runs', 'v1', h('tenant-a', 16), h('run-a', 32), h('a1', 32), str(len(content))+'-'+sha, 'report.md'))
        path = self.root / key
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
        self.run['result'] = {'artifacts': [{'id': 'a1', 'uri': 'artifact://'+key, 'sha256': sha, 'size_bytes': len(content), 'file_name': 'report.md'}]}
        self.run['status'] = 'completed'
        return path

    def trace(self, name='bash', error=False, runtime='repair-run'):
        folder = self.root / 'agent-runs' / 'repair-run'
        folder.mkdir(parents=True, exist_ok=True)
        refs = []
        for index, event in enumerate([{'kind': 'tool_call', 'tool_call': {'name': name, 'tool_use_id': 'check-a'}},
                                      {'kind': 'tool_result', 'tool_result': {'tool_use_id': 'check-a', 'is_error': error, 'content': 'Verified affected endpoint'}}]):
            path = folder / (str(index)+'.json')
            path.write_text(json.dumps({'payload': {'event': event}}), encoding='utf-8')
            refs.append('/uploads/'+path.relative_to(self.root).as_posix())
        path = folder / 'runtime-trace.json'
        path.write_text(json.dumps({'run_id': runtime, 'event_refs': refs}), encoding='utf-8')
        return {'trace_ref': '/uploads/'+path.relative_to(self.root).as_posix(), 'run': {'runtime_run_id': 'repair-run', 'status': 'completed'}}

    def case(self):
        return {'job_id': 'job-a', 'status': 'resolved', 'summary': 'Scoped repair activated and verified', 'evidence': ['actual endpoint receipt'],
                'contract_digest': self.context['digest'], 'repair_kind': 'repaired', 'verification_tool_ids': ['check-a'], 'business_run_id': 'run-a',
                'user_summary': '业务已完成。', 'next_step': '无需处理。'}

    def test_original_snapshot_and_contract_are_frozen(self):
        self.assertTrue(self.context['available'])
        self.assertEqual(self.context['intent']['instructions'], 'Do not publish before approval')
        self.task['Contract']['Constraints'][0]['Description'] = 'Publish freely'
        self.assertEqual(self.subject.verify(self.context, 'run-a')['status'], 'pending')
        self.assertIn('已变更', self.subject.verify(self.context, 'run-a')['reason'])

    def test_no_contract_or_cross_tenant_binding_fails_closed(self):
        for change in ('tenant', 'goal', 'missing'):
            with self.subTest(change=change):
                original = copy.deepcopy(self.run)
                if change == 'tenant': self.run['tenant_id'] = 'tenant-b'
                elif change == 'goal': self.run['configuration_snapshot']['target']['task_goal'] = 'Different goal'
                else: self.run['configuration_snapshot'] = {}
                self.assertFalse(self.subject.capture(self.job)['available'])
                self.run.clear(); self.run.update(original)

    def test_completion_requires_actual_file_submission_and_evidence(self):
        self.artifact()
        self.assertEqual(self.subject.verify(self.context, 'run-a')['status'], 'verified')
        self.related[('mission_task_evidence_submissions', 'submission_id', 'submission-a')][0]['Evidence']['AcceptanceCriterionID'] = 'different'
        self.assertEqual(self.subject.verify(self.context, 'run-a')['status'], 'pending')

    def test_corrupt_missing_and_foreign_artifacts_cannot_prove_business_recovery(self):
        path = self.artifact()
        path.write_bytes(b'X'*path.stat().st_size)
        self.assertEqual(self.subject.verify(self.context, 'run-a')['status'], 'pending')
        path.unlink()
        self.assertEqual(self.subject.verify(self.context, 'run-a')['status'], 'pending')
        self.artifact()
        self.run['result']['artifacts'][0]['uri'] = 'artifact://scheduled-runs/another-tenant/report.md'
        self.assertEqual(self.subject.verify(self.context, 'run-a')['status'], 'pending')

    def test_reading_artifact_can_update_access_time_without_invalidating_evidence(self):
        path = self.artifact().resolve()
        original_open = Path.open
        @contextmanager
        def open_with_access_update(target, *args, **kwargs):
            with original_open(target, *args, **kwargs) as stream:
                yield stream
            if target == path:
                info = path.stat()
                os.utime(path, ns=(info.st_atime_ns + 10_000_000_000, info.st_mtime_ns))
        with patch.object(Path, 'open', open_with_access_update):
            self.assertEqual(self.subject.verify(self.context, 'run-a')['status'], 'verified')

    def test_goal_equivalence_cannot_cross_tasks_tenants_or_pre_failure_runs(self):
        self.artifact()
        for field, value in [('tenant_id', 'tenant-b'), ('scheduled_task_id', 'schedule-b'), ('started_at', '2026-09-08T00:00:00Z')]:
            with self.subTest(field=field):
                candidate = copy.deepcopy(self.run); candidate.update(scheduled_run_id='run-b'); candidate[field] = value
                self.records[('scheduled_task_runs', 'run-b')] = candidate
                self.assertEqual(self.subject.verify(self.context, 'run-b')['status'], 'pending')

    def test_required_delivery_and_quality_review_are_not_bypassed(self):
        self.run['delivery'] = {'mode': 'announce'}
        self.task['Contract'].update(QACheckRequired=True, QAPlanRequired=True, QAPlanCompleted=False)
        context = self.subject.capture(self.job)
        self.artifact()
        self.assertIn('交付', self.subject.verify(context, 'run-a')['reason'])
        self.run['delivery_status'] = 'delivered'
        self.assertIn('计划', self.subject.verify(context, 'run-a')['reason'])
        self.task['Contract']['QAPlanCompleted'] = True
        self.assertIn('质量验收', self.subject.verify(context, 'run-a')['reason'])
        self.related[('mission_task_qa_results', 'task_id', 'task-a')] = [{'TaskID': 'task-a', 'SubmissionID': 'submission-a', 'Status': 'approved', 'CheckedCriteria': [{'CriterionID': 'c1', 'Status': 'passed'}], 'EvidenceResults': [{'RequirementID': 'r1', 'EvidenceID': 'e1', 'Status': 'passed'}]}]
        self.assertEqual(self.subject.verify(context, 'run-a')['status'], 'verified')

    def test_technical_only_does_not_keep_agent_business_success_claim(self):
        case = self.subject.assess(self.case(), self.job, self.trace())
        self.assertEqual(case['status'], 'technical_recovered')
        self.assertIsNone(repair_notification('报告任务', case['status'], case))
        self.artifact()
        case = self.subject.assess(self.case(), self.job, self.trace())
        self.assertEqual(case['verification']['business']['status'], 'verified')
        self.assertEqual(case['status'], 'resolved')

    def test_technical_proof_requires_real_non_context_success_in_this_run(self):
        for kwargs in ({'name': 'knowledge_search'}, {'error': True}, {'runtime': 'another-run'}):
            with self.subTest(kwargs=kwargs):
                self.assertEqual(self.subject.assess(self.case(), self.job, self.trace(**kwargs))['status'], 'needs_attention')
        case = self.case(); case['contract_digest'] = 'invented'
        self.assertEqual(self.subject.assess(case, self.job, self.trace())['status'], 'needs_attention')

    def test_platform_gate_is_integrated_and_agent_cannot_supply_its_verdict(self):
        adapter = DirectRepair({'artifact_root': self.temp.name}, Mock(), Mock())
        adapter.business = self.subject
        case = self.case(); case['verification'] = {'version': 1, 'business': {'status': 'verified'}}
        snapshot = self.trace(); snapshot['messages'] = [{'Content': json.dumps({'cases': [case]})}]
        with patch('direct_repair.context_receipts'):
            results, error = adapter.results(snapshot, {'jobs': [self.job]})
        self.assertFalse(error)
        self.assertEqual(results[0]['status'], 'technical_recovered')
        with patch('direct_repair.context_receipts'):
            results, _ = adapter.results(snapshot, {'jobs': [{'job_id': 'job-a'}]})
        self.assertNotIn('verification', results[0])

    def test_sensitive_or_unavailable_original_context_is_not_inferred(self):
        self.run['configuration_snapshot']['target']['prompt'] = 'password=private-value'
        context = self.subject.capture(self.job)
        self.assertFalse(context['available'])
        self.assertNotIn('private-value', json.dumps(context))
        self.assertEqual(self.subject.verify(context, 'run-a')['status'], 'pending')

    def test_natural_completion_promotes_once_without_new_dispatch_or_changing_business(self):
        config = {'state_dir': self.temp.name, 'workspace': self.temp.name, 'artifact_root': self.temp.name,
                  'source_root': self.temp.name, 'agent_id': 'steward', 'tenant_id': 'tenant-a', 'execution_mode': 'direct', 'sources': ['scheduled'],
                  'result_recipient': {'human_profile_id': 'human-a', 'tenant_id': 'tenant-a'}}
        worker = Worker(config); self.addCleanup(worker.queue.db.close)
        observation = dict(self.job['observations'][0], observation_key='o1', job_key='runtime:runtime-a', priority=10, summary='Failure', title='报告任务')
        worker.queue.ingest(observation, 1)
        batch, jobs = worker.queue.claim(1, 2)
        jobs[0]['business_context'] = self.context
        case = self.subject.assess(self.case(), self.job, self.trace()); case['job_id'] = jobs[0]['job_id']
        worker.queue.finish(batch, [case], 3)
        directory = self.root / 'attempts' / batch; directory.mkdir(parents=True)
        (directory / 'assignments.json').write_text(json.dumps(jobs), encoding='utf-8')
        worker.direct.business = self.subject
        self.subject.latest_candidates = Mock(return_value=['run-a'])
        worker.prepare_reports({'batch': batch, 'jobs': jobs})
        self.assertEqual(worker.queue.claim(1, 4)[1], [])
        self.artifact(); original = copy.deepcopy(self.run)
        with patch.object(worker, 'native') as native:
            worker.recheck_business(); worker.recheck_business(); worker.recheck_business()
            native.assert_not_called()
        self.assertEqual(worker.queue.status()['counts'], {'resolved': 1})
        self.assertEqual(worker.queue.db.execute('SELECT COUNT(*) FROM reports').fetchone()[0], 0)
        self.assertEqual(self.run, original)
        worker.queue.ingest(dict(observation, observation_key='o2'), 5)
        self.assertEqual(worker.queue.status()['counts'], {'pending': 1})


if __name__ == '__main__':
    unittest.main()
