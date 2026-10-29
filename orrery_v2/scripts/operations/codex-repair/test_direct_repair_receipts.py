import json
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import Mock, patch

from direct_repair import DirectRepair, DISPATCHER
from direct_repair_receipts import publish_receipts, receipt_prompt


class ReceiptLedgerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.active = {'tenant': 'tenant-a', 'batch': 'batch-a', 'directory': Path(self.temp.name)}
        self.adapter = DirectRepair({'direct_runtime': 'codex', 'agent_id': 'agent-a',
                                     'artifact_root': self.temp.name}, Mock(), Mock())
        self.command = {'ConversationID': 'conversation-a', 'TurnID': 'turn-a', 'MessageID': 'message-a'}
        self.turn = {'TurnID': 'turn-a', 'ConversationID': 'conversation-a', 'RootMessageID': 'message-a',
                     'IdempotencyKey': self.adapter.key(self.active), 'Status': 'running', 'RunIDs': ['run-a']}
        self.run = {'RunID': 'run-a', 'TurnID': 'turn-a', 'ConversationID': 'conversation-a', 'AgentID': 'agent-a',
                    'ToolCalls': [{'tool_call_id': 'call-real', 'name': 'commandExecution', 'status': 'running',
                                   'arguments': {'command': 'python3 -m unittest', 'secret': 'unpublished'},
                                   'result': {'secret': 'unpublished'}}]}
        self.path = self.active['directory'] / 'runtime-tool-receipts.json'

    def test_snapshot_publishes_current_receipts_and_refreshes_status(self):
        records = {'conversation-a': {'TenantID': 'tenant-a', 'CreatedBy': DISPATCHER, 'DirectAgentID': 'agent-a'},
                   'turn-a': self.turn, 'run-a': self.run}
        self.adapter.reader.rows.return_value = []
        with patch.object(self.adapter, 'command', return_value=self.command), \
                patch.object(self.adapter, 'record', side_effect=lambda table, key: records.get(key)):
            self.adapter.snapshot(self.active)
            document = json.loads(self.path.read_text())
            self.assertEqual(document['run_id'], 'run-a')
            self.assertEqual(document['tools'][0]['tool_call_id'], 'call-real')
            self.assertEqual(document['tools'][0]['status'], 'running')
            self.assertNotIn('unpublished', self.path.read_text())
            self.run['ToolCalls'][0]['status'] = 'completed'
            self.adapter.snapshot(self.active)
            self.assertEqual(json.loads(self.path.read_text())['tools'][0]['status'], 'completed')
            records['conversation-a']['TenantID'] = 'tenant-b'
            before = self.path.read_bytes()
            with self.assertRaisesRegex(ValueError, 'binding'):
                self.adapter.snapshot(self.active)
            self.assertEqual(self.path.read_bytes(), before)
        if os.name != 'nt':
            self.assertEqual(stat.S_IMODE(self.path.stat().st_mode), 0o600)
        self.assertEqual(list(self.active['directory'].glob('.runtime-tool-receipts-*')), [])

    def test_live_run_lookup_ignores_another_agent_or_turn(self):
        wrong = dict(self.run, RunID='wrong-run', AgentID='another-agent', CreatedAt='9999')
        with patch('direct_repair_recovery.runs_for_turn', return_value=[wrong, self.run]):
            publish_receipts(self.adapter, self.active, self.command, self.turn, {})
        self.assertEqual(json.loads(self.path.read_text())['run_id'], 'run-a')
        self.run['TurnID'] = 'another-turn'
        with patch('direct_repair_recovery.runs_for_turn', return_value=[wrong, self.run]):
            self.path.unlink()
            publish_receipts(self.adapter, self.active, self.command, self.turn, {})
        self.assertFalse(self.path.exists())

    def test_mismatched_run_and_other_runtime_never_publish(self):
        wrong = dict(self.run, ConversationID='another-conversation')
        with self.assertRaisesRegex(ValueError, 'binding'):
            publish_receipts(self.adapter, self.active, self.command, self.turn, wrong)
        self.assertFalse(self.path.exists())
        self.adapter.config['direct_runtime'] = 'pi'
        publish_receipts(self.adapter, self.active, self.command, self.turn, self.run)
        self.assertFalse(self.path.exists())

    def test_prompt_explains_canonical_ids_and_bounded_wait_without_reexecution(self):
        prompt = receipt_prompt(self.active['directory'])
        for term in ('runtime-tool-receipts.json', 'tool_call_id', 'Chunk ID', '90 seconds',
                     'Do not repeat', 'independently verifies', 'needs_attention'):
            self.assertIn(term, prompt)

    def test_long_command_can_be_identified_by_tail_and_digest_without_losing_its_id(self):
        import hashlib
        command = 'python3 ' + 'x' * 600 + ' --check-affected-component'
        self.run['ToolCalls'][0]['arguments']['command'] = command
        publish_receipts(self.adapter, self.active, self.command, self.turn, self.run)
        item = json.loads(self.path.read_text())['tools'][0]
        self.assertEqual(item['tool_call_id'], 'call-real')
        self.assertTrue(item['command_tail'].endswith('--check-affected-component'))
        self.assertEqual(item['command_sha256'], hashlib.sha256(command.encode()).hexdigest())


if __name__ == '__main__':
    unittest.main()
