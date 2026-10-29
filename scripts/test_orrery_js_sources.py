import copy
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('own_sources', ROOT/'scripts/orrery-js-sources.py')
own = importlib.util.module_from_spec(spec)
spec.loader.exec_module(own)


class OrrerySourcesTest(unittest.TestCase):
    def read(self, name):
        return (ROOT/name).read_bytes()

    def test_all_four_packages_have_complete_source_and_license_identity(self):
        records = own.sources(self.read)
        self.assertEqual(len(records), 4)
        self.assertEqual({record['license'] for record in records.values()}, {'Apache-2.0'})

    def test_unknown_local_dependency_is_rejected_instead_of_skipped(self):
        with self.assertRaisesRegex(ValueError, 'Unregistered local npm'):
            own.local_package(self.read, 'orrery_v2/web/package-lock.json',
                              dict(resolved='file:../../unknown', name='unknown', version='1.0.0', license='MIT'))

    def test_escaping_local_dependency_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'Unregistered local npm'):
            own.local_package(self.read, 'orrery_v2/web/package-lock.json',
                              dict(resolved='file:../../../../outside', name='unknown', version='1.0.0', license='MIT'))

    def test_registry_dependencies_retain_the_normal_collection_path(self):
        self.assertIsNone(own.local_package(self.read, 'orrery_v2/web/package-lock.json',
                                           dict(resolved='https://registry.npmjs.org/example/-/example-1.tgz')))

    def test_changed_implementation_cannot_reuse_frozen_license_evidence(self):
        changed = own.BASE+'/packages/ast-reference/index.js'
        def read(name):
            return self.read(name) + (b'\n// changed\n' if name == changed else b'')
        with self.assertRaisesRegex(ValueError, 'Stale Orrery source digest'):
            own.sources(read)

    def test_deleting_a_source_file_from_the_registry_is_rejected(self):
        manifest = copy.deepcopy(json.loads(self.read(own.MANIFEST)))
        record = manifest['packages'][0]
        del record['files'][next(key for key in record['files'] if key.endswith('index.js'))]
        def read(name):
            return json.dumps(manifest).encode() if name == own.MANIFEST else self.read(name)
        with self.assertRaisesRegex(ValueError, 'file set differs'):
            own.sources(read)

    def test_changing_integration_code_also_requires_refreshed_evidence(self):
        changed = own.BASE+'/install.mjs'
        def read(name):
            return self.read(name) + (b'\n// changed\n' if name == changed else b'')
        with self.assertRaisesRegex(ValueError, 'Stale or unsafe Orrery integration'):
            own.sources(read)

    def test_alias_identity_must_match_our_implementation(self):
        package = dict(resolved='file:../runtime/orrery-js/packages/ast-reference',
                       name='is-reference', version='3.0.3', license='MIT')
        with self.assertRaisesRegex(ValueError, 'Unregistered local npm'):
            own.local_package(self.read, 'orrery_v2/web/package-lock.json', package)

    def test_deleting_an_integration_file_from_the_registry_is_rejected(self):
        manifest = copy.deepcopy(json.loads(self.read(own.MANIFEST)))
        del manifest['integrationFiles'][own.BASE+'/install.mjs']
        def read(name):
            return json.dumps(manifest).encode() if name == own.MANIFEST else self.read(name)
        with self.assertRaisesRegex(ValueError, 'integration source file set differs'):
            own.sources(read)

    def test_linked_unknown_npm_package_is_not_silently_skipped(self):
        spec = importlib.util.spec_from_file_location('license_bundle', ROOT/'scripts/third-party-license-bundle.py')
        bundle = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(bundle)
        def read(name):
            if name == 'orrery_v2/web/package-lock.json':
                return b'{"packages":{"node_modules/unknown":{"link":true,"resolved":"../unknown"}}}'
            return self.read(name)
        with self.assertRaisesRegex(ValueError, 'Linked npm package requires install-links'):
            bundle.dependencies_from_reader(read)


if __name__ == '__main__':
    unittest.main()
