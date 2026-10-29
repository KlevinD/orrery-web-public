"""Coordinate new repair dispatch without stopping collection or reconciliation."""
from contextlib import contextmanager
import datetime
import fcntl
import json
import os
from pathlib import Path
import tempfile
import time


PROTOCOL_VERSION = '1'


def open_lock(path):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    return os.open(path, os.O_RDWR | os.O_CREAT | os.O_CLOEXEC, 0o600)


def acquire(fd, exclusive, deadline):
    operation = fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH
    while True:
        try:
            fcntl.flock(fd, operation | fcntl.LOCK_NB)
            return
        except BlockingIOError:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('Timed out waiting for the deployment/dispatch lock') from None
            time.sleep(min(0.1, remaining))


class DispatchPermit:
    """Never block the worker loop when deployment owns the exclusive lock."""
    def __init__(self, state_dir):
        self.path = Path(state_dir) / 'dispatch.lock'
        self.fd = None
        self.allowed = False
        self.error = ''

    def __enter__(self):
        try:
            self.fd = open_lock(self.path)
            fcntl.flock(self.fd, fcntl.LOCK_SH | fcntl.LOCK_NB)
            self.allowed = True
        except BlockingIOError:
            pass
        except OSError as error:
            self.error = str(error)
        return self

    def __exit__(self, *_):
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None


def atomic_json(path, value):
    """Metadata is separate: replacing a lock inode would break mutual exclusion."""
    path = Path(path)
    descriptor, temporary = tempfile.mkstemp(prefix='.' + path.name + '-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w', encoding='utf-8') as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


class DispatchPause:
    """The kernel releases this lease after its last owner exits, including crashes."""
    def __init__(self, state_dir, timeout=60, reason='production activation'):
        self.root = Path(state_dir)
        self.timeout = timeout
        self.reason = reason
        self.fd = None
        self.metadata = None

    def __enter__(self):
        self.fd = open_lock(self.root / 'dispatch.lock')
        try:
            acquire(self.fd, True, time.monotonic() + self.timeout)
            self.metadata = {'protocol_version': PROTOCOL_VERSION, 'pid': os.getpid(),
                             'paused': True, 'reason': self.reason[:300],
                             'at': datetime.datetime.now(datetime.timezone.utc).isoformat()}
            atomic_json(self.root / 'dispatch-pause.json', self.metadata)
            return self
        except BaseException:
            os.close(self.fd)
            self.fd = None
            raise

    def __exit__(self, *_):
        try:
            self.metadata.update(paused=False, released_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
            atomic_json(self.root / 'dispatch-pause.json', self.metadata)
        finally:
            os.close(self.fd)
            self.fd = None


@contextmanager
def worker_dispatch_permit(worker):
    with DispatchPermit(worker.root) as permit:
        state = 'error' if permit.error else ('ready' if permit.allowed else 'paused')
        previous = worker.queue.get_meta('dispatch_state')
        worker.queue.set_meta('dispatch_state', state)
        # Do not expose filesystem errors or assignment contents through user reports.
        from codex_repair_worker import redact
        worker.queue.set_meta('dispatch_gate_error', redact(permit.error))
        if previous != state:
            print(json.dumps({'event': 'orrery_repair_dispatch_state', 'state': state}), flush=True)
        yield permit.allowed
