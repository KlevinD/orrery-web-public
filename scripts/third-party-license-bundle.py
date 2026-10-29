#!/usr/bin/env python3
"""Collect original notices and verify/package them without installing dependencies."""
import argparse
import base64
import concurrent.futures
import gzip
import hashlib
import html
import importlib.util
import io
import json
from pathlib import Path, PurePosixPath
import re
import tarfile
import threading
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
MANIFEST = 'compliance/license-texts.json'
OVERRIDES = 'compliance/license-text-overrides.json'
TEXTS = 'compliance/licenses/collected'
LOCKS = ['orrery_v2/go.mod', 'orrery_v2/go.sum',
         'orrery_v2/web/package-lock.json',
         'orrery_v2/runtime/pi-coding-agent/package-lock.json',
         'orrery_v2/scripts/audio-codec/package-lock.json']
NOTICE_NAME = re.compile(r'^(licen[cs]es?|copying|notices?|copyright|patents|third[-_]party[-_]notices?)(?:[._-].*)?$', re.I)
NOTICE_VARIANT = re.compile(r'^(?:.+\.licen[cs]e(?:\.txt)?|copyrightnotice(?:\.txt)?|thirdpartynotice(?:s|text)?(?:\.txt)?)$', re.I)
WRITE_LOCK = threading.Lock()
_spec = importlib.util.spec_from_file_location('orrery_js_sources', ROOT/'scripts/orrery-js-sources.py')
own_sources = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(own_sources)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def safe(name):
    parts = PurePosixPath(name).parts
    if not parts or name.startswith('/') or '\\' in name or any(p in {'.', '..'} or ':' in p for p in parts):
        raise ValueError('Unsafe notice path: ' + name)
    if '/'.join(parts) != name:
        raise ValueError('Noncanonical notice path: ' + name)
    return name


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + '\n').encode('utf-8')


def lock_hashes(root):
    return {name: sha((root/name).read_bytes().replace(b'\r\n', b'\n')) for name in LOCKS}


def dependencies(root):
    return dependencies_from_reader(lambda name: (root/name).read_bytes())


def dependencies_from_reader(read):
    records = {}
    for line in read('orrery_v2/go.sum').decode('utf-8').splitlines():
        name, version, integrity = line.split()
        if version.endswith('/go.mod'):
            continue
        key = ('golang', name, version)
        records[key] = dict(ecosystem='golang', name=name, version=version,
                            integrity=integrity, scopes=['orrery_v2/go.sum'])
    for lock in LOCKS:
        if not lock.endswith('package-lock.json'):
            continue
        data = json.loads(read(lock))
        for location, package in data.get('packages', {}).items():
            if location and package.get('link'):
                raise ValueError('Linked npm package requires install-links=true: ' + location)
            if not location or package.get('link') or not package.get('version'):
                continue
            name = package.get('name') or location.split('node_modules/')[-1]
            key = ('npm', name, package['version'])
            record = records.setdefault(key, dict(ecosystem='npm', name=name,
                version=package['version'], integrity=package.get('integrity'),
                archive=package.get('resolved'), license=package.get('license'), scopes=[], bundledSources=[]))
            local = own_sources.local_package(read, lock, package)
            if local:
                record['localSource'] = local
            if record['integrity'] and package.get('integrity') and record['integrity'] != package['integrity']:
                raise ValueError('Conflicting archive identity: ' + name)
            if not record['integrity'] and package.get('integrity'):
                record.update(integrity=package['integrity'], archive=package['resolved'])
            if package.get('inBundle'):
                parent = location.rsplit('/node_modules/', 1)[0]
                while not data['packages'][parent].get('integrity'):
                    parent = parent.rsplit('/node_modules/', 1)[0]
                owner = data['packages'][parent]
                source = dict(archive=owner['resolved'], integrity=owner['integrity'],
                              prefix='package/' + location[len(parent)+1:])
                if source not in record['bundledSources']:
                    record['bundledSources'].append(source)
            if lock not in record['scopes']:
                record['scopes'].append(lock)
    return [records[key] for key in sorted(records)]


def is_notice(name):
    path = PurePosixPath(name)
    in_license_directory = (any(part.lower() in {'licenses', 'licences'} for part in path.parts[:-1])
                            and path.suffix.lower() in {'', '.txt', '.md', '.rst', '.html'})
    return bool(NOTICE_NAME.match(path.name) or NOTICE_VARIANT.match(path.name) or in_license_directory)


def archive_path(name):
    # npm tarballs sometimes use a harmless ./ within an otherwise relative path.
    if name.startswith('/') or '\\' in name or any(p == '..' or ':' in p for p in name.split('/')):
        raise ValueError('Unsafe notice path: ' + name)
    return safe('/'.join(p for p in name.split('/') if p not in {'', '.'}))


def stored_notice(root, name, data):
    safe(name)
    if not data.strip() or b'\x00' in data:
        raise ValueError('Empty or binary notice: ' + name)
    digest = sha(data)
    relative = TEXTS + '/' + digest + '.txt'
    target = root/relative
    target.parent.mkdir(parents=True, exist_ok=True)
    with WRITE_LOCK:
        if target.exists() and target.read_bytes() != data:
            raise ValueError('Notice content collision')
        target.write_bytes(data)
    return dict(originalPath=name, path=relative, sha256=digest, size=len(data))


def fetch(url):
    if not url.startswith('https://'):
        raise ValueError('HTTPS source required: ' + url)
    request = urllib.request.Request(url, headers={'User-Agent': 'Orrery-license-collector'})
    with urllib.request.urlopen(request, timeout=35) as response:
        if not response.geturl().startswith('https://'):
            raise ValueError('Non-HTTPS redirect')
        return response.read()


def check_integrity(data, integrity):
    # Prefer the strongest supported digest when an SRI field has alternatives.
    for algorithm in ['sha512', 'sha384', 'sha256']:
        choices = [part.split('-', 1)[1] for part in (integrity or '').split()
                   if part.startswith(algorithm + '-')]
        if choices:
            actual = base64.b64encode(hashlib.new(algorithm, data).digest()).decode()
            if actual not in choices:
                raise ValueError('Archive integrity mismatch')
            return
    raise ValueError('Missing supported archive integrity')


def npm_archive(source, cache):
    url = source['archive']
    if not url or not url.startswith('https://registry.npmjs.org/'):
        raise ValueError('Unreviewed npm registry: ' + str(url))
    archive = cache/'npm'/(sha(source['integrity'].encode()) + '.tgz')
    if not archive.exists():
        data = fetch(url)
        check_integrity(data, source['integrity'])
        archive.parent.mkdir(parents=True, exist_ok=True)
        archive.write_bytes(data)
    data = archive.read_bytes()
    check_integrity(data, source['integrity'])
    return data


def npm_notices(root, record, cache):
    notices = []
    sources = list(record['bundledSources'])
    if record['integrity']:
        sources.append(dict(archive=record['archive'], integrity=record['integrity'], prefix='package'))
    for source in sources:
        data = npm_archive(source, cache)
        prefix = source['prefix'] + '/'
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as package:
            members = sorted(package.getmembers(), key=lambda p: p.name)
            normalized = {member.name: archive_path(member.name) for member in members}
            candidates = [m for m in members if m.isfile() and normalized[m.name] == prefix+'package.json']
            if not candidates and source['prefix'] == 'package':
                candidates = [m for m in members if m.isfile() and len(PurePosixPath(normalized[m.name]).parts) == 2
                              and PurePosixPath(m.name).name == 'package.json']
                if len(candidates) == 1:
                    prefix = normalized[candidates[0].name].rsplit('/', 1)[0] + '/'
            if len(candidates) != 1:
                raise ValueError('Ambiguous or missing package manifest: ' + record['name'])
            manifest = json.loads(package.extractfile(candidates[0]).read())
            if (manifest['name'], manifest['version']) != (record['name'], record['version']):
                raise ValueError('Package identity mismatch')
            for member in members:
                name = normalized[member.name]
                if name.startswith(prefix) and member.isfile() and is_notice(name):
                    item = stored_notice(root, name, package.extractfile(member).read())
                    item.update(sourceArchive=source['archive'], archiveSha256=sha(data))
                    if item not in notices:
                        notices.append(item)
    return dict(record, notices=notices,
                evidence='integrity-verified-npm-tarball')


def verify_npm_supplement(record, notice, cache):
    """Check a reviewed source notice against its locked archive and byte range."""
    member_name = notice.get('sourceArchiveMember')
    if not member_name:
        # Some platform-only packages (for example Rolldown native bindings)
        # ship no notice file of their own. Their publisher release is covered
        # by the parent package's identical license text, which is recorded by
        # sourceComponent and verified against the local evidence file below.
        if notice.get('sourceComponent'):
            if not isinstance(notice['sourceComponent'], dict):
                raise ValueError('Invalid source component evidence')
            return
        if 'sourceByteRange' in notice or 'sourceMemberSha256' in notice:
            raise ValueError('Embedded notice has no archive member')
        return
    sources = list(record.get('bundledSources', []))
    if record.get('integrity'):
        sources.append(dict(archive=record['archive'], integrity=record['integrity']))
    sources = [source for source in sources if source['archive'] == notice.get('sourceArchive')]
    if len(sources) != 1:
        raise ValueError('Embedded notice archive is outside locked component')
    source = sources[0]
    name = archive_path(member_name)
    if source.get('prefix') and not name.startswith(source['prefix'] + '/'):
        raise ValueError('Embedded notice is outside bundled component')
    raw = npm_archive(source, cache)
    if sha(raw) != notice.get('archiveSha256'):
        raise ValueError('Embedded notice archive digest differs')
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        matches = [member for member in archive.getmembers()
                   if member.isfile() and archive_path(member.name) == name]
        if len(matches) != 1:
            raise ValueError('Embedded notice member missing or ambiguous')
        content = archive.extractfile(matches[0]).read()
    if 'sourceByteRange' in notice:
        bounds = notice['sourceByteRange']
        if (not isinstance(bounds, list) or len(bounds) != 2
                or any(type(bound) is not int for bound in bounds)
                or not 0 <= bounds[0] < bounds[1] <= len(content)):
            raise ValueError('Invalid embedded notice byte range')
        if sha(content) != notice.get('sourceMemberSha256'):
            raise ValueError('Embedded notice source digest differs')
        content = content[bounds[0]:bounds[1]]
    elif 'sourceMemberSha256' in notice and sha(content) != notice['sourceMemberSha256']:
        raise ValueError('Embedded notice source digest differs')
    if sha(content) != notice['sha256'] or len(content) != notice['size']:
        raise ValueError('Embedded notice bytes differ from locked source')


def go_notices(root, record, cache):
    escaped = ''.join('!' + c.lower() if c.isupper() else c for c in record['name'])
    archive = cache/'cache/download'/escaped/'@v'/(record['version'] + '.zip')
    with zipfile.ZipFile(archive) as package:
        # Go's dirhash.HashZip hashes sorted file names and their content digests.
        sums = hashlib.sha256()
        for name in sorted(package.namelist()):
            if name.endswith('/'):
                continue
            safe(name)
            sums.update((sha(package.read(name)) + '  ' + name + '\n').encode())
        actual = 'h1:' + base64.b64encode(sums.digest()).decode()
        if actual != record['integrity']:
            raise ValueError('Go module checksum mismatch: ' + record['name'])
        notices = [stored_notice(root, name, package.read(name))
                   for name in sorted(package.namelist()) if not name.endswith('/') and is_notice(name)]
    return dict(record, notices=notices, evidence='go-sum-verified-module-archive')


def collect(root, cache, go_cache, go_root):
    expected = dependencies(root)
    def one(record):
        try:
            if record.get('localSource'):
                directory = record['localSource']['directory']
                notices = [stored_notice(root, directory+'/'+name, (root/directory/name).read_bytes())
                           for name in ['LICENSE', 'NOTICE']]
                return dict(record, notices=notices, evidence='independently-implemented-orrery-source')
            result = (npm_notices(root, record, cache) if record['ecosystem'] == 'npm'
                      else go_notices(root, record, go_cache))
            return result
        except Exception as error:
            return dict(record, notices=[], error=str(error))
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        records = list(pool.map(one, expected))
    # Explicit source evidence for packages that omit their original notices.
    overrides_path = root/OVERRIDES
    overrides = json.loads(overrides_path.read_text(encoding='utf-8')) if overrides_path.exists() else {'components': []}
    by_key = {(r['ecosystem'], r['name'], r['version']): r for r in records}
    for override in overrides['components']:
        key = (override['ecosystem'], override['name'], override['version'])
        record = by_key[key]
        if override['integrity'] != record['integrity']:
            raise ValueError('Stale notice override: ' + str(key))
        for item in override['notices']:
            data = (root/safe(item['path'])).read_bytes()
            if sha(data) != item['sha256']:
                raise ValueError('Stale supplemental notice: ' + item['path'])
            if record['ecosystem'] == 'npm':
                verify_npm_supplement(record, item, cache)
            record['notices'].append(item)
        record['supplementalEvidence'] = override['evidence']
    bundled = []
    for directory in ['orrery_v2/third_party/centrifuge', 'orrery_v2/third_party/lark-sdk',
                      'orrery_v2/third_party/dingtalk-stream-sdk']:
        for path in sorted((root/directory).rglob('*')):
            if path.is_file() and is_notice(path.name):
                bundled.append(stored_notice(root, path.relative_to(root).as_posix(), path.read_bytes()))
    toolchain = []
    for path in [go_root/'LICENSE', go_root/'PATENTS', *sorted((go_root/'src').rglob('LICENSE*'))]:
        if path.is_file():
            toolchain.append(stored_notice(root, path.relative_to(go_root).as_posix(), path.read_bytes()))
    result = dict(formatVersion=1, scope='Orrery Web/API source lock graphs, including build/test/platform packages; external PageIndex excluded',
        locks=lock_hashes(root), components=records, vendoredNotices=bundled,
        goToolchain=dict(version=(go_root/'VERSION').read_text().splitlines()[0], notices=toolchain),
        redistributionApproved=False)
    (root/MANIFEST).write_bytes(json_bytes(result))
    failures = [r['name'] + '@' + r['version'] + ': ' + r.get('error', 'no original notice supplied')
                for r in records if not r['notices'] or r.get('error')]
    print(json.dumps(dict(components=len(records), missing=failures, manifest=MANIFEST), indent=2), flush=True)
    return not failures


def verify(root):
    def read(name):
        path = root/safe(name)
        if not path.resolve().is_relative_to(root.resolve()):
            raise ValueError('Notice escapes repository')
        return path.read_bytes()
    return verify_files(read)


def verify_files(read):
    manifest = json.loads(read(MANIFEST))
    locks = {name: sha(read(name).replace(b'\r\n', b'\n')) for name in LOCKS}
    if manifest['formatVersion'] != 1 or manifest['locks'] != locks:
        raise ValueError('Stale license lock graph')
    expected = {(r['ecosystem'], r['name'], r['version']): r for r in dependencies_from_reader(read)}
    actual = {}
    notices = list(manifest['vendoredNotices']) + list(manifest['goToolchain']['notices'])
    required = {'orrery_v2/third_party/'+name for name in [
        'centrifuge/LICENSE', 'centrifuge/internal/websocket/LICENSE',
        'lark-sdk/LICENSE', 'dingtalk-stream-sdk/LICENSE']}
    if not required.issubset({n['originalPath'] for n in manifest['vendoredNotices']}) or not manifest['goToolchain']['notices']:
        raise ValueError('Missing vendored or Go runtime notice')
    for row in manifest['components']:
        key = (row['ecosystem'], row['name'], row['version'])
        if key in actual:
            raise ValueError('Duplicate license component')
        actual[key] = row
        if row.get('error') or not row['notices']:
            raise ValueError('Original notice missing: ' + row['name'])
        notices.extend(row['notices'])
    if set(actual) != set(expected):
        raise ValueError('License component set differs from lock graphs')
    for key, row in expected.items():
        if any(actual[key].get(k) != row.get(k) for k in row):
            raise ValueError('Stale component evidence: ' + str(key))
    overrides = json.loads(read(OVERRIDES))
    if overrides.get('formatVersion') != 1 or not isinstance(overrides.get('components'), list):
        raise ValueError('Invalid supplemental notice registry')
    registered = set()
    for override in overrides['components']:
        key = (override['ecosystem'], override['name'], override['version'])
        if key in registered or key not in actual:
            raise ValueError('Duplicate or unknown supplemental component: ' + str(key))
        registered.add(key)
        row = actual[key]
        if override['integrity'] != row.get('integrity'):
            raise ValueError('Stale supplemental archive identity: ' + str(key))
        if (row.get('supplementalEvidence') != override['evidence']
                or not override['notices']
                or any(item not in row['notices'] for item in override['notices'])):
            raise ValueError('Supplemental notice omitted or evidence changed: ' + str(key))
        for item in override['notices']:
            source_component = item.get('sourceComponent')
            if source_component:
                source_key = (source_component.get('ecosystem'), source_component.get('name'), source_component.get('version'))
                source = actual.get(source_key)
                if not source or source.get('license') != row.get('license'):
                    raise ValueError('Supplemental source component license mismatch: ' + str(key))
    if any(key not in registered for key, row in actual.items() if row.get('supplementalEvidence')):
        raise ValueError('Supplemental evidence has no registered source')
    for row in notices:
        data = read(safe(row['path']))
        if not data.strip() or sha(data) != row['sha256'] or len(data) != row['size']:
            raise ValueError('Original notice digest differs: ' + row['path'])
    for row in manifest['vendoredNotices']:
        if read(safe(row['originalPath'])).replace(b'\r\n', b'\n') != read(row['path']).replace(b'\r\n', b'\n'):
            raise ValueError('Vendored source notice changed: ' + row['originalPath'])
    return manifest


def reference_only_components(manifest):
    return [row['name'] + '@' + row['version'] for row in manifest['components']
            if row.get('supplementalEvidence', {}).get('kind')
            == 'publisher-license-reference-with-canonical-text']


def build_bundle(root, output):
    manifest = verify(root)
    names = {'LICENSE', 'NOTICE', 'THIRD_PARTY.md', MANIFEST,
             'compliance/source-adaptations.json', 'compliance/web-api-source-inventory.json',
             'compliance/web-api-sbom.cdx.json', 'compliance/web-asset-rights.json'}
    names.add(OVERRIDES)
    names.add('docs/third-party-license-bundle.md')
    # Keep the evidence behind follow-up links available in the offline bundle.
    names.update(p.relative_to(root).as_posix() for p in (root/'docs').glob('license-upstream-*.md') if p.is_file())
    names.update(p.relative_to(root).as_posix() for p in (root/'compliance').glob('license-upstream-review-*.json') if p.is_file())
    for directory, pattern in [('docs', 'opensource-self-developed-sync-*.md'),
                               ('docs', 'self-developed-js-validation-*.md'),
                               ('compliance', 'self-developed-sync-validation-*.json')]:
        names.update(p.relative_to(root).as_posix() for p in (root/directory).glob(pattern) if p.is_file())
    source_inputs = {}
    if any(row.get('localSource') for row in manifest['components']):
        names.add(own_sources.MANIFEST)
        names.update({'orrery_v2/runtime/orrery-js/README.md', 'scripts/orrery-js-sources.py'})
        own = json.loads((root/own_sources.MANIFEST).read_bytes())
        names.update(own['integrationFiles'])
        for package in own['packages']:
            names.update(package['files'])
            source_inputs.update(package['files'])
        source_inputs.update(own['integrationFiles'])
        source_inputs[own_sources.MANIFEST] = own_sources.digest((root/own_sources.MANIFEST).read_bytes())
    for row in manifest['components']:
        names.update(n['path'] for n in row['notices'])
    names.update(n['path'] for n in manifest['vendoredNotices'])
    names.update(n['path'] for n in manifest['goToolchain']['notices'])
    names.update(p.relative_to(root).as_posix() for p in (root/'compliance/licenses').rglob('*') if p.is_file())
    names.update(['orrery_v2/web/static/fonts/OFL-DM-Sans.txt',
                  'orrery_v2/web/static/fonts/OFL-JetBrains-Mono.txt',
                  'orrery_v2/docs/runtime/runtime-source-provenance.md'])
    payload = {name: (root/safe(name)).read_bytes() for name in sorted(names)}
    rows = []
    references = reference_only_components(manifest)
    for row in manifest['components']:
        links = ' '.join('<a href="' + html.escape(n['path'], quote=True) + '">' +
                         html.escape(n['originalPath']) + '</a>' for n in row['notices'])
        label = ('发布者仅提供协议引用；附参考文本' if row['name'] + '@' + row['version']
                 in references else '许可与来源声明')
        if row.get('localSource'):
            label = 'Orrery 自研组件许可与源码摘要'
        if row.get('supplementalEvidence', {}).get('projectLicenseConflict'):
            label += '；锁定包与后续项目许可有差异，见清单证据'
        embedded = sorted({n['embeddedLicense'] for n in row['notices'] if n.get('embeddedLicense')})
        if embedded:
            label += '；嵌入源码许可：' + html.escape(', '.join(embedded))
        rows.append('<tr><td>' + html.escape(row['name']) + '</td><td>' +
                    html.escape(row['version']) + '</td><td>' + label + '<br>' + links + '</td></tr>')
    page = ('<!doctype html><html lang="zh-CN"><meta charset="utf-8">'
        '<meta name="viewport" content="width=device-width,initial-scale=1">'
        '<title>Orrery 第三方软件声明</title><main><h1>第三方软件声明</h1>'
        '<p>Orrery 使用的开源组件保留各自的版权与许可。以下清单同时包含构建、测试及可选平台依赖。</p>'
        '<p>' + str(len(references)) + ' 个锁定包的完整原始许可仍未确认，表中单列发布者声明及 MIT 参考文本。'
        '材料核验不等于这些上游缺口已解决。<a href="docs/third-party-license-bundle.md">收集范围与已知缺口</a></p>'
        '<p><a href="LICENSE">Orrery 许可证</a> · <a href="NOTICE">版权声明</a> · '
        '<a href="THIRD_PARTY.md">组件说明</a> · <a href="THIRD_PARTY_NOTICES.zip">下载完整许可附件</a></p>'
        '<p>Orrery 支持连接独立的 <a href="https://github.com/VectifyAI/PageIndex">PageIndex 知识服务</a>。</p>'
        '<table><thead><tr><th>组件</th><th>版本</th><th>原始许可与声明</th></tr></thead><tbody>' +
        ''.join(rows) + '</tbody></table></main></html>')
    payload['index.html'] = page.encode('utf-8')
    index = dict(formatVersion=1, scope=manifest['scope'], locks=manifest['locks'],
                 sourceInputs=source_inputs,
                 components=len(manifest['components']),
                 publisherReferenceOnly=references,
                 publisherLicenseConflicts=[row['name'] + '@' + row['version'] for row in manifest['components']
                     if row.get('supplementalEvidence', {}).get('projectLicenseConflict')],
                 embeddedSourceNotices=[dict(component=row['name'] + '@' + row['version'],
                     license=n['embeddedLicense'], originalPath=n['originalPath'], path=n['path'])
                     for row in manifest['components'] for n in row['notices'] if n.get('embeddedLicense')],
                 files={name: sha(data) for name, data in payload.items()},
                 redistributionApproved=False)
    payload['BUNDLE_MANIFEST.json'] = json_bytes(index)
    if output.exists():
        raise ValueError('Refusing to overwrite bundle: ' + str(output))
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open('xb') as stream, zipfile.ZipFile(stream, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
        for name, data in payload.items():
            entry = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            entry.external_attr = 0o100644 << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(entry, data)
    return dict(path=str(output), sha256=sha(output.read_bytes()), files=len(payload),
                components=len(manifest['components']), redistributionApproved=False)


def augment_source(source, bundle, output, expected_source_sha256, source_commit, attachment_commit):
    """Add notices to an immutable source snapshot without changing original members."""
    for commit in [source_commit, attachment_commit]:
        if not re.fullmatch(r'[0-9a-f]{40}', commit):
            raise ValueError('A full source and attachment commit are required')
    original_hash = sha(source.read_bytes())
    if original_hash != expected_source_sha256:
        raise ValueError('Source snapshot digest differs')
    if output.exists():
        raise ValueError('Refusing to overwrite augmented source')
    with zipfile.ZipFile(bundle) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)):
            raise ValueError('Duplicate bundle member')
        for name in names:
            safe(name)
        index = json.loads(archive.read('BUNDLE_MANIFEST.json'))
        if set(names) != set(index['files']) | {'BUNDLE_MANIFEST.json'}:
            raise ValueError('Bundle member set differs')
        payload = {name: archive.read(name) for name in names}
        if any(sha(payload[name]) != digest for name, digest in index['files'].items()):
            raise ValueError('Bundle digest differs')
    with tarfile.open(source, 'r:gz') as original:
        members = original.getmembers()
        seen = set()
        files = {}
        for member in members:
            name = archive_path(member.name)
            if name in seen or not (member.isfile() or member.isdir()):
                raise ValueError('Duplicate or unsupported source member: ' + name)
            seen.add(name)
            if member.isfile():
                files[name] = member
        for name, digest in index['locks'].items():
            if name not in files or sha(original.extractfile(files[name]).read().replace(b'\r\n', b'\n')) != digest:
                raise ValueError('Production source lock graph differs: ' + name)
        for name, digest in index.get('sourceInputs', {}).items():
            if name not in files or sha(original.extractfile(files[name]).read().replace(b'\r\n', b'\n')) != digest:
                raise ValueError('Production Orrery package source differs: ' + name)
        for name in set(payload) & set(files):
            if original.extractfile(files[name]).read().replace(b'\r\n', b'\n') != payload[name].replace(b'\r\n', b'\n'):
                raise ValueError('Source and notice attachment conflict: ' + name)
        if set(payload) & (seen - set(files)):
            raise ValueError('Notice conflicts with original directory')
        added = {name: data for name, data in payload.items() if name not in seen}
        receipt_name = 'SOURCE_NOTICE_ATTACHMENT.json'
        if receipt_name in seen:
            raise ValueError('Source already contains an attachment receipt')
        receipt = dict(formatVersion=1, sourceCommit=source_commit, attachmentCommit=attachment_commit,
                       originalSourceSha256=original_hash, noticeBundleSha256=sha(bundle.read_bytes()),
                       originalFileCount=len(files), originalFilesPreserved=True,
                       addedFiles={name: sha(data) for name, data in sorted(added.items())})
        added[receipt_name] = json_bytes(receipt)
        output.parent.mkdir(parents=True, exist_ok=True)
        created = False
        try:
            with output.open('xb') as stream:
                created = True
                with gzip.GzipFile(filename='', mode='wb', fileobj=stream, mtime=0) as compressed:
                    with tarfile.open(fileobj=compressed, mode='w|', format=tarfile.PAX_FORMAT) as combined:
                        for member in members:
                            combined.addfile(member, original.extractfile(member) if member.isfile() else None)
                        for name, data in sorted(added.items()):
                            member = tarfile.TarInfo(safe(name))
                            member.size, member.mode, member.mtime = len(data), 0o644, 0
                            combined.addfile(member, io.BytesIO(data))
        except BaseException:
            if created:
                output.unlink(missing_ok=True)
            raise
    return dict(path=str(output), sha256=sha(output.read_bytes()), **receipt)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, default=ROOT)
    sub = parser.add_subparsers(dest='command', required=True)
    gather = sub.add_parser('collect')
    gather.add_argument('--cache', type=Path, required=True)
    gather.add_argument('--go-cache', type=Path, required=True)
    gather.add_argument('--go-root', type=Path, required=True)
    sub.add_parser('check')
    bundle = sub.add_parser('bundle')
    bundle.add_argument('--output', type=Path, required=True)
    augmented = sub.add_parser('augment-source')
    augmented.add_argument('--source', type=Path, required=True)
    augmented.add_argument('--bundle', type=Path, required=True)
    augmented.add_argument('--output', type=Path, required=True)
    augmented.add_argument('--expected-source-sha256', required=True)
    augmented.add_argument('--source-commit', required=True)
    augmented.add_argument('--attachment-commit', required=True)
    args = parser.parse_args()
    root = args.repo.resolve()
    if args.command == 'collect':
        if not collect(root, args.cache, args.go_cache, args.go_root):
            raise SystemExit(1)
    elif args.command == 'check':
        record = verify(root)
        print(json.dumps(dict(components=len(record['components']), license_evidence_verified=True,
                              publisher_reference_only=reference_only_components(record))))
    elif args.command == 'bundle':
        print(json.dumps(build_bundle(root, args.output), indent=2))
    else:
        print(json.dumps(augment_source(args.source, args.bundle, args.output, args.expected_source_sha256,
                                      args.source_commit, args.attachment_commit), indent=2))


if __name__ == '__main__':
    main()
