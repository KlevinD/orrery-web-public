import io
import json
import os
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import license_attachment as notices
if os.name == 'posix':
    import production_release as release
    from repair_dispatch_control import DispatchPermit


class AttachmentFixtures(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.registry = self.root / 'compliance'
        self.source = {name: (name+'\n').encode() for name in (*notices.LOCKS, *notices.VENDORED)}

    def attachment(self, commit, source=None, omit=(), local_sources=None):
        source = self.source if source is None else source
        directory = self.registry / 'releases' / commit
        public = directory / 'public'
        public.mkdir(parents=True)
        locks = {name: notices.digest(source[name]) for name in notices.LOCKS}
        corpus = {'locks': locks, 'vendoredNotices': []}
        payload = {name: (name+' notice\n').encode() for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY.md', 'index.html')}
        if local_sources:
            registry = 'compliance/orrery-js-sources.json'
            payload.update(local_sources)
            corpus['components'] = [{'localSource': {
                'manifest': registry, 'manifestSha256': notices.digest(local_sources[registry]),
                'files': {name: notices.digest(data) for name, data in local_sources.items() if name != registry}}}]
        for i, name in enumerate(notices.VENDORED):
            path = 'compliance/licenses/'+str(i)+'.txt'
            payload[path] = source[name]
            corpus['vendoredNotices'].append({'originalPath': name, 'path': path})
        payload['compliance/license-texts.json'] = json.dumps(corpus).encode()
        for name in omit:
            payload.pop(name)
        index = {'formatVersion': 1, 'locks': locks, 'components': 1, 'publisherReferenceOnly': [],
                 'files': {name: notices.digest(data) for name, data in payload.items()}}
        if local_sources:
            index['sourceInputs'] = {name: notices.digest(data) for name, data in local_sources.items()}
        payload['BUNDLE_MANIFEST.json'] = json.dumps(index).encode()
        for name, data in payload.items():
            path = public / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        with zipfile.ZipFile(public / 'THIRD_PARTY_NOTICES.zip', 'w') as archive:
            for name, data in payload.items():
                archive.writestr(name, data)
        selection = {'attachmentCommit': commit,
                     'bundleSha256': notices.file_digest(public / 'THIRD_PARTY_NOTICES.zip'),
                     'bundleManifestSha256': notices.file_digest(public / 'BUNDLE_MANIFEST.json')}
        (directory / 'SELECTION.json').write_text(json.dumps(selection))
        return directory

    def snapshot(self, path, values):
        with tarfile.open(path, 'w:gz') as archive:
            for name, data in values.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))

    def candidate(self, name, commit, source=None):
        candidate = self.root / name
        candidate.mkdir()
        candidate.joinpath('web').mkdir()
        candidate.joinpath('web/index.html').write_text('application')
        candidate.joinpath('COMMIT').write_text(commit)
        candidate.joinpath('WEB_COMMIT').write_text(commit)
        self.snapshot(candidate / 'SOURCE_SNAPSHOT.tar.gz', source or self.source)
        return candidate

    def local_source_inputs(self):
        return {'compliance/orrery-js-sources.json': b'{"integrationFiles":{}}',
                'orrery_v2/runtime/orrery-js/packages/fixture/index.js': b'export const value = 1;\n'}


class PackageSourceEvidenceTests(AttachmentFixtures):
    def test_matching_local_package_source_is_required_even_when_locks_match(self):
        own = self.local_source_inputs()
        directory = self.attachment('d'*40, local_sources=own)
        attachment = notices.Attachment(directory, self.registry)
        candidate = self.candidate('own-match', 'c'*40, dict(self.source, **own))
        self.assertEqual(attachment.evidence(candidate)['attachmentCommit'], 'd'*40)
        changed = dict(self.source, **own)
        changed['orrery_v2/runtime/orrery-js/packages/fixture/index.js'] = b'export const value = 2;\n'
        self.snapshot(candidate / 'SOURCE_SNAPSHOT.tar.gz', changed)
        with self.assertRaisesRegex(ValueError, 'Release Orrery package source differs'):
            attachment.evidence(candidate)

    def test_missing_local_package_cannot_be_supplied_by_the_notice_attachment(self):
        own = self.local_source_inputs()
        directory = self.attachment('d'*40, local_sources=own)
        candidate = self.candidate('own-missing', 'c'*40)
        with self.assertRaisesRegex(ValueError, 'Release Orrery package source differs'):
            notices.Attachment(directory, self.registry).evidence(candidate)

    def test_separate_web_snapshot_must_include_its_own_local_package_sources(self):
        own = self.local_source_inputs()
        directory = self.attachment('d'*40, local_sources=own)
        candidate = self.candidate('own-web-missing', 'c'*40, dict(self.source, **own))
        self.snapshot(candidate / 'WEB_SOURCE_SNAPSHOT.tar.gz', {'web/package-lock.json': self.source[notices.LOCKS[2]]})
        with self.assertRaisesRegex(ValueError, 'Web Orrery package source differs'):
            notices.Attachment(directory, self.registry).evidence(candidate)

    def test_web_snapshot_cannot_mask_stale_api_package_source(self):
        own = self.local_source_inputs()
        directory = self.attachment('d'*40, local_sources=own)
        candidate = self.candidate('own-api-missing', 'c'*40)
        self.snapshot(candidate / 'WEB_SOURCE_SNAPSHOT.tar.gz', dict(own, **{'web/package-lock.json': self.source[notices.LOCKS[2]]}))
        with self.assertRaisesRegex(ValueError, 'Release Orrery package source differs'):
            notices.Attachment(directory, self.registry).evidence(candidate)


@unittest.skipUnless(os.name == 'posix', 'Production attachment symlinks and flock require POSIX')
class LicenseAttachmentTests(AttachmentFixtures):
    def setUp(self):
        super().setUp()
        self.directory = self.attachment('a'*40)
        self.registry.joinpath('current').symlink_to(self.directory, target_is_directory=True)
        self.live = self.candidate('old', 'b'*40)
        self.current = self.root / 'current'
        self.current.symlink_to(self.live, target_is_directory=True)

    def test_subsequent_release_carries_notices_with_its_own_source_receipt(self):
        candidate = self.candidate('next', 'c'*40)
        original = notices.file_digest(candidate / 'SOURCE_SNAPSHOT.tar.gz')
        plan = notices.prepare_activation(self.current, 'web', candidate, registry=self.registry)
        self.assertTrue(self.live.joinpath('web/third-party/index.html').is_file())
        self.current.unlink()
        self.current.symlink_to(candidate, target_is_directory=True)
        result = plan.complete()
        receipt = json.loads(candidate.joinpath('THIRD_PARTY_ATTACHMENT.json').read_bytes())
        self.assertEqual(receipt['productionCommit'], 'c'*40)
        self.assertEqual(receipt['sources']['SOURCE_SNAPSHOT.tar.gz'], original)
        self.assertEqual(result['license_attachment_commit'], 'a'*40)
        self.assertEqual(notices.file_digest(candidate / 'SOURCE_SNAPSHOT.tar.gz'), original)
        self.assertEqual(candidate.joinpath('web/index.html').read_text(), 'application')
        self.assertEqual(candidate.joinpath('web/third-party').resolve(), self.directory / 'public')
        self.assertFalse(candidate.joinpath('SOURCE_WITH_NOTICES.tar.gz').exists())
        before = candidate.joinpath('THIRD_PARTY_ATTACHMENT.json').stat().st_mtime_ns
        plan.complete()
        self.assertEqual(candidate.joinpath('THIRD_PARTY_ATTACHMENT.json').stat().st_mtime_ns, before)

    def test_changed_dependency_requires_new_matching_attachment(self):
        changed = dict(self.source, **{notices.LOCKS[2]: b'new locked dependency'})
        candidate = self.candidate('changed', 'c'*40, changed)
        with self.assertRaisesRegex(ValueError, 'lock graph differs'):
            notices.prepare_activation(self.current, 'web', candidate, registry=self.registry)
        self.assertFalse(candidate.joinpath('web/third-party').exists())
        selected = self.attachment('d'*40, changed)
        plan = notices.prepare_activation(self.current, 'web', candidate, selected, registry=self.registry)
        self.assertEqual(self.registry.joinpath('current').resolve(), self.directory)
        self.current.unlink()
        self.current.symlink_to(candidate, target_is_directory=True)
        plan.complete()
        self.assertEqual(self.registry.joinpath('current').resolve(), selected)

    def test_changed_vendored_notice_and_missing_web_source_are_rejected(self):
        changed = dict(self.source, **{notices.VENDORED[0]: b'different copyright'})
        candidate = self.candidate('changed-vendor', 'c'*40, changed)
        with self.assertRaisesRegex(ValueError, 'vendored notice differs'):
            notices.prepare_activation(self.current, 'api', candidate, registry=self.registry)
        candidate.joinpath('WEB_COMMIT').write_text('e'*40)
        self.snapshot(candidate / 'SOURCE_SNAPSHOT.tar.gz', self.source)
        with self.assertRaisesRegex(ValueError, 'requires its own source'):
            notices.prepare_activation(self.current, 'web', candidate, registry=self.registry)
        self.snapshot(candidate / 'WEB_SOURCE_SNAPSHOT.tar.gz', {'web/package-lock.json': b'new web graph'})
        with self.assertRaisesRegex(ValueError, 'lock graph differs'):
            notices.prepare_activation(self.current, 'web', candidate, registry=self.registry)
        self.snapshot(candidate / 'WEB_SOURCE_SNAPSHOT.tar.gz', {'web/package-lock.json': self.source[notices.LOCKS[2]]})
        notices.prepare_activation(self.current, 'web', candidate, registry=self.registry)

    def test_foreign_destinations_are_preserved_before_any_link_change(self):
        self.live.joinpath('NOTICE').write_text('foreign notice')
        with self.assertRaisesRegex(ValueError, 'Foreign license attachment destination'):
            notices.Attachment(self.directory, self.registry).attach(self.live)
        self.assertFalse(self.live.joinpath('web/third-party').exists())
        self.assertEqual(self.live.joinpath('NOTICE').read_text(), 'foreign notice')

    def test_failed_attachment_upgrade_rolls_back_only_managed_links(self):
        old = notices.Attachment(self.directory, self.registry)
        old.attach(self.live)
        receipt = self.live.joinpath('THIRD_PARTY_ATTACHMENT.json').read_bytes()
        selected = notices.Attachment(self.attachment('d'*40), self.registry)
        original = notices.replace_link
        calls = 0
        def fail_second(path, target):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise OSError('injected publication failure')
            original(path, target)
        with patch.object(notices, 'replace_link', side_effect=fail_second):
            with self.assertRaisesRegex(OSError, 'publication failure'):
                selected.attach(self.live)
        self.assertEqual(self.live.joinpath('web/third-party').resolve(), old.public)
        self.assertEqual(self.live.joinpath('THIRD_PARTY_ATTACHMENT.json').read_bytes(), receipt)

    def test_corrupt_public_file_and_noncanonical_archive_are_rejected(self):
        self.directory.joinpath('public/LICENSE').write_text('tampered')
        with self.assertRaisesRegex(ValueError, 'file digest differs'):
            notices.Attachment(self.directory, self.registry)
        self.snapshot(self.live / 'SOURCE_SNAPSHOT.tar.gz', {'../go.mod': b'bad'})
        with self.assertRaisesRegex(ValueError, 'Unsafe source'):
            notices.archive_files(self.live / 'SOURCE_SNAPSHOT.tar.gz', set(notices.LOCKS))

    def test_internally_consistent_bundle_cannot_omit_root_notices(self):
        missing = self.attachment('d'*40, omit=('NOTICE',))
        with self.assertRaisesRegex(ValueError, 'Incomplete public license documents'):
            notices.Attachment(missing, self.registry)

    def test_registry_requires_explicit_candidate_and_preserves_foreign_symlinks(self):
        with self.assertRaisesRegex(ValueError, '--candidate-release'):
            notices.prepare_activation(self.current, 'web', registry=self.registry)
        self.live.joinpath('web/third-party').symlink_to(self.root, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, 'Foreign license attachment symlink'):
            notices.prepare_activation(self.current, 'worker', registry=self.registry)
        self.assertEqual(self.live.joinpath('web/third-party').resolve(), self.root)

    @unittest.skipUnless(os.name == 'posix', 'Production flock lease requires POSIX')
    def test_manual_guard_preflights_before_child_and_finishes_under_existing_lease(self):
        candidate = self.candidate('next', 'c'*40)
        state = self.root / 'state'
        state.mkdir()
        config = self.root / 'config.json'
        config.write_text(json.dumps({'state_dir': str(state)}))
        script = self.root / 'activate.py'
        script.write_text('import json,os,pathlib,sys\n'
            'assert len(json.loads(os.environ["ORRERY_PRODUCTION_RELEASE_LOCK_FDS"])) == 5\n'
            'candidate=pathlib.Path(sys.argv[1]); current=pathlib.Path(sys.argv[2])\n'
            'assert (candidate/"web/third-party/index.html").is_file()\n'
            'current.unlink(); current.symlink_to(candidate, target_is_directory=True)\n')
        cli = ['--config', str(config), '--expected-release', str(self.live), '--expected-commit', 'b'*40,
               '--component', 'web', '--candidate-release', str(candidate), '--', sys.executable, str(script),
               str(candidate), str(self.current)]
        locks = [self.root / ('deploy-'+str(i)+'.lock') for i in range(5)]
        lease = release.activation_lease
        def actual_lease(*args, **kwargs):
            return lease(*args, **dict(kwargs, locks=locks))
        def health():
            with DispatchPermit(state) as permit:
                self.assertFalse(permit.allowed)
            return {'release': str(self.current.resolve()), 'commit': self.current.resolve().joinpath('COMMIT').read_text()}
        with patch.object(release, 'CURRENT', str(self.current)), patch.object(notices, 'REGISTRY', self.registry), \
                patch.object(release, 'require_cooperative_worker'), patch.object(release, 'check_health', side_effect=health), \
                patch.object(release, 'activation_lease', side_effect=actual_lease):
            self.assertEqual(release.main(cli), 0)
            bad = self.candidate('bad', 'e'*40, dict(self.source, **{notices.LOCKS[0]: b'changed graph'}))
            bad_cli = ['--config', str(config), '--expected-release', str(candidate), '--expected-commit', 'c'*40,
                       '--component', 'web', '--candidate-release', str(bad), '--', 'never-run']
            with patch.object(release, 'run_activation') as activate:
                self.assertEqual(release.main(bad_cli), 1)
                activate.assert_not_called()
        with DispatchPermit(state) as permit:
            self.assertTrue(permit.allowed)
        self.assertEqual(self.current.resolve(), candidate)
        receipt = json.loads(candidate.joinpath('THIRD_PARTY_ATTACHMENT.json').read_bytes())
        self.assertEqual(receipt['productionCommit'], 'c'*40)

    def test_fresh_install_is_optional_but_broken_configured_selection_fails_closed(self):
        self.assertIsNone(notices.prepare_activation(self.current, 'worker', registry=self.root / 'absent'))
        self.registry.joinpath('current').unlink()
        with self.assertRaisesRegex(ValueError, 'no immutable current selection'):
            notices.prepare_activation(self.current, 'worker', registry=self.registry)


if __name__ == '__main__':
    unittest.main()
