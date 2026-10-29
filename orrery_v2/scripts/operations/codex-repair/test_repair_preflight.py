import copy
import io
import json
from pathlib import Path
import tempfile
import unittest
from contextlib import redirect_stdout

from repair_preflight import check, main, workflow_prompt


class RepairPreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.attempt = self.root / 'attempt'
        self.attempt.mkdir()
        self.events = self.root / 'artifacts/discussion-conversations/conversation-a/runs/run-a/trace-v2/events'
        self.events.mkdir(parents=True)
        self.snapshot = {'found': True, 'discussion_id': 'conversation-a', 'turn_id': 'turn-a',
                         'run': {'runtime_run_id': 'run-a', 'status': 'running'}}
        self.document = {'cases': [{'job_id': 'job-a', 'status': 'resolved', 'summary': 'Recovered component verified',
                         'evidence': ['affected component check'], 'repair_kind': 'already_recovered',
                         'contract_digest': 'contract-a', 'verification_tool_ids': ['call_gVqzJaJJzKx9QfoVX4YqK38O'],
                         'user_report': '已核实原问题恢复，相关功能检查通过。本次没有修改代码。'}]}
        self.candidate = self.root / 'repair-result.json'
        self.write(self.attempt / 'assignments.json', [{'job_id': 'job-a', 'failure_context': {'digest': 'contract-a'}}])
        self.write(self.attempt / 'native-run.json', self.snapshot)
        self.write(self.candidate, self.document)
        for name in ('enterprise_graph.search_objects', 'enterprise_graph.build_context_bundle', 'knowledge_search', 'memory_search'):
            self.receipt('context-' + name, name)
        self.receipt('call_gVqzJaJJzKx9QfoVX4YqK38O', 'commandExecution')

    def write(self, path, value):
        path.write_text(json.dumps(value, ensure_ascii=False), encoding='utf-8')

    def receipt(self, identifier, name, error=False, content='affected behavior verified'):
        count = len(list(self.events.glob('*.json')))
        events = [{'kind': 'tool_call', 'tool_call': {'tool_use_id': identifier, 'name': name}},
                  {'kind': 'tool_result', 'tool_result': {'tool_use_id': identifier, 'is_error': error, 'content': content}}]
        for offset, event in enumerate(events):
            self.write(self.events / ('%05d.json' % (count+offset)), {'payload': {'event': event}})

    def result(self):
        self.write(self.candidate, self.document)
        return check(self.attempt, self.candidate, self.root / 'artifacts')

    def test_real_live_verification_passes_without_terminal_manifest_or_any_writes(self):
        before = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        outcome = check(self.attempt, self.candidate, self.root / 'artifacts')
        self.assertEqual(outcome['status'], 'ready_for_final')
        self.assertTrue(outcome['terminal_acceptance_pending'])
        self.assertEqual(outcome['runtime_run_id'], 'run-a')
        self.assertEqual(before, {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()})

    def test_production_one_character_typo_is_rejected_and_agent_can_correct_it(self):
        self.document['cases'][0]['verification_tool_ids'] = ['call_gVqJaJJzKx9QfoVX4YqK38O']
        rejected = self.result()
        self.assertEqual(rejected['status'], 'needs_correction')
        self.assertIn('call_gVqzJaJJzKx9QfoVX4YqK38O', rejected['errors'][0])
        self.assertEqual(self.document['cases'][0]['verification_tool_ids'], ['call_gVqJaJJzKx9QfoVX4YqK38O'])
        self.document['cases'][0]['verification_tool_ids'] = ['call_gVqzJaJJzKx9QfoVX4YqK38O']
        self.assertEqual(self.result()['status'], 'ready_for_final')

    def test_fake_ledger_cannot_establish_a_verification(self):
        self.write(self.attempt / 'runtime-tool-receipts.json', {'tools': [{'tool_call_id': 'invented', 'status': 'completed'}]})
        self.document['cases'][0]['verification_tool_ids'] = ['invented']
        self.assertEqual(self.result()['status'], 'needs_correction')

    def test_context_only_failed_empty_and_unfinished_checks_are_not_proof(self):
        self.receipt('failed', 'commandExecution', error=True)
        self.receipt('empty', 'fileChange', content=[])
        for identifier in ('context-knowledge_search', 'failed', 'empty', 'unfinished'):
            with self.subTest(identifier=identifier):
                self.document['cases'][0]['verification_tool_ids'] = [identifier]
                self.assertEqual(self.result()['status'], 'needs_correction')

    def test_other_runtime_and_path_escape_do_not_supply_receipts(self):
        for run in ('another-run', '../run-a'):
            with self.subTest(run=run):
                self.snapshot['run']['runtime_run_id'] = run
                self.write(self.attempt / 'native-run.json', self.snapshot)
                with self.assertRaises(ValueError):
                    self.result()

    def test_final_report_contract_and_coverage_are_checked(self):
        original = copy.deepcopy(self.document)
        for key, value in (('user_report', ''), ('contract_digest', 'wrong'), ('repair_kind', 'planned'), ('invented_field', True)):
            with self.subTest(key=key):
                self.document = copy.deepcopy(original)
                self.document['cases'][0][key] = value
                self.assertEqual(self.result()['status'], 'needs_correction')
        self.document = {'cases': []}
        self.assertEqual(self.result()['status'], 'needs_correction')

    def test_missing_required_context_and_cancelled_run_cannot_claim_resolved(self):
        self.snapshot['run']['status'] = 'cancelled'
        self.write(self.attempt / 'native-run.json', self.snapshot)
        self.assertEqual(self.result()['status'], 'needs_correction')
        self.snapshot['run']['status'] = 'running'
        self.write(self.attempt / 'native-run.json', self.snapshot)
        next(self.events.glob('00007.json')).unlink()
        self.assertEqual(self.result()['status'], 'needs_correction')

    def test_honest_attention_can_finish_without_technical_success(self):
        case = self.document['cases'][0]
        case['status'] = 'needs_attention'
        case.pop('verification_tool_ids')
        case['user_report'] = '已定位问题，但需要现有授权范围之外的凭证配置，尚未完成修复。'
        self.assertEqual(self.result()['status'], 'ready_for_final')

    def test_cli_fails_with_actionable_feedback_and_success_is_only_preflight(self):
        args = ['--attempt-dir', str(self.attempt), '--artifact-root', str(self.root/'artifacts'), '--result', str(self.candidate)]
        output = io.StringIO()
        with redirect_stdout(output):
            self.assertEqual(main(args), 0)
        self.assertTrue(json.loads(output.getvalue())['terminal_acceptance_pending'])
        self.candidate.write_text('{invalid', encoding='utf-8')
        with redirect_stdout(io.StringIO()):
            self.assertEqual(main(args), 1)

    def test_prompt_requires_full_flow_and_same_run_correction_without_replay(self):
        prompt = workflow_prompt(self.attempt, self.root/'workspace with spaces', self.root/'artifacts')
        for term in ('SAME execution', 'Diagnose', 'Apply', 'Run checks', 'Activate', 'user_report',
                     'programmatically', 'at most twice', 'exact checked file contents', 'already recovered',
                     'without repeating tests or business actions', 'repair_preflight.py'):
            self.assertIn(term, prompt)


if __name__ == '__main__':
    unittest.main()
