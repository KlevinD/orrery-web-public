#!/usr/bin/env python3
"""Verify explicitly registered Orrery packages; never exempt arbitrary file dependencies."""
import hashlib
import json
from pathlib import Path, PurePosixPath
import posixpath

MANIFEST = 'compliance/orrery-js-sources.json'
BASE = 'orrery_v2/runtime/orrery-js'


def digest(data):
    return hashlib.sha256(data.replace(b'\r\n', b'\n')).hexdigest()


def sources(read):
    manifest = json.loads(read(MANIFEST))
    if manifest.get('formatVersion') != 1:
        raise ValueError('Unsupported Orrery source manifest')
    records = {}
    for record in manifest['packages']:
        directory = record['directory']
        if directory not in {BASE+'/packages/'+name for name in ['ast-reference', 'source-location', 'error-callsite', 'proxy-negotiate', 'deepmerge']}:
            raise ValueError('Unregistered Orrery source directory')
        package = json.loads(read(directory+'/package.json'))
        if any(record[key] != package[key] for key in ['name', 'version', 'license']):
            raise ValueError('Orrery source identity differs: '+directory)
        if record['license'] != 'Apache-2.0' or not package.get('private'):
            raise ValueError('Unexpected Orrery source license or publication setting')
        expected = {directory+'/'+name for name in package['files'] + ['package.json']}
        if expected != set(record['files']) or directory in records:
            raise ValueError('Orrery source file set differs')
        for name, checksum in record['files'].items():
            if name not in expected or any(part in {'.', '..'} for part in PurePosixPath(name).parts):
                raise ValueError('Unsafe Orrery source path')
            if digest(read(name)) != checksum:
                raise ValueError('Stale Orrery source digest: '+name)
        if read(directory+'/LICENSE').replace(b'\r\n', b'\n') != read('LICENSE').replace(b'\r\n', b'\n'):
            raise ValueError('Orrery package license differs from project license')
        records[directory] = record
    expected_integration = {BASE+'/'+name for name in ['bundles.mjs', 'install.mjs', 'prepare-bundles.mjs',
                                                      'sources.mjs', 'bundled-replacements.json']}
    if set(manifest['integrationFiles']) != expected_integration:
        raise ValueError('Orrery integration source file set differs')
    for name, checksum in manifest['integrationFiles'].items():
        if not name.startswith(BASE+'/') or '..' in PurePosixPath(name).parts or digest(read(name)) != checksum:
            raise ValueError('Stale or unsafe Orrery integration source: '+name)
    return records


def local_package(read, lock, package):
    resolved = package.get('resolved', '')
    if not resolved.startswith('file:'):
        return None
    directory = posixpath.normpath(str(PurePosixPath(lock).parent / resolved[5:]))
    record = sources(read).get(directory)
    package_name = package.get('name')
    if package_name is None:
        package_name = json.loads(read(directory+'/package.json')).get('name')
    if record is None or any(value != record[key] for key, value in [
        ('name', package_name), ('version', package.get('version')), ('license', package.get('license'))
    ]):
        raise ValueError('Unregistered local npm package: '+directory)
    return dict(manifest=MANIFEST, manifestSha256=digest(read(MANIFEST)), directory=directory,
                files=record['files'], license=record['license'])


def refresh(root):
    packages = []
    for directory in sorted((root/BASE/'packages').iterdir()):
        package = json.loads((directory/'package.json').read_text(encoding='utf-8'))
        files = {str((directory/name).relative_to(root).as_posix()): digest((directory/name).read_bytes())
                 for name in sorted(package['files'] + ['package.json'])}
        packages.append(dict(name=package['name'], version=package['version'], license=package['license'],
                             directory=directory.relative_to(root).as_posix(), files=files))
    integration = {path.relative_to(root).as_posix(): digest(path.read_bytes())
                   for path in sorted((root/BASE).glob('*')) if path.is_file() and path.suffix in {'.mjs', '.json'}}
    content = dict(formatVersion=1, packages=packages, integrationFiles=integration)
    (root/MANIFEST).write_text(json.dumps(content, indent=2, sort_keys=True)+'\n', encoding='utf-8', newline='\n')
    sources(lambda name: (root/name).read_bytes())


if __name__ == '__main__':
    refresh(Path(__file__).resolve().parent.parent)
