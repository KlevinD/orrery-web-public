"""Prepare an immutable repair baseline from the actual active release."""
import copy
import hashlib
import json
from pathlib import Path
import re
import shutil
import tarfile
import tempfile


def prepare(config, base):
    release = Path(config['production_release_root']).resolve(strict=True)
    commit = (release / 'COMMIT').read_text(encoding='ascii').strip()
    if not re.fullmatch(r'[0-9a-f]{40,64}', commit):
        raise ValueError('production release commit is invalid')
    archives = [release / 'SOURCE_SNAPSHOT.tar.gz', release / 'WEB_SOURCE_SNAPSHOT.tar.gz']
    hashes = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in archives}
    identity = hashlib.sha256(json.dumps(hashes, sort_keys=True).encode()).hexdigest()
    cache = base / '_sources'
    cache.mkdir(exist_ok=True, mode=0o700)
    destination = cache / identity
    marker = destination / '.source-complete'
    receipt = {'release': str(release), 'commit': commit, 'archives': hashes}
    if destination.exists():
        if not marker.is_file() or json.loads(marker.read_text()) != receipt:
            raise ValueError('production source cache has no matching completion receipt')
        return release, destination, commit
    temporary = Path(tempfile.mkdtemp(prefix='.production-', dir=cache))
    try:
        for path in archives:
            with tarfile.open(path, 'r:gz') as stream:
                for original in stream:
                    member = copy.copy(original)
                    name = member.name.removeprefix('./')
                    if name.startswith('web/') or name == 'web':
                        name = 'orrery_v2/' + name
                    member.name = name
                    target = Path(name)
                    if target.is_absolute() or '..' in target.parts or member.issym() or member.islnk() or not (member.isdir() or member.isfile()):
                        raise ValueError('production source archive contains an unsafe entry')
                    stream.extract(member, path=temporary, filter='data')
            if hashlib.sha256(path.read_bytes()).hexdigest() != hashes[path.name]:
                raise ValueError('production source archive changed during preparation')
        (temporary / '.source-complete').write_text(json.dumps(receipt, sort_keys=True), encoding='utf-8')
        temporary.rename(destination)
    except Exception:
        shutil.rmtree(temporary, ignore_errors=True)
        raise
    return release, destination, commit
