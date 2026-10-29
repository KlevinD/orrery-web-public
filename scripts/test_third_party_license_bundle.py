"""Regression checks for incomplete evidence, archive tampering and release omissions."""
import base64
import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('licenses', Path(__file__).with_name('third-party-license-bundle.py'))
licenses = importlib.util.module_from_spec(spec)
spec.loader.exec_module(licenses)


class LicenseBundleTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='orrery-notices-')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.integrity = 'sha512-' + base64.b64encode(hashlib.sha512(b'fixture').digest()).decode()
        for name in licenses.LOCKS:
            data = b'' if name.endswith(('.sum', '.mod')) else b'{"packages":{}}'
            self.write(name, data)
        self.audio = licenses.LOCKS[-1]
        self.write(self.audio, licenses.json_bytes(dict(packages={
            'node_modules/silk-wasm': dict(version='3.7.1', license='MIT',
                resolved='https://registry.npmjs.org/silk-wasm/-/silk-wasm-3.7.1.tgz', integrity=self.integrity)})))
        self.original = b'Original author copyright and permission\r\n'
        self.notice = licenses.stored_notice(self.root, 'package/LICENSE', self.original)
        self.record = dict(licenses.dependencies(self.root)[0], notices=[self.notice])
        self.vendor = [dict(self.notice, originalPath='orrery_v2/third_party/'+name) for name in [
            'centrifuge/LICENSE', 'centrifuge/internal/websocket/LICENSE',
            'lark-sdk/LICENSE', 'dingtalk-stream-sdk/LICENSE']]
        for notice in self.vendor:
            self.write(notice['originalPath'], self.original)
        self.manifest = dict(formatVersion=1, scope='fixture', locks=licenses.lock_hashes(self.root),
            components=[self.record], vendoredNotices=self.vendor,
            goToolchain=dict(version='go1.25.0', notices=[self.notice]), redistributionApproved=False)
        self.write(licenses.OVERRIDES, licenses.json_bytes(dict(formatVersion=1, components=[])))
        self.save()

    def write(self, name, data):
        path = self.root/name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def save(self):
        self.write(licenses.MANIFEST, licenses.json_bytes(self.manifest))

    def register_supplement(self, notices):
        row = self.manifest['components'][0]
        override = {key: row[key] for key in ['ecosystem', 'name', 'version', 'integrity']}
        override.update(notices=copy.deepcopy(notices), evidence=copy.deepcopy(row['supplementalEvidence']))
        self.write(licenses.OVERRIDES, licenses.json_bytes(dict(formatVersion=1, components=[override])))

    def test_original_notice_bytes_are_preserved(self):
        licenses.verify(self.root)
        self.assertEqual((self.root/self.notice['path']).read_bytes(), self.original)

    def test_audio_dependency_cannot_be_dropped_from_evidence(self):
        self.manifest['components'] = []
        self.save()
        with self.assertRaisesRegex(ValueError, 'component set differs'):
            licenses.verify(self.root)

    def test_dependency_update_requires_new_evidence(self):
        content = (self.root/self.audio).read_bytes().replace(b'3.7.1', b'3.7.2')
        self.write(self.audio, content)
        with self.assertRaisesRegex(ValueError, 'Stale license lock graph'):
            licenses.verify(self.root)

    def test_tampered_or_empty_text_is_rejected(self):
        for data in [b'', b'Replacement generic license']:
            with self.subTest(data=data):
                self.write(self.notice['path'], data)
                with self.assertRaisesRegex(ValueError, 'notice digest differs'):
                    licenses.verify(self.root)

    def test_metadata_only_is_not_a_license_text(self):
        self.manifest['components'][0]['notices'] = []
        self.save()
        with self.assertRaisesRegex(ValueError, 'Original notice missing'):
            licenses.verify(self.root)

    def test_vendored_and_standard_library_notices_are_required(self):
        self.manifest['vendoredNotices'].pop()
        self.save()
        with self.assertRaisesRegex(ValueError, 'Missing vendored or Go runtime'):
            licenses.verify(self.root)

    def test_vendored_source_license_drift_is_rejected(self):
        self.write(self.vendor[0]['originalPath'], b'changed author notice')
        with self.assertRaisesRegex(ValueError, 'Vendored source notice changed'):
            licenses.verify(self.root)

    def test_duplicate_component_is_rejected(self):
        self.manifest['components'].append(copy.deepcopy(self.record))
        self.save()
        with self.assertRaisesRegex(ValueError, 'Duplicate license component'):
            licenses.verify(self.root)

    def test_notice_cannot_escape_source_root(self):
        self.manifest['components'][0]['notices'][0]['path'] = '../outside.txt'
        self.save()
        with self.assertRaisesRegex(ValueError, 'Unsafe notice path'):
            licenses.verify(self.root)

    def test_tarball_sri_is_checked_before_reading_notices(self):
        with self.assertRaisesRegex(ValueError, 'integrity mismatch'):
            licenses.check_integrity(b'tampered', self.integrity)
        licenses.check_integrity(b'fixture', self.integrity)

    def test_publisher_notice_filename_variants_are_collected_without_code_false_positives(self):
        members = {
            'package/package.json': b'{"name":"silk-wasm","version":"3.7.1"}',
            'package/ThirdPartyNotices.txt': b'Original third-party attribution\r\n',
            'package/ThirdPartyNoticeText.txt': b'Original dependency license texts\n',
            'package/CopyrightNotice.txt': b'Original publisher copyright\n',
            'package/lib/bundle.js.LICENSE': b'Original bundled source licenses\n',
            'package/lib/bundle.js.LICENSE.txt': b'Original bundled source notices\n',
            'package/tests/MIT.LICENSE': b'Original test library license\n',
            'package/LICENSES/MIT.txt': b'Original project license\n',
            'package/LICENSES/CC-BY-ND-4.0.txt': b'Original asset license\n',
            'package/LICENCES/upstream-sdk.txt': b'Original embedded SDK license\n',
            'package/LICENSES/generator.js': b'Not a notice',
            'package/lib/info-license.js': b'Not a notice',
            'package/lib/docNoticeRole.js': b'Not a notice',
            'package/externalLicense.yaml': b'Not a notice',
        }
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode='w:gz') as archive:
            for name, data in members.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        raw = stream.getvalue()
        record = copy.deepcopy(self.record)
        record['integrity'] = 'sha512-' + base64.b64encode(hashlib.sha512(raw).digest()).decode()
        cache = self.root/'cache'
        target = cache/'npm'/(licenses.sha(record['integrity'].encode())+'.tgz')
        target.parent.mkdir(parents=True)
        target.write_bytes(raw)
        result = licenses.npm_notices(self.root, record, cache)
        expected = {name for name in members if licenses.is_notice(name)}
        self.assertEqual(len(expected), 9)
        self.assertEqual({n['originalPath'] for n in result['notices']}, expected)
        for notice in result['notices']:
            self.assertEqual((self.root/notice['path']).read_bytes(), members[notice['originalPath']])
            self.assertEqual(notice['archiveSha256'], licenses.sha(raw))

    def test_notice_filename_matching_is_case_insensitive_and_basename_scoped(self):
        for name in ['vendor/THIRDPARTYNOTICES.TXT', 'copyrightnotice.TXT', 'nested/mit.license',
                     'nested/test.js.license.txt', 'vendor/NOTICE.md', 'COPYRIGHT',
                     'vendor/LICENSES/BSD-3-Clause.txt', 'LICENCES/upstream.md']:
            with self.subTest(name=name):
                self.assertTrue(licenses.is_notice(name))
        for name in ['ThirdPartyNotices.txt/source.js', 'test.license.js', 'unlicensed.js',
                     'info-license-strict.test.ts', 'licenseUtils.ts', 'docNoticeRole.js',
                     'LICENSES/generator.js', 'vendor/LICENCES/test.ts']:
            with self.subTest(name=name):
                self.assertFalse(licenses.is_notice(name))

    def source_supplement(self):
        original = b'/* Copyright Bj\xc3\xb6rn\r\nOriginal permission and warranty\r\n*/'
        source = b'const before = 1;\n' + original + b'\nconst after = 2;\n'
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode='w:gz') as archive:
            member = tarfile.TarInfo('package/lib/source.js')
            member.size = len(source)
            archive.addfile(member, io.BytesIO(source))
        raw = stream.getvalue()
        record = copy.deepcopy(self.record)
        record['integrity'] = 'sha512-' + base64.b64encode(hashlib.sha512(raw).digest()).decode()
        cache = self.root/'cache'
        target = cache/'npm'/(licenses.sha(record['integrity'].encode())+'.tgz')
        target.parent.mkdir(parents=True)
        target.write_bytes(raw)
        start = source.index(original)
        notice = dict(sourceArchive=record['archive'], archiveSha256=licenses.sha(raw),
            sourceArchiveMember='package/lib/source.js', sourceMemberSha256=licenses.sha(source),
            sourceByteRange=[start, start+len(original)], sha256=licenses.sha(original), size=len(original))
        return record, notice, cache, source

    def test_source_supplement_is_verified_as_original_comment_bytes_or_entire_file(self):
        record, notice, cache, source = self.source_supplement()
        licenses.verify_npm_supplement(record, notice, cache)
        whole = dict(notice, sha256=licenses.sha(source), size=len(source))
        whole.pop('sourceByteRange')
        licenses.verify_npm_supplement(record, whole, cache)

    def test_source_supplement_rejects_wrong_source_archive_member_range_or_bytes(self):
        record, notice, cache, _ = self.source_supplement()
        changes = [dict(sourceArchive='https://registry.npmjs.org/unrelated/-/unrelated.tgz'),
            dict(archiveSha256='0'*64), dict(sourceArchiveMember='package/missing.js'),
            dict(sourceArchiveMember='package/../source.js'), dict(sourceMemberSha256='0'*64),
            dict(sourceByteRange=[0, 999999]), dict(sourceByteRange=[-1, 10]),
            dict(sourceByteRange=[True, 10]), dict(sourceByteRange=[10, 10]),
            dict(sourceByteRange=[0]), dict(sha256='0'*64), dict(size=notice['size']+1)]
        for change in changes:
            with self.subTest(change=change), self.assertRaises(ValueError):
                licenses.verify_npm_supplement(record, dict(notice, **change), cache)
        missing = dict(notice)
        missing.pop('sourceArchiveMember')
        with self.assertRaisesRegex(ValueError, 'no archive member'):
            licenses.verify_npm_supplement(record, missing, cache)

    def test_bundled_supplement_cannot_borrow_a_parent_or_sibling_source_file(self):
        record, notice, cache, _ = self.source_supplement()
        record['bundledSources'] = [dict(archive=record['archive'], integrity=record['integrity'],
            prefix='package/node_modules/child')]
        record['integrity'] = None
        with self.assertRaisesRegex(ValueError, 'outside bundled component'):
            licenses.verify_npm_supplement(record, notice, cache)

    def test_bundled_package_is_bound_to_its_parent_tarball(self):
        lock = json.loads((self.root/self.audio).read_text())
        lock['packages']['node_modules/parent'] = dict(version='1.0.0',
            resolved='https://registry.npmjs.org/parent/-/parent-1.0.0.tgz', integrity=self.integrity)
        lock['packages']['node_modules/parent/node_modules/child'] = dict(version='2.0.0', inBundle=True, license='MIT')
        self.write(self.audio, licenses.json_bytes(lock))
        child = next(r for r in licenses.dependencies(self.root) if r['name'] == 'child')
        self.assertEqual(child['bundledSources'], [dict(archive='https://registry.npmjs.org/parent/-/parent-1.0.0.tgz',
            integrity=self.integrity, prefix='package/node_modules/child')])
        self.assertIsNone(child['integrity'])

    def test_archive_traversal_is_rejected_without_extracting(self):
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode='w:gz') as archive:
            for name, data in [('package/package.json', b'{"name":"silk-wasm","version":"3.7.1"}'),
                               ('package/../LICENSE', b'copyright')]:
                entry = tarfile.TarInfo(name)
                entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))
        data = stream.getvalue()
        record = copy.deepcopy(self.record)
        record['integrity'] = 'sha512-' + base64.b64encode(hashlib.sha512(data).digest()).decode()
        cache = self.root/'cache'
        target = cache/'npm'/(licenses.sha(record['integrity'].encode())+'.tgz')
        target.parent.mkdir(parents=True)
        target.write_bytes(data)
        with self.assertRaisesRegex(ValueError, 'Unsafe notice path'):
            licenses.npm_notices(self.root, record, cache)

    def prepare_bundle(self):
        for name in ['LICENSE', 'NOTICE', 'THIRD_PARTY.md', 'compliance/source-adaptations.json',
                     'compliance/web-api-source-inventory.json', 'compliance/web-api-sbom.cdx.json',
                     'compliance/web-asset-rights.json', 'orrery_v2/web/static/fonts/OFL-DM-Sans.txt',
                     'orrery_v2/web/static/fonts/OFL-JetBrains-Mono.txt',
                     'orrery_v2/docs/runtime/runtime-source-provenance.md',
                     'docs/third-party-license-bundle.md']:
            self.write(name, b'fixture original text\n')

    def test_bundle_carries_root_notices_fonts_and_original_text(self):
        self.prepare_bundle()
        first = licenses.build_bundle(self.root, self.root/'first.zip')
        second = licenses.build_bundle(self.root, self.root/'second.zip')
        self.assertEqual(first['sha256'], second['sha256'])
        with zipfile.ZipFile(self.root/'first.zip') as archive:
            self.assertEqual(archive.read(self.notice['path']), self.original)
            for name in ['LICENSE', 'NOTICE', 'THIRD_PARTY.md', 'BUNDLE_MANIFEST.json',
                         'orrery_v2/web/static/fonts/OFL-DM-Sans.txt']:
                self.assertIn(name, archive.namelist())
            index = json.loads(archive.read('BUNDLE_MANIFEST.json'))
            for name, expected in index['files'].items():
                self.assertEqual(licenses.sha(archive.read(name)), expected)
        with self.assertRaisesRegex(ValueError, 'Refusing to overwrite'):
            licenses.build_bundle(self.root, self.root/'first.zip')

    def test_later_project_license_conflict_does_not_close_locked_package_gap(self):
        self.prepare_bundle()
        self.manifest['components'][0]['supplementalEvidence'] = {
            'kind': 'publisher-license-reference-with-canonical-text',
            'projectLicenseConflict': {'declaredLicense': 'MIT', 'projectLicense': 'MIT-0'}}
        self.register_supplement([self.notice])
        self.save()
        licenses.build_bundle(self.root, self.root/'conflict.zip')
        with zipfile.ZipFile(self.root/'conflict.zip') as archive:
            index = json.loads(archive.read('BUNDLE_MANIFEST.json'))
            self.assertEqual(index['publisherReferenceOnly'], ['silk-wasm@3.7.1'])
            self.assertEqual(index['publisherLicenseConflicts'], ['silk-wasm@3.7.1'])
            self.assertIn('锁定包与后续项目许可有差异', archive.read('index.html').decode())

    def source_inputs(self, changed_lock=False):
        self.prepare_bundle()
        bundle = self.root/'notices.zip'
        licenses.build_bundle(self.root, bundle)
        source = self.root/'source.tar.gz'
        originals = {name: (self.root/name).read_bytes() for name in licenses.LOCKS}
        originals['orrery_v2/original.go'] = b'package original\r\n'
        if changed_lock:
            originals[licenses.LOCKS[-1]] = b'{"packages":{}}'
        with tarfile.open(source, 'w:gz') as archive:
            for name, data in originals.items():
                member = tarfile.TarInfo(name)
                member.size, member.mode, member.mtime = len(data), 0o640, 123456
                archive.addfile(member, io.BytesIO(data))
        return source, bundle, originals

    def test_reference_only_publisher_gap_is_explicit_in_public_bundle(self):
        self.prepare_bundle()
        self.manifest['components'][0]['supplementalEvidence'] = dict(
            kind='publisher-license-reference-with-canonical-text')
        self.register_supplement([self.notice])
        self.save()
        output = self.root/'reference.zip'
        licenses.build_bundle(self.root, output)
        with zipfile.ZipFile(output) as archive:
            index = json.loads(archive.read('BUNDLE_MANIFEST.json'))
            self.assertEqual(index['publisherReferenceOnly'], ['silk-wasm@3.7.1'])
            self.assertIn('发布者仅提供协议引用'.encode(), archive.read('index.html'))

    def test_offline_bundle_retains_linked_upstream_review_and_drafts(self):
        self.prepare_bundle()
        documents = {
            'docs/license-upstream-followup-20261026.md':
                b'[review](../compliance/license-upstream-review-20261026.json)\n'
                b'[drafts](license-upstream-requests-20261026.md)\n',
            'docs/license-upstream-requests-20261026.md': b'Unsent draft\r\n',
            'compliance/license-upstream-review-20261026.json': b'{"originalLicenseGapsClosed":0}\n',
        }
        self.write('docs/third-party-license-bundle.md',
                   b'[follow-up](license-upstream-followup-20261026.md)\n')
        for name, content in documents.items():
            self.write(name, content)
        output = self.root/'followup.zip'
        licenses.build_bundle(self.root, output)
        with zipfile.ZipFile(output) as archive:
            index = json.loads(archive.read('BUNDLE_MANIFEST.json'))
            for name, content in documents.items():
                self.assertEqual(archive.read(name), content)
                self.assertEqual(index['files'][name], hashlib.sha256(content).hexdigest())
            self.assertFalse(index['redistributionApproved'])

    def embedded_source_notice(self):
        original = b'// Copyright original source author\r\n// BSD terms preserved in source\r\ncode();\r\n'
        notice = licenses.stored_notice(self.root, 'package/formatstack.js', original)
        notice.update(embeddedLicense='BSD-3-Clause', archiveSha256='a'*64,
            sourceArchive='https://registry.npmjs.org/fixture/-/fixture.tgz')
        row = self.manifest['components'][0]
        row['notices'].append(notice)
        row['supplementalEvidence'] = dict(kind='publisher-license-reference-with-canonical-text')
        self.register_supplement([notice])
        self.save()
        return notice, original

    def test_registered_embedded_notice_cannot_be_dropped_while_generic_notice_remains(self):
        notice, _ = self.embedded_source_notice()
        licenses.verify(self.root)
        self.manifest['components'][0]['notices'].remove(notice)
        self.save()
        with self.assertRaisesRegex(ValueError, 'Supplemental notice omitted'):
            licenses.verify(self.root)

    def test_supplemental_archive_identity_cannot_drift(self):
        self.embedded_source_notice()
        self.manifest['components'][0]['integrity'] = 'sha512-changed'
        lock = json.loads((self.root/self.audio).read_bytes())
        lock['packages']['node_modules/silk-wasm']['integrity'] = 'sha512-changed'
        self.write(self.audio, licenses.json_bytes(lock))
        self.manifest['locks'] = licenses.lock_hashes(self.root)
        self.save()
        with self.assertRaisesRegex(ValueError, 'Stale supplemental archive identity'):
            licenses.verify(self.root)

    def test_embedded_notice_does_not_allow_falsely_clearing_publisher_gap(self):
        self.embedded_source_notice()
        self.manifest['components'][0]['supplementalEvidence']['kind'] = 'same-project-original-license'
        self.save()
        with self.assertRaisesRegex(ValueError, 'Supplemental notice omitted or evidence changed'):
            licenses.verify(self.root)

    def test_embedded_license_is_included_without_clearing_publisher_gap(self):
        self.prepare_bundle()
        notice, original = self.embedded_source_notice()
        licenses.build_bundle(self.root, self.root/'embedded.zip')
        with zipfile.ZipFile(self.root/'embedded.zip') as archive:
            self.assertEqual(archive.read(notice['path']), original)
            index = json.loads(archive.read('BUNDLE_MANIFEST.json'))
            self.assertEqual(index['publisherReferenceOnly'], ['silk-wasm@3.7.1'])
            self.assertEqual(index['embeddedSourceNotices'][0]['license'], 'BSD-3-Clause')
            self.assertIn('嵌入源码许可：BSD-3-Clause', archive.read('index.html').decode())

    def test_augmented_source_preserves_original_files_and_both_input_hashes(self):
        source, bundle, originals = self.source_inputs()
        source_hash = licenses.sha(source.read_bytes())
        output = self.root/'augmented.tar.gz'
        result = licenses.augment_source(source, bundle, output, source_hash, 'a'*40, 'b'*40)
        self.assertEqual(licenses.sha(source.read_bytes()), source_hash)
        with tarfile.open(output, 'r:gz') as archive:
            for name, data in originals.items():
                self.assertEqual(archive.extractfile(name).read(), data)
                self.assertEqual(archive.getmember(name).mode, 0o640)
                self.assertEqual(archive.getmember(name).mtime, 123456)
            self.assertIn('LICENSE', archive.getnames())
            receipt = json.loads(archive.extractfile('SOURCE_NOTICE_ATTACHMENT.json').read())
        self.assertEqual(receipt['originalSourceSha256'], source_hash)
        self.assertEqual(receipt['noticeBundleSha256'], licenses.sha(bundle.read_bytes()))
        self.assertEqual(result['sourceCommit'], 'a'*40)
        with self.assertRaisesRegex(ValueError, 'Refusing to overwrite'):
            licenses.augment_source(source, bundle, output, source_hash, 'a'*40, 'b'*40)

    def test_augmented_source_rejects_other_production_dependency_graph(self):
        source, bundle, _ = self.source_inputs(changed_lock=True)
        with self.assertRaisesRegex(ValueError, 'Production source lock graph differs'):
            licenses.augment_source(source, bundle, self.root/'bad.tar.gz', licenses.sha(source.read_bytes()), 'a'*40, 'b'*40)

    def test_augmented_source_rejects_tampered_notice_bundle(self):
        source, bundle, _ = self.source_inputs()
        with zipfile.ZipFile(bundle) as archive:
            payload = {n: archive.read(n) for n in archive.namelist()}
        payload['NOTICE'] = b'altered copyright'
        with zipfile.ZipFile(bundle, 'w') as archive:
            for name, data in payload.items():
                archive.writestr(name, data)
        with self.assertRaisesRegex(ValueError, 'Bundle digest differs'):
            licenses.augment_source(source, bundle, self.root/'bad.tar.gz', licenses.sha(source.read_bytes()), 'a'*40, 'b'*40)

    def test_augmented_source_cannot_inject_missing_local_implementation_as_notice_material(self):
        source, bundle, _ = self.source_inputs()
        name = 'orrery_v2/runtime/orrery-js/packages/fixture/index.js'
        with zipfile.ZipFile(bundle) as archive:
            payload = {member: archive.read(member) for member in archive.namelist()}
        payload[name] = b'export const value = 1;\n'
        index = json.loads(payload['BUNDLE_MANIFEST.json'])
        index['sourceInputs'] = {name: licenses.sha(payload[name])}
        index['files'][name] = licenses.sha(payload[name])
        payload['BUNDLE_MANIFEST.json'] = licenses.json_bytes(index)
        with zipfile.ZipFile(bundle, 'w') as archive:
            for member, data in payload.items():
                archive.writestr(member, data)
        with self.assertRaisesRegex(ValueError, 'Production Orrery package source differs'):
            licenses.augment_source(source, bundle, self.root/'bad.tar.gz', licenses.sha(source.read_bytes()), 'a'*40, 'b'*40)


if __name__ == '__main__':
    unittest.main()
