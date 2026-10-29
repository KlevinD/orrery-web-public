#!/usr/bin/env python3
"""Web/API source inventory that also runs from the exported source without Git."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
from urllib.parse import quote

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('source_inventory', ROOT / 'scripts/opensource-inventory.py')
inventory = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inventory)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    locks = ['orrery_v2/go.mod', 'orrery_v2/go.sum', 'orrery_v2/web/package-lock.json',
             'orrery_v2/runtime/pi-coding-agent/package-lock.json',
             'orrery_v2/scripts/audio-codec/package-lock.json']
    # Frozen Go license hashes are evidence, not a guess based on package names.
    frozen = json.loads((ROOT / 'compliance/source-inventory.json').read_text(encoding='utf-8'))
    expected = {(name, version, checksum) for line in (ROOT / 'orrery_v2/go.sum').read_text().splitlines()
                for name, version, checksum in [line.split()] if not version.endswith('/go.mod')}
    actual = {(row['name'], row['version'], row['checksum']) for row in frozen['go']}
    if expected != actual:
        raise SystemExit('Refresh the frozen Go inventory before generating the Web/API source list')
    assets = json.loads((ROOT / 'compliance/web-asset-rights.json').read_text(encoding='utf-8'))['assets']
    for asset in assets:
        if hashlib.sha256((ROOT / asset['path']).read_bytes()).hexdigest() != asset['sha256']:
            raise SystemExit('Stale Web asset evidence: ' + asset['path'])
    result = dict(formatVersion=1,
        scope='Web/API source only: all retained Go sum archive versions, Web/PI npm lock graphs including build/test tools, retained source adaptations and Web assets; not a production image SBOM',
        locks={name: inventory.digest(ROOT / name) for name in locks},
        go=frozen['go'], npm={name: inventory.npm_modules(ROOT / name) for name in locks if name.endswith('package-lock.json')},
        sourceAdaptations=inventory.source_adaptations(), assets=assets)
    evidence = json.loads((ROOT / 'compliance/npm-license-evidence.json').read_text(encoding='utf-8'))
    for package in evidence['packages']:
        matches = [row for rows in result['npm'].values() for row in rows
                   if (row['name'], row['version']) == (package['name'], package['version'])]
        if not matches or any(row['license'] != package['license'] or
                              row['integrity'] != package['integrity'] for row in matches):
            raise SystemExit('Stale npm license metadata: ' + package['name'])
        for record in package['files']:
            path = (ROOT / record['path']).resolve()
            if not path.is_relative_to(ROOT.resolve()) or inventory.digest(path) != record['sha256']:
                raise SystemExit('Stale npm license file: ' + record['path'])
    result['npmLicenseEvidence'] = evidence
    inventory.write('web-api-source-inventory.json', result, args.check)
    components = {}
    for ecosystem, scope, records in [('golang', 'retained Go sum archive entries', result['go'])] + [
            ('npm', name, records) for name, records in result['npm'].items()]:
        for row in records:
            ref = 'pkg:' + ecosystem + '/' + '/'.join(quote(p, safe='') for p in row['name'].split('/')) + '@' + quote(row['version'], safe='')
            component = components.setdefault(ref, dict(type='library', **{'bom-ref':ref}, purl=ref,
                name=row['name'],version=row['version'],properties=[]))
            prop = dict(name='orrery:source-scope', value=scope)
            if prop not in component['properties']:
                component['properties'].append(prop)
            if row.get('license'):
                component['licenses'] = [dict(license=dict(name=row['license']))]
            if row.get('localSource'):
                local = row['localSource']
                component['hashes'] = [dict(alg='SHA-256', content=hashlib.sha256(json.dumps(local['files'], sort_keys=True, separators=(',', ':')).encode()).hexdigest())]
                component['properties'] += [dict(name='orrery:source-manifest', value=local['manifest']),
                    dict(name='orrery:source-manifest-sha256', value=local['manifestSha256'])]
    inventory.write('web-api-sbom.cdx.json', dict(bomFormat='CycloneDX',specVersion='1.5',version=1,
        metadata=dict(component=dict(type='application',name='Orrery Web/API source'),
            properties=[dict(name='orrery:scope',value=result['scope'])]),
        components=[components[key] for key in sorted(components)]), args.check)
    print(f"Web/API source: {len(result['go'])} Go entries, {sum(map(len,result['npm'].values()))} npm entries, {len(assets)} assets; no client/Rust graph")


if __name__ == '__main__':
    main()
