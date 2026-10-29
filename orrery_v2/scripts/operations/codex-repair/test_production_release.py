import argparse
from contextlib import contextmanager
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import production_release as release
import production_release_handoff as handoff
from repair_dispatch_control import DispatchPermit


class ProductionReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.live = self.root / 'release'; self.live.mkdir()
        (self.live / 'COMMIT').write_text('a'*40)
        self.current = self.root / 'current'; self.current.symlink_to(self.live, target_is_directory=True)
        self.config = self.root / 'config.json'; self.config.write_text(json.dumps({'state_dir': str(self.root)}))
        self.args = argparse.Namespace(config=str(self.config), expected_release=str(self.live), expected_commit='a'*40,
                                       component='api', lock_timeout=1.0, drain_timeout=60.0, handoff_id=None)
        self.command = ['/usr/bin/python3', '/reviewed/activate.py']

    def test_api_caller_defers_without_waiting_for_its_own_turn(self):
        calls = []
        def run(argv, **kwargs):
            calls.append(argv)
            if argv[0] == 'systemctl':
                # The second request sees the existing independent unit.
                return subprocess.CompletedProcess(argv, 0, 'active' if len(calls)>2 else 'inactive', '')
            return subprocess.CompletedProcess(argv, 0, '', '')
        cli = ['--config', str(self.config), '--expected-release', str(self.live), '--expected-commit', 'a'*40, '--lock-timeout', '1', '--drain-timeout', '60', '--', *self.command]
        with patch.object(handoff, 'inside_api', return_value=True), patch.object(release, 'require_baseline'), patch.object(release, 'require_cooperative_worker'), patch.object(release, 'activation_lease') as lease, patch.object(release, 'wait_for_idle') as wait, patch.object(handoff.subprocess, 'run', side_effect=run):
            self.assertEqual(release.main(cli), 0)
            self.assertEqual(release.main(cli), 0)
        lease.assert_not_called(); wait.assert_not_called()
        services = [args for args in calls if args[0]=='systemd-run']
        self.assertEqual(len(services), 1)
        self.assertIn('--user', services[0]); self.assertIn('--handoff-id', services[0])
        requests = list((self.root/'activation-requests').glob('*.json'))
        self.assertEqual(len(requests), 1)
        receipt = json.loads(requests[0].read_text())
        self.assertEqual(receipt['status'], 'queued')
        self.assertEqual(receipt['specification']['command'], self.command)
        self.args.handoff_id = receipt['request_id']
        handoff.claim(self.args, self.command, {'state_dir': str(self.root)})
        with self.assertRaises(ValueError):
            handoff.claim(self.args, self.command, {'state_dir': str(self.root)})
        handoff.finish(self.args, {'state_dir': str(self.root)}, 'completed', {'health_checks': 17})
        self.assertEqual(json.loads(requests[0].read_text())['status'], 'completed')

    def test_command_tamper_or_baseline_change_never_activates(self):
        spec = handoff.specification(self.args, self.command)
        self.args.handoff_id = handoff.identity(spec)
        with self.assertRaises(ValueError):
            handoff.claim(self.args, ['/unreviewed/command'], {'state_dir': str(self.root)})
        (self.live/'COMMIT').write_text('b'*40)
        with self.assertRaisesRegex(RuntimeError, 'commit changed'):
            with release.activation_lease(self.root, self.current, self.live, 'a'*40, locks=[self.root/'deploy.lock']):
                self.fail('activated a changed baseline')
        with DispatchPermit(self.root) as permit:
            self.assertTrue(permit.allowed)

    def test_lease_pauses_only_new_dispatch_and_releases_after_failure(self):
        with self.assertRaisesRegex(RuntimeError, 'activation failed'):
            with release.activation_lease(self.root, self.current, self.live, 'a'*40, locks=[self.root/'deploy.lock']):
                with DispatchPermit(self.root) as permit:
                    self.assertFalse(permit.allowed)
                raise RuntimeError('activation failed')
        with DispatchPermit(self.root) as permit:
            self.assertTrue(permit.allowed)

    def test_detached_guard_still_refuses_other_running_work(self):
        state = {'pid':'7', 'children':[], 'turns':['a-real-user-turn']}
        with patch.object(release.time, 'sleep'), patch.object(release.time, 'monotonic', side_effect=[0, 2]):
            with self.assertRaises(TimeoutError):
                release.wait_for_idle(lambda: state, 1, lambda: None, lambda: None)

    def test_deferred_request_binds_candidate_and_notice_identity(self):
        candidate = self.root / 'candidate'; candidate.mkdir()
        attachment = self.root / 'attachment'; attachment.mkdir()
        original = handoff.identity(handoff.specification(self.args, self.command))
        self.args.candidate_release = str(candidate)
        self.args.license_attachment = str(attachment)
        spec = handoff.specification(self.args, self.command)
        self.assertNotEqual(handoff.identity(spec), original)
        with patch.object(handoff.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')) as run:
            handoff.defer_activation(self.args, self.command, {'state_dir': str(self.root)}, release.__file__)
        invocation = run.call_args.args[0]
        self.assertEqual(invocation[invocation.index('--candidate-release')+1], str(candidate))
        self.assertEqual(invocation[invocation.index('--license-attachment')+1], str(attachment))
        self.args.handoff_id = handoff.identity(spec)
        self.args.license_attachment = None
        with self.assertRaisesRegex(ValueError, 'does not match'):
            handoff.claim(self.args, self.command, {'state_dir': str(self.root)})


if __name__ == '__main__':
    unittest.main()
