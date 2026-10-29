"""Check the real Compose merge without starting services or requiring an engine."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import unittest

ROOT = Path(__file__).resolve().parent.parent


@unittest.skipUnless(shutil.which('docker'), 'Docker Compose CLI required')
class WebAPIDeploymentTests(unittest.TestCase):
    def compose(self, pageindex=False, configured=False, missing_credential=None):
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('PAGEINDEX_', 'ORRERY_', 'COMPOSE_'))}
        env.update(ORRERY_POSTGRES_PASSWORD='deployment-test-only',
                   ORRERY_BOOTSTRAP_SUPERADMIN_PASSWORD='deployment-test-only',
                   ORRERY_SECRET_ONTOLOGY_CURSOR_KEY_V1='deployment-test-only')
        if configured:
            env.update(PAGEINDEX_IMAGE='pageindex-contract:test', PAGEINDEX_API_KEY='deployment-test-only',
                       PAGEINDEX_POSTGRES_PASSWORD='deployment-test-only')
        if missing_credential:
            env[missing_credential] = ''
        cmd = ['docker', 'compose', '--env-file', 'orrery_v2/config/build.env',
               '--env-file', 'orrery_v2/deploy/releases/local.env', '-f', 'docker-compose.yml']
        if pageindex:
            cmd += ['-f', 'docker-compose.pageindex.yml']
        return subprocess.run(cmd + ['config', '--format', 'json'], cwd=ROOT, env=env,
                              capture_output=True, text=True, encoding='utf-8')

    def test_default_stack_needs_no_pageindex_credentials(self):
        result = self.compose()
        self.assertEqual(result.returncode, 0, result.stderr)
        services = json.loads(result.stdout)['services']
        self.assertEqual(set(services), {'orrery-postgres', 'orrery-api', 'orrery-web'})
        self.assertEqual(set(services['orrery-api']['depends_on']), {'orrery-postgres'})
        self.assertEqual(set(services['orrery-web']['depends_on']), {'orrery-api'})
        self.assertFalse(services['orrery-api']['environment']['ORRERY_SECRET_KNOWLEDGE_PAGEINDEX'])

    def test_optional_service_validates_its_own_credentials(self):
        # Compose may interpolate services in either order. Check each required
        # credential separately so both contracts are verified deterministically.
        for credential in ('PAGEINDEX_POSTGRES_PASSWORD', 'PAGEINDEX_API_KEY'):
            with self.subTest(credential=credential):
                result = self.compose(pageindex=True, configured=True,
                                      missing_credential=credential)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(credential, result.stderr)

    def test_optional_service_merges_without_reintroducing_core_dependency(self):
        result = self.compose(pageindex=True, configured=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        services = json.loads(result.stdout)['services']
        self.assertIn('pageindex', services)
        self.assertNotIn('pageindex', services['orrery-api']['depends_on'])
        self.assertEqual(services['orrery-api']['environment']['ORRERY_SECRET_KNOWLEDGE_PAGEINDEX'], 'deployment-test-only')


if __name__ == '__main__':
    unittest.main()
