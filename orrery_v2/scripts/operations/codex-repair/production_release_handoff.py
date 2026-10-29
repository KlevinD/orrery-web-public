"""Durable activation handoff out of the API's own execution cgroup."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
from contextlib import contextmanager

from repair_dispatch_control import acquire, atomic_json, open_lock


def inside_api(prop):
    group = prop('orrery-api-production.service', 'ControlGroup')
    if not group or group == '/':
        raise RuntimeError('Cannot establish API execution group')
    for line in Path('/proc/self/cgroup').read_text().splitlines():
        path = line.split(':', 2)[-1]
        if path == group or path.startswith(group.rstrip('/') + '/'):
            return True
    return False


def specification(args, command):
    spec = {'config': str(Path(args.config).resolve(strict=True)),
            'expected_release': str(Path(args.expected_release).resolve(strict=True)),
            'expected_commit': args.expected_commit, 'component': args.component,
            'lock_timeout': args.lock_timeout, 'drain_timeout': args.drain_timeout,
            'command': command}
    # Leave identities of existing requests without these options unchanged.
    for name in ('candidate_release', 'license_attachment'):
        value = getattr(args, name, None)
        if value is not None:
            if Path(value).is_symlink():
                raise ValueError('Activation preparation must name an immutable directory')
            spec[name] = str(Path(value).resolve(strict=True))
    return spec


def identity(spec):
    return hashlib.sha256(json.dumps(spec, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


@contextmanager
def request_lock(state_dir, request_id):
    if not re.fullmatch('[0-9a-f]{64}', request_id):
        raise ValueError('Invalid activation request ID')
    root = Path(state_dir).resolve(strict=True) / 'activation-requests'
    root.mkdir(mode=0o700, exist_ok=True)
    if root.is_symlink():
        raise ValueError('Activation request directory must not be a symlink')
    fd = open_lock(root / (request_id + '.lock'))
    try:
        acquire(fd, True, time.monotonic() + 10)
        path = root / (request_id + '.json')
        if path.is_symlink():
            raise ValueError('Activation request must not be a symlink')
        yield path
    finally:
        os.close(fd)


def defer_activation(args, command, config, script):
    spec = specification(args, command)
    request_id = identity(spec)
    unit = 'orrery-release-' + request_id[:24]
    with request_lock(config['state_dir'], request_id) as path:
        if path.exists():
            receipt = json.loads(path.read_text())
            if receipt.get('specification') != spec or receipt.get('request_id') != request_id:
                raise ValueError('Activation request identity changed')
            if receipt.get('status') not in ('queued', 'running', 'completed'):
                raise RuntimeError('Previous activation failed; inspect receipt and revalidate before resubmission')
            # Crash after writing intent but before starting the unit is safe to
            # recover with the same deterministic unit name. Never duplicate it.
            if receipt['status'] != 'queued':
                return receipt
        else:
            receipt = {'version': 1, 'request_id': request_id, 'status': 'queued',
                       'unit': unit, 'created_at': time.time(), 'specification': spec}
            atomic_json(path, receipt)
        active = subprocess.run(['systemctl', '--user', 'show', unit, '-p', 'ActiveState', '--value'],
                                capture_output=True, text=True, timeout=10)
        if active.stdout.strip() not in ('active', 'activating'):
            invocation = [sys.executable, '-B', str(Path(script).resolve(strict=True)),
                          '--config', spec['config'], '--expected-release', spec['expected_release'],
                          '--expected-commit', spec['expected_commit'], '--component', spec['component'],
                          '--lock-timeout', str(spec['lock_timeout']), '--drain-timeout', str(spec['drain_timeout']),
                          '--handoff-id', request_id]
            for name in ('candidate_release', 'license_attachment'):
                if name in spec:
                    invocation.extend(['--' + name.replace('_', '-'), spec[name]])
            invocation.extend(['--', *command])
            try:
                subprocess.run(['systemd-run', '--user', '--collect', '--service-type=exec',
                                '--unit=' + unit, '--', *invocation], check=True,
                               capture_output=True, text=True, timeout=15)
            except (OSError, subprocess.SubprocessError):
                receipt.update(status='failed', error='independent activation service could not start', finished_at=time.time())
                atomic_json(path, receipt)
                raise
        return receipt


def claim(args, command, config):
    spec = specification(args, command)
    if identity(spec) != args.handoff_id:
        raise ValueError('Activation command does not match the durable request')
    with request_lock(config['state_dir'], args.handoff_id) as path:
        receipt = json.loads(path.read_text())
        if receipt.get('specification') != spec or receipt.get('status') != 'queued':
            raise ValueError('Activation request was already consumed or changed')
        receipt.update(status='running', started_at=time.time(), activation_pid=os.getpid())
        atomic_json(path, receipt)


def finish(args, config, status, evidence):
    with request_lock(config['state_dir'], args.handoff_id) as path:
        receipt = json.loads(path.read_text())
        if receipt.get('status') != 'running' or receipt.get('activation_pid') != os.getpid():
            raise ValueError('Activation receipt is owned by another execution')
        receipt.update(status=status, finished_at=time.time(), evidence=evidence)
        atomic_json(path, receipt)
