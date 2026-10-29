#!/usr/bin/env python3
"""Reproducible source inventory. Missing evidence stays visible; no license guesses."""
from pathlib import Path
import argparse
import hashlib
import importlib.util
import json
import re
import subprocess
import tomllib
from urllib.parse import quote

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'compliance'
_spec = importlib.util.spec_from_file_location('orrery_js_sources', ROOT/'scripts/orrery-js-sources.py')
own_sources = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(own_sources)

def digest(p):
    content = p.read_bytes()
    if p.is_relative_to(ROOT) and p.suffix.lower() in {'.mod', '.sum', '.lock', '.json', '.svg'}:
        content = content.replace(b'\r\n', b'\n')
    return hashlib.sha256(content).hexdigest()

def write(name, data, check):
    content = json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True) + '\n'
    p = OUT / name
    if check:
        if not p.exists() or p.read_text(encoding='utf-8') != content:
            raise SystemExit(f'Stale inventory: {p.relative_to(ROOT)}')
    else:
        p.write_text(content, encoding='utf-8', newline='\n')

def go_modules(refresh_evidence=False):
    # Inventory every checksum entry, including retained historical versions.
    # This conservative scope is stable offline and is explicitly not a binary SBOM.
    cache = Path(subprocess.check_output(['go','env','GOMODCACHE'],text=True).strip())
    checksums = {}
    for line in (ROOT/'orrery_v2/go.sum').read_text(encoding='utf-8').splitlines():
        name, version, checksum = line.split()
        if not version.endswith('/go.mod'): checksums[(name,version)] = checksum
    previous = {}
    prior = OUT / 'source-inventory.json'
    if prior.exists():
        previous = {(m['name'],m['version']):m for m in json.loads(prior.read_text(encoding='utf-8'))['go']}
    modules = []
    for (name,version), checksum in sorted(checksums.items()):
        escaped = ''.join('!'+c.lower() if c.isupper() else c for c in name)
        directory = cache / (escaped+'@'+version)
        evidence = previous.get((name,version),{}).get('licenseFiles',[])
        if (refresh_evidence or not previous) and directory.is_dir():
            evidence = []
            for p in sorted(directory.glob('*')):
                if p.is_file() and re.match(r'^(LICENSE|LICENCE|COPYING|NOTICE)([._-].*)?$',p.name,re.I):
                    evidence.append({'file':p.name,'sha256':digest(p)})
        modules.append({'name':name,'version':version,'checksum':checksum,
                        'licenseFiles':evidence,'status':'license-text-review' if evidence else 'missing-license-evidence'})
    return sorted(modules,key=lambda m:(m['name'],m['version']))

def npm_modules(file):
    lock=json.loads(file.read_text(encoding='utf-8')); records={}
    for location, package in lock.get('packages',{}).items():
        if location and package.get('link'):
            raise ValueError('Linked npm package requires install-links=true: '+location)
        if not location or package.get('link') or not package.get('version'):continue
        name=package.get('name') or location.split('node_modules/')[-1]
        key=(name,package['version'])
        records[key]={'name':name,'version':package['version'],'license':package.get('license'),
                      'integrity':package.get('integrity'),'dev':package.get('dev',False),
                      'status':'metadata-only' if package.get('license') else 'missing-license-evidence'}
        local = own_sources.local_package(lambda name: (ROOT/name).read_bytes(), file.relative_to(ROOT).as_posix(), package)
        if local:
            records[key].update(status='orrery-source-verified', localSource=local)
    return sorted(records.values(),key=lambda p:(p['name'],p['version']))

def source_adaptations():
    """Keep imported source visible even when no external runtime is installed."""
    name = 'compliance/source-adaptations.json'
    manifest = json.loads((ROOT/name).read_text(encoding='utf-8'))
    if manifest.get('formatVersion') != 1:
        raise SystemExit('Unsupported source adaptation manifest')
    records = []
    for source in manifest['sources']:
        files = source['localFiles'] + [{'path':source['licenseFile'],'sha256':source['licenseSha256']}]
        files += [{'path':record['noticeFile'],'sha256':record['noticeSha256']}
                  for record in source['localFiles'] if record.get('noticeFile')]
        for record in files:
            path = ROOT/record['path']
            if not path.resolve().is_relative_to(ROOT.resolve()) or not path.is_file():
                raise SystemExit('Invalid source provenance path: '+record['path'])
            actual = hashlib.sha256(path.read_bytes().replace(b'\r\n',b'\n')).hexdigest()
            if actual != record['sha256']:
                raise SystemExit('Stale source provenance: '+record['path'])
        records.append({'name':source['name'],'version':source['commit'],'purl':source['purl'],
                        'license':source['license'],'licenseFile':source['licenseFile'],
                        'licenseEvidence':source['licenseEvidence'],'localFileCount':len(source['localFiles']),
                        'externalInstallRequired':source['externalInstallRequired'],
                        'upstreamBinaryBundled':source['upstreamBinaryBundled'],
                        'upstreamCheckoutRequired':source['upstreamCheckoutRequired']})
    return {'manifest':name,'sha256':digest(ROOT/name),'sources':records}

def source_lock_paths():
    """Return lock graphs that are actually part of this source export.

    The private monorepo also has a desktop/Tauri graph, while the public
    Web/API repository intentionally does not.  Inventory generation must
    follow the checked-out tree instead of assuming the private layout.
    """
    candidates = [
        'orrery_v2/go.mod',
        'orrery_v2/go.sum',
        'orrery_v2/web/package-lock.json',
        'orrery_v2/runtime/pi-coding-agent/package-lock.json',
        'orrery_v2/scripts/audio-codec/package-lock.json',
        'orrery_v2/src-tauri/Cargo.lock',
        'orrery-desktop/bun.lock',
        'orrery-desktop/upstream-lock.json',
    ]
    return [name for name in candidates if (ROOT / name).is_file()]

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--check',action='store_true')
    parser.add_argument('--refresh-license-evidence',action='store_true',help='Explicitly refresh Go license-file evidence from the local module cache')
    args=parser.parse_args()
    if args.check and args.refresh_license_evidence:
        parser.error('--check and --refresh-license-evidence cannot be combined')
    OUT.mkdir(exist_ok=True)
    locks=source_lock_paths()
    rust_path = ROOT / 'orrery_v2/src-tauri/Cargo.lock'
    rust=tomllib.loads(rust_path.read_text(encoding='utf-8')) if rust_path.is_file() else {'package': []}
    desktop_sbom = 'orrery-desktop/resources/compliance/sbom.cdx.json'
    has_desktop = (ROOT / desktop_sbom).is_file()
    scope = ('npm/Rust/desktop source lock graphs, all Go sum archive entries (including historical versions), '
             'and retained source adaptations; not a binary redistribution approval') if has_desktop else (
             'Web/API npm lock graphs, all Go sum archive entries (including historical versions), '
             'audio codec tooling, and retained source adaptations; not a binary redistribution approval')
    inventory={'formatVersion':1,'profile':'full' if has_desktop else 'web-api','scope':scope,
        'sourceTextNormalization':'CRLF-to-LF for textual source locks; binary files unchanged',
        'locks':{name:digest(ROOT/name) for name in locks},'go':go_modules(args.refresh_license_evidence),
        'npm':{name:npm_modules(ROOT/name) for name in locks if name.endswith('package-lock.json')},
        'rust':[{'name':p['name'],'version':p['version'],'checksum':p.get('checksum'),
                 'source':p.get('source'),'status':'maintenance-only-license-review'} for p in rust['package']],
        'sourceAdaptations':source_adaptations()}
    if has_desktop:
        inventory['desktopSbom'] = desktop_sbom
    write('source-inventory.json',inventory,args.check)
    components = []
    def component(ecosystem, record, scope):
        ref = f"pkg:{ecosystem}/" + '/'.join(quote(part,safe='') for part in record['name'].split('/')) + '@' + quote(record['version'],safe='')
        item = {'type':'library','bom-ref':ref,'purl':ref,'name':record['name'],'version':record['version'],
                'properties':[{'name':'orrery:source-scope','value':scope},{'name':'orrery:license-evidence','value':record['status']}]}
        if record.get('license'): item['licenses']=[{'license':{'name':record['license']}}]
        if record.get('localSource'):
            local = record['localSource']
            item['hashes']=[{'alg':'SHA-256','content':hashlib.sha256(json.dumps(local['files'],sort_keys=True,separators=(',',':')).encode()).hexdigest()}]
            item['properties'] += [{'name':'orrery:source-manifest','value':local['manifest']},
                                   {'name':'orrery:source-manifest-sha256','value':local['manifestSha256']}]
        return item
    for m in inventory['go']:components.append(component('golang',m,'Go sum archive entries, including retained historical versions'))
    for name, records in inventory['npm'].items():
        for p in records:components.append(component('npm',p,name))
    for p in inventory['rust']:components.append(component('cargo',p,'maintenance-only Tauri lock graph'))
    for source in inventory['sourceAdaptations']['sources']:
        components.append({'type':'library','bom-ref':source['purl'],'purl':source['purl'],
            'name':source['name'],'version':source['version'],'licenses':[{'license':{'id':source['license']}}],
            'properties':[{'name':'orrery:source-scope','value':'historical source adaptations; not an external runtime installation'},
                          {'name':'orrery:license-evidence','value':source['licenseEvidence']},
                          {'name':'orrery:provenance-manifest','value':inventory['sourceAdaptations']['manifest']},
                          {'name':'orrery:external-install-required','value':str(source['externalInstallRequired']).lower()},
                          {'name':'orrery:upstream-binary-bundled','value':str(source['upstreamBinaryBundled']).lower()}]})
    if inventory.get('desktopSbom'):
        desktop=json.loads((ROOT/inventory['desktopSbom']).read_text(encoding='utf-8'))
        components.extend(desktop['components'])
    # One component can occur in multiple source lock graphs.
    merged = {}
    for item in components:
        ref=item['bom-ref']
        if ref in merged:
            props=merged[ref].setdefault('properties',[])
            for prop in item.get('properties',[]):
                if prop not in props:props.append(prop)
        else:merged[ref]=item
    write('sbom.cdx.json',{'bomFormat':'CycloneDX','specVersion':'1.5','version':1,
        'metadata':{'component':{'type':'application','bom-ref':'orrery:source','name':'Orrery source snapshot','version':'0.1.0'},
                    'properties':[{'name':'orrery:scope','value':inventory['scope']}]},
        'components':[merged[k] for k in sorted(merged)]},args.check)
    names=subprocess.check_output(['git','ls-files','-z','--cached','--others','--exclude-standard'],cwd=ROOT).decode('utf-8').split('\0')
    rights_path=OUT/'web-asset-rights.json'
    rights=json.loads(rights_path.read_text(encoding='utf-8'))
    if rights['formatVersion']!=1:raise SystemExit('Unsupported asset rights format')
    reviewed={}
    for entry in rights['assets']:
        name=entry['path']
        if name in reviewed:raise SystemExit('Duplicate asset rights path: '+name)
        path=(ROOT/name).resolve()
        if not path.is_relative_to(ROOT.resolve()) or not path.is_file():raise SystemExit('Missing or unsafe asset rights path: '+name)
        if entry['sha256']!=digest(ROOT/name):raise SystemExit('Stale asset rights digest: '+name)
        if entry['status'] not in {'license-evidence-verified','pending-rights-evidence'}:raise SystemExit('Unknown asset rights status: '+name)
        if entry['status']=='license-evidence-verified':
            evidence=entry['evidence'];license_path=(ROOT/evidence['licensePath']).resolve()
            if not license_path.is_relative_to(ROOT.resolve()) or not license_path.is_file():raise SystemExit('Missing or unsafe asset license: '+name)
            actual=hashlib.sha256(license_path.read_bytes().replace(b'\r\n',b'\n')).hexdigest()
            if actual!=evidence['licenseLfSha256'] or entry['sha256']!=evidence['sourceSha256']:raise SystemExit('Stale asset source/license evidence: '+name)
        reviewed[name]=entry
    assets=[]
    for name in sorted(set(names)):
        p=ROOT/name
        if not name or not p.is_file() or p.suffix.lower() not in {'.png','.jpg','.jpeg','.gif','.webp','.svg','.ico','.icns','.woff','.woff2','.ttf','.otf','.mp4','.mp3'}:continue
        if not p.resolve().is_relative_to(ROOT.resolve()):raise SystemExit('Resource escapes repository: '+name)
        asset={'path':name,'sha256':digest(p),'provenance':'AionUi-derived repository resource' if name.startswith('orrery-desktop/') else 'existing Orrery repository resource',
               'licenseEvidence':'per-file-authorship-and-license-review-required'}
        if name in reviewed:
            entry=reviewed[name];asset.update(licenseEvidence=entry['status'],rightsReview='compliance/web-asset-rights.json')
            if entry.get('license'):asset['license']=entry['license']
        assets.append(asset)
    if set(reviewed)-{entry['path'] for entry in assets}:raise SystemExit('Asset rights entry missing from inventory')
    write('assets.json',{'formatVersion':1,'scope':'repository provenance is not a redistribution grant',
                        'sourceTextNormalization':'CRLF-to-LF for SVG source; binary resources unchanged','assets':assets},args.check)
    print(f'Inventory: {len(inventory["go"])} Go modules, {sum(map(len,inventory["npm"].values()))} npm entries, {len(inventory["rust"])} Rust entries, {len(assets)} resources')

if __name__=='__main__':main()
