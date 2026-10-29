import hashlib
import fcntl
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

from codex_repair_worker import Worker
from repair_dispatch_control import DispatchPause, DispatchPermit
from production_release import activation_lease, run_activation, wait_for_idle


HERE = Path(__file__).resolve().parent


class DispatchControlTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = {'state_dir': str(self.root / 'state'), 'workspace': str(self.root),
                       'source_root': str(self.root), 'artifact_root': str(self.root), 'agent_id': 'steward'}
        self.worker = Worker(self.config)
        self.addCleanup(self.worker.queue.db.close)
        self.ingest('one')

    def ingest(self, name):
        self.worker.queue.ingest({'job_key': 'runtime:' + name, 'observation_key': name,
                                  'summary': 'failure', 'priority': 1, 'tenant_id': 'system'}, time.time())

    def start(self):
        with patch.object(self.worker, 'native', return_value={'scheduled_task_id': 'same-task'}):
            self.worker.start()

    def test_paused_dispatch_retains_pending_jobs_and_attempt_count(self):
        with DispatchPause(self.worker.root), patch.object(self.worker, 'native') as native:
            self.ingest('two')
            self.worker.start()
            native.assert_not_called()
            self.assertIsNone(self.worker.active)
            self.assertEqual(self.worker.queue.status()['counts'], {'pending': 2})
            self.assertEqual(sum(row[0] for row in self.worker.queue.db.execute('SELECT attempts FROM jobs')), 0)
            self.assertEqual(self.worker.queue.get_meta('dispatch_state'), 'paused')
        self.start()
        self.assertEqual(self.worker.queue.status()['counts'], {'running': 2})
        self.assertEqual(self.worker.queue.get_meta('dispatch_state'), 'ready')

    def test_uncertain_submission_keeps_original_identity_until_resume(self):
        with patch.object(self.worker, 'native', side_effect=RuntimeError('reply lost')):
            self.worker.start()
        original = self.worker.active['batch']
        restarted = Worker(self.config)
        self.addCleanup(restarted.queue.db.close)
        restarted.restore()
        with DispatchPause(self.worker.root), patch.object(restarted, 'native') as native:
            restarted.reap()
            native.assert_not_called()
        with patch.object(restarted, 'native', return_value={'scheduled_task_id': 'same-task'}) as native:
            restarted.reap()
        native.assert_called_once()
        self.assertEqual(restarted.active['batch'], original)
        self.assertEqual(restarted.queue.db.execute('SELECT attempts FROM jobs').fetchone()[0], 1)

    def test_existing_result_is_reconciled_and_report_delivered_while_paused(self):
        self.start()
        active = self.worker.active
        body = '本次排查已结束，文章仍待封面服务恢复后验收。'
        job_id = active['jobs'][0]['job_id']
        case = {'job_id': job_id, 'status': 'needs_attention', 'summary': 'internal', 'evidence': [],
                'agent_report': {'version': 1, 'source': 'agent_final', 'agent_id': 'steward',
                                 'runtime_run_id': 'original-run', 'job_id': job_id, 'status': 'needs_attention',
                                 'body': body, 'body_sha256': hashlib.sha256(body.encode()).hexdigest()}}
        # The genuine result-acceptance boundary is exercised by the existing suite;
        # here the accepted report is passed through the real queue and delivery loop.
        self.worker.direct = Mock()
        self.worker.active['execution_mode'] = 'direct'
        self.worker.direct.report.return_value = {'accepted': True, 'discussion_id': 'original-report'}
        self.worker.direct.report_status.side_effect = [
            {'delivered': False, 'status': 'pending'}, {'delivered': True, 'status': 'delivered'}]
        with DispatchPause(self.worker.root), \
             patch.object(self.worker, 'native', return_value={'found': True, 'run': {'status': 'completed'}}), \
             patch.object(self.worker, 'read_results', return_value=([case], '')):
            self.ingest('two')
            self.worker.reap()
            self.worker.flush_reports()
            self.assertEqual(self.worker.queue.db.execute('SELECT delivered FROM reports').fetchone()[0], 0)
            # Queue acceptance is not delivery. Poll the same receipt after its retry time.
            with patch('codex_repair_worker.time.time', return_value=time.time()+31):
                self.worker.flush_reports()
            self.worker.start()
        self.worker.direct.report.assert_called_once()
        self.assertEqual(self.worker.direct.report_status.call_count, 2)
        self.assertEqual(self.worker.direct.report_status.call_args.args[1]['discussion_id'], 'original-report')
        self.assertEqual(self.worker.direct.report.call_args.args[0]['body'], body)
        self.assertEqual(self.worker.queue.status()['counts'], {'needs_attention': 1, 'pending': 1})
        self.assertEqual(self.worker.queue.db.execute('SELECT delivered FROM reports').fetchone()[0], 1)

    def test_configured_timeout_still_closes_the_existing_run_during_pause(self):
        self.start()
        self.worker.direct = Mock()
        self.worker.direct.cancel.return_value = {'cancelled': True}
        self.worker.config['run_timeout_seconds'] = 1
        self.worker.active.update(execution_mode='direct', started=time.time() - 5)
        with DispatchPause(self.worker.root), patch.object(self.worker, 'native', return_value={
                'found': True, 'run': {'status': 'running'}}):
            self.worker.reap()
        self.worker.direct.cancel.assert_called_once()
        self.assertIsNone(self.worker.active)
        self.assertEqual(self.worker.queue.status()['counts'], {'needs_attention': 1})

    def test_worker_loop_keeps_collection_feedback_and_heartbeat_running(self):
        for signum in (signal.SIGTERM, signal.SIGINT):
            self.addCleanup(signal.signal, signum, signal.getsignal(signum))
        def scan():
            self.ingest('during-pause')
        with DispatchPause(self.worker.root), \
             patch.object(self.worker, 'scan', side_effect=scan) as collect, \
             patch.object(self.worker, 'recheck_business') as verify, \
             patch.object(self.worker, 'flush_reports') as reports, \
             patch.object(self.worker, 'native') as native, \
             patch('failure_center.write_feedback') as feedback, \
             patch('codex_repair_worker.time.sleep', side_effect=lambda _: setattr(self.worker, 'stopping', True)):
            self.worker.run()
        collect.assert_called_once()
        verify.assert_called_once()
        reports.assert_called_once()
        feedback.assert_called_once_with(self.worker)
        native.assert_not_called()
        self.assertEqual(self.worker.queue.status()['counts'], {'pending': 2})
        self.assertTrue(self.worker.queue.get_meta('heartbeat_at'))
        self.assertEqual(self.worker.queue.get_meta('dispatch_protocol_pid'), str(os.getpid()))

    def test_gate_io_failure_blocks_new_dispatch_without_losing_the_job(self):
        with patch('repair_dispatch_control.open_lock', side_effect=PermissionError('fixture-denied')):
            self.worker.start()
        self.assertIsNone(self.worker.active)
        self.assertEqual(self.worker.queue.status()['counts'], {'pending': 1})
        self.assertEqual(self.worker.queue.get_meta('dispatch_state'), 'error')
        self.assertIn('fixture-denied', self.worker.queue.get_meta('dispatch_gate_error'))

    def child(self, code, *args):
        process = subprocess.Popen([sys.executable, '-c', code, *map(str, args)], cwd=HERE,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        def cleanup():
            if process.poll() is None:
                process.kill()
            process.communicate(timeout=5)
        self.addCleanup(cleanup)
        return process

    def wait_file(self, path):
        deadline = time.monotonic() + 5
        while not path.exists():
            if time.monotonic() > deadline:
                self.fail('fixture process did not signal readiness')
            time.sleep(0.01)

    def test_pause_waits_for_an_in_flight_submission(self):
        ready, finish = self.root / 'ready', self.root / 'finish'
        process = self.child("from repair_dispatch_control import DispatchPermit; from pathlib import Path; "
                             "import sys,time\nwith DispatchPermit(sys.argv[1]) as p:\n"
                             " assert p.allowed\n Path(sys.argv[2]).touch()\n"
                             " while not Path(sys.argv[3]).exists(): time.sleep(.01)", self.worker.root, ready, finish)
        self.wait_file(ready)
        with self.assertRaises(TimeoutError):
            with DispatchPause(self.worker.root, timeout=0.05):
                self.fail('paused before the in-flight submission released its slot')
        finish.touch()
        self.assertEqual(process.wait(timeout=5), 0)
        with DispatchPause(self.worker.root), DispatchPermit(self.worker.root) as permit:
            self.assertFalse(permit.allowed)

    def test_crashed_pause_owner_cannot_leave_a_permanent_pause(self):
        ready = self.root / 'ready'
        process = self.child("from repair_dispatch_control import DispatchPause; from pathlib import Path; "
                             "import sys,time\nwith DispatchPause(sys.argv[1]):\n"
                             " Path(sys.argv[2]).touch()\n time.sleep(20)", self.worker.root, ready)
        self.wait_file(ready)
        with DispatchPermit(self.worker.root) as permit:
            self.assertFalse(permit.allowed)
        process.kill()
        process.wait(timeout=5)
        # The metadata may still say paused; the live kernel lock is authoritative.
        with DispatchPermit(self.worker.root) as permit:
            self.assertTrue(permit.allowed)


class PublicationCoordinationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.current = self.root / 'current'
        self.current.mkdir()
        (self.current / 'COMMIT').write_text('fixture-commit')
        self.state = self.root / 'state'
        self.locks = [self.root / ('lock-' + str(i)) for i in range(5)]

    def lease(self, **kwargs):
        return activation_lease(self.state, self.current, self.current, 'fixture-commit',
                                timeout=0.1, locks=self.locks, **kwargs)

    def test_second_publisher_cannot_release_the_first_pause(self):
        with self.lease():
            with self.assertRaises(TimeoutError):
                with self.lease():
                    self.fail('two publishers activated together')
            with DispatchPermit(self.state) as permit:
                self.assertFalse(permit.allowed)
        with self.lease():
            pass
        with DispatchPermit(self.state) as permit:
            self.assertTrue(permit.allowed)

    def test_stale_baseline_aborts_before_pausing_or_running_a_command(self):
        (self.current / 'COMMIT').write_text('newer-live-commit')
        with self.assertRaisesRegex(RuntimeError, 'commit changed'):
            with self.lease():
                self.fail('stale release was allowed to activate')
        with DispatchPermit(self.state) as permit:
            self.assertTrue(permit.allowed)

    def test_failed_activation_releases_only_its_own_lease(self):
        with self.lease() as descriptors:
            self.assertEqual(run_activation([sys.executable, '-c', 'raise SystemExit(7)'], descriptors), 7)
        with DispatchPermit(self.state) as permit:
            self.assertTrue(permit.allowed)

    def test_activation_child_inherits_pause_and_publication_descriptors(self):
        code = ("import fcntl,json,os,sys\n"
                "fds=json.loads(os.environ['ORRERY_PRODUCTION_RELEASE_LOCK_FDS'])"
                "+[int(os.environ['ORRERY_REPAIR_DISPATCH_LOCK_FD'])]\n"
                "assert len(fds)==len(sys.argv[1:])\n"
                "for fd,name in zip(fds,sys.argv[1:]):\n"
                " assert os.fstat(fd).st_ino==os.stat(name).st_ino\n"
                " with open(name,'a') as f:\n"
                "  try: fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)\n"
                "  except BlockingIOError: pass\n"
                "  else: raise SystemExit(9)\n")
        with self.lease() as descriptors:
            self.assertEqual(run_activation([sys.executable, '-c', code,
                                             *map(str, self.locks), str(self.state / 'dispatch.lock')], descriptors), 0)

    def test_waiting_for_another_publication_does_not_pause_the_worker(self):
        with self.locks[0].open('a') as other:
            fcntl.flock(other, fcntl.LOCK_EX)
            with self.assertRaises(TimeoutError):
                with self.lease():
                    self.fail('overtook another publication')
            with DispatchPermit(self.state) as permit:
                self.assertTrue(permit.allowed)

    def test_baseline_is_rechecked_after_in_flight_submission_drains(self):
        def changed_during_checks():
            (self.current / 'COMMIT').write_text('newer-live-commit')
        with self.assertRaisesRegex(RuntimeError, 'commit changed'):
            with self.lease(validate_worker=changed_during_checks):
                self.fail('ignored a baseline change during acquisition')
        self.assertEqual((self.current / 'COMMIT').read_text(), 'newer-live-commit')
        with DispatchPermit(self.state) as permit:
            self.assertTrue(permit.allowed)

    def test_incompatible_worker_does_not_pause_or_activate(self):
        def incompatible():
            raise RuntimeError('worker upgrade required')
        with self.assertRaisesRegex(RuntimeError, 'upgrade required'):
            with self.lease(validate_worker=incompatible):
                self.fail('an old worker could ignore dispatch control')
        with DispatchPermit(self.state) as permit:
            self.assertTrue(permit.allowed)

    def test_busy_api_times_out_without_running_activation_or_cancelling_work(self):
        probe = Mock(return_value={'pid': 'same-api', 'children': [123], 'turns': ['existing']})
        with self.assertRaisesRegex(TimeoutError, 'left running'):
            wait_for_idle(probe, 0.01, lambda: None, lambda: None)
        self.assertTrue(probe.called)


if __name__ == '__main__':
    unittest.main()
