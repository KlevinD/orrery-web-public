import copy
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

from codex_repair_worker import Worker, utc
from direct_repair import DirectRepair, DISPATCHER


class TerminalRecoveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = {'artifact_root': str(self.root), 'agent_id': 'steward'}
        self.reader = Mock()
        self.adapter = DirectRepair(self.config, self.reader, Mock())
        self.active = {'batch': 'batch-a', 'tenant': 'tenant-a', 'directory': self.root,
                       'jobs': [{'job_id': 'job-a'}]}
        (self.root / 'prompt.txt').write_text('Assigned repair only.', encoding='utf-8')
        self.command = {'ConversationID': 'conversation-a', 'TurnID': 'turn-a', 'MessageID': 'message-a',
                        'IdempotencyKey': self.adapter.key(self.active)}
        self.turn = {'TurnID': 'turn-a', 'ConversationID': 'conversation-a', 'RootMessageID': 'message-a',
                     'IdempotencyKey': self.adapter.key(self.active), 'Status': 'running', 'RunIDs': None}
        self.conversation = {'TenantID': 'tenant-a', 'CreatedBy': DISPATCHER, 'DirectAgentID': 'steward',
                             'ActiveTurnID': 'turn-a', 'Revision': 2}
        self.source = {'ConversationID': 'conversation-a', 'TurnID': 'turn-a', 'AuthorID': DISPATCHER,
                       'Role': 'user', 'Content': 'Assigned repair only.'}
        self.run = {'RunID': 'run-a', 'ConversationID': 'conversation-a', 'TurnID': 'turn-a', 'AgentID': 'steward',
                    'Status': 'completed', 'CreatedAt': utc(time.time() - 600), 'StartedAt': utc(time.time() - 600),
                    'CompletedAt': utc(time.time() - 300),
                    'TraceRef': '/uploads/discussion-conversations/conversation-a/runs/run-a/trace-v2/runtime-trace.json'}
        self.case = {'job_id': 'job-a', 'status': 'needs_attention', 'summary': 'Needs owner input.', 'evidence': ['receipt']}
        self.trace = {'run_id': 'run-a', 'agent_id': 'steward', 'status': 'completed', 'completed_at': self.run['CompletedAt'],
                      'result': {'RunID': 'run-a', 'AgentID': 'steward', 'Blocker': None,
                                 'Summary': {'Text': json.dumps({'cases': [self.case]})}}}
        self.trace_path = self.root / self.run['TraceRef'].removeprefix('/uploads/')
        self.trace_path.parent.mkdir(parents=True)
        self.write_trace()
        self.runs = [self.run]
        self.claims = []
        self.approvals = []
        self.trace_indexes = []
        self.failure_events = []
        self.reader.rows.side_effect = self.rows
        self.adapter.command = Mock(side_effect=lambda active: copy.deepcopy(self.command))
        self.adapter.record = Mock(side_effect=self.record)

    def write_trace(self):
        self.trace_path.write_text(json.dumps(self.trace), encoding='utf-8')

    def rows(self, database, query):
        if database == 'orrery_runtime':
            return self.claims
        if 'FROM public.agent_runtime_traces' in query:
            return self.trace_indexes
        if "table_name='discussion_session_runs'" in query:
            return [{'p': row} for row in self.runs]
        if "table_name='discussion_session_events'" in query:
            if 'discussion_runtime_failed' in query:
                return [{'p': event} for event in self.failure_events]
            return self.approvals
        return []

    def record(self, table, key):
        return copy.deepcopy({'discussion_sessions': self.conversation, 'discussion_session_turns': self.turn,
                              'discussion_session_messages': self.source, 'discussion_session_runs': self.run}.get(table))

    def test_recovers_unlinked_terminal_trace_without_submitting_or_writing_a_message(self):
        snapshot = self.adapter.snapshot(self.active)
        self.assertEqual(snapshot['run'], {'status': 'completed', 'runtime_run_id': 'run-a'})
        self.assertEqual(snapshot['result_source'], 'terminal_trace')
        self.assertNotIn('messages', snapshot)
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(error, '')
        self.assertEqual(cases, [self.case])
        self.adapter.api.request.assert_not_called()
        self.assertEqual(self.turn['Status'], 'running')

    def test_start_failure_without_trace_preserves_real_cause_and_never_reexecutes(self):
        self.run.update(Status='failed', TraceRef='')
        event = {'EventID': 'failure-event-a', 'Kind': 'discussion_agent_mention_failed', 'Status': 'failed',
                 'ConversationID': self.command['ConversationID'],
                 'Summary': 'invalid_payload: content is required token=private',
                 'Metadata': {'run_id': 'run-a', 'turn_id': 'turn-a', 'target_agent_id': 'steward'}}
        self.failure_events = [event]
        self.turn.update(Status='failed', RunIDs=['run-a'])
        snapshot = self.adapter.snapshot(self.active)
        self.assertEqual(snapshot['result_source'], 'canonical_failure')
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(error, '')
        self.assertEqual(cases[0]['status'], 'needs_attention')
        self.assertIn('invalid_payload: content is required', cases[0]['summary'])
        self.assertNotIn('private', cases[0]['summary'])
        self.assertIn('failure-event-a', cases[0]['evidence'])
        self.assertEqual(cases[0]['failure_diagnostics']['runtime_status'], 'failed')
        self.assertNotIn('agent_report', cases[0])
        self.adapter.api.request.assert_not_called()
        for key, value in [('run_id', 'another-run'), ('turn_id', 'another-turn'), ('target_agent_id', 'another-agent')]:
            with self.subTest(field=key), patch.dict(event['Metadata'], {key: value}):
                self.assertNotIn('runtime_failure', self.adapter.snapshot(self.active))

    def test_start_failure_without_saved_reason_reports_that_gap_explicitly(self):
        self.run.update(Status='failed', TraceRef='')
        self.turn.update(Status='failed', RunIDs=['run-a'])
        snapshot = self.adapter.snapshot(self.active)
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(error, '')
        self.assertIn('runtime failure cause was not persisted', cases[0]['summary'])
        self.assertEqual(cases[0]['status'], 'needs_attention')
        self.adapter.api.request.assert_not_called()

    def test_indexed_pipeline_failure_keeps_trace_cause_without_a_failure_event(self):
        self.indexed_timeout()
        self.trace.update(status='failed', error_code='agent_pipeline_failed',
                          error_message='invalid_payload: content is required token="private-secret"')
        self.trace_indexes[0]['terminal_status'] = 'failed'
        self.write_trace()
        self.turn.update(Status='failed', RunIDs=['run-a'])
        snapshot = self.adapter.snapshot(self.active)
        self.assertEqual(snapshot['result_source'], 'terminal_trace')
        self.assertNotIn('runtime_failure', snapshot)
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(error, '')
        case = cases[0]
        self.assertEqual(case['status'], 'needs_attention')
        self.assertIn('invalid_payload: content is required', case['summary'])
        self.assertIn('Final case result unavailable', case['summary'])
        self.assertIn('content is required', case['failure_diagnostics']['runtime_cause'])
        self.assertEqual(case['failure_diagnostics']['runtime_error_code'], 'agent_pipeline_failed')
        self.assertIn(self.trace_indexes[0]['trace_ref'], case['evidence'])
        self.assertNotIn('private-secret', json.dumps(snapshot) + json.dumps(case))
        self.assertNotIn('agent_report', case)
        self.assertNotIn('user_summary', case)
        self.adapter.api.request.assert_not_called()

    def test_terminal_failure_diagnostic_is_bounded_and_non_text_is_ignored(self):
        from direct_repair_recovery import terminal_outcome
        outcome = terminal_outcome({'status': 'failed', 'error_message': 'token=private-secret ' + 'x' * 4000})
        self.assertEqual(len(outcome['error_message']), 1500)
        self.assertNotIn('private-secret', outcome['error_message'])
        for message in [None, {}, [], '', '   ']:
            with self.subTest(message=message):
                self.assertNotIn('error_message', terminal_outcome({'status': 'failed', 'error_message': message}))
        self.adapter.api.request.assert_not_called()

    def test_live_or_blocked_runs_and_recent_completions_still_wait(self):
        for changed in ({'Status': 'running'}, {'Status': 'blocked'}, {'CompletedAt': utc()}, {'CompletedAt': ''}):
            with self.subTest(changed=changed), patch.dict(self.run, changed):
                self.assertEqual(self.adapter.snapshot(self.active)['run']['status'], 'running')
        self.runs.append({**self.run, 'RunID': 'newer-run', 'Status': 'running', 'CreatedAt': utc()})
        self.assertNotIn('result_source', self.adapter.snapshot(self.active))

    def test_native_compact_assignment_is_bound_to_the_original_job(self):
        self.config['direct_runtime'] = 'codex'
        self.source['Content'] = self.adapter.dispatch_content(self.active)
        self.assertNotEqual(self.source['Content'], (self.root / 'prompt.txt').read_text(encoding='utf-8'))
        with patch('direct_repair_receipts.publish_receipts') as publish:
            snapshot = self.adapter.snapshot(self.active, publish_ledger=False)
            publish.assert_not_called()
        self.assertEqual(snapshot['result_source'], 'terminal_trace')
        self.active['jobs'] = [{'job_id': 'another-job'}]
        with self.assertRaisesRegex(ValueError, 'assignment'):
            self.adapter.snapshot(self.active)

    def test_cancelled_unlinked_run_without_terminal_trace_releases_only_to_attention(self):
        self.run['Status'] = 'cancelled'
        self.trace_path.unlink()
        self.conversation.update(Status='cancelled', ActiveTurnID='')
        snapshot = self.adapter.snapshot(self.active)
        self.assertEqual(snapshot['run'], {'status': 'cancelled', 'runtime_run_id': 'run-a'})
        self.assertEqual(snapshot['result_source'], 'canonical_cancellation')
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(error, '')
        self.assertEqual(cases[0]['status'], 'needs_attention')
        self.adapter.api.request.assert_not_called()
        self.claims = [{'run_id': 'discussion-turn:turn-a'}]
        self.assertNotIn('result_source', self.adapter.snapshot(self.active))

    def test_missing_terminal_artifact_cannot_strand_failed_or_completed_repair(self):
        self.trace_path.unlink()
        for status in ['failed','completed']:
            self.run['Status'] = status
            snapshot = self.adapter.snapshot(self.active)
            self.assertEqual(snapshot['run']['status'], status)
            cases, error = self.adapter.results(snapshot, self.active)
            self.assertEqual(error, '')
            self.assertEqual(cases[0]['status'], 'needs_attention')
            self.assertNotIn('agent_report', cases[0])
            self.adapter.api.request.assert_not_called()
        # A still-owned execution, pending approval, or invalid identity must
        # never be recovered merely because the file is absent.
        self.claims = [{'run_id':'discussion-turn:turn-a'}]
        self.assertEqual(self.adapter.snapshot(self.active)['run']['status'], 'running')
        self.claims = []; self.approvals = [{'record_key':'pending'}]
        self.assertEqual(self.adapter.snapshot(self.active)['run']['status'], 'running')
        self.approvals = [];self.source['Content'] = 'foreign assignment'
        with self.assertRaises(ValueError):self.adapter.snapshot(self.active)

    def test_active_execution_leases_and_pending_approvals_block_recovery(self):
        self.claims = [{'run_id': 'discussion-turn:turn-a'}]
        self.assertNotIn('result_source', self.adapter.snapshot(self.active))
        self.claims = []
        self.approvals = [{'record_key': 'pending-approval'}]
        self.assertNotIn('result_source', self.adapter.snapshot(self.active))
        self.reader.rows.side_effect = RuntimeError('lease database unavailable')
        with self.assertRaises(RuntimeError):
            self.adapter.snapshot(self.active)

    def test_changed_source_or_cross_agent_run_cannot_supply_results(self):
        self.source['Content'] = 'A different assignment.'
        with self.assertRaisesRegex(ValueError, 'assignment'):
            self.adapter.snapshot(self.active)
        self.source['Content'] = 'Assigned repair only.'
        for field, value in [('AgentID', 'other-agent'), ('ConversationID', 'other-chat'), ('TurnID', 'other-turn')]:
            with self.subTest(field=field), patch.dict(self.run, {field: value}):
                self.assertNotIn('result_source', self.adapter.snapshot(self.active))

    def test_trace_identity_status_path_and_result_identity_must_match(self):
        for field, value in [('run_id', 'other-run'), ('agent_id', 'other-agent'), ('status', 'running'), ('completed_at', '')]:
            with self.subTest(field=field), patch.dict(self.trace, {field: value}):
                self.write_trace()
                with self.assertRaises(ValueError):
                    self.adapter.snapshot(self.active)
        self.write_trace()
        with patch.dict(self.trace['result'], {'RunID': 'other-run'}):
            self.write_trace()
            with self.assertRaises(ValueError):
                self.adapter.snapshot(self.active)
        self.trace_path.write_text('[]', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'object'):
            self.adapter.snapshot(self.active)
        for reference in ['/uploads/private/runtime-trace.json', '/uploads/discussion-conversations/conversation-a/runs/run-a/../../../../outside/runtime-trace.json']:
            with self.subTest(reference=reference), patch.dict(self.run, {'TraceRef': reference}):
                with self.assertRaises(ValueError):
                    self.adapter.snapshot(self.active)

    def test_concurrent_turn_or_lease_change_does_not_close_work(self):
        for changed in ({**self.turn, 'Status': 'cancelled'}, self.turn):
            calls = 0
            def records(table, key):
                nonlocal calls
                if table == 'discussion_session_turns':
                    calls += 1
                    return copy.deepcopy(self.turn if calls == 1 else changed)
                return self.record(table, key)
            self.adapter.record.side_effect = records
            if changed == self.turn:
                count = 0
                def rows(database, query):
                    nonlocal count
                    if database == 'orrery_runtime':
                        count += 1
                        return [] if count == 1 else [{'run_id': 'discussion-turn:turn-a'}]
                    return self.rows(database, query)
                self.reader.rows.side_effect = rows
            self.assertNotIn('result_source', self.adapter.snapshot(self.active))

    def test_recovered_success_still_requires_verification_and_no_blocker(self):
        self.case['status'] = 'resolved'
        self.trace['result']['Summary']['Text'] = json.dumps({'cases': [self.case]})
        self.write_trace()
        snapshot = self.adapter.snapshot(self.active)
        with patch('direct_repair.context_receipts', return_value=['verified receipt']) as receipts:
            cases, _ = self.adapter.results(snapshot, self.active)
            self.assertEqual(cases[0]['status'], 'resolved')
            receipts.assert_called_once()
        for changed in ({'recovered_blocker': True}, {'run': {'status': 'failed'}}):
            with patch.dict(snapshot, changed):
                cases, _ = self.adapter.results(snapshot, self.active)
                self.assertEqual(cases[0]['status'], 'needs_attention')
        with patch('direct_repair.context_receipts', side_effect=ValueError('missing evidence')):
            cases, _ = self.adapter.results(snapshot, self.active)
            self.assertEqual(cases[0]['status'], 'needs_attention')

    def test_missing_or_partial_recovered_result_is_held_instead_of_retried(self):
        for content in [None, {}, [], '', 'not JSON', '{"cases": []}', '{"cases": [{"job_id": "other-job"}]}']:
            with self.subTest(content=content):
                self.trace['result']['Summary']['Text'] = content
                self.write_trace()
                snapshot = self.adapter.snapshot(self.active)
                cases, error = self.adapter.results(snapshot, self.active)
                self.assertEqual(error, '')
                self.assertEqual(cases[0]['status'], 'needs_attention')
                self.assertEqual(cases[0]['job_id'], 'job-a')

    def test_terminal_turn_with_missing_message_also_recovers_and_cancel_is_preserved(self):
        self.turn.update(Status='completed', RunIDs=['run-a'])
        self.assertEqual(self.adapter.snapshot(self.active)['result_source'], 'terminal_trace')
        self.turn['Status'] = 'cancelled'
        snapshot = self.adapter.snapshot(self.active)
        self.assertEqual(snapshot['run']['status'], 'cancelled')
        self.assertNotIn('result_source', snapshot)

    def indexed_timeout(self):
        self.run.update(Status='failed', TraceRef='')
        self.trace.update(trace_version=2, status='timeout', error_code='runtime_timeout',
                          result={'RunID': '', 'AgentID': '', 'Summary': {'Text': ''},
                                  'InternalTraceRef': '/private/reference'})
        self.trace_indexes = [{'tenant_id': 'tenant-a', 'run_id': 'run-a', 'trace_version': 2,
                               'status': 'hot', 'terminal_status': 'timeout',
                               'trace_ref': '/uploads/discussion-conversations/conversation-a/runs/run-a/trace-v2/runtime-trace.json'}]
        self.write_trace()

    def test_timeout_recovers_from_tenant_trace_index_without_a_final_agent_result(self):
        self.indexed_timeout()
        snapshot = self.adapter.snapshot(self.active)
        self.assertEqual(snapshot['run'], {'status': 'failed', 'runtime_run_id': 'run-a'})
        self.assertEqual(snapshot['terminal_outcome'], {'status': 'timeout', 'error_code': 'runtime_timeout'})
        self.assertEqual(snapshot['trace_ref'], self.trace_indexes[0]['trace_ref'])
        cases, error = self.adapter.results(snapshot, self.active)
        self.assertEqual(error, '')
        self.assertEqual(cases[0]['status'], 'needs_attention')
        self.assertIn('runtime_timeout', cases[0]['summary'])
        self.assertNotIn('agent_report', cases[0])
        self.adapter.api.request.assert_not_called()
        query = next(call.args[1] for call in self.reader.rows.call_args_list if 'agent_runtime_traces' in call.args[1])
        self.assertIn('tenant_id=', query)
        self.assertIn('run_id=', query)

    def test_trace_index_cannot_cross_tenant_run_version_or_terminal_status(self):
        self.indexed_timeout()
        for field, value in [('tenant_id', 'other-tenant'), ('run_id', 'other-run'),
                             ('trace_version', 1), ('terminal_status', 'completed'), ('status', 'running'),
                             ('trace_ref', '/uploads/private/runtime-trace.json')]:
            with self.subTest(field=field), patch.dict(self.trace_indexes[0], {field: value}):
                with self.assertRaises(ValueError):
                    self.adapter.snapshot(self.active)

    def test_timeout_trace_with_foreign_result_or_hidden_final_document_is_rejected(self):
        self.indexed_timeout()
        for result in [{'RunID': 'other-run', 'AgentID': 'steward'},
                       {'RunID': '', 'AgentID': '', 'Summary': {'Text': json.dumps({'cases': [self.case]})}}]:
            self.trace['result'] = result
            self.write_trace()
            with self.assertRaises(ValueError):
                self.adapter.snapshot(self.active)

    def test_overdue_restored_terminal_run_is_read_before_cancellation_or_submission(self):
        config = {**self.config, 'state_dir': str(self.root / 'state'), 'workspace': str(self.root),
                  'source_root': str(self.root), 'execution_mode': 'direct', 'run_timeout_seconds': 10,
                  'sources': ['scheduled'], 'result_recipient': {'human_profile_id': 'human-a'}}
        worker = Worker(config)
        self.addCleanup(worker.queue.db.close)
        worker.queue.ingest({'observation_key': 'old', 'job_key': 'old', 'source': 'scheduled', 'priority': 1,
                             'summary': 'failed', 'tenant_id': 'tenant-a'}, 1)
        batch, jobs = worker.queue.claim(1, 2)
        active = {**self.active, 'batch': batch, 'jobs': jobs, 'started': 2,
                  'submitted': False, 'execution_mode': 'direct'}
        case = {**self.case, 'job_id': jobs[0]['job_id']}
        for status in ['failed', 'completed']:
            worker.active = active
            with patch.object(worker, 'native', return_value={'found': True, 'run': {'status': status, 'runtime_run_id': 'run-a'}}) as native, \
                 patch.object(worker.direct, 'results', return_value=([case], '')), \
                 patch.object(worker.direct, 'cancel', return_value={'status': 'cancelled'}) as cancel, patch.object(worker, 'prepare_reports'):
                worker.reap()
            self.assertIsNone(worker.active)
            cancel.assert_not_called()
            self.assertEqual([call.args[0] for call in native.call_args_list], ['status'])
        row=worker.queue.db.execute('SELECT status,attempts,last_result FROM jobs').fetchone()
        self.assertEqual((row['status'], row['attempts']), ('needs_attention', 1))
        self.assertIn('Needs owner input', row['last_result'])

    def test_overdue_unreadable_status_does_not_cancel_or_redispatch(self):
        config = {**self.config, 'state_dir': str(self.root / 'state'), 'workspace': str(self.root),
                  'source_root': str(self.root), 'execution_mode': 'direct', 'run_timeout_seconds': 10,
                  'sources': ['scheduled'], 'result_recipient': {'human_profile_id': 'human-a'}}
        worker = Worker(config)
        self.addCleanup(worker.queue.db.close)
        worker.active = {**self.active, 'started': 2, 'submitted': False, 'execution_mode': 'direct'}
        active = worker.active
        with patch.object(worker, 'native', side_effect=ValueError('binding mismatch')) as native, \
             patch.object(worker.direct, 'cancel', return_value={'status': 'cancelled'}) as cancel:
            worker.reap()
        self.assertIs(worker.active, active)
        cancel.assert_not_called()
        self.assertEqual([call.args[0] for call in native.call_args_list], ['status'])
        self.assertIn('binding mismatch', worker.queue.get_meta('handoff_error'))

    def test_overdue_live_run_still_cancels_after_recording_current_identity(self):
        config = {**self.config, 'state_dir': str(self.root / 'state'), 'workspace': str(self.root),
                  'source_root': str(self.root), 'execution_mode': 'direct', 'run_timeout_seconds': 10,
                  'sources': ['scheduled'], 'result_recipient': {'human_profile_id': 'human-a'}}
        worker = Worker(config)
        self.addCleanup(worker.queue.db.close)
        worker.queue.ingest({'observation_key': 'old', 'job_key': 'old', 'source': 'scheduled', 'priority': 1,
                             'summary': 'failed', 'tenant_id': 'tenant-a'}, 1)
        batch, jobs = worker.queue.claim(1, 2)
        worker.active = {**self.active, 'batch': batch, 'jobs': jobs, 'started': 2,
                         'submitted': False, 'execution_mode': 'direct'}
        snapshot = {'found': True, 'run': {'status': 'running', 'runtime_run_id': 'run-a'}}
        with patch.object(worker, 'native', return_value=snapshot) as native, \
             patch.object(worker.direct, 'cancel', return_value={'status': 'cancelled'}) as cancel:
            worker.reap()
        self.assertEqual([call.args[0] for call in native.call_args_list], ['status'])
        cancel.assert_called_once()
        self.assertIsNone(worker.active)
        self.assertEqual(json.loads((self.root / 'native-run.json').read_text())['run']['runtime_run_id'], 'run-a')
        self.assertEqual(worker.queue.db.execute('SELECT status,attempts FROM jobs').fetchone()[:], ('needs_attention', 1))

    def test_worker_finishes_recovered_case_and_starts_next_without_replaying_old_work(self):
        config = {**self.config, 'state_dir': str(self.root / 'state'), 'workspace': str(self.root),
                  'source_root': str(self.root), 'tenant_id': 'tenant-a', 'execution_mode': 'direct',
                  'sources': ['scheduled'], 'batch_size': 1, 'result_recipient': {'human_profile_id': 'human-a'}}
        worker = Worker(config)
        self.addCleanup(worker.queue.db.close)
        for key in ['old', 'next']:
            worker.queue.ingest({'observation_key': key, 'job_key': key, 'source': 'scheduled',
                                 'priority': 10 if key == 'old' else 1, 'summary': 'failed', 'tenant_id': 'tenant-a'}, 1)
        batch, jobs = worker.queue.claim(1, time.time(), 'scheduled')
        self.active['jobs'] = jobs
        self.case['job_id'] = jobs[0]['job_id']
        self.trace['result']['Summary']['Text'] = json.dumps({'cases': [self.case]})
        self.write_trace()
        snapshot = self.adapter.snapshot(self.active)
        worker.active = {**self.active, 'batch': batch, 'submitted': True, 'execution_mode': 'direct'}
        with patch.object(worker, 'native', return_value=snapshot):
            worker.reap()
        self.assertIsNone(worker.active)
        self.assertEqual(worker.queue.db.execute("SELECT attempts FROM jobs WHERE id=?", (jobs[0]['job_id'],)).fetchone()[0], 1)
        with patch.object(worker, 'native', return_value={'discussion_id': 'next-chat'}) as submit:
            worker.start()
        self.assertNotEqual(worker.active['jobs'][0]['job_id'], jobs[0]['job_id'])
        self.assertEqual(submit.call_args.args[0], 'submit')


if __name__ == '__main__':
    unittest.main()
