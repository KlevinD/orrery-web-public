import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

from production_source import prepare


class ProductionSourceTests(unittest.TestCase):
    def test_current_release_backend_and_web_replace_legacy_source_and_cache_is_verified(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            release, base = root / 'release', root / 'workspace'
            release.mkdir(); base.mkdir()
            (release / 'COMMIT').write_text('a' * 40)
            def archive(name, entries):
                with tarfile.open(release / name, 'w:gz') as stream:
                    for path, text in entries.items():
                        data = text.encode(); member = tarfile.TarInfo(path); member.size = len(data)
                        stream.addfile(member, io.BytesIO(data))
            archive('SOURCE_SNAPSHOT.tar.gz', {'orrery_v2/go.mod': 'module current', 'orrery_v2/cmd/current.go': 'current backend'})
            archive('WEB_SOURCE_SNAPSHOT.tar.gz', {'web/src/current.ts': 'current web'})
            config = {'production_release_root': str(release), 'source_root': str(root / 'missing-legacy-checkout')}
            resolved, snapshot, commit = prepare(config, base)
            self.assertEqual(resolved, release.resolve())
            self.assertEqual(commit, 'a' * 40)
            self.assertEqual((snapshot / 'orrery_v2/cmd/current.go').read_text(), 'current backend')
            self.assertEqual((snapshot / 'orrery_v2/web/src/current.ts').read_text(), 'current web')
            self.assertEqual(prepare(config, base)[1], snapshot)
            (snapshot / '.source-complete').write_text('{}')
            with self.assertRaisesRegex(ValueError, 'receipt'):
                prepare(config, base)

    def test_source_snapshot_cannot_escape_workspace(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); base = root / 'workspace'; base.mkdir()
            (root / 'COMMIT').write_text('a' * 40)
            for name in ('SOURCE_SNAPSHOT.tar.gz', 'WEB_SOURCE_SNAPSHOT.tar.gz'):
                with tarfile.open(root / name, 'w:gz') as stream:
                    member = tarfile.TarInfo('../../escape'); member.size = 1
                    stream.addfile(member, io.BytesIO(b'x'))
            with self.assertRaisesRegex(ValueError, 'unsafe'):
                prepare({'production_release_root': str(root)}, base)
            self.assertFalse((root / 'escape').exists())
